package goya

import "weak"

// Neither branch retains its target. A separate agent-owned set implements
// AddToKeptObjects until the current script/promise job has finished.
type weakTarget struct {
	object weak.Pointer[Object]
	symbol weak.Pointer[Symbol]
}

func (r *Runtime) canBeHeldWeakly(value Value) bool {
	switch value := value.(type) {
	case *Object:
		return true
	case *Symbol:
		return !r.isRegisteredSymbol(value)
	}
	return false
}
func makeWeakTarget(value Value) weakTarget {
	switch value := value.(type) {
	case *Object:
		return weakTarget{object: weak.Make(value)}
	case *Symbol:
		return weakTarget{symbol: weak.Make(value)}
	}
	return weakTarget{}
}
func (target weakTarget) value() Value {
	if object := target.object.Value(); object != nil {
		return object
	}
	if symbol := target.symbol.Value(); symbol != nil {
		return symbol
	}
	return _undefined
}
func (r *Runtime) keepWeakTarget(value Value) {
	if r.keptObjects == nil {
		r.keptObjects = make(map[Value]struct{})
	}
	r.keptObjects[value] = struct{}{}
}

type weakRefObject struct {
	baseObject
	target weakTarget
}

func (r *Runtime) builtin_newWeakRef(args []Value, newTarget *Object) *Object {
	if newTarget == nil {
		panic(r.needNew("WeakRef"))
	}
	target := _undefined
	if len(args) > 0 {
		target = args[0]
	}
	if !r.canBeHeldWeakly(target) {
		panic(r.NewTypeError("WeakRef target must be an object or a non-registered symbol"))
	}
	prototype := r.getPrototypeFromCtor(newTarget, r.getWeakRef(), r.getWeakRefPrototype())
	object := &Object{runtime: r}
	ref := &weakRefObject{baseObject: baseObject{val: object, prototype: prototype, class: classObject, extensible: true}, target: makeWeakTarget(target)}
	object.self = ref
	ref.init()
	r.keepWeakTarget(target)
	return object
}
func (r *Runtime) weakRefDeref(call FunctionCall) Value {
	if object, ok := call.This.(*Object); ok {
		if ref, ok := object.self.(*weakRefObject); ok {
			value := ref.target.value()
			if !IsUndefined(value) {
				r.keepWeakTarget(value)
			}
			return value
		}
	}
	panic(r.NewTypeError("incompatible WeakRef receiver"))
}
func (r *Runtime) getWeakRefPrototype() *Object {
	if r.global.WeakRefPrototype == nil {
		object := &Object{runtime: r}
		r.global.WeakRefPrototype = object
		prototype := newBaseObjectObj(object, r.global.ObjectPrototype, classObject)
		prototype._putProp("constructor", r.getWeakRef(), true, false, true)
		prototype._putProp("deref", r.newNativeFunc(r.weakRefDeref, "deref", 0), true, false, true)
		prototype._putSym(SymToStringTag, valueProp(asciiString("WeakRef"), false, false, true))
	}
	return r.global.WeakRefPrototype
}
func (r *Runtime) getWeakRef() *Object {
	if r.global.WeakRef == nil {
		object := &Object{runtime: r}
		r.global.WeakRef = object
		r.newNativeConstructOnly(object, r.builtin_newWeakRef, r.getWeakRefPrototype(), "WeakRef", 1)
	}
	return r.global.WeakRef
}
