package goya

import (
	"math/big"
	"strconv"
	"testing"
)

func TestBigInt(t *testing.T) {
	const SCRIPT = `0xabcdef0123456789abcdef0123n`
	b := new(big.Int)
	b.SetString("0xabcdef0123456789abcdef0123", 0)
	testScript(SCRIPT, (*valueBigInt)(b), t)
}

func TestBigIntAsN(t *testing.T) {
	const SCRIPT = `
for (const bits of [0, 1, 2, 7, 8, 31, 32, 63, 64, 65, 127, 128, 129]) {
	const modulus = 2n ** BigInt(bits);
	const half = modulus / 2n;
	for (const value of [0n, 1n, -1n, half - 1n, half, half + 1n,
		-half - 1n, -half, -half + 1n, modulus - 1n, modulus, modulus + 1n,
		-modulus - 1n, -modulus, -modulus + 1n, modulus * modulus + 3n,
		-modulus * modulus - 3n]) {
		const unsigned = ((value % modulus) + modulus) % modulus;
		const signed = bits > 0 && unsigned >= half ? unsigned - modulus : unsigned;
		assert.sameValue(BigInt.asUintN(bits, value), unsigned, "asUintN " + bits + ": " + value);
		assert.sameValue(BigInt.asIntN(bits, value), signed, "asIntN " + bits + ": " + value);
	}
}

for (const bits of [67108864, 1073741824, 1073741825]) {
	assert.sameValue(BigInt.asUintN(bits, 0n), 0n);
	assert.sameValue(BigInt.asUintN(bits, 1n), 1n);
	assert.sameValue(BigInt.asIntN(bits, 0n), 0n);
	assert.sameValue(BigInt.asIntN(bits, 1n), 1n);
	assert.sameValue(BigInt.asIntN(bits, -1n), -1n);
	assert.sameValue(BigInt.asIntN(bits, -(2n ** 127n)), -(2n ** 127n));
}
try {
	BigInt.asUintN(1073741825, -1n);
	throw new Error("expected RangeError");
} catch (e) {
	assert.sameValue(e instanceof RangeError, true);
	assert.sameValue(e.message, "Maximum BigInt size exceeded");
}

for (const convert of [BigInt.asUintN, BigInt.asIntN]) {
	assert.throws(TypeError, () => convert(0, 1));
	assert.throws(TypeError, () => convert(1073741825, 1));
	assert.sameValue(convert(0, -1n), 0n);
	assert.sameValue(convert(0, true), 0n);
	assert.sameValue(convert(67108864, "1"), 1n);
	const log = [];
	assert.sameValue(convert({valueOf() { log.push("bits"); return 1073741825; }},
		{valueOf() { log.push("bigint"); return 1n; }}), 1n);
	assert.sameValue(log.join(","), "bits,bigint");
	assert.throws(RangeError, () => convert(-1, {valueOf() { log.push("unexpected"); return 1n; }}));
	assert.sameValue(log.join(","), "bits,bigint");
}
`
	testScriptWithTestLib(SCRIPT, _undefined, t)
	if strconv.IntSize == 64 {
		testScriptWithTestLib(`
assert.sameValue(BigInt.asUintN(Number.MAX_SAFE_INTEGER, 1n), 1n);
assert.sameValue(BigInt.asIntN(Number.MAX_SAFE_INTEGER, -1n), -1n);
assert.throws(RangeError, () => BigInt.asUintN(Number.MAX_SAFE_INTEGER, -1n));
`, _undefined, t)
	}
}

func TestBigIntAsNAllocations(t *testing.T) {
	r := New()
	for _, tc := range []struct {
		name string
		fn   func(FunctionCall) Value
		x    int64
	}{
		{"asUintN/zero", r.bigint_asUintN, 0},
		{"asUintN/positive", r.bigint_asUintN, 1},
		{"asIntN/zero", r.bigint_asIntN, 0},
		{"asIntN/positive", r.bigint_asIntN, 1},
		{"asIntN/negative", r.bigint_asIntN, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := (*valueBigInt)(big.NewInt(tc.x))
			call := FunctionCall{Arguments: []Value{intToValue(67108864), x}}
			var result Value
			allocs := testing.AllocsPerRun(5, func() { result = tc.fn(call) })
			if !result.SameAs(x) {
				t.Fatalf("got %v, want %v", result, x)
			}
			// ToNumber may box bits; the BigInt conversion itself must not allocate.
			if allocs > 1 {
				t.Fatalf("already fitting value allocated %v times per call, want at most 1", allocs)
			}
		})
	}
}

func BenchmarkBigIntAsN(b *testing.B) {
	r := New()
	for _, tc := range []struct {
		name string
		fn   func(FunctionCall) Value
	}{
		{"asUintN", r.bigint_asUintN},
		{"asIntN", r.bigint_asIntN},
	} {
		for _, bits := range []int64{8, 67108864} {
			b.Run(tc.name+"/"+strconv.FormatInt(bits, 10), func(b *testing.B) {
				x := (*valueBigInt)(big.NewInt(1))
				call := FunctionCall{Arguments: []Value{intToValue(bits), x}}
				b.ReportAllocs()
				b.ResetTimer()
				var result Value
				for i := 0; i < b.N; i++ {
					result = tc.fn(call)
				}
				b.StopTimer()
				if !result.SameAs(x) {
					b.Fatalf("got %v, want %v", result, x)
				}
			})
		}
	}
}

func TestBigIntExportTo(t *testing.T) {
	vm := New()

	t.Run("bigint exportType", func(t *testing.T) {
		v, err := vm.runVMString(`BigInt(Number.MAX_SAFE_INTEGER + 10);`)
		if err != nil {
			t.Fatal(err)
		}
		if typ := v.ExportType(); typ != typeBigInt {
			t.Fatal(typ)
		}
	})

	t.Run("bigint", func(t *testing.T) {
		var b big.Int
		err := vm.ExportTo(vm.ToValue(big.NewInt(10)), &b)
		if err != nil {
			t.Fatal(err)
		}
		if b.Cmp(big.NewInt(10)) != 0 {
			t.Fatalf("bigint: %s", b.String())
		}
	})
}

func TestBigIntFormat(t *testing.T) {
	const SCRIPT = `
assert.sameValue((1n).toString(undefined), "1", "radius undefined");
assert.throws(RangeError, () => { (1n).toString(-1); }, "radius -1");
assert.throws(RangeError, () => { (1n).toString(37); }, "radius 37");
assert.sameValue((1n).toString(2), "1", "radius 2");
assert.sameValue((10n).toString(3), "101", "radius 3");
`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestBigIntOperator(t *testing.T) {
	const SCRIPT = `
assert.throws(TypeError, () => { 1 - 1n; }, "mix type add");
assert.throws(TypeError, () => { 1n - 1; }, "mix type add");
assert.throws(TypeError, () => { 1n + 1; }, "mix type sub");
assert.throws(TypeError, () => { 1 + 1n; }, "mix type sub");
assert.throws(TypeError, () => { 1 * 1n; }, "mix type mul");
assert.throws(TypeError, () => { 1n * 1; }, "mix type mul");
assert.throws(TypeError, () => { 1 / 1n; }, "mix type div");
assert.throws(TypeError, () => { 1n / 1; }, "mix type div");
assert.throws(TypeError, () => { 1 % 1n; }, "mix type mod");
assert.throws(TypeError, () => { 1n % 1; }, "mix type mod");
assert.throws(TypeError, () => { 1n ** 1; }, "mix type exp");
assert.throws(TypeError, () => { 1 ** 1n; }, "mix type exp");
assert.throws(TypeError, () => { 1 & 1n; }, "mix type and");
assert.throws(TypeError, () => { 1n & 1; }, "mix type and");
assert.throws(TypeError, () => { 1 | 1n; }, "mix type or");
assert.throws(TypeError, () => { 1n | 1; }, "mix type or");
assert.throws(TypeError, () => { 1 ^ 1n; }, "mix type xor");
assert.throws(TypeError, () => { 1n ^ 1; }, "mix type xor");
assert.throws(TypeError, () => { 1 << 1n; }, "mix type lsh");
assert.throws(TypeError, () => { 1n << 1; }, "mix type lsh");
assert.throws(TypeError, () => { 1 >> 1n; }, "mix type rsh");
assert.throws(TypeError, () => { 1n >> 1; }, "mix type rsh");
assert.throws(TypeError, () => { 1 >>> 1n; }, "mix type ursh");
assert.throws(TypeError, () => { 1n >>> 1; }, "mix type ursh");

assert.sameValue(1n + 1n, 2n, "add");
assert.sameValue(1n - 1n, 0n, "sub");
assert.sameValue(1n * 2n, 2n, "mul");
assert.sameValue(1n / 2n, 0n, "div");
assert.sameValue(1n % 2n, 1n, "mod");
assert.sameValue(1n ** 2n, 1n, "exp");
assert.sameValue(1n & 1n, 1n, "and");
assert.sameValue(1n | 1n, 1n, "or");
assert.sameValue(2n ^ 1n, 3n, "xor");
assert.sameValue(1n << 1n, 2n, "lsh");
assert.sameValue(4n << -1n, 2n, "neg lsh");
assert.sameValue(4n >> 1n, 2n, "rsh");
assert.sameValue(2n >> -2n, 8n, "neg rsh");
assert.sameValue(1n >> 2n ** 64n, 0n, "rsh 2^64");
assert.sameValue(-5n >> 2n ** 64n, -1n, "neg rsh 2^64");
assert.sameValue(-5n << -(2n ** 64n), -1n, "neg lsh -2^64");
assert.sameValue(0n << 2n ** 64n, 0n, "zero lsh 2^64");
assert.throws(RangeError, () => { 1n << 2n ** 64n; }, "lsh 2^64");
assert.throws(RangeError, () => { 1n << 2n ** 30n; }, "lsh 2^30");
assert.throws(RangeError, () => { 2n << (2n ** 30n - 1n); }, "lsh past 2^30 bits");
assert.sameValue(1n >> (2n ** 30n + 1n), 0n, "rsh 2^30+1");
try {
	1n << 2n ** 30n;
} catch (e) {
	assert.sameValue(e.message, "Maximum BigInt size exceeded", "lsh 2^30 message");
}
assert.sameValue(2n ** 64n == false, false, "2^64 == false");
assert.sameValue(2n ** 64n + 1n == true, false, "2^64+1 == true");
assert.sameValue(-1n == true, false, "-1 == true");
assert.sameValue(1n == true, true, "1 == true");

let a = 1n;
assert.sameValue(++a, 2n, "inc");
assert.sameValue(--a, 1n, "dec");

assert.sameValue(Object(1n) - 1n, 0n, "primitive sub");
assert.sameValue(Object(Object(1n)) - 1n, 0n, "primitive sub");
assert.sameValue({ [Symbol.toPrimitive]: () => 1n } - 1n, 0n, "primitive sub");
assert.sameValue({ valueOf: () => 1n } - 1n, 0n, "valueOf sub");

assert.sameValue(1n > 0, true, "gt");
assert.sameValue(0 > 1n, false, "gt");
assert.sameValue(Object(1n) > 0, true, "gt");
assert.sameValue(0 > Object(1n), false, "gt");

assert.sameValue(1n < 0, false, "lt");
assert.sameValue(0 < 1n, true, "lt");
assert.sameValue(Object(1n) < 0, false, "lt");
assert.sameValue(0 < Object(1n), true, "lt");

assert.sameValue(1n >= 0, true, "ge");
assert.sameValue(0 >= 1n, false, "ge");
assert.sameValue(1n <= 0, false, "le");
assert.sameValue(0 <= 1n, true, "le");
`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestNumberFromBigInt(t *testing.T) {
	const SCRIPT = `
assert.sameValue(Number(2n ** 63n), 2 ** 63, "2^63");
assert.sameValue(Number(2n ** 64n), 2 ** 64, "2^64");
assert.sameValue(Number(-(2n ** 63n) - 1n), -(2 ** 63), "-2^63-1");
assert.sameValue(Number(2n ** 64n + 2048n), 2 ** 64, "tie rounds to even (down)");
assert.sameValue(Number(2n ** 64n + 2049n), 2 ** 64 + 4096, "above tie rounds up");
assert.sameValue(Number(2n ** 64n + 6144n), 2 ** 64 + 8192, "tie rounds to even (up)");
assert.sameValue(Number(2n ** 1024n - 2n ** 970n - 1n), Number.MAX_VALUE, "max value");
assert.sameValue(Number(2n ** 1024n - 2n ** 970n), Infinity, "overflow tie");
assert.sameValue(Number(10n ** 400n), Infinity, "+Infinity");
assert.sameValue(Number(-(10n ** 400n)), -Infinity, "-Infinity");
assert.sameValue(Number(Object(2n ** 64n)), 2 ** 64, "Object");
assert.sameValue(Number({valueOf() { return 2n ** 64n; }}), 2 ** 64, "valueOf");
assert.sameValue(new Number(2n ** 64n).valueOf(), 2 ** 64, "new");
assert.sameValue(new Number(Object(2n ** 64n)).valueOf(), 2 ** 64, "new Object");

assert.sameValue(Number(0n), 0, "0");
assert.sameValue(Number(10n), 10, "10");
assert.sameValue(Number(-5n), -5, "-5");
assert.sameValue(Number(2n ** 53n + 1n), 2 ** 53, "2^53+1");
assert.sameValue(Number(2n ** 63n - 1n), 2 ** 63, "2^63-1");
assert.sameValue(new Number(7n).valueOf(), 7, "new 7");
`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}
