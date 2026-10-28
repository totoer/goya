package goya

import (
	"math"
	"runtime"
	"sync"
	"time"
	"weak"
)

type waitAgentState struct {
	sync.Mutex
	entries map[*atomicWaiter]struct{}
}
type atomicWaiter struct {
	storage *sharedArrayStorage
	index   int
	ready   chan struct{}
	queue   weak.Pointer[runtimeJobQueue]
	agent   *waitAgentState
	id      uint64
	waiting bool
	result  string
	timer   *time.Timer
	timeout float64
	started time.Time
}

func (agent *waitAgentState) add(waiter *atomicWaiter) {
	agent.Lock()
	agent.entries[waiter] = struct{}{}
	agent.Unlock()
}
func (agent *waitAgentState) remove(waiter *atomicWaiter) {
	agent.Lock()
	delete(agent.entries, waiter)
	agent.Unlock()
}
func (agent *waitAgentState) cancelAll() {
	agent.Lock()
	entries := make([]*atomicWaiter, 0, len(agent.entries))
	for entry := range agent.entries {
		entries = append(entries, entry)
	}
	clear(agent.entries)
	agent.Unlock()
	for _, entry := range entries {
		entry.cancel("abandoned")
	}
}
func cleanupWaitAgent(agent *waitAgentState) { agent.cancelAll() }
func (r *Runtime) atomicWaitAgent() *waitAgentState {
	if r.waitAgent == nil {
		r.waitAgent = &waitAgentState{entries: make(map[*atomicWaiter]struct{})}
		// The cleanup argument owns no Runtime, promise capability or script value.
		runtime.AddCleanup(r, cleanupWaitAgent, r.waitAgent)
	}
	return r.waitAgent
}

// Called under the storage's waiter lock. Notification ordering therefore
// follows FIFO removal ordering even when several Go agents notify at once.
func (waiter *atomicWaiter) finishLocked(result string, deliver bool, owner *Runtime) (bool, bool) {
	if !waiter.waiting {
		return false, false
	}
	list := waiter.storage.waiters[waiter.index]
	for i, candidate := range list {
		if candidate == waiter {
			copy(list[i:], list[i+1:])
			list[len(list)-1] = nil
			list = list[:len(list)-1]
			if len(list) == 0 {
				delete(waiter.storage.waiters, waiter.index)
			} else {
				waiter.storage.waiters[waiter.index] = list
			}
			break
		}
	}
	waiter.waiting = false
	waiter.result = result
	if waiter.timer != nil {
		waiter.timer.Stop()
		waiter.timer = nil
	}
	if waiter.id == 0 {
		close(waiter.ready)
		return true, deliver
	}
	if deliver {
		if queue := waiter.queue.Value(); queue != nil {
			if owner != nil && owner.jobQueue == queue {
				owner.resolveAtomicWait(waiter.id, result)
			} else {
				queue.push(runtimeJob{waitID: waiter.id, waitResult: result})
			}
			return true, true
		}
	}
	return true, false
}
func (waiter *atomicWaiter) cancel(reason string) {
	waiter.storage.waitMu.Lock()
	finished, _ := waiter.finishLocked(reason, false, nil)
	waiter.storage.waitMu.Unlock()
	if finished {
		waiter.agent.remove(waiter)
	}
}
func (waiter *atomicWaiter) scheduleLocked() {
	if !waiter.waiting || math.IsInf(waiter.timeout, 1) {
		return
	}
	remaining := waiter.timeout - float64(time.Since(waiter.started))/float64(time.Millisecond)
	// Chunk long finite deadlines without overflowing time.Duration.
	duration := time.Duration(max(0, min(remaining, float64(24*time.Hour)/float64(time.Millisecond))) * float64(time.Millisecond))
	waiter.timer = time.AfterFunc(duration, waiter.expire)
}
func (waiter *atomicWaiter) expire() {
	storage := waiter.storage
	storage.waitMu.Lock()
	if !waiter.waiting {
		storage.waitMu.Unlock()
		return
	}
	remaining := waiter.timeout - float64(time.Since(waiter.started))/float64(time.Millisecond)
	if remaining > 0 {
		waiter.scheduleLocked()
		storage.waitMu.Unlock()
		return
	}
	finished, _ := waiter.finishLocked("timed-out", true, nil)
	storage.waitMu.Unlock()
	if finished {
		waiter.agent.remove(waiter)
	}
}
func (r *Runtime) atomicWaitResult(async bool, value Value) Value {
	object := r.NewObject()
	createDataPropertyOrThrow(object, asciiString("async"), r.toBoolean(async))
	createDataPropertyOrThrow(object, asciiString("value"), value)
	return object
}
func (r *Runtime) atomicWait(call FunctionCall, async bool) Value {
	array := r.atomicArray(call.Argument(0), true)
	storage := array.viewedArrayBuf.shared
	if storage == nil {
		panic(r.NewTypeError("Atomics.wait requires shared memory"))
	}
	length := array.lengthValue()
	position := r.toIndex(call.Argument(1))
	if position >= length {
		panic(r.newError(r.getRangeError(), "Atomics wait index out of bounds"))
	}
	index := (array.offset + position) * array.elemSize
	expected := sharedElementBits(typedArrayElementName(array.typedArray), typedArrayNumericValue(array, call.Argument(2))) & sharedMask(array.elemSize)
	timeout := call.Argument(3).ToNumber().ToFloat()
	if math.IsNaN(timeout) || math.IsInf(timeout, 1) {
		timeout = math.Inf(1)
	} else {
		timeout = max(timeout, 0)
	}
	r.vm.checkInterrupt()
	var capability *promiseCapability
	if async {
		capability = r.newPromiseCapability(r.getPromise())
	}
	agent := r.atomicWaitAgent()
	storage.waitMu.Lock()
	actual := nativeSharedBits(storage.load(index, array.elemSize), array.elemSize)
	if actual != expected {
		storage.waitMu.Unlock()
		if async {
			return r.atomicWaitResult(false, asciiString("not-equal"))
		}
		return asciiString("not-equal")
	}
	if async && timeout == 0 {
		storage.waitMu.Unlock()
		return r.atomicWaitResult(false, asciiString("timed-out"))
	}
	waiter := &atomicWaiter{storage: storage, index: index, agent: agent, ready: make(chan struct{}), waiting: true, timeout: timeout, started: time.Now()}
	if async {
		r.waitSequence++
		waiter.id = r.waitSequence
		waiter.queue = weak.Make(r.jobQueue)
		if r.waitPromises == nil {
			r.waitPromises = make(map[uint64]*promiseCapability)
		}
		r.waitPromises[waiter.id] = capability
	}
	if storage.waiters == nil {
		storage.waiters = make(map[int][]*atomicWaiter)
	}
	storage.waiters[index] = append(storage.waiters[index], waiter)
	agent.add(waiter)
	waiter.scheduleLocked()
	if !async {
		r.blockingWait.Store(waiter)
	}
	storage.waitMu.Unlock()
	if async {
		return r.atomicWaitResult(true, capability.promise)
	}
	defer func() { r.blockingWait.CompareAndSwap(waiter, nil); waiter.cancel("abandoned") }()
	r.vm.checkInterrupt()
	<-waiter.ready
	r.vm.checkInterrupt()
	if waiter.result == "interrupted" {
		r.vm.throwInterrupt()
	}
	return asciiString(waiter.result)
}
func (r *Runtime) atomicNotify(call FunctionCall) Value {
	array, index := r.atomicAccess(call.Argument(0), call.Argument(1), true)
	count := int64(math.MaxInt64)
	if !IsUndefined(call.Argument(2)) {
		count = max(call.Argument(2).ToInteger(), 0)
	}
	storage := array.viewedArrayBuf.shared
	if storage == nil {
		return _positiveZero
	}
	storage.waitMu.Lock()
	var removed []*atomicWaiter
	notified := int64(0)
	for notified < count {
		list := storage.waiters[index]
		if len(list) == 0 {
			break
		}
		waiter := list[0]
		_, delivered := waiter.finishLocked("ok", true, r)
		removed = append(removed, waiter)
		if delivered {
			notified++
		}
	}
	storage.waitMu.Unlock()
	for _, waiter := range removed {
		waiter.agent.remove(waiter)
	}
	return intToValue(notified)
}
