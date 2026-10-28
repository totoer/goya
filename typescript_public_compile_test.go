package goya_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/totoer/goya"
)

func TestTypeScriptPublicCompileSharesCheckingGate(t *testing.T) {
	calls := 0
	registry := goya.NewRegistry()
	registerHost(t, registry, "record", func() { calls++ })
	environment := freezeHost(t, registry)
	for _, tc := range []struct {
		name    string
		files   map[string]string
		options goya.CompileOptions
	}{
		{"type", map[string]string{"/main.ts": `record();const value:number="wrong";`}, nil},
		{"syntax", map[string]string{"/main.ts": `record();const value: = 1;`}, nil},
		{"declaration", map[string]string{"/main.ts": `record();`, "/api.d.ts": `declare const value: MissingType;`}, nil},
		{"configuration", map[string]string{"/main.ts": `record();`}, goya.CompileOptions{"noCheck": true}},
		{"module", map[string]string{"/main.ts": `import {value} from "./missing.js";record();`}, nil},
		{"dependency", map[string]string{"/main.ts": `import {value} from "./value.js";record();`, "/value.ts": `record();export const value:number="wrong";`}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := goya.Project{EntryPoints: []string{"/main.ts"}, Files: tc.files}
			expected, err := goya.Check(context.Background(), project, environment, tc.options)
			if err != nil || !goya.HasTypeErrors(expected) {
				t.Fatalf("check: %v %+v", err, expected)
			}
			program, actual, err := goya.Compile(context.Background(), project, environment, tc.options)
			if err != nil || program != nil || !reflect.DeepEqual(actual, expected) || calls != 0 {
				t.Fatalf("compile gate: %v %+v != %+v; effects %d", err, actual, expected, calls)
			}
		})
	}
	project := goya.Project{Files: map[string]string{"/main.ts": `record();const value:number=42;value;`}}
	program, ds, err := goya.Compile(context.Background(), project, environment, nil)
	if err != nil || program == nil || goya.HasTypeErrors(ds) || calls != 0 {
		t.Fatalf("valid compile: %v %+v", err, ds)
	}
	runtime := goya.New()
	if err := environment.Bind(runtime); err != nil {
		t.Fatal(err)
	}
	value, err := runtime.RunProgram(program)
	if err != nil || value.ToInteger() != 42 || calls != 1 {
		t.Fatalf("checked execution: %v %v %d", value, err, calls)
	}
}

func TestTypeScriptCompileCapturesResolverOnce(t *testing.T) {
	calls := 0
	program, ds, err := goya.Compile(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, Resolver: goya.ResolverFunc(func(context.Context) (map[string]string, error) {
		calls++
		return map[string]string{"/main.ts": `const value:number=42;value;`}, nil
	})}, nil, nil)
	if err != nil || program == nil || goya.HasTypeErrors(ds) || calls != 1 {
		t.Fatalf("resolver capture: %v %+v calls %d", err, ds, calls)
	}
}

func TestTypeScriptConvenienceAPIsRequireChecking(t *testing.T) {
	calls := 0
	registry := goya.NewRegistry()
	registerHost(t, registry, "record", func(value float64) float64 { calls++; return value })
	environment := freezeHost(t, registry)
	runtime := goya.New()
	if err := environment.Bind(runtime); err != nil {
		t.Fatal(err)
	}
	for _, run := range []func() (goya.Value, error){
		func() (goya.Value, error) { return runtime.RunString(`record(1);const value:number="wrong";`) },
		func() (goya.Value, error) {
			return runtime.RunScript("/main.ts", `record(1);import {value} from "./missing.js";`)
		},
		func() (goya.Value, error) { return runtime.RunScript("/main.js", `record(1);`) },
	} {
		value, err := run()
		var compilation *goya.CompilationError
		if value != nil || !errors.As(err, &compilation) || !goya.HasTypeErrors(compilation.Diagnostics) || calls != 0 {
			t.Fatalf("unchecked execution: %v %v calls %d", value, err, calls)
		}
	}
	value, err := runtime.RunString(`const answer:number=record(42);answer;`)
	if err != nil || value.ToInteger() != 42 || calls != 1 {
		t.Fatalf("checked convenience: %v %v %d", value, err, calls)
	}
	program := goya.MustCompile(context.Background(), goya.Project{Files: map[string]string{"/main.ts": `record(42);`}}, environment, nil)
	if _, err := runtime.RunProgram(program); err != nil || calls != 2 {
		t.Fatalf("must compile: %v calls %d", err, calls)
	}
	func() {
		defer func() {
			value := recover()
			compilation, ok := value.(*goya.CompilationError)
			if !ok || !goya.HasTypeErrors(compilation.Diagnostics) || calls != 2 {
				t.Fatalf("must compile rejection: %v calls %d", value, calls)
			}
		}()
		goya.MustCompile(context.Background(), goya.Project{Files: map[string]string{"/main.ts": `record(1);const value:number="wrong";`}}, environment, nil)
	}()
}
