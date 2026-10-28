package goya_test

import (
	"math"
	"math/big"
	"testing"

	"github.com/totoer/goya"
)

func TestHostNumericPolicies(t *testing.T) {
	registry := goya.NewRegistry()
	calls := 0
	bigints := goya.NumericPolicy{Integers: goya.IntegerBigInt}
	safe := goya.NumericPolicy{Integers: goya.IntegerSafeNumber}
	registerHost(t, registry, "signed", goya.HostBinding{Value: func(v int64) int64 { calls++; return v }, Numbers: bigints})
	registerHost(t, registry, "unsigned", goya.HostBinding{Value: func(v uint64) uint64 { calls++; return v }, Numbers: bigints})
	registerHost(t, registry, "safe", goya.HostBinding{Value: func(v int64) int64 { calls++; return v }, Numbers: safe})
	registerHost(t, registry, "unsafeResult", goya.HostBinding{Value: func() uint64 { return math.MaxUint64 }, Numbers: safe})
	registerHost(t, registry, "platform", goya.HostBinding{Value: func(v int) int { return v }, Numbers: goya.NumericPolicy{Integers: goya.IntegerBigInt, IntBits: 32}})
	registerHost(t, registry, "exact", goya.HostBinding{Value: func(v float32) float32 { calls++; return v }, Numbers: goya.NumericPolicy{Float32: goya.Float32Exact}})
	registerHost(t, registry, "rounded", goya.HostBinding{Value: func(v float32) float32 { calls++; return v }, Numbers: goya.NumericPolicy{Float32: goya.Float32Round}})
	environment := freezeHost(t, registry)
	checkHost(t, environment, `const a: bigint = signed(9223372036854775807n); const b: bigint = unsigned(18446744073709551615n); const c: number = safe(123); const d: number = rounded(0.1);`, false)
	checkHost(t, environment, `signed(1); unsigned("1"); safe(1n);`, true)
	r := goya.New()
	if err := environment.Bind(r); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, value string }{{"signed", "-9223372036854775808"}, {"signed", "9223372036854775807"}, {"unsigned", "18446744073709551615"}} {
		number, _ := new(big.Int).SetString(tc.value, 10)
		got := hostCall(t, r, r.Get(tc.name), r.ToValue(number)).Export().(*big.Int)
		if got.Cmp(number) != 0 {
			t.Fatal("bigint roundtrip lost precision")
		}
	}
	for _, tc := range []struct{ name, value string }{{"signed", "9223372036854775808"}, {"signed", "-9223372036854775809"}, {"unsigned", "18446744073709551616"}, {"unsigned", "-1"}, {"platform", "2147483648"}} {
		number, _ := new(big.Int).SetString(tc.value, 10)
		function, _ := goya.AssertFunction(r.Get(tc.name))
		before := calls
		if _, err := function(goya.Undefined(), r.ToValue(number)); err == nil || calls != before {
			t.Fatalf("out-of-range %s(%s) reached Go", tc.name, tc.value)
		}
	}
	for _, number := range []float64{-9007199254740991, 9007199254740991} {
		if hostCall(t, r, r.Get("safe"), r.ToValue(number)).ToFloat() != number {
			t.Fatal("safe boundary changed")
		}
	}
	for _, number := range []float64{-9007199254740992, 9007199254740992, 1.5, math.NaN(), math.Inf(1)} {
		function, _ := goya.AssertFunction(r.Get("safe"))
		before := calls
		if _, err := function(goya.Undefined(), r.ToValue(number)); err == nil || calls != before {
			t.Fatalf("unsafe number accepted: %v", number)
		}
	}
	function, _ := goya.AssertFunction(r.Get("unsafeResult"))
	if _, err := function(goya.Undefined()); err == nil {
		t.Fatal("unsafe Go result was rounded")
	}
	if hostCall(t, r, r.Get("exact"), r.ToValue(0.5)).ToFloat() != 0.5 {
		t.Fatal("exact float32 conversion")
	}
	exact, _ := goya.AssertFunction(r.Get("exact"))
	if _, err := exact(goya.Undefined(), r.ToValue(0.1)); err == nil {
		t.Fatal("inexact float32 silently rounded")
	}
	if got := hostCall(t, r, r.Get("rounded"), r.ToValue(0.1)).ToFloat(); got != float64(float32(0.1)) {
		t.Fatal("explicit rounding differs from Go float32")
	}
	rounded, _ := goya.AssertFunction(r.Get("rounded"))
	if _, err := rounded(goya.Undefined(), r.ToValue(math.MaxFloat64)); err == nil {
		t.Fatal("float32 overflow accepted")
	}
	negativeZero := hostCall(t, r, r.Get("exact"), r.ToValue(math.Copysign(0, -1))).ToFloat()
	if !math.Signbit(negativeZero) {
		t.Fatal("negative zero lost")
	}
}

func TestHostNumericPolicyRegistrationAndSchema(t *testing.T) {
	for _, binding := range []goya.HostBinding{
		{Value: int(1), Numbers: goya.NumericPolicy{Integers: goya.IntegerBigInt}},
		{Value: uintptr(1), Numbers: goya.NumericPolicy{Integers: goya.IntegerBigInt, IntBits: 16}},
		{Value: float32(1), Numbers: goya.NumericPolicy{Float32: "truncate"}},
		{Value: int64(1), Numbers: goya.NumericPolicy{Integers: "number"}},
	} {
		if err := goya.NewRegistry().Register("bad", binding); err == nil {
			t.Fatal("invalid numeric policy accepted")
		}
	}
	makeEnvironment := func(policy goya.NumericPolicy) *goya.Environment {
		registry := goya.NewRegistry()
		registerHost(t, registry, "value", goya.HostBinding{Value: int64(1), Numbers: policy})
		return freezeHost(t, registry)
	}
	if makeEnvironment(goya.NumericPolicy{Integers: goya.IntegerBigInt}).SchemaID() == makeEnvironment(goya.NumericPolicy{Integers: goya.IntegerSafeNumber}).SchemaID() {
		t.Fatal("numeric policy omitted from digest")
	}
}
