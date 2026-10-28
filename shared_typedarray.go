package goya

import (
	"math"
	"math/big"
	"reflect"
)

type sharedTypedArray struct {
	inner  typedArray
	buffer *arrayBufferObject
	size   int
	name   asciiString
}

func (array *sharedTypedArray) raw(index int) uint64 {
	return nativeSharedBits(array.buffer.shared.load(index*array.size, array.size), array.size)
}
func (array *sharedTypedArray) putRaw(index int, value uint64) {
	array.buffer.shared.store(index*array.size, array.size, nativeSharedBits(value, array.size))
}
func sharedElementValue(name asciiString, value uint64) Value {
	switch name {
	case "Int8Array":
		return intToValue(int64(int8(value)))
	case "Uint8Array", "Uint8ClampedArray":
		return intToValue(int64(uint8(value)))
	case "Int16Array":
		return intToValue(int64(int16(value)))
	case "Uint16Array":
		return intToValue(int64(uint16(value)))
	case "Int32Array":
		return intToValue(int64(int32(value)))
	case "Uint32Array":
		return intToValue(int64(uint32(value)))
	case "Float16Array":
		return floatToValue(halfToFloat64(uint16(value)))
	case "Float32Array":
		return floatToValue(float64(math.Float32frombits(uint32(value))))
	case "Float64Array":
		return floatToValue(math.Float64frombits(value))
	case "BigInt64Array":
		return (*valueBigInt)(new(big.Int).SetInt64(int64(value)))
	case "BigUint64Array":
		return (*valueBigInt)(new(big.Int).SetUint64(value))
	}
	panic("unknown shared element kind")
}
func sharedElementBits(name asciiString, value Value) uint64 {
	switch name {
	case "Int8Array":
		return uint64(toInt8(value))
	case "Uint8Array":
		return uint64(toUint8(value))
	case "Uint8ClampedArray":
		return uint64(toUint8Clamp(value))
	case "Int16Array":
		return uint64(toInt16(value))
	case "Uint16Array":
		return uint64(toUint16(value))
	case "Int32Array":
		return uint64(toInt32(value))
	case "Uint32Array":
		return uint64(toUint32(value))
	case "Float16Array":
		return uint64(float64ToHalf(value.ToFloat()))
	case "Float32Array":
		return uint64(math.Float32bits(float32(value.ToFloat())))
	case "Float64Array":
		return math.Float64bits(value.ToFloat())
	case "BigInt64Array":
		return uint64(toBigInt64(value).Int64())
	case "BigUint64Array":
		return toBigUint64(value).Uint64()
	}
	panic("unknown shared element kind")
}
func (array *sharedTypedArray) get(index int) Value {
	return sharedElementValue(array.name, array.raw(index))
}
func (array *sharedTypedArray) set(index int, value Value) {
	array.putRaw(index, sharedElementBits(array.name, value))
}
func (array *sharedTypedArray) less(i, j int) bool {
	x, y := array.get(i), array.get(j)
	if typedArrayContainsBigInt(array.inner) {
		return (*big.Int)(x.(*valueBigInt)).Cmp((*big.Int)(y.(*valueBigInt))) < 0
	}
	return typedFloatLess(x.ToFloat(), y.ToFloat())
}
func (array *sharedTypedArray) swap(i, j int) {
	x, y := array.raw(i), array.raw(j)
	array.putRaw(i, y)
	array.putRaw(j, x)
}
func (array *sharedTypedArray) indexOf(value Value, start, length int) int {
	for i := 0; i < length; i++ {
		if value.StrictEquals(array.get(start + i)) {
			return i
		}
	}
	return -1
}
func (array *sharedTypedArray) lastIndexOf(value Value, start, length int) int {
	for i := length - 1; i >= 0; i-- {
		if value.StrictEquals(array.get(start + i)) {
			return i
		}
	}
	return -1
}
func (array *sharedTypedArray) includes(value Value, start, length int) bool {
	for i := 0; i < length; i++ {
		candidate := array.get(start + i)
		if value.SameAs(candidate) || value.StrictEquals(candidate) {
			return true
		}
	}
	return false
}
func (array *sharedTypedArray) fill(value Value, start, length int) {
	bits := sharedElementBits(array.name, value)
	for i := 0; i < length; i++ {
		array.putRaw(start+i, bits)
	}
}
func plainTypedArray(name asciiString, data *[]byte) typedArray {
	switch name {
	case "Int8Array":
		return (*int8Array)(data)
	case "Uint8Array":
		return (*uint8Array)(data)
	case "Uint8ClampedArray":
		return (*uint8ClampedArray)(data)
	case "Int16Array":
		return (*int16Array)(data)
	case "Uint16Array":
		return (*uint16Array)(data)
	case "Int32Array":
		return (*int32Array)(data)
	case "Uint32Array":
		return (*uint32Array)(data)
	case "Float16Array":
		return (*float16Array)(data)
	case "Float32Array":
		return (*float32Array)(data)
	case "Float64Array":
		return (*float64Array)(data)
	case "BigInt64Array":
		return (*bigInt64Array)(data)
	case "BigUint64Array":
		return (*bigUint64Array)(data)
	}
	panic("unknown shared element kind")
}
func (array *sharedTypedArray) export(offset, length int) interface{} {
	data := allocByteSlice(length * array.size)
	copy := plainTypedArray(array.name, &data)
	for i := 0; i < length; i++ {
		copy.set(i, array.get(offset+i))
	}
	return copy.export(0, length)
}
func (array *sharedTypedArray) exportType() reflect.Type { return array.inner.exportType() }
