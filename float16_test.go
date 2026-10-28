package goya

import (
	"math"
	"testing"
)

func TestHalfPrecisionAllRepresentations(t *testing.T) {
	for bits := uint32(0); bits <= 0xffff; bits++ {
		half := uint16(bits)
		value := halfToFloat64(half)
		back := float64ToHalf(value)
		if half&0x7c00 == 0x7c00 && half&0x03ff != 0 {
			if !math.IsNaN(value) || back&0x7c00 != 0x7c00 || back&0x03ff == 0 {
				t.Fatalf("NaN %04x", half)
			}
			continue
		}
		if back != half {
			t.Fatalf("binary16 roundtrip %04x -> %v -> %04x", half, value, back)
		}
	}
	for _, tc := range []struct {
		value float64
		bits  uint16
	}{
		{0, 0}, {math.Copysign(0, -1), 0x8000}, {1, 0x3c00}, {-2, 0xc000}, {65504, 0x7bff}, {65520, 0x7c00}, {math.Ldexp(1, -14), 0x0400}, {math.Ldexp(1, -24), 1}, {math.Ldexp(1, -25), 0}, {3 * math.Ldexp(1, -25), 2}, {1 + math.Ldexp(1, -11), 0x3c00}, {1 + 3*math.Ldexp(1, -11), 0x3c02}, {1 + math.Ldexp(1, -11) + math.Ldexp(1, -40), 0x3c01}, {math.Inf(-1), 0xfc00},
	} {
		if bits := float64ToHalf(tc.value); bits != tc.bits {
			t.Fatalf("round %v -> %04x want %04x", tc.value, bits, tc.bits)
		}
	}
}
