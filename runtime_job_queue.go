package goya

import "sync"

// The owner invokes callbacks. Other Go agents/timers enqueue only a numeric
// wait identity and a primitive result; they never access promise capabilities.
type runtimeJob struct {
	run        func()
	waitID     uint64
	waitResult string
}
type runtimeJobQueue struct {
	sync.Mutex
	entries []runtimeJob
	head    int
}

func (queue *runtimeJobQueue) push(job runtimeJob) {
	queue.Lock()
	queue.entries = append(queue.entries, job)
	queue.Unlock()
}
func (queue *runtimeJobQueue) pop() (runtimeJob, bool) {
	queue.Lock()
	defer queue.Unlock()
	if queue.head == len(queue.entries) {
		return runtimeJob{}, false
	}
	job := queue.entries[queue.head]
	queue.entries[queue.head] = runtimeJob{}
	queue.head++
	if queue.head == len(queue.entries) {
		queue.entries = nil
		queue.head = 0
	}
	return job, true
}
func (queue *runtimeJobQueue) clear() {
	queue.Lock()
	queue.entries = nil
	queue.head = 0
	queue.Unlock()
}
func (r *Runtime) resolveAtomicWait(id uint64, result string) {
	capability := r.waitPromises[id]
	if capability == nil {
		return
	}
	delete(r.waitPromises, id)
	capability.resolve(asciiString(result))
}
