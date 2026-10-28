package goya

import "github.com/totoer/goya/unistring"

type stackResource struct {
	value   Value
	dispose func(FunctionCall) Value
}
type disposableStackObject struct {
	baseObject
	async, disposed bool
	resources       []stackResource
}

func (r *Runtime) newDisposableStack(proto *Object, async bool) *disposableStackObject {
	o := &Object{runtime: r}
	s := &disposableStackObject{baseObject: baseObject{val: o, prototype: proto, class: classObject, extensible: true}, async: async}
	o.self = s
	s.init()
	return s
}
func (r *Runtime) stackReceiver(value Value, async bool) *disposableStackObject {
	if o, ok := value.(*Object); ok {
		if s, ok := o.self.(*disposableStackObject); ok && s.async == async {
			return s
		}
	}
	panic(r.NewTypeError("incompatible disposable stack receiver"))
}
func (s *disposableStackObject) pending() {
	if s.disposed {
		panic(s.val.runtime.newError(s.val.runtime.getReferenceError(), "disposable stack is already disposed"))
	}
}

func (r *Runtime) stackUse(call FunctionCall, async bool) Value {
	s := r.stackReceiver(call.This, async)
	s.pending()
	value := call.Argument(0)
	if IsNull(value) || IsUndefined(value) {
		if async {
			s.resources = append(s.resources, stackResource{value: _undefined})
		}
		return value
	}
	o := r.toObject(value)
	var method func(FunctionCall) Value
	if async {
		method = toMethod(o.self.getSym(SymAsyncDispose, nil))
	}
	if method == nil {
		method = toMethod(o.self.getSym(SymDispose, nil))
		if method != nil && async {
			syncMethod := method
			method = func(call FunctionCall) Value {
				if ex := r.vm.try(func() { syncMethod(call) }); ex != nil {
					cap := r.newPromiseCapability(r.getPromise())
					cap.reject(ex.val)
					return cap.promise
				}
				return _undefined
			}
		}
	}
	if method == nil {
		panic(r.NewTypeError("resource has no disposal method"))
	}
	s.resources = append(s.resources, stackResource{value: value, dispose: method})
	return value
}

func (r *Runtime) stackAdopt(call FunctionCall, async bool) Value {
	s := r.stackReceiver(call.This, async)
	s.pending()
	value := call.Argument(0)
	method := r.toCallable(call.Argument(1))
	s.resources = append(s.resources, stackResource{value: _undefined, dispose: func(FunctionCall) Value { return method(FunctionCall{This: _undefined, Arguments: []Value{value}}) }})
	return value
}
func (r *Runtime) stackDefer(call FunctionCall, async bool) Value {
	s := r.stackReceiver(call.This, async)
	s.pending()
	method := r.toCallable(call.Argument(0))
	s.resources = append(s.resources, stackResource{value: _undefined, dispose: method})
	return _undefined
}
func (r *Runtime) stackMove(call FunctionCall, async bool) Value {
	s := r.stackReceiver(call.This, async)
	s.pending()
	ctor := r.getDisposableStack(async)
	next := r.newDisposableStack(r.toObject(ctor.Get("prototype")), async)
	next.resources = s.resources
	s.resources = nil
	s.disposed = true
	return next.val
}

type stackDisposal struct {
	r                      *Runtime
	resources              []stackResource
	cap                    *promiseCapability
	error                  Value
	failed, awaited, dummy bool
}

func (s *stackDisposal) fail(reason Value) {
	if s.failed {
		reason = s.r.builtin_new(s.r.getSuppressedError(), []Value{reason, s.error, asciiString("An error was suppressed during disposal.")})
	}
	s.error = reason
	s.failed = true
}
func (s *stackDisposal) finish() {
	if s.failed {
		s.cap.reject(s.error)
	} else {
		s.cap.resolve(_undefined)
	}
}
func (s *stackDisposal) next() {
	for len(s.resources) > 0 {
		i := len(s.resources) - 1
		record := s.resources[i]
		s.resources[i] = stackResource{}
		s.resources = s.resources[:i]
		if record.dispose == nil {
			s.dummy = true
			continue
		}
		var result Value
		if ex := s.r.vm.try(func() { result = record.dispose(FunctionCall{This: record.value}) }); ex != nil {
			s.fail(ex.val)
			continue
		}
		s.awaited = true
		if ex := s.r.vm.try(func() { s.r.tsThen(result, func(Value) { s.next() }, func(reason Value) { s.fail(reason); s.next() }) }); ex != nil {
			s.fail(ex.val)
			continue
		}
		return
	}
	if s.dummy && !s.awaited {
		s.awaited = true
		if ex := s.r.vm.try(func() {
			s.r.tsThen(_undefined, func(Value) { s.finish() }, func(reason Value) { s.fail(reason); s.finish() })
		}); ex != nil {
			s.fail(ex.val)
			s.finish()
		}
		return
	}
	s.finish()
}
func (r *Runtime) stackDispose(call FunctionCall, async bool) Value {
	if async {
		cap := r.newPromiseCapability(r.getPromise())
		cap.try(func() {
			s := r.stackReceiver(call.This, true)
			if s.disposed {
				cap.resolve(_undefined)
				return
			}
			s.disposed = true
			state := &stackDisposal{r: r, resources: s.resources, cap: cap}
			s.resources = nil
			state.next()
		})
		return cap.promise
	}
	s := r.stackReceiver(call.This, false)
	if s.disposed {
		return _undefined
	}
	s.disposed = true
	state := &stackDisposal{r: r, resources: s.resources}
	s.resources = nil
	for i := len(state.resources) - 1; i >= 0; i-- {
		record := state.resources[i]
		state.resources[i] = stackResource{}
		if ex := r.vm.try(func() { record.dispose(FunctionCall{This: record.value}) }); ex != nil {
			state.fail(ex.val)
		}
	}
	if state.failed {
		panic(state.error)
	}
	return _undefined
}

func (r *Runtime) getDisposableStack(async bool) *Object {
	slot := &r.global.DisposableStack
	name := "DisposableStack"
	disposeName := "dispose"
	symbol := SymDispose
	if async {
		slot = &r.global.AsyncDisposableStack
		name = "AsyncDisposableStack"
		disposeName = "disposeAsync"
		symbol = SymAsyncDispose
	}
	if *slot != nil {
		return *slot
	}
	ctor := &Object{runtime: r}
	*slot = ctor
	proto := r.NewObject()
	r.newNativeConstructOnly(ctor, func(_ []Value, newTarget *Object) *Object {
		if newTarget == nil {
			panic(r.NewTypeError("%s requires new", name))
		}
		return r.newDisposableStack(r.getPrototypeFromCtor(newTarget, ctor, proto), async).val
	}, proto, unistring.NewFromString(name), 0)
	proto.self._putProp("constructor", ctor, true, false, true)
	proto.self._putProp("use", r.newNativeFunc(func(c FunctionCall) Value { return r.stackUse(c, async) }, "use", 1), true, false, true)
	proto.self._putProp("adopt", r.newNativeFunc(func(c FunctionCall) Value { return r.stackAdopt(c, async) }, "adopt", 2), true, false, true)
	proto.self._putProp("defer", r.newNativeFunc(func(c FunctionCall) Value { return r.stackDefer(c, async) }, "defer", 1), true, false, true)
	proto.self._putProp("move", r.newNativeFunc(func(c FunctionCall) Value { return r.stackMove(c, async) }, "move", 0), true, false, true)
	dispose := r.newNativeFunc(func(c FunctionCall) Value { return r.stackDispose(c, async) }, unistring.NewFromString(disposeName), 0)
	proto.self._putProp(unistring.NewFromString(disposeName), dispose, true, false, true)
	proto.self._putSym(symbol, valueProp(dispose, true, false, true))
	proto.self._putSym(SymToStringTag, valueProp(newStringValue(name), false, false, true))
	proto.self.defineOwnPropertyStr("disposed", PropertyDescriptor{Getter: r.newNativeFunc(func(c FunctionCall) Value { return r.toBoolean(r.stackReceiver(c.This, async).disposed) }, "get disposed", 0), Configurable: FLAG_TRUE}, true)
	return ctor
}
