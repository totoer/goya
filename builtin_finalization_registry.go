package goya

import (
	"errors"
	"runtime"
	"sync"
	"weak"
)

// GC callbacks only append weak identities here. They never enter the VM,
// inspect registry cells, or retain a registry, its callback, or held values.
type finalizationNotification struct {
	registry weak.Pointer[finalizationRegistryObject]
	id       uint64
}
type finalizationQueue struct {
	sync.Mutex
	notifications []finalizationNotification
	head          int
}

func (q *finalizationQueue) push(n finalizationNotification) {
	q.Lock()
	q.notifications = append(q.notifications, n)
	q.Unlock()
}
func (q *finalizationQueue) pop() (finalizationNotification, bool) {
	q.Lock()
	defer q.Unlock()
	if q.head == len(q.notifications) {
		return finalizationNotification{}, false
	}
	n := q.notifications[q.head]
	q.notifications[q.head] = finalizationNotification{}
	q.head++
	if q.head == len(q.notifications) {
		q.notifications = nil
		q.head = 0
	}
	return n, true
}

type finalizationCell struct {
	held    Value
	token   weakTarget
	cleanup runtime.Cleanup
}
type finalizationRegistryObject struct {
	baseObject
	callback func(FunctionCall) Value
	cells    map[uint64]finalizationCell
	nextID   uint64
}
type finalizationCleanupArg struct {
	queue        *finalizationQueue
	notification finalizationNotification
}

func enqueueFinalization(arg finalizationCleanupArg) { arg.queue.push(arg.notification) }

func (r *Runtime) builtin_newFinalizationRegistry(args []Value, newTarget *Object) *Object {
	if newTarget == nil {
		panic(r.needNew("FinalizationRegistry"))
	}
	callbackValue := _undefined
	if len(args) > 0 {
		callbackValue = args[0]
	}
	callback := r.toCallable(callbackValue)
	prototype := r.getPrototypeFromCtor(newTarget, r.getFinalizationRegistry(), r.getFinalizationRegistryPrototype())
	object := &Object{runtime: r}
	registry := &finalizationRegistryObject{baseObject: baseObject{val: object, prototype: prototype, class: classObject, extensible: true}, callback: callback, cells: make(map[uint64]finalizationCell)}
	object.self = registry
	registry.init()
	return object
}
func (r *Runtime) finalizationReceiver(value Value) *finalizationRegistryObject {
	if object, ok := value.(*Object); ok {
		if registry, ok := object.self.(*finalizationRegistryObject); ok {
			return registry
		}
	}
	panic(r.NewTypeError("incompatible FinalizationRegistry receiver"))
}
func (r *Runtime) finalizationRegister(call FunctionCall) Value {
	registry := r.finalizationReceiver(call.This)
	target, held, token := call.Argument(0), call.Argument(1), call.Argument(2)
	if !r.canBeHeldWeakly(target) {
		panic(r.NewTypeError("invalid FinalizationRegistry target"))
	}
	if target.SameAs(held) {
		panic(r.NewTypeError("FinalizationRegistry target and held value must differ"))
	}
	if !IsUndefined(token) && !r.canBeHeldWeakly(token) {
		panic(r.NewTypeError("invalid FinalizationRegistry unregister token"))
	}
	if r.finalizations == nil {
		r.finalizations = &finalizationQueue{}
	}
	registry.nextID++
	id := registry.nextID
	arg := finalizationCleanupArg{queue: r.finalizations, notification: finalizationNotification{registry: weak.Make(registry), id: id}}
	var cleanup runtime.Cleanup
	switch target := target.(type) {
	case *Object:
		cleanup = runtime.AddCleanup(target, enqueueFinalization, arg)
	case *Symbol:
		cleanup = runtime.AddCleanup(target, enqueueFinalization, arg)
	}
	registry.cells[id] = finalizationCell{held: held, token: makeWeakTarget(token), cleanup: cleanup}
	runtime.KeepAlive(target)
	return _undefined
}
func (r *Runtime) finalizationUnregister(call FunctionCall) Value {
	registry := r.finalizationReceiver(call.This)
	token := call.Argument(0)
	if !r.canBeHeldWeakly(token) {
		panic(r.NewTypeError("invalid FinalizationRegistry unregister token"))
	}
	identity := makeWeakTarget(token)
	removed := false
	for id, cell := range registry.cells {
		if cell.token == identity {
			cell.cleanup.Stop()
			delete(registry.cells, id)
			removed = true
		}
	}
	runtime.KeepAlive(token)
	return r.toBoolean(removed)
}
func (r *Runtime) getFinalizationRegistryPrototype() *Object {
	if r.global.FinalizationRegistryPrototype == nil {
		object := &Object{runtime: r}
		r.global.FinalizationRegistryPrototype = object
		prototype := newBaseObjectObj(object, r.global.ObjectPrototype, classObject)
		prototype._putProp("constructor", r.getFinalizationRegistry(), true, false, true)
		prototype._putProp("register", r.newNativeFunc(r.finalizationRegister, "register", 2), true, false, true)
		prototype._putProp("unregister", r.newNativeFunc(r.finalizationUnregister, "unregister", 1), true, false, true)
		prototype._putSym(SymToStringTag, valueProp(asciiString("FinalizationRegistry"), false, false, true))
	}
	return r.global.FinalizationRegistryPrototype
}
func (r *Runtime) getFinalizationRegistry() *Object {
	if r.global.FinalizationRegistry == nil {
		object := &Object{runtime: r}
		r.global.FinalizationRegistry = object
		r.newNativeConstructOnly(object, r.builtin_newFinalizationRegistry, r.getFinalizationRegistryPrototype(), "FinalizationRegistry", 1)
	}
	return r.global.FinalizationRegistry
}

// RunJobs delivers shared-wait notifications, garbage-collection cleanup callbacks
// and promise continuations on the runtime's owner goroutine. It creates no event
// loop and does not force garbage collection. Call it only while Runtime is
// idle. A cleanup exception is returned; later notifications remain pending.
func (r *Runtime) RunJobs() error {
	if r.drainingCleanup || r.drainingJobs || len(r.vm.callStack) > 0 || r.vm.prg != nil {
		return errors.New("RunJobs requires an idle Runtime")
	}
	r.drainingCleanup = true
	defer func() { r.drainingCleanup = false }()
	for {
		found := false
		err := r.runWrapped(func() {
			r.vm.checkInterrupt()
			if r.finalizations == nil {
				return
			}
			n, ok := r.finalizations.pop()
			if !ok {
				return
			}
			found = true
			registry := n.registry.Value()
			if registry == nil {
				return
			}
			cell, ok := registry.cells[n.id]
			if !ok {
				return
			}
			delete(registry.cells, n.id)
			cell.cleanup.Stop()
			registry.callback(FunctionCall{This: _undefined, Arguments: []Value{cell.held}})
			runtime.KeepAlive(registry)
		})
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
	}
}
