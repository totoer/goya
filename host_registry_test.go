package goya_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/totoer/goya"
)

func freezeHost(t *testing.T, registry *goya.Registry) *goya.Environment {
	t.Helper()
	environment, err := registry.Freeze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return environment
}

func registerHost(t *testing.T, registry *goya.Registry, name string, value any) {
	t.Helper()
	if err := registry.Register(name, value); err != nil {
		t.Fatal(err)
	}
}

func checkHost(t *testing.T, environment *goya.Environment, source string, wantError bool) []goya.Diagnostic {
	t.Helper()
	diagnostics, err := goya.Check(context.Background(), goya.Project{Files: map[string]string{"/main.ts": source}}, environment, nil)
	if err != nil || goya.HasTypeErrors(diagnostics) != wantError {
		t.Fatalf("checking %q: %v, %+v", source, err, diagnostics)
	}
	return diagnostics
}

func TestHostRegistryFreezeSnapshot(t *testing.T) {
	registry := goya.NewRegistry()
	registerHost(t, registry, "title", "first")
	exports := map[string]any{"default": false, "odd-name": float64(3)}
	if err := registry.RegisterModule("host:settings", exports); err != nil {
		t.Fatal(err)
	}
	environment := freezeHost(t, registry)
	digest := environment.SchemaID()
	exports["default"] = "changed"
	registerHost(t, registry, "later", true)
	declarations := environment.TypeDeclarations()
	for key := range declarations {
		declarations[key] = "broken"
	}
	checkHost(t, environment, `import enabled, { "odd-name" as n } from "host:settings"; const a: boolean = enabled; const b: number = n; const c: string = title;`, false)
	checkHost(t, environment, `later;`, true)
	checkHost(t, environment, `import { __goya_export_0 } from "host:settings";`, true)
	if environment.SchemaID() != digest {
		t.Fatal("frozen schema changed")
	}
	other := goya.NewRegistry()
	if err := other.RegisterModule("host:settings", map[string]any{"odd-name": float64(19), "default": true}); err != nil {
		t.Fatal(err)
	}
	registerHost(t, other, "title", "another implementation")
	if got := freezeHost(t, other).SchemaID(); got != digest {
		t.Fatalf("registration order or values affect schema: %s != %s", got, digest)
	}
	var empty goya.Registry
	if freezeHost(t, &empty).SchemaID() != freezeHost(t, goya.NewRegistry()).SchemaID() {
		t.Fatal("zero and constructed registries differ")
	}
}

func TestHostRegistryFunctions(t *testing.T) {
	calls := 0
	registry := goya.NewRegistry()
	registerHost(t, registry, "add", func(a, b float64) float64 { calls++; return a + b })
	registerHost(t, registry, "label", func(enabled bool, label string) string {
		if !enabled {
			return "off"
		}
		return label
	})
	environment := freezeHost(t, registry)
	checkHost(t, environment, `const result: number = add(1, 2); const labelResult: string = label(true, "ready");`, false)
	diagnostics := checkHost(t, environment, `add("1", 2);`, true)
	if len(diagnostics) != 1 || diagnostics[0].Code != 2345 {
		t.Fatalf("wrong argument diagnostic: %+v", diagnostics)
	}
	if calls != 0 {
		t.Fatal("type checking invoked Go")
	}
	runtime := goya.New()
	if err := environment.Bind(runtime); err != nil {
		t.Fatal(err)
	}
	add, ok := goya.AssertFunction(runtime.Get("add"))
	if !ok {
		t.Fatal("registered value is not callable")
	}
	result, err := add(goya.Undefined(), runtime.ToValue(1), runtime.ToValue(2))
	if err != nil || result.ToFloat() != 3 || calls != 1 {
		t.Fatalf("add: %v %v, calls=%d", result, err, calls)
	}
	for _, args := range [][]goya.Value{{runtime.ToValue("1"), runtime.ToValue(2)}, {runtime.ToValue(1)}, {runtime.ToValue(1), runtime.ToValue(2), runtime.ToValue(3)}} {
		if _, err := add(goya.Undefined(), args...); err == nil {
			t.Fatal("invalid host arguments accepted")
		}
	}
	if calls != 1 {
		t.Fatal("invalid arguments reached Go")
	}
	if err := runtime.Set("add", 0); err == nil {
		t.Fatal("host binding is writable")
	}
	if err := environment.Bind(runtime); err != nil {
		t.Fatal("same binding should be idempotent", err)
	}
	if err := freezeHost(t, registry).Bind(runtime); err == nil {
		t.Fatal("different environment replaced binding")
	}
}

func TestHostRegistryIntegerBoundary(t *testing.T) {
	calls := 0
	registry := goya.NewRegistry()
	registerHost(t, registry, "signed", func(value int8) int8 { calls++; return value })
	registerHost(t, registry, "unsigned", func(value uint32) uint32 { calls++; return value })
	environment := freezeHost(t, registry)
	runtime := goya.New()
	if err := environment.Bind(runtime); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name           string
		valid, invalid []float64
	}{
		{"signed", []float64{-128, 127}, []float64{-129, 128, 0.5, math.NaN(), math.Inf(1)}},
		{"unsigned", []float64{0, 4294967295}, []float64{-1, 4294967296, 0.5}},
	} {
		function, _ := goya.AssertFunction(runtime.Get(tc.name))
		for _, number := range tc.valid {
			result, err := function(goya.Undefined(), runtime.ToValue(number))
			if err != nil || result.ToFloat() != number {
				t.Fatalf("%s(%v): %v %v", tc.name, number, result, err)
			}
		}
		before := calls
		for _, number := range tc.invalid {
			if _, err := function(goya.Undefined(), runtime.ToValue(number)); err == nil {
				t.Fatalf("%s accepted %v", tc.name, number)
			}
		}
		if calls != before {
			t.Fatal("invalid integer reached Go")
		}
	}
}

type namedHostNumber float64
type namedHostFunction func(namedHostNumber) namedHostNumber
type recursiveHostCallback func() recursiveHostCallback

func TestHostRegistrySchemaConversionRules(t *testing.T) {
	makeEnvironment := func(value any) *goya.Environment {
		registry := goya.NewRegistry()
		registerHost(t, registry, "value", value)
		return freezeHost(t, registry)
	}
	a := makeEnvironment(func(n float64) float64 { return n })
	b := makeEnvironment(namedHostFunction(func(n namedHostNumber) namedHostNumber { return n }))
	if a.SchemaID() != b.SchemaID() {
		t.Fatal("equivalent named types differ")
	}
	if makeEnvironment(func(n int8) int8 { return n }).SchemaID() == makeEnvironment(func(n int16) int16 { return n }).SchemaID() {
		t.Fatal("integer ranges omitted from schema")
	}
	for _, value := range []any{int64(1), int(1), float32(1), func(v any) {}, goya.Undefined(), string([]byte{255})} {
		if err := goya.NewRegistry().Register("bad", value); err == nil {
			t.Fatalf("ambiguous value accepted: %T", value)
		}
	}
}

type testHostAdapter struct {
	id    string
	calls int
}

func (a *testHostAdapter) SchemaID() string { return a.id }
func (a *testHostAdapter) Bind(r *goya.Runtime, value any) (goya.Value, error) {
	a.calls++
	return r.ToValue(value), nil
}

func TestHostRegistryExplicitDeclarations(t *testing.T) {
	adapter := &testHostAdapter{id: "text/v1"}
	registry := goya.NewRegistry()
	registerHost(t, registry, "text", goya.HostBinding{Value: "ok", TypeScript: "HostText", Adapter: adapter})
	if err := registry.DeclareTypes("/types/host.d.ts", `type HostText = string;`); err != nil {
		t.Fatal(err)
	}
	environment := freezeHost(t, registry)
	checkHost(t, environment, `const a: string = text;`, false)
	if adapter.calls != 0 {
		t.Fatal("checking bound adapter")
	}
	adapter.id = "text/v2"
	if err := environment.Bind(goya.New()); err == nil || adapter.calls != 0 {
		t.Fatal("changed adapter schema accepted")
	}
	bad := goya.NewRegistry()
	registerHost(t, bad, "broken", goya.HostBinding{Value: "ok", TypeScript: "UnknownHostType", Adapter: adapter})
	_, err := bad.Freeze(context.Background())
	var checkError *goya.TypeCheckError
	if !errors.As(err, &checkError) || !goya.HasTypeErrors(checkError.Diagnostics) {
		t.Fatalf("invalid host declarations: %v", err)
	}
}

func TestHostRegistryExplicitDynamicAndCallback(t *testing.T) {
	registry := goya.NewRegistry()
	for _, binding := range []goya.HostBinding{
		{Value: func(goya.FunctionCall) goya.Value { return goya.Undefined() }},
		{Value: map[string]any{"answer": 42}},
		{Value: 42, TypeScript: "number"},
		{Value: 42, Adapter: goya.HostAdapterFuncs{ID: "number/v1"}},
	} {
		if err := registry.Register("bad", binding); err == nil {
			t.Fatal("incomplete explicit contract accepted")
		}
	}
	binds, calls := 0, 0
	adapter := goya.HostAdapterFuncs{ID: "strict-number-callback/v1", BindValue: func(r *goya.Runtime, _ any) (goya.Value, error) {
		binds++
		return r.ToValue(func(call goya.FunctionCall) goya.Value {
			if len(call.Arguments) != 1 {
				panic(r.NewTypeError("expected one argument"))
			}
			switch call.Argument(0).Export().(type) {
			case int64, float64:
			default:
				panic(r.NewTypeError("expected number"))
			}
			calls++
			return r.ToValue(call.Argument(0).ToFloat() + 1)
		}), nil
	}}
	registerHost(t, registry, "callback", goya.HostBinding{Value: struct{}{}, TypeScript: "(value: number) => number", Adapter: adapter})
	registerHost(t, registry, "dynamic", goya.HostBinding{TypeScript: "{readonly answer: number}", Adapter: goya.HostAdapterFuncs{ID: "dynamic-answer/v1", BindValue: func(r *goya.Runtime, _ any) (goya.Value, error) {
		object := r.NewObject()
		if err := object.DefineDataProperty("answer", r.ToValue(42), goya.FLAG_FALSE, goya.FLAG_FALSE, goya.FLAG_TRUE); err != nil {
			return nil, err
		}
		return object, nil
	}}})
	environment := freezeHost(t, registry)
	checkHost(t, environment, `const n: number = callback(dynamic.answer);`, false)
	checkHost(t, environment, `callback("wrong"); dynamic.answer = 3;`, true)
	if binds != 0 || calls != 0 {
		t.Fatal("Check invoked adapter")
	}
	r := goya.New()
	if err := environment.Bind(r); err != nil {
		t.Fatal(err)
	}
	if hostCall(t, r, r.Get("callback"), r.ToValue(2)).ToInteger() != 3 || calls != 1 || binds != 1 {
		t.Fatal("callback was not bound")
	}
	function, _ := goya.AssertFunction(r.Get("callback"))
	if _, err := function(goya.Undefined(), r.ToValue("2")); err == nil || calls != 1 {
		t.Fatal("adapter failed to validate boundary")
	}
	foreign := goya.New().NewObject()
	bad := goya.NewRegistry()
	registerHost(t, bad, "foreign", goya.HostBinding{TypeScript: "object", Adapter: goya.HostAdapterFuncs{ID: "foreign/v1", BindValue: func(*goya.Runtime, any) (goya.Value, error) { return foreign, nil }}})
	if err := freezeHost(t, bad).Bind(goya.New()); err == nil {
		t.Fatal("foreign runtime value accepted")
	}
	failing := goya.NewRegistry()
	registerHost(t, failing, "failed", goya.HostBinding{TypeScript: "unknown", Adapter: goya.HostAdapterFuncs{ID: "failure/v1", BindValue: func(*goya.Runtime, any) (goya.Value, error) { return nil, fmt.Errorf("invalid result") }}})
	if err := freezeHost(t, failing).Bind(goya.New()); err == nil {
		t.Fatal("adapter conversion error ignored")
	}
}

func TestHostBindingSchemaIncludesReferencedDeclarations(t *testing.T) {
	makeEnvironment := func(definition string, extra bool) *goya.Environment {
		registry := goya.NewRegistry()
		adapter := goya.HostAdapterFuncs{ID: "identity-declared/v1", BindValue: func(r *goya.Runtime, _ any) (goya.Value, error) {
			return r.ToValue(func(call goya.FunctionCall) goya.Value { return call.Argument(0) }), nil
		}}
		registerHost(t, registry, "identity", goya.HostBinding{TypeScript: "(value: Contract) => Contract", Adapter: adapter})
		if err := registry.DeclareTypes("/contract.d.ts", definition); err != nil {
			t.Fatal(err)
		}
		if extra {
			registerHost(t, registry, "unrelated", true)
			if err := registry.DeclareTypes("/extra.d.ts", `interface Unrelated {other: boolean}`); err != nil {
				t.Fatal(err)
			}
		}
		return freezeHost(t, registry)
	}
	strings := makeEnvironment(`type Contract = string;`, false)
	program := compileTypeScript(t, `identity("text");`, strings)
	numbers := makeEnvironment(`type Contract = number;`, false)
	r := goya.New()
	if err := numbers.Bind(r); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunProgram(program); err == nil {
		t.Fatal("changed alias hid behind the same adapter ID")
	}
	compatible := goya.New()
	if err := makeEnvironment(`type Contract = string;`, true).Bind(compatible); err != nil {
		t.Fatal(err)
	}
	result, err := compatible.RunProgram(program)
	if err != nil || result.String() != "text" {
		t.Fatalf("unrelated declaration changed required schema: %v %v", result, err)
	}
}

func TestHostBindingSchemaFollowsImportedTypeAliases(t *testing.T) {
	makeEnvironment := func(typ string) *goya.Environment {
		registry := goya.NewRegistry()
		adapter := goya.HostAdapterFuncs{ID: "import-type-identity/v1", BindValue: func(r *goya.Runtime, _ any) (goya.Value, error) {
			return r.ToValue(func(call goya.FunctionCall) goya.Value { return call.Argument(0) }), nil
		}}
		registerHost(t, registry, "identity", goya.HostBinding{TypeScript: "(value: Contract) => Contract", Adapter: adapter})
		if err := registry.DeclareTypes("/dto.d.ts", "export type DTO = "+typ+";"); err != nil {
			t.Fatal(err)
		}
		if err := registry.DeclareTypes("/contract.d.ts", `type Contract = import("./dto.js").DTO;`); err != nil {
			t.Fatal(err)
		}
		return freezeHost(t, registry)
	}
	program := compileTypeScript(t, `identity("text");`, makeEnvironment("string"))
	r := goya.New()
	if err := makeEnvironment("number").Bind(r); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunProgram(program); err == nil {
		t.Fatal("changed import type alias hid behind unchanged declarations")
	}
}
