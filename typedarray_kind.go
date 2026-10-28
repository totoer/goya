package goya

// Element identity comes from the private storage type, never from mutable
// public constructor names or subclass names.
func typedArrayElementName(array typedArray) asciiString {
	if shared, ok := array.(*sharedTypedArray); ok {
		return shared.name
	}
	switch array.(type) {
	case *uint8Array:
		return "Uint8Array"
	case *uint8ClampedArray:
		return "Uint8ClampedArray"
	case *int8Array:
		return "Int8Array"
	case *uint16Array:
		return "Uint16Array"
	case *int16Array:
		return "Int16Array"
	case *uint32Array:
		return "Uint32Array"
	case *int32Array:
		return "Int32Array"
	case *float16Array:
		return "Float16Array"
	case *float32Array:
		return "Float32Array"
	case *float64Array:
		return "Float64Array"
	case *bigInt64Array:
		return "BigInt64Array"
	case *bigUint64Array:
		return "BigUint64Array"
	default:
		panic("unknown internal typed array storage")
	}
}
func typedArrayContainsBigInt(array typedArray) bool {
	if shared, ok := array.(*sharedTypedArray); ok {
		array = shared.inner
	}
	switch array.(type) {
	case *bigInt64Array, *bigUint64Array:
		return true
	default:
		return false
	}
}
