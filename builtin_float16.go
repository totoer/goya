package goya

import (
	"math"
	"reflect"
)

func roundHalfSignificand(value uint64, shift uint) uint64 {
	whole := value >> shift
	remainder := value & ((uint64(1) << shift) - 1)
	half := uint64(1) << (shift - 1)
	if remainder > half || remainder == half && whole&1 != 0 {
		whole++
	}
	return whole
}

// Convert directly from binary64. An intermediate float32 would double-round
// values close to a half-precision midpoint.
func float64ToHalf(value float64) uint16 {
	bits := math.Float64bits(value)
	sign := uint16(bits>>48) & 0x8000
	exponent := int(bits >> 52 & 0x7ff)
	fraction := bits & ((uint64(1) << 52) - 1)
	if exponent == 0x7ff {
		if fraction != 0 {
			return sign | 0x7e00
		}
		return sign | 0x7c00
	}
	e := exponent - 1023
	if exponent == 0 || e < -25 {
		return sign
	}
	if e > 15 {
		return sign | 0x7c00
	}
	significand := fraction | uint64(1)<<52
	if e < -14 {
		return sign | uint16(roundHalfSignificand(significand, uint(28-e)))
	}
	rounded := roundHalfSignificand(significand, 42)
	if rounded == 2048 {
		e++
		rounded = 1024
	}
	if e > 15 {
		return sign | 0x7c00
	}
	return sign | uint16(e+15)<<10 | uint16(rounded-1024)
}

func halfToFloat64(bits uint16) float64 {
	exponent := int(bits >> 10 & 31)
	fraction := bits & 1023
	var value float64
	if exponent == 31 {
		if fraction == 0 {
			value = math.Inf(1)
		} else {
			value = math.NaN()
		}
	} else if exponent == 0 {
		value = math.Ldexp(float64(fraction), -24)
	} else {
		value = math.Ldexp(float64(1024+fraction), exponent-25)
	}
	if bits&0x8000 != 0 {
		return -value
	}
	return value
}

type float16Array []byte

func (a *float16Array) raw(index int) *uint16      { return (*uint16Array)(a).ptr(index) }
func (a *float16Array) get(index int) Value        { return floatToValue(halfToFloat64(*a.raw(index))) }
func (a *float16Array) set(index int, value Value) { *a.raw(index) = float64ToHalf(value.ToFloat()) }
func (a *float16Array) less(i, j int) bool {
	return typedFloatLess(halfToFloat64(*a.raw(i)), halfToFloat64(*a.raw(j)))
}
func (a *float16Array) swap(i, j int) { (*uint16Array)(a).swap(i, j) }
func (a *float16Array) indexOf(value Value, start, length int) int {
	switch value.(type) {
	case valueInt, valueFloat:
	default:
		return -1
	}
	number := value.ToFloat()
	for i := 0; i < length; i++ {
		if halfToFloat64(*a.raw(start + i)) == number {
			return i
		}
	}
	return -1
}
func (a *float16Array) lastIndexOf(value Value, start, length int) int {
	switch value.(type) {
	case valueInt, valueFloat:
	default:
		return -1
	}
	number := value.ToFloat()
	for i := length - 1; i >= 0; i-- {
		if halfToFloat64(*a.raw(start + i)) == number {
			return i
		}
	}
	return -1
}
func (a *float16Array) includes(value Value, start, length int) bool {
	switch value.(type) {
	case valueInt, valueFloat:
	default:
		return false
	}
	number := value.ToFloat()
	for i := 0; i < length; i++ {
		v := halfToFloat64(*a.raw(start + i))
		if v == number || math.IsNaN(v) && math.IsNaN(number) {
			return true
		}
	}
	return false
}
func (a *float16Array) fill(value Value, start, length int) {
	bits := float64ToHalf(value.ToFloat())
	for i := 0; i < length; i++ {
		*a.raw(start + i) = bits
	}
}
func (a *float16Array) export(offset, length int) interface{} {
	out := make([]float64, length)
	for i := range out {
		out[i] = halfToFloat64(*a.raw(offset + i))
	}
	return out
}
func (a *float16Array) exportType() reflect.Type { return typeFloat64Array }

func (r *Runtime) getFloat16Array() *Object {
	if r.global.Float16Array == nil {
		o := &Object{runtime: r}
		r.global.Float16Array = o
		r.createTypedArrayCtor(o, func(args []Value, newTarget, proto *Object) *Object {
			return r._newTypedArray(args, newTarget, func(buf *arrayBufferObject, offset, length int, proto *Object) *typedArrayObject {
				return r._newTypedArrayObject(buf, offset, length, 2, o, (*float16Array)(&buf.data), proto)
			}, proto)
		}, "Float16Array", 2)
	}
	return r.global.Float16Array
}
func (r *Runtime) math_f16round(call FunctionCall) Value {
	return floatToValue(halfToFloat64(float64ToHalf(call.Argument(0).ToFloat())))
}
func (r *Runtime) dataViewProto_getFloat16(call FunctionCall) Value {
	if dv, ok := r.toObject(call.This).self.(*dataViewObject); ok {
		return floatToValue(halfToFloat64(dv.viewedArrayBuf.getUint16(dv.getIdxAndByteOrder(r.toIndex(call.Argument(0)), call.Argument(1), 2))))
	}
	panic(r.NewTypeError("DataView.prototype.getFloat16 called on incompatible receiver"))
}
func (r *Runtime) dataViewProto_setFloat16(call FunctionCall) Value {
	if dv, ok := r.toObject(call.This).self.(*dataViewObject); ok {
		index := r.toIndex(call.Argument(0))
		bits := float64ToHalf(call.Argument(1).ToFloat())
		offset, order := dv.getIdxAndByteOrder(index, call.Argument(2), 2)
		dv.viewedArrayBuf.setUint16(offset, bits, order)
		return _undefined
	}
	panic(r.NewTypeError("DataView.prototype.setFloat16 called on incompatible receiver"))
}
