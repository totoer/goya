package goya

import (
	"fmt"
	"math"
	"math/big"
	"reflect"
)

type IntegerRepresentation string

const (
	IntegerBigInt     IntegerRepresentation = "bigint"
	IntegerSafeNumber IntegerRepresentation = "safe-number"
)

type Float32Conversion string

const (
	Float32Exact Float32Conversion = "exact"
	Float32Round Float32Conversion = "round-to-nearest"
)

// NumericPolicy makes potentially lossy Go representations explicit. IntBits
// fixes the contract of int, uint and uintptr to 32 or 64 bits; registration
// rejects a contract wider than the current Go target can represent.
type NumericPolicy struct {
	Integers IntegerRepresentation
	IntBits  int
	Float32  Float32Conversion
}

func (p NumericPolicy) validate() error {
	if p.Integers != "" && p.Integers != IntegerBigInt && p.Integers != IntegerSafeNumber {
		return fmt.Errorf("unknown integer representation %q", p.Integers)
	}
	if p.IntBits != 0 && p.IntBits != 32 && p.IntBits != 64 {
		return fmt.Errorf("IntBits must be 32 or 64")
	}
	if p.Float32 != "" && p.Float32 != Float32Exact && p.Float32 != Float32Round {
		return fmt.Errorf("unknown float32 conversion %q", p.Float32)
	}
	return nil
}

func (c *hostConversion) numberInput(value Value, n *hostType) reflect.Value {
	result := reflect.New(n.typeOf).Elem()
	if n.kind == reflect.Float32 {
		switch value.(type) {
		case valueInt, valueFloat:
		default:
			panic(c.r.NewTypeError("expected number for float32"))
		}
		number := value.ToFloat()
		rounded := float32(number)
		if n.numberMode == string(Float32Exact) && float64(rounded) != number && !math.IsNaN(number) {
			panic(c.r.NewTypeError("number is not exactly representable as float32"))
		}
		if !math.IsInf(number, 0) && math.IsInf(float64(rounded), 0) {
			panic(c.r.NewTypeError("float32 overflow"))
		}
		result.SetFloat(float64(rounded))
		return result
	}
	integer := new(big.Int)
	if n.numberMode == string(IntegerBigInt) {
		actual, ok := value.(*valueBigInt)
		if !ok {
			panic(c.r.NewTypeError("expected bigint for %s", n.typeOf))
		}
		integer.Set((*big.Int)(actual))
	} else {
		switch value.(type) {
		case valueInt, valueFloat:
		default:
			panic(c.r.NewTypeError("expected number for %s", n.typeOf))
		}
		number := value.ToFloat()
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || math.Abs(number) > maxHostSafeInteger || number == 0 && math.Signbit(number) {
			panic(c.r.NewTypeError("expected a safe integer for %s", n.typeOf))
		}
		integer.SetInt64(int64(number))
	}
	unsigned := n.kind == reflect.Uint || n.kind == reflect.Uint64 || n.kind == reflect.Uintptr
	if unsigned {
		if integer.Sign() < 0 || integer.BitLen() > n.numberBits {
			panic(c.r.NewTypeError("bigint is outside unsigned %d-bit range", n.numberBits))
		}
		result.SetUint(integer.Uint64())
	} else {
		limit := new(big.Int).Lsh(big.NewInt(1), uint(n.numberBits-1))
		minimum := new(big.Int).Neg(new(big.Int).Set(limit))
		if integer.Cmp(minimum) < 0 || integer.Cmp(limit) >= 0 {
			panic(c.r.NewTypeError("integer is outside signed %d-bit range", n.numberBits))
		}
		result.SetInt(integer.Int64())
	}
	return result
}

const maxHostSafeInteger = 9007199254740991

func (c *hostConversion) numberOutput(value reflect.Value, n *hostType) Value {
	if n.kind == reflect.Float32 {
		return c.r.ToValue(value.Float())
	}
	integer := new(big.Int)
	if n.kind == reflect.Uint || n.kind == reflect.Uint64 || n.kind == reflect.Uintptr {
		integer.SetUint64(value.Uint())
	} else {
		integer.SetInt64(value.Int())
	}
	// Apply the same declared bounds to Go results, including narrowed int.
	bigint := (*valueBigInt)(integer)
	if n.numberMode == string(IntegerBigInt) {
		c.numberInput(bigint, n)
		return bigint
	}
	if integer.Cmp(big.NewInt(-maxHostSafeInteger)) < 0 || integer.Cmp(big.NewInt(maxHostSafeInteger)) > 0 {
		panic(c.r.NewTypeError("host integer result exceeds safe-number range"))
	}
	result := intToValue(integer.Int64())
	c.numberInput(result, n)
	return result
}
