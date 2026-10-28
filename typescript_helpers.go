// Decorator algorithms adapted from the pinned Microsoft TypeScript helpers.
// See internal/tsfrontend/LICENSE.typescript (Apache-2.0).
package goya

import "github.com/totoer/goya/unistring"

type loadTypeScriptHelper string

func (name loadTypeScriptHelper) exec(vm *vm) { vm.push(vm.r.typeScriptHelper(string(name))); vm.pc++ }

func (r *Runtime) typeScriptHelper(name string) Value {
	if value := r.typeScriptHelpers[name]; value != nil {
		return value
	}
	if name == "__await" {
		value := r.tsAwaitHelper()
		if r.typeScriptHelpers == nil {
			r.typeScriptHelpers = make(map[string]Value)
		}
		r.typeScriptHelpers[name] = value
		return value
	}
	var call func(FunctionCall) Value
	length := 0
	switch name {
	case "__esDecorate":
		call = r.tsESDecorate
		length = 6
	case "__runInitializers":
		call = r.tsRunInitializers
		length = 2
	case "__propKey":
		call = func(c FunctionCall) Value {
			value := c.Argument(0)
			if _, ok := value.(*Symbol); ok {
				return value
			}
			return value.toString()
		}
		length = 1
	case "__setFunctionName":
		call = r.tsSetFunctionName
		length = 2
	case "__decorate":
		call = r.tsDecorate
		length = 4
	case "__metadata":
		call = r.tsMetadata
		length = 2
	case "__param":
		call = r.tsParam
		length = 2
	case "__classPrivateFieldGet":
		call = r.tsPrivateGet
		length = 4
	case "__classPrivateFieldSet":
		call = r.tsPrivateSet
		length = 5
	case "__classPrivateFieldIn":
		call = r.tsPrivateIn
		length = 2
	case "__asyncGenerator":
		call = r.tsAsyncGenerator
		length = 3
	case "__asyncDelegator":
		call = r.tsAsyncDelegator
		length = 1
	case "__asyncValues":
		call = r.tsAsyncValues
		length = 1
	case "__awaiter":
		call = r.tsAwaiter
		length = 4
	case "__superIndex":
		call = r.tsSuperIndex
		length = 2
	case "__addDisposableResource":
		call = r.tsAddDisposableResource
		length = 3
	case "__disposeResources":
		call = r.tsDisposeResources
		length = 1
	case "__createBinding":
		call = r.tsCreateBinding
		length = 4
	case "__setModuleDefault":
		call = r.tsSetModuleDefault
		length = 2
	case "__importStar":
		call = r.tsImportStar
		length = 1
	case "__importDefault":
		call = r.tsImportDefault
		length = 1
	case "__exportStar":
		call = r.tsExportStar
		length = 2
	case "__rewriteRelativeImportExtension":
		call = r.tsRewriteImportExtension
		length = 2
	case "__assign":
		call = r.object_assign
		length = 2
	case "__rest":
		call = r.tsObjectRest
		length = 2
	case "__makeTemplateObject":
		call = func(c FunctionCall) Value {
			o := c.Argument(0).ToObject(r)
			o.defineOwnProperty(asciiString("raw"), PropertyDescriptor{Value: c.Argument(1), Writable: FLAG_FALSE, Configurable: FLAG_FALSE, Enumerable: FLAG_FALSE}, true)
			return o
		}
		length = 2
	default:
		panic(r.NewTypeError("unknown internal TypeScript helper %s", name))
	}
	value := r.newNativeFunc(call, unistring.NewFromString(name), length)
	if r.typeScriptHelpers == nil {
		r.typeScriptHelpers = make(map[string]Value)
	}
	r.typeScriptHelpers[name] = value
	return value
}

func (r *Runtime) tsSet(object *Object, name string, value Value) {
	object.self.setOwnStr(unistring.NewFromString(name), nilSafe(value), true)
}
func (r *Runtime) tsAppend(array Value, value Value, front bool) {
	object := array.ToObject(r)
	length := toLength(object.Get("length"))
	if front {
		for i := length; i > 0; i-- {
			object.self.setOwnIdx(valueInt(i), nilSafe(object.GetIdx(i-1)), true)
		}
		object.self.setOwnIdx(0, value, true)
	} else {
		object.self.setOwnIdx(valueInt(length), value, true)
	}
	object.self.setOwnStr("length", intToValue(length+1), true)
}
func (r *Runtime) tsCopyEnumerable(target *Object, source Value) {
	if source == nil || IsNull(source) || IsUndefined(source) {
		return
	}
	object := source.ToObject(r)
	for item, next := enumerateRecursive(object)(); next != nil; item, next = next() {
		key := item.name.string()
		target.self.setOwnStr(key, nilSafe(object.self.getStr(key, nil)), true)
	}
}
func (r *Runtime) tsAcceptFunction(value Value) Value {
	value = nilSafe(value)
	if IsUndefined(value) {
		return value
	}
	if object, ok := value.(*Object); ok {
		if _, ok := object.self.assertCallable(); ok {
			return value
		}
	}
	panic(r.NewTypeError("Function expected"))
}

func (r *Runtime) tsESDecorate(call FunctionCall) Value {
	ctor, descriptorIn := call.Argument(0), call.Argument(1)
	decorators, contextIn := call.Argument(2).ToObject(r), call.Argument(3).ToObject(r)
	initializers, extra := call.Argument(4), call.Argument(5)
	kind := contextIn.Get("kind").String()
	key := "value"
	if kind == "getter" {
		key = "get"
	}
	if kind == "setter" {
		key = "set"
	}
	var target *Object
	if !descriptorIn.ToBoolean() && ctor.ToBoolean() {
		target = ctor.ToObject(r)
		if !contextIn.Get("static").ToBoolean() {
			target = target.Get("prototype").ToObject(r)
		}
	}
	var descriptor *Object
	if descriptorIn.ToBoolean() {
		descriptor = descriptorIn.ToObject(r)
	} else if target != nil {
		value := r.object_getOwnPropertyDescriptor(FunctionCall{Arguments: []Value{target, nilSafe(contextIn.Get("name"))}})
		descriptor = value.ToObject(r)
	} else {
		descriptor = r.NewObject()
	}
	done := false
	for index := toLength(decorators.Get("length")) - 1; index >= 0; index-- {
		context := r.NewObject()
		for item, next := enumerateRecursive(contextIn)(); next != nil; item, next = next() {
			name := item.name.string()
			value := nilSafe(contextIn.self.getStr(name, nil))
			if name == "access" {
				value = r.NewObject()
			}
			context.self.setOwnStr(name, value, true)
		}
		if access := contextIn.Get("access"); access != nil && !IsUndefined(access) && !IsNull(access) {
			r.tsCopyEnumerable(context.Get("access").ToObject(r), access)
		}
		add := r.newNativeFunc(func(c FunctionCall) Value {
			if done {
				panic(r.NewTypeError("Cannot add initializers after decoration has completed"))
			}
			value := c.Argument(0)
			if !value.ToBoolean() {
				value = _null
			}
			r.tsAppend(extra, r.tsAcceptFunction(value), false)
			return _undefined
		}, "addInitializer", 1)
		r.tsSet(context, "addInitializer", add)
		value := nilSafe(descriptor.Get(key))
		if kind == "accessor" {
			pair := r.NewObject()
			r.tsSet(pair, "get", nilSafe(descriptor.Get("get")))
			r.tsSet(pair, "set", nilSafe(descriptor.Get("set")))
			value = pair
		}
		result := r.moduleCall(decorators.GetIdx(index), _undefined, value, context)
		if kind == "accessor" {
			if IsUndefined(result) {
				continue
			}
			object, ok := result.(*Object)
			if !ok {
				panic(r.NewTypeError("Object expected"))
			}
			if _, callable := object.self.assertCallable(); callable {
				panic(r.NewTypeError("Object expected"))
			}
			for _, name := range []string{"get", "set", "init"} {
				function := r.tsAcceptFunction(object.Get(name))
				if !function.ToBoolean() {
					continue
				}
				if name == "init" {
					r.tsAppend(initializers, function, true)
				} else {
					r.tsSet(descriptor, name, function)
				}
			}
		} else {
			function := r.tsAcceptFunction(result)
			if function.ToBoolean() {
				if kind == "field" {
					r.tsAppend(initializers, function, true)
				} else {
					r.tsSet(descriptor, key, function)
				}
			}
		}
	}
	if target != nil {
		r.object_defineProperty(FunctionCall{Arguments: []Value{target, nilSafe(contextIn.Get("name")), descriptor}})
	}
	done = true
	return _undefined
}

func (r *Runtime) tsRunInitializers(call FunctionCall) Value {
	this, array := call.Argument(0), call.Argument(1).ToObject(r)
	value := call.Argument(2)
	useValue := len(call.Arguments) > 2
	for index := int64(0); index < toLength(array.Get("length")); index++ {
		if useValue {
			value = r.moduleCall(array.GetIdx(index), this, value)
		} else {
			value = r.moduleCall(array.GetIdx(index), this)
		}
	}
	if useValue {
		return value
	}
	return _undefined
}

func (r *Runtime) tsSetFunctionName(call FunctionCall) Value {
	function, name, prefix := call.Argument(0).ToObject(r), call.Argument(1), call.Argument(2)
	if symbol, ok := name.(*Symbol); ok {
		if symbol.desc != nil && symbol.desc.Length() > 0 {
			name = asciiString("[").Concat(symbol.desc).Concat(asciiString("]"))
		} else {
			name = asciiString("")
		}
	}
	if prefix.ToBoolean() {
		name = prefix.toString().Concat(asciiString(" ")).Concat(name.toString())
	}
	function.defineOwnProperty(asciiString("name"), PropertyDescriptor{Value: name, Configurable: FLAG_TRUE}, true)
	return function
}

func (r *Runtime) tsReflect() Value {
	for scope := r.vm.stash; scope != nil; scope = scope.outer {
		if scope.module != nil && scope.module.reflectLookup != nil {
			return r.moduleCall(scope.module.reflectLookup, _undefined)
		}
	}
	return nilSafe(r.Get("Reflect"))
}
func (r *Runtime) tsReflectMethod(name string) (*Object, Value) {
	object, ok := r.tsReflect().(*Object)
	if !ok {
		return nil, nil
	}
	if _, callable := object.self.assertCallable(); callable {
		return nil, nil
	}
	method := object.Get(name)
	if function, ok := method.(*Object); ok {
		if _, ok := function.self.assertCallable(); ok {
			return object, method
		}
	}
	return nil, nil
}
func (r *Runtime) tsDecorate(call FunctionCall) Value {
	decorators, target, key, descriptor := call.Argument(0).ToObject(r), call.Argument(1), call.Argument(2), call.Argument(3)
	count := len(call.Arguments)
	result := descriptor
	if count < 3 {
		result = target
	} else if IsNull(descriptor) {
		descriptor = r.object_getOwnPropertyDescriptor(FunctionCall{Arguments: []Value{target, key}})
		result = descriptor
	}
	if object, method := r.tsReflectMethod("decorate"); method != nil {
		result = r.moduleCall(method, object, decorators, target, key, descriptor)
	} else {
		for index := toLength(decorators.Get("length")) - 1; index >= 0; index-- {
			decorator := nilSafe(decorators.GetIdx(index))
			if !decorator.ToBoolean() {
				continue
			}
			var value Value
			if count < 3 {
				value = r.moduleCall(decorator, _undefined, result)
			} else if count > 3 {
				value = r.moduleCall(decorator, _undefined, target, key, result)
			} else {
				value = r.moduleCall(decorator, _undefined, target, key)
			}
			if value.ToBoolean() {
				result = value
			}
		}
	}
	if count > 3 && result.ToBoolean() {
		r.object_defineProperty(FunctionCall{Arguments: []Value{target, key, result}})
	}
	return result
}
func (r *Runtime) tsMetadata(call FunctionCall) Value {
	if object, method := r.tsReflectMethod("metadata"); method != nil {
		return r.moduleCall(method, object, call.Argument(0), call.Argument(1))
	}
	return _undefined
}
func (r *Runtime) tsParam(call FunctionCall) Value {
	index, decorator := call.Argument(0), call.Argument(1)
	return r.newNativeFunc(func(c FunctionCall) Value {
		r.moduleCall(decorator, _undefined, c.Argument(0), c.Argument(1), index)
		return _undefined
	}, "", 2)
}

func (r *Runtime) tsPrivateBrand(state, receiver, field Value) bool {
	object := state.ToObject(r)
	if _, function := object.self.assertCallable(); function {
		return receiver.StrictEquals(state) && field.ToBoolean()
	}
	return r.moduleCall(object.Get("has"), object, receiver).ToBoolean()
}
func (r *Runtime) tsPrivateGet(call FunctionCall) Value {
	receiver, state, kind, field := call.Argument(0), call.Argument(1), call.Argument(2), call.Argument(3)
	if kind.String() == "a" && !field.ToBoolean() {
		panic(r.NewTypeError("Private accessor was defined without a getter"))
	}
	if !r.tsPrivateBrand(state, receiver, field) {
		panic(r.NewTypeError("Cannot read private member from an object whose class did not declare it"))
	}
	if kind.String() == "m" {
		return field
	}
	if kind.String() == "a" {
		return r.moduleCall(field, receiver)
	}
	if field.ToBoolean() {
		return nilSafe(field.ToObject(r).Get("value"))
	}
	object := state.ToObject(r)
	return r.moduleCall(object.Get("get"), object, receiver)
}
func (r *Runtime) tsPrivateSet(call FunctionCall) Value {
	receiver, state, value, kind, field := call.Argument(0), call.Argument(1), call.Argument(2), call.Argument(3), call.Argument(4)
	if kind.String() == "m" {
		panic(r.NewTypeError("Private method is not writable"))
	}
	if kind.String() == "a" && !field.ToBoolean() {
		panic(r.NewTypeError("Private accessor was defined without a setter"))
	}
	if !r.tsPrivateBrand(state, receiver, field) {
		panic(r.NewTypeError("Cannot write private member to an object whose class did not declare it"))
	}
	if kind.String() == "a" {
		r.moduleCall(field, receiver, value)
	} else if field.ToBoolean() {
		r.tsSet(field.ToObject(r), "value", value)
	} else {
		object := state.ToObject(r)
		r.moduleCall(object.Get("set"), object, receiver, value)
	}
	return value
}
func (r *Runtime) tsPrivateIn(call FunctionCall) Value {
	state, receiver := call.Argument(0).ToObject(r), call.Argument(1)
	if _, ok := receiver.(*Object); !ok {
		panic(r.NewTypeError("Cannot use 'in' operator on non-object"))
	}
	if _, function := state.self.assertCallable(); function {
		return valueBool(receiver.StrictEquals(state))
	}
	return r.moduleCall(state.Get("has"), state, receiver)
}
