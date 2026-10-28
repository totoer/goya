// Promise/iterator algorithms adapted from the pinned TypeScript helpers.
// See internal/tsfrontend/LICENSE.typescript (Apache-2.0).
package goya

import "github.com/totoer/goya/unistring"

type tsAwaitObject struct {
	baseObject
	resumeMethod string
}

func (r *Runtime) tsObjectRest(c FunctionCall) Value {
	target := r.NewObject()
	source := c.Argument(0)
	if IsNull(source) || IsUndefined(source) {
		return target
	}
	o := source.ToObject(r)
	excluded := c.Argument(1).ToObject(r)
	for item, next := o.self.iterateKeys()(); next != nil; item, next = next() {
		key := item.name
		found := false
		for i, n := int64(0), toLength(excluded.Get("length")); i < n; i++ {
			if key.StrictEquals(nilSafe(excluded.GetIdx(i))) {
				found = true
				break
			}
		}
		if !found {
			desc := o.getOwnProp(key)
			if desc == nil {
				continue
			}
			if property, ok := desc.(*valueProperty); ok && !property.enumerable {
				continue
			}
			target.defineOwnProperty(key, PropertyDescriptor{Value: nilSafe(o.get(key, nil)), Writable: FLAG_TRUE, Configurable: FLAG_TRUE, Enumerable: FLAG_TRUE}, true)
		}
	}
	return target
}

func (r *Runtime) tsSuperIndex(c FunctionCall) Value {
	get, set := c.Argument(0), c.Argument(1)
	cache := r.CreateObject(nil)
	return r.newNativeFunc(func(c FunctionCall) Value {
		key := toPropertyKey(c.Argument(0))
		if entry := cache.get(key, nil); entry != nil {
			return entry
		}
		entry := r.NewObject()
		entry.defineOwnProperty(asciiString("value"), PropertyDescriptor{Getter: r.newNativeFunc(func(FunctionCall) Value { return r.moduleCall(get, _undefined, key) }, "get value", 0), Setter: r.newNativeFunc(func(c FunctionCall) Value { return r.moduleCall(set, _undefined, key, c.Argument(0)) }, "set value", 1), Enumerable: FLAG_TRUE, Configurable: FLAG_TRUE}, true)
		cache.setOwn(key, entry, true)
		return entry
	}, "", 1)
}

func (r *Runtime) tsAwaitValue(value Value, proto *Object) *Object {
	o := &Object{runtime: r}
	b := &tsAwaitObject{baseObject: baseObject{val: o, class: classObject, prototype: proto, extensible: true}}
	o.self = b
	b.init()
	b._putProp("v", value, true, true, true)
	return o
}

func (r *Runtime) tsAwaitHelper() Value {
	proto := r.NewObject()
	return r.newNativeFuncAndConstruct(nil, func(c FunctionCall) Value {
		return r.tsAwaitValue(c.Argument(0), proto)
	}, func(args []Value, target *Object) *Object {
		return r.tsAwaitValue(FunctionCall{Arguments: args}.Argument(0), r.getPrototypeFromCtor(target, nil, proto))
	}, proto, "__await", intToValue(1)).val
}

func (r *Runtime) tsThen(value Value, fulfilled, rejected func(Value)) {
	p := r.promiseResolve(r.getPromise(), value)
	r.performPromiseThen(p.self.(*Promise), r.newNativeFunc(func(c FunctionCall) Value {
		fulfilled(c.Argument(0))
		return _undefined
	}, "", 1), r.newNativeFunc(func(c FunctionCall) Value {
		rejected(c.Argument(0))
		return _undefined
	}, "", 1), nil)
}

func (r *Runtime) tsGeneratorApply(call FunctionCall, index int) Value {
	var args []Value
	if v := call.Argument(1); !IsUndefined(v) && !IsNull(v) {
		o := v.ToObject(r)
		for i, n := int64(0), toLength(o.Get("length")); i < n; i++ {
			args = append(args, nilSafe(o.GetIdx(i)))
		}
	}
	return r.moduleCall(call.Argument(index), call.Argument(0), args...)
}

func (r *Runtime) tsAwaiter(call FunctionCall) Value {
	ctor := r.getPromise()
	if v := call.Argument(2); v.ToBoolean() {
		ctor = r.toObject(v)
	}
	cap := r.newPromiseCapability(ctor)
	cap.try(func() {
		g := r.tsGeneratorApply(call, 3).ToObject(r)
		var resume func(string, Value)
		resume = func(method string, value Value) {
			cap.try(func() {
				step := r.toObject(r.moduleCall(g.Get(method), g, value))
				if step.Get("done").ToBoolean() {
					cap.resolve(nilSafe(step.Get("value")))
					return
				}
				adopted := r.newPromiseCapability(ctor)
				adopted.resolve(nilSafe(step.Get("value")))
				r.moduleCall(adopted.promise.Get("then"), adopted.promise,
					r.newNativeFunc(func(c FunctionCall) Value { resume("next", c.Argument(0)); return _undefined }, "", 1),
					r.newNativeFunc(func(c FunctionCall) Value { resume("throw", c.Argument(0)); return _undefined }, "", 1))
			})
		}
		resume("next", _undefined)
	})
	return cap.promise
}

type tsAsyncRequest struct {
	method string
	value  Value
	cap    *promiseCapability
}

type tsAsyncGenerator struct {
	r     *Runtime
	g     *Object
	queue []*tsAsyncRequest
}

func (g *tsAsyncGenerator) settle(value Value, reject bool) {
	request := g.queue[0]
	if reject {
		request.cap.reject(value)
	} else {
		request.cap.resolve(value)
	}
	g.queue[0] = nil
	g.queue = g.queue[1:]
	if len(g.queue) != 0 {
		g.start()
	}
}

func (g *tsAsyncGenerator) resume(method string, value Value) {
	r := g.r
	ex := r.vm.try(func() {
		step := r.toObject(r.moduleCall(g.g.Get(method), g.g, value))
		v := nilSafe(step.Get("value"))
		if o, ok := v.(*Object); ok {
			if marker, ok := o.self.(*tsAwaitObject); ok {
				method := "next"
				if marker.resumeMethod != "" {
					method = marker.resumeMethod
				}
				r.tsThen(nilSafe(o.Get("v")), func(v Value) { g.resume(method, v) }, func(v Value) { g.resume("throw", v) })
				return
			}
		}
		g.settle(step, false)
	})
	if ex != nil {
		g.settle(ex.val, true)
	}
}

func (g *tsAsyncGenerator) start() {
	request := g.queue[0]
	if request.method == "return" {
		g.r.tsThen(request.value, func(v Value) { g.resume("return", v) }, func(v Value) { g.resume("throw", v) })
	} else {
		g.resume(request.method, request.value)
	}
}

func (r *Runtime) tsAsyncGenerator(call FunctionCall) Value {
	g := &tsAsyncGenerator{r: r, g: r.toObject(r.tsGeneratorApply(call, 2))}
	proto := r.getTSAsyncGeneratorPrototype()
	if owner, ok := call.Argument(3).(*Object); ok {
		if p, ok := owner.Get("prototype").(*Object); ok {
			proto = p
		}
	}
	i := r.CreateObject(proto)
	// The private brand makes borrowed prototype methods reject other objects.
	branded := &tsAsyncGeneratorObject{baseObject: *i.self.(*baseObject), generator: g}
	i.self = branded
	return i
}

type tsAsyncGeneratorObject struct {
	baseObject
	generator *tsAsyncGenerator
}

func (r *Runtime) tsAsyncGeneratorVerb(method string, c FunctionCall) Value {
	cap := r.newPromiseCapability(r.getPromise())
	cap.try(func() {
		o, ok := c.This.(*Object)
		if !ok {
			panic(r.NewTypeError("AsyncGenerator method called on incompatible receiver"))
		}
		g, ok := o.self.(*tsAsyncGeneratorObject)
		if !ok {
			panic(r.NewTypeError("AsyncGenerator method called on incompatible receiver"))
		}
		g.generator.queue = append(g.generator.queue, &tsAsyncRequest{method: method, value: c.Argument(0), cap: cap})
		if len(g.generator.queue) == 1 {
			g.generator.start()
		}
	})
	return cap.promise
}

func (r *Runtime) tsAsyncDelegator(call FunctionCall) Value {
	o := r.toObject(call.Argument(0))
	i := r.NewObject()
	pending := false
	for _, name := range []string{"next", "throw", "return"} {
		if value := nilSafe(o.Get(name)); !IsUndefined(value) && !IsNull(value) {
			i.self.setOwnStr(unistring.NewFromString(name), r.newNativeFunc(func(c FunctionCall) Value {
				pending = !pending
				if pending {
					marker := r.tsAwaitValue(r.moduleCall(o.Get(name), o, c.Argument(0)), r.typeScriptHelper("__await").ToObject(r).Get("prototype").ToObject(r))
					if name == "return" {
						marker.self.(*tsAwaitObject).resumeMethod = "return"
					}
					return r.createIterResultObject(marker, false)
				}
				if name == "throw" {
					panic(c.Argument(0))
				}
				return c.Argument(0)
			}, "", 1), true)
		} else if name == "throw" {
			i.self.setOwnStr("throw", r.newNativeFunc(func(c FunctionCall) Value {
				pending = !pending
				if !pending {
					panic(c.Argument(0))
				}
				cap := r.newPromiseCapability(r.getPromise())
				cap.try(func() {
					if method := toMethod(o.Get("return")); method != nil {
						result := method(FunctionCall{This: o})
						r.tsThen(result, func(v Value) {
							cap.try(func() { r.toObject(v); panic(r.NewTypeError("The iterator does not provide a 'throw' method")) })
						}, cap.reject)
					} else {
						cap.reject(r.NewTypeError("The iterator does not provide a 'throw' method"))
					}
				})
				return r.createIterResultObject(r.tsAwaitValue(cap.promise, r.typeScriptHelper("__await").ToObject(r).Get("prototype").ToObject(r)), false)
			}, "", 1), true)
		}
	}
	i.self.setOwnSym(SymIterator, r.newNativeFunc(func(c FunctionCall) Value { return c.This }, "[Symbol.iterator]", 0), true)
	return i
}

func (r *Runtime) tsAsyncValues(call FunctionCall) Value {
	value := call.Argument(0)
	if method := toMethod(r.getV(value, SymAsyncIterator)); method != nil {
		return r.toObject(method(FunctionCall{This: value}))
	}
	o := r.getIterator(value, nil).iterator
	i := r.NewObject()
	for _, name := range []string{"next", "throw", "return"} {
		if method := nilSafe(o.Get(name)); !IsUndefined(method) && !IsNull(method) {
			i.self.setOwnStr(unistring.NewFromString(name), r.newNativeFunc(func(c FunctionCall) Value {
				cap := r.newPromiseCapability(r.getPromise())
				cap.try(func() {
					step := r.toObject(r.moduleCall(o.Get(name), o, c.Argument(0)))
					done := iteratorComplete(step)
					r.tsThen(iteratorValue(step), func(v Value) { cap.resolve(r.createIterResultObject(v, done)) }, cap.reject)
				})
				return cap.promise
			}, "", 1), true)
		}
	}
	i.self.setOwnSym(SymAsyncIterator, r.newNativeFunc(func(c FunctionCall) Value { return c.This }, "[Symbol.asyncIterator]", 0), true)
	return i
}

type tsAsyncGeneratorFunctionObject struct{ methodFuncObject }

type newTSNormalizedAsyncFunction struct{ newMethod }

func (n *newTSNormalizedAsyncFunction) exec(vm *vm) {
	f := vm.r.newMethod(n.name, n.length, n.strict)
	f.prototype = vm.r.getAsyncFunctionPrototype()
	n._exec(vm, f)
}

type newTSNormalizedAsyncArrow struct{ newArrowFunc }

func (n *newTSNormalizedAsyncArrow) exec(vm *vm) {
	f := vm.r.newArrowFunc(n.name, n.length, n.strict)
	f.prototype = vm.r.getAsyncFunctionPrototype()
	n._exec(vm, f)
}

type newTSAsyncGeneratorFunction struct{ newMethod }

func (n *newTSAsyncGeneratorFunction) exec(vm *vm) {
	f := &tsAsyncGeneratorFunctionObject{}
	vm.r.initBaseJsFunction(&f.baseJsFuncObject, n.strict)
	f.val.self = f
	f.prototype = vm.r.getTSAsyncGeneratorFunctionPrototype()
	f.init(n.name, intToValue(int64(n.length)))
	f._putProp("prototype", vm.r.CreateObject(vm.r.getTSAsyncGeneratorPrototype()), true, false, false)
	n._exec(vm, &f.methodFuncObject)
}

func (r *Runtime) getTSAsyncIteratorPrototype() *Object {
	if r.global.TSAsyncIteratorPrototype == nil {
		o := r.NewObject()
		o.self._putSym(SymAsyncIterator, valueProp(r.newNativeFunc(func(c FunctionCall) Value { return c.This }, "[Symbol.asyncIterator]", 0), true, false, true))
		o.self._putSym(SymAsyncDispose, valueProp(r.newNativeFunc(r.asyncIteratorDispose, "[Symbol.asyncDispose]", 0), true, false, true))
		r.global.TSAsyncIteratorPrototype = o
	}
	return r.global.TSAsyncIteratorPrototype
}

func (r *Runtime) getTSAsyncGeneratorPrototype() *Object {
	if r.global.TSAsyncGeneratorPrototype == nil {
		o := r.CreateObject(r.getTSAsyncIteratorPrototype())
		r.global.TSAsyncGeneratorPrototype = o
		for _, method := range []string{"next", "throw", "return"} {
			o.self._putProp(unistring.NewFromString(method), r.newNativeFunc(func(c FunctionCall) Value { return r.tsAsyncGeneratorVerb(method, c) }, unistring.NewFromString(method), 1), true, false, true)
		}
		o.self._putSym(SymToStringTag, valueProp(asciiString("AsyncGenerator"), false, false, true))
		o.self._putProp("constructor", r.getTSAsyncGeneratorFunctionPrototype(), false, false, true)
	}
	return r.global.TSAsyncGeneratorPrototype
}

func (r *Runtime) getTSAsyncGeneratorFunctionPrototype() *Object {
	if r.global.TSAsyncGeneratorFunctionPrototype == nil {
		o := r.CreateObject(r.getFunctionPrototype())
		r.global.TSAsyncGeneratorFunctionPrototype = o
		o.self._putSym(SymToStringTag, valueProp(asciiString("AsyncGeneratorFunction"), false, false, true))
		o.self._putProp("prototype", r.getTSAsyncGeneratorPrototype(), false, false, true)
		o.self._putProp("constructor", r.getTSAsyncGeneratorFunction(), false, false, true)
	}
	return r.global.TSAsyncGeneratorFunctionPrototype
}

func (r *Runtime) getTSAsyncGeneratorFunction() *Object {
	if r.global.TSAsyncGeneratorFunction == nil {
		o := &Object{runtime: r}
		r.global.TSAsyncGeneratorFunction = o
		o.self = r.newNativeFuncConstructObj(o, func(args []Value, proto *Object) *Object { return r.functionCtor(args, proto, true, true) }, "AsyncGeneratorFunction", r.getTSAsyncGeneratorFunctionPrototype(), 1)
	}
	return r.global.TSAsyncGeneratorFunction
}
