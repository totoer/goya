package goya

import "github.com/totoer/goya/unistring"

func (r *Runtime) sharedBufferReceiver(value Value) *arrayBufferObject {
	if object, ok := value.(*Object); ok {
		if buffer, ok := object.self.(*arrayBufferObject); ok && buffer.shared != nil {
			return buffer
		}
	}
	panic(r.NewTypeError("incompatible SharedArrayBuffer receiver"))
}
func (r *Runtime) builtin_newSharedArrayBuffer(args []Value, newTarget *Object) *Object {
	if newTarget == nil {
		panic(r.needNew("SharedArrayBuffer"))
	}
	length := 0
	if len(args) > 0 {
		length = r.toIndex(args[0])
	}
	var maximum []int
	if len(args) > 1 {
		if options, ok := args[1].(*Object); ok {
			if value := nilSafe(options.Get("maxByteLength")); !IsUndefined(value) {
				maximum = []int{r.toIndex(value)}
			}
		}
	}
	if len(maximum) > 0 && length > maximum[0] {
		panic(r.newError(r.getRangeError(), "byteLength exceeds maxByteLength"))
	}
	prototype := r.getPrototypeFromCtor(newTarget, r.getSharedArrayBuffer(), r.getSharedArrayBufferPrototype())
	storage, err := NewSharedArrayBuffer(length, maximum...)
	if err != nil {
		panic(r.newError(r.getRangeError(), err.Error()))
	}
	object := r._newArrayBuffer(prototype, nil)
	object.shared = storage.storage
	return object.val
}
func (r *Runtime) sharedBufferByteLength(call FunctionCall) Value {
	return intToValue(int64(r.sharedBufferReceiver(call.This).byteLength()))
}
func (r *Runtime) sharedBufferMaxByteLength(call FunctionCall) Value {
	return intToValue(int64(r.sharedBufferReceiver(call.This).shared.maximum))
}
func (r *Runtime) sharedBufferGrowable(call FunctionCall) Value {
	return r.toBoolean(r.sharedBufferReceiver(call.This).shared.growable)
}
func (r *Runtime) sharedBufferGrow(call FunctionCall) Value {
	buffer := r.sharedBufferReceiver(call.This)
	if !buffer.shared.growable {
		panic(r.NewTypeError("SharedArrayBuffer is not growable"))
	}
	length := r.toIndex(call.Argument(0))
	if err := (SharedArrayBuffer{storage: buffer.shared}).Grow(length); err != nil {
		panic(r.newError(r.getRangeError(), err.Error()))
	}
	return _undefined
}
func (r *Runtime) sharedBufferSlice(call FunctionCall) Value {
	buffer := r.sharedBufferReceiver(call.This)
	length := int64(buffer.byteLength())
	first := relToIdx(call.Argument(0).ToInteger(), length)
	end := length
	if !IsUndefined(call.Argument(1)) {
		end = relToIdx(call.Argument(1).ToInteger(), length)
	}
	count := max(end-first, 0)
	object := r.speciesConstructor(buffer.val, r.getSharedArrayBuffer())([]Value{intToValue(count)}, nil)
	result := r.sharedBufferReceiver(object)
	if result.shared == buffer.shared {
		panic(r.NewTypeError("SharedArrayBuffer species returned the same storage"))
	}
	if int64(result.byteLength()) < count {
		panic(r.NewTypeError("SharedArrayBuffer species result is too small"))
	}
	result.copyBytesFrom(0, buffer, int(first), int(count), false)
	return object
}
func (r *Runtime) getSharedArrayBufferPrototype() *Object {
	if r.global.SharedArrayBufferPrototype == nil {
		object := &Object{runtime: r}
		r.global.SharedArrayBufferPrototype = object
		prototype := newBaseObjectObj(object, r.global.ObjectPrototype, classObject)
		prototype._putProp("constructor", r.getSharedArrayBuffer(), true, false, true)
		for _, property := range []struct {
			name   string
			method func(FunctionCall) Value
		}{{"byteLength", r.sharedBufferByteLength}, {"maxByteLength", r.sharedBufferMaxByteLength}, {"growable", r.sharedBufferGrowable}} {
			prototype._put(unistring.NewFromString(property.name), &valueProperty{accessor: true, configurable: true, getterFunc: r.newNativeFunc(property.method, unistring.NewFromString("get "+property.name), 0)})
		}
		prototype._putProp("grow", r.newNativeFunc(r.sharedBufferGrow, "grow", 1), true, false, true)
		prototype._putProp("slice", r.newNativeFunc(r.sharedBufferSlice, "slice", 2), true, false, true)
		prototype._putSym(SymToStringTag, valueProp(asciiString("SharedArrayBuffer"), false, false, true))
	}
	return r.global.SharedArrayBufferPrototype
}
func (r *Runtime) getSharedArrayBuffer() *Object {
	if r.global.SharedArrayBuffer == nil {
		object := &Object{runtime: r}
		r.global.SharedArrayBuffer = object
		constructor := r.newNativeConstructOnly(object, r.builtin_newSharedArrayBuffer, r.getSharedArrayBufferPrototype(), "SharedArrayBuffer", 1)
		r.putSpeciesReturnThis(constructor)
	}
	return r.global.SharedArrayBuffer
}
