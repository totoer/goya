// Resource helper algorithms adapted from the pinned TypeScript helpers.
// See internal/tsfrontend/LICENSE.typescript (Apache-2.0).
package goya

func (r *Runtime) getSuppressedError() *Object {
	if r.global.SuppressedError == nil {
		o := &Object{runtime: r}
		r.global.SuppressedError = o
		r.newNativeFuncConstructProto(o, func(args []Value, proto *Object) *Object {
			c := FunctionCall{Arguments: args}
			e := r.newErrorObject(proto, classError)
			if !IsUndefined(c.Argument(2)) {
				e._putProp("message", c.Argument(2).toString(), true, false, true)
			}
			e._putProp("error", c.Argument(0), true, false, true)
			e._putProp("suppressed", c.Argument(1), true, false, true)
			return e.val
		}, "SuppressedError", r.createErrorPrototype(asciiString("SuppressedError"), o), r.getError(), 3)
	}
	return r.global.SuppressedError
}

func (r *Runtime) tsAddDisposableResource(c FunctionCall) Value {
	env, value, async := c.Argument(0).ToObject(r), c.Argument(1), c.Argument(2).ToBoolean()
	if IsNull(value) || IsUndefined(value) {
		if async {
			entry := r.NewObject()
			r.tsSet(entry, "async", valueTrue)
			r.tsAppend(env.Get("stack"), entry, false)
		}
		return value
	}
	o, ok := value.(*Object)
	if !ok {
		panic(r.NewTypeError("Object expected"))
	}
	method := Value(_undefined)
	if async {
		method = nilSafe(o.GetSymbol(SymAsyncDispose))
	}
	if IsUndefined(method) {
		method = nilSafe(o.GetSymbol(SymDispose))
		if async {
			inner := r.tsAcceptFunction(method)
			if IsUndefined(inner) {
				panic(r.NewTypeError("Object not disposable"))
			}
			method = r.newNativeFunc(func(c FunctionCall) Value {
				if ex := r.vm.try(func() { r.moduleCall(inner, c.This) }); ex != nil {
					cap := r.newPromiseCapability(r.getPromise())
					cap.reject(ex.val)
					return cap.promise
				}
				// Sync disposal results, including thenables, are ignored.
				return _undefined
			}, "", 0)
		}
	}
	method = r.tsAcceptFunction(method)
	if IsUndefined(method) {
		panic(r.NewTypeError("Object not disposable"))
	}
	entry := r.NewObject()
	r.tsSet(entry, "value", value)
	r.tsSet(entry, "dispose", method)
	r.tsSet(entry, "async", r.toBoolean(async))
	r.tsAppend(env.Get("stack"), entry, false)
	return value
}

func (r *Runtime) tsChain(value Value, fulfilled func(Value) Value, rejected func(Value) Value) Value {
	cap := r.newPromiseCapability(r.getPromise())
	p := r.promiseResolve(r.getPromise(), value)
	var f, g Value = _undefined, _undefined
	if fulfilled != nil {
		f = r.newNativeFunc(func(c FunctionCall) Value { return fulfilled(c.Argument(0)) }, "", 1)
	}
	if rejected != nil {
		g = r.newNativeFunc(func(c FunctionCall) Value { return rejected(c.Argument(0)) }, "", 1)
	}
	return r.performPromiseThen(p.self.(*Promise), f, g, cap)
}

func (r *Runtime) tsDisposeResources(c FunctionCall) Value {
	env := c.Argument(0).ToObject(r)
	stack := env.Get("stack").ToObject(r)
	state := 0
	fail := func(reason Value) {
		if nilSafe(env.Get("hasError")).ToBoolean() {
			reason = r.builtin_new(r.getSuppressedError(), []Value{reason, nilSafe(env.Get("error")), asciiString("An error was suppressed during disposal.")})
		}
		r.tsSet(env, "error", reason)
		r.tsSet(env, "hasError", valueTrue)
	}
	var next func() Value
	next = func() Value {
		for toLength(stack.Get("length")) > 0 {
			index := toLength(stack.Get("length")) - 1
			entry := stack.GetIdx(index).ToObject(r)
			stack.self.deleteIdx(valueInt(index), true)
			r.tsSet(stack, "length", intToValue(index))
			async := nilSafe(entry.Get("async")).ToBoolean()
			if !async && state == 1 {
				state = 0
				r.tsAppend(stack, entry, false)
				return r.tsChain(_undefined, func(Value) Value { return next() }, nil)
			}
			method := nilSafe(entry.Get("dispose"))
			var result Value
			if ex := r.vm.try(func() {
				if !IsUndefined(method) {
					result = r.moduleCall(method, nilSafe(entry.Get("value")))
				}
			}); ex != nil {
				fail(ex.val)
				continue
			}
			if !IsUndefined(method) {
				if async {
					state |= 2
					return r.tsChain(result, func(Value) Value { return next() }, func(v Value) Value { fail(v); return next() })
				}
			} else {
				state |= 1
			}
		}
		if state == 1 {
			cap := r.newPromiseCapability(r.getPromise())
			if nilSafe(env.Get("hasError")).ToBoolean() {
				cap.reject(nilSafe(env.Get("error")))
			} else {
				cap.resolve(_undefined)
			}
			return cap.promise
		}
		if nilSafe(env.Get("hasError")).ToBoolean() {
			panic(nilSafe(env.Get("error")))
		}
		return _undefined
	}
	return next()
}

func (r *Runtime) iteratorDispose(c FunctionCall) Value {
	if method := toMethod(r.toObject(c.This).Get("return")); method != nil {
		method(FunctionCall{This: c.This})
	}
	return _undefined
}

func (r *Runtime) asyncIteratorDispose(c FunctionCall) Value {
	cap := r.newPromiseCapability(r.getPromise())
	cap.try(func() {
		if method := toMethod(r.toObject(c.This).Get("return")); method != nil {
			r.tsThen(method(FunctionCall{This: c.This}), func(Value) { cap.resolve(_undefined) }, cap.reject)
		} else {
			cap.resolve(_undefined)
		}
	})
	return cap.promise
}
