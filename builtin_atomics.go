package goya

import (
	"math"
	"runtime"

	"github.com/totoer/goya/unistring"
)

func (r *Runtime) atomicArray(value Value, wait bool) *typedArrayObject {
	array := r.typedArrayReceiver(value)
	name := typedArrayElementName(array.typedArray)
	valid := false
	if wait {
		valid = name == "Int32Array" || name == "BigInt64Array"
	} else {
		switch name {
		case "Int8Array", "Uint8Array", "Int16Array", "Uint16Array", "Int32Array", "Uint32Array", "BigInt64Array", "BigUint64Array":
			valid = true
		}
	}
	if !valid {
		panic(r.NewTypeError("invalid integer typed array for Atomics"))
	}
	array.ensureInBounds(true)
	return array
}
func (r *Runtime) atomicAccess(value, index Value, wait bool) (*typedArrayObject, int) {
	array := r.atomicArray(value, wait)
	length := array.lengthValue()
	position := r.toIndex(index)
	if position >= length {
		panic(r.newError(r.getRangeError(), "Atomics index out of bounds"))
	}
	return array, (array.offset + position) * array.elemSize
}
func (r *Runtime) revalidateAtomicAccess(array *typedArrayObject, index int) {
	array.ensureInBounds(true)
	if index > array.viewedArrayBuf.byteLength()-array.elemSize {
		panic(r.newError(r.getRangeError(), "Atomics index out of bounds after conversion"))
	}
}
func atomicIntegerValue(array *typedArrayObject, value Value) Value {
	if typedArrayContainsBigInt(array.typedArray) {
		return toBigInt(value)
	}
	number := value.ToNumber().ToFloat()
	if number == 0 || math.IsNaN(number) {
		return _positiveZero
	}
	return floatToValue(math.Trunc(number))
}
func atomicReadBits(array *typedArrayObject, index int) uint64 {
	if storage := array.viewedArrayBuf.shared; storage != nil {
		return nativeSharedBits(storage.load(index, array.elemSize), array.elemSize)
	}
	return sharedElementBits(typedArrayElementName(array.typedArray), array.typedArray.get(index/array.elemSize)) & sharedMask(array.elemSize)
}
func atomicModifyBits(array *typedArrayObject, index int, operation func(uint64) uint64) uint64 {
	if storage := array.viewedArrayBuf.shared; storage != nil {
		previous := storage.modify(index, array.elemSize, func(value uint64) uint64 {
			return nativeSharedBits(operation(nativeSharedBits(value, array.elemSize)), array.elemSize)
		})
		return nativeSharedBits(previous, array.elemSize)
	}
	previous := atomicReadBits(array, index)
	next := operation(previous) & sharedMask(array.elemSize)
	array.typedArray.set(index/array.elemSize, sharedElementValue(typedArrayElementName(array.typedArray), next))
	return previous
}
func (r *Runtime) atomicLoad(call FunctionCall) Value {
	array, index := r.atomicAccess(call.Argument(0), call.Argument(1), false)
	r.revalidateAtomicAccess(array, index)
	return sharedElementValue(typedArrayElementName(array.typedArray), atomicReadBits(array, index))
}
func (r *Runtime) atomicStore(call FunctionCall) Value {
	array, index := r.atomicAccess(call.Argument(0), call.Argument(1), false)
	value := atomicIntegerValue(array, call.Argument(2))
	r.revalidateAtomicAccess(array, index)
	bits := sharedElementBits(typedArrayElementName(array.typedArray), value)
	atomicModifyBits(array, index, func(uint64) uint64 { return bits })
	return value
}
func (r *Runtime) atomicModify(call FunctionCall, operation string) Value {
	array, index := r.atomicAccess(call.Argument(0), call.Argument(1), false)
	value := atomicIntegerValue(array, call.Argument(2))
	bits := sharedElementBits(typedArrayElementName(array.typedArray), value)
	var replacement uint64
	if operation == "compareExchange" {
		replacement = sharedElementBits(typedArrayElementName(array.typedArray), atomicIntegerValue(array, call.Argument(3)))
	}
	r.revalidateAtomicAccess(array, index)
	previous := atomicModifyBits(array, index, func(previous uint64) uint64 {
		switch operation {
		case "add":
			return previous + bits
		case "sub":
			return previous - bits
		case "and":
			return previous & bits
		case "or":
			return previous | bits
		case "xor":
			return previous ^ bits
		case "exchange":
			return bits
		case "compareExchange":
			if previous == bits&sharedMask(array.elemSize) {
				return replacement
			}
			return previous
		}
		panic("unknown atomic operation")
	})
	return sharedElementValue(typedArrayElementName(array.typedArray), previous)
}
func (r *Runtime) getAtomics() *Object {
	if r.global.Atomics == nil {
		object := r.NewObject()
		r.global.Atomics = object
		prototype := object.self
		for _, name := range []string{"add", "sub", "and", "or", "xor", "exchange", "compareExchange"} {
			length := 3
			if name == "compareExchange" {
				length = 4
			}
			prototype._putProp(unistring.NewFromString(name), r.newNativeFunc(func(call FunctionCall) Value { return r.atomicModify(call, name) }, unistring.NewFromString(name), length), true, false, true)
		}
		prototype._putProp("wait", r.newNativeFunc(func(call FunctionCall) Value { return r.atomicWait(call, false) }, "wait", 4), true, false, true)
		prototype._putProp("waitAsync", r.newNativeFunc(func(call FunctionCall) Value { return r.atomicWait(call, true) }, "waitAsync", 4), true, false, true)
		prototype._putProp("notify", r.newNativeFunc(r.atomicNotify, "notify", 3), true, false, true)
		prototype._putProp("load", r.newNativeFunc(r.atomicLoad, "load", 2), true, false, true)
		prototype._putProp("store", r.newNativeFunc(r.atomicStore, "store", 3), true, false, true)
		prototype._putProp("isLockFree", r.newNativeFunc(func(call FunctionCall) Value {
			size := call.Argument(0).ToInteger()
			return r.toBoolean(size == 1 || size == 2 || size == 4 || size == 8)
		}, "isLockFree", 1), true, false, true)
		prototype._putProp("pause", r.newNativeFunc(func(FunctionCall) Value { runtime.Gosched(); return _undefined }, "pause", 0), true, false, true)
		prototype._putSym(SymToStringTag, valueProp(asciiString("Atomics"), false, false, true))
	}
	return r.global.Atomics
}
