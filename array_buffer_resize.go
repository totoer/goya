package goya

// View lengths are derived from the current backing slice. Original offsets and
// fixed lengths never change, so out-of-bounds views recover on regrowth without
// buffers retaining lists of live views.
func (a *typedArrayObject) outOfBounds() bool {
	buffer := a.viewedArrayBuf
	if buffer.detached || a.offset > buffer.byteLength()/a.elemSize {
		return true
	}
	return !a.autoLength && a.length > buffer.byteLength()/a.elemSize-a.offset
}
func (a *typedArrayObject) lengthValue() int {
	if a.outOfBounds() {
		return 0
	}
	if a.autoLength {
		return a.viewedArrayBuf.byteLength()/a.elemSize - a.offset
	}
	return a.length
}
func (a *typedArrayObject) ensureInBounds(throw bool) bool {
	if a.outOfBounds() {
		a.val.runtime.typeErrorResult(throw, "TypedArray is detached or out of bounds")
		return false
	}
	return true
}
func (a *typedArrayObject) preventExtensions(throw bool) bool {
	if a.autoLength || a.viewedArrayBuf.resizable {
		a.val.runtime.typeErrorResult(throw, "Cannot prevent extensions of a resizable ArrayBuffer view")
		return false
	}
	return a.baseObject.preventExtensions(throw)
}
func (a *typedArrayObject) element(index int) Value {
	return nilSafe(a._getIdx(index))
}
func (r *Runtime) typedArrayReceiver(value Value) *typedArrayObject {
	if object, ok := value.(*Object); ok {
		if array, ok := object.self.(*typedArrayObject); ok {
			return array
		}
	}
	panic(r.NewTypeError("incompatible TypedArray receiver"))
}
func typedArrayNumericValue(array *typedArrayObject, value Value) Value {
	if typedArrayContainsBigInt(array.typedArray) {
		return toBigInt(value)
	}
	return value.ToNumber()
}
func (v *dataViewObject) outOfBounds() bool {
	b := v.viewedArrayBuf
	return b.detached || v.byteOffset > b.byteLength() || !v.autoLength && v.byteLen > b.byteLength()-v.byteOffset
}
func (v *dataViewObject) lengthValue() int {
	if v.autoLength {
		return v.viewedArrayBuf.byteLength() - v.byteOffset
	}
	return v.byteLen
}
func (v *dataViewObject) ensureInBounds() {
	if v.outOfBounds() {
		panic(v.val.runtime.NewTypeError("DataView is detached or out of bounds"))
	}
}
func (r *Runtime) arrayBufferReceiver(value Value) *arrayBufferObject {
	if o, ok := value.(*Object); ok {
		if buffer, ok := o.self.(*arrayBufferObject); ok && buffer.shared == nil {
			return buffer
		}
	}
	panic(r.NewTypeError("incompatible ArrayBuffer receiver"))
}
func (r *Runtime) arrayBufferProto_getResizable(call FunctionCall) Value {
	return r.toBoolean(r.arrayBufferReceiver(call.This).resizable)
}
func (r *Runtime) arrayBufferProto_getMaxByteLength(call FunctionCall) Value {
	buffer := r.arrayBufferReceiver(call.This)
	if buffer.detached {
		return _positiveZero
	}
	if buffer.resizable {
		return intToValue(int64(buffer.maxByteLength))
	}
	return intToValue(int64(buffer.byteLength()))
}
func (r *Runtime) arrayBufferProto_resize(call FunctionCall) Value {
	buffer := r.arrayBufferReceiver(call.This)
	if !buffer.resizable {
		panic(r.NewTypeError("ArrayBuffer is not resizable"))
	}
	length := r.toIndex(call.Argument(0))
	buffer.ensureNotDetached(true)
	if length > buffer.maxByteLength {
		panic(r.newError(r.getRangeError(), "new length exceeds ArrayBuffer maxByteLength"))
	}
	if length == buffer.byteLength() {
		return _undefined
	}
	data := allocByteSlice(length)
	copy(data, buffer.data)
	buffer.data = data
	return _undefined
}
