package goya_test

import (
	"fmt"
	"testing"

	"github.com/totoer/goya"
)

func TestHostFunctionRestOptionalTupleAndError(t *testing.T) {
	registry := goya.NewRegistry()
	calls := 0
	registerHost(t, registry, "join", func(prefix string, rest ...string) string {
		calls++
		for _, s := range rest {
			prefix += s
		}
		return prefix
	})
	registerHost(t, registry, "optional", func(v goya.Optional[int32]) string {
		if !v.Present {
			return "missing"
		}
		return fmt.Sprint(v.Value)
	})
	registerHost(t, registry, "optionalRest", func(v goya.Optional[string], rest ...string) string {
		result := "missing"
		if v.Present {
			result = v.Value
		}
		for _, s := range rest {
			result += s
		}
		return result
	})
	registerHost(t, registry, "pair", func(fail bool) (string, int32, error) {
		if fail {
			return "unused", 0, fmt.Errorf("failed")
		}
		return "first", 42, nil
	})
	registerHost(t, registry, "onlyError", func(fail bool) error {
		if fail {
			return fmt.Errorf("failed")
		}
		return nil
	})
	registerHost(t, registry, "nothing", func() { calls++ })
	registerHost(t, registry, "maybe", func(present bool) goya.Optional[string] {
		return goya.Optional[string]{Value: "present", Present: present}
	})
	environment := freezeHost(t, registry)
	checkHost(t, environment, `const a: string = join("a", "b", "c"); optional(); optional(undefined); optional(0); optionalRest(); optionalRest(undefined, "a"); const p: [string,number] = pair(false); const v: void = onlyError(false); const o: string | undefined = maybe(true);`, false)
	checkHost(t, environment, `join(); join("a", 1); pair(); nothing(1); optional("a");`, true)
	r := goya.New()
	if err := environment.Bind(r); err != nil {
		t.Fatal(err)
	}
	if hostCall(t, r, r.Get("join"), r.ToValue("a"), r.ToValue("b"), r.ToValue("c")).String() != "abc" {
		t.Fatal("rest order")
	}
	for _, args := range [][]goya.Value{nil, {goya.Undefined()}} {
		if hostCall(t, r, r.Get("optional"), args...).String() != "missing" {
			t.Fatal("missing optional argument")
		}
	}
	if hostCall(t, r, r.Get("optional"), r.ToValue(0)).String() != "0" {
		t.Fatal("zero conflated with missing")
	}
	if hostCall(t, r, r.Get("optionalRest")).String() != "missing" || hostCall(t, r, r.Get("optionalRest"), goya.Undefined(), r.ToValue("x")).String() != "missingx" {
		t.Fatal("optional/rest boundary")
	}
	pair := hostObject(t, hostCall(t, r, r.Get("pair"), r.ToValue(false)))
	if pair.Get("0").String() != "first" || pair.Get("1").ToInteger() != 42 || pair.Get("length").ToInteger() != 2 {
		t.Fatal("tuple order/type")
	}
	for _, name := range []string{"pair", "onlyError"} {
		function, _ := goya.AssertFunction(r.Get(name))
		if _, err := function(goya.Undefined(), r.ToValue(true)); err == nil {
			t.Fatal("Go error not thrown")
		}
	}
	if !goya.IsUndefined(hostCall(t, r, r.Get("onlyError"), r.ToValue(false))) || !goya.IsUndefined(hostCall(t, r, r.Get("nothing"))) {
		t.Fatal("void result is not undefined")
	}
	if !goya.IsUndefined(hostCall(t, r, r.Get("maybe"), r.ToValue(false))) || hostCall(t, r, r.Get("maybe"), r.ToValue(true)).String() != "present" {
		t.Fatal("optional return")
	}
	before := calls
	function, _ := goya.AssertFunction(r.Get("join"))
	if _, err := function(goya.Undefined(), r.ToValue("a"), r.ToValue(2)); err == nil || calls != before {
		t.Fatal("invalid rest element reached Go")
	}
	bad := goya.NewRegistry()
	if err := bad.Register("bad", func(goya.Optional[int32], string) {}); err == nil {
		t.Fatal("required argument after optional accepted")
	}
}

func TestHostTypedCallbackBoundary(t *testing.T) {
	registry := goya.NewRegistry()
	registerHost(t, registry, "invoke", func(fn func(int32) int32) int32 { return fn(3) })
	registerHost(t, registry, "factory", func() func(int32) int32 { return func(n int32) int32 { return n + 1 } })
	environment := freezeHost(t, registry)
	checkHost(t, environment, `const n: number = invoke(x => x + 1); const m: number = factory()(1);`, false)
	r := goya.New()
	if err := environment.Bind(r); err != nil {
		t.Fatal(err)
	}
	callback := r.ToValue(func(call goya.FunctionCall) goya.Value { return r.ToValue(call.Argument(0).ToInteger() + 1) })
	if hostCall(t, r, r.Get("invoke"), callback).ToInteger() != 4 {
		t.Fatal("typed callback conversion")
	}
	bad := r.ToValue(func(goya.FunctionCall) goya.Value { return r.ToValue("wrong") })
	invoke, _ := goya.AssertFunction(r.Get("invoke"))
	if _, err := invoke(goya.Undefined(), bad); err == nil {
		t.Fatal("wrong callback result accepted")
	}
	if hostCall(t, r, hostCall(t, r, r.Get("factory")), r.ToValue(2)).ToInteger() != 3 {
		t.Fatal("returned callback conversion")
	}
}

func TestHostBoundaryChecksSuppressedTypesAndResults(t *testing.T) {
	registry := goya.NewRegistry()
	calls := 0
	registerHost(t, registry, "consume", func(n int32) int32 { calls++; return n })
	registerHost(t, registry, "badText", func() string { return string([]byte{255}) })
	registerHost(t, registry, "badFunction", func() func() { return nil })
	registerHost(t, registry, "text", func(s string) string { calls++; return s })
	environment := freezeHost(t, registry)
	checkHost(t, environment, `consume("wrong" as any); consume("wrong" as unknown as number);`, false)
	r := goya.New()
	if err := environment.Bind(r); err != nil {
		t.Fatal(err)
	}
	consume, _ := goya.AssertFunction(r.Get("consume"))
	if _, err := consume(goya.Undefined(), r.ToValue("wrong")); err == nil || calls != 0 {
		t.Fatal("suppressed type error bypassed boundary")
	}
	for _, name := range []string{"badText", "badFunction"} {
		function, _ := goya.AssertFunction(r.Get(name))
		if _, err := function(goya.Undefined()); err == nil {
			t.Fatalf("%s contract violation not detected", name)
		}
	}
	text, _ := goya.AssertFunction(r.Get("text"))
	if _, err := text(goya.Undefined(), goya.StringFromUTF16([]uint16{0xd800})); err == nil || calls != 0 {
		t.Fatal("ill-formed string was silently replaced")
	}
	valid := goya.StringFromUTF16([]uint16{0xd83d, 0xde00})
	if hostCall(t, r, r.Get("text"), valid).String() != "😀" {
		t.Fatal("valid surrogate pair rejected")
	}
}
