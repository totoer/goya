package goya_test

import (
	"context"
	"github.com/totoer/goya"
	"testing"
)

func TestTypeScriptEvalIsAbsent(t *testing.T) {
	for _, target := range []string{"es2016", "es2017", "es2025", "esnext"} {
		for _, source := range []string{`eval("42");`, `const e=eval;e("42");`, `(0,eval)("42");`} {
			project := goya.Project{Files: map[string]string{"/main.ts": source}}
			options := goya.CompileOptions{"target": target}
			ds, err := goya.Check(context.Background(), project, nil, options)
			if err != nil || !goya.HasTypeErrors(ds) {
				t.Fatalf("eval accepted: %v %v", err, ds)
			}
			p, ds, err := goya.Compile(context.Background(), project, nil, options)
			if err != nil || p != nil || !goya.HasTypeErrors(ds) {
				t.Fatalf("eval compiled: %v %v", err, ds)
			}
		}
		p, ds, err := goya.Compile(context.Background(), goya.Project{Files: map[string]string{"/main.ts": `!Object.hasOwn(globalThis,"eval")&&(globalThis as any).eval===undefined;`}}, nil, goya.CompileOptions{"target": target})
		if err != nil || p == nil {
			t.Fatalf("compile %v %v", err, ds)
		}
		v, err := goya.New().RunProgram(p)
		if err != nil || !v.ToBoolean() {
			t.Fatalf("eval exposed: %v %v", v, err)
		}
	}
}

func TestTypeScriptEvalGlobalIsReserved(t *testing.T) {
	registry := goya.NewRegistry()
	if err := registry.Register("eval", func(source string) string { return source }); err == nil {
		t.Fatal("reserved eval global accepted")
	}
	registry.SetFieldNameMapper(goya.TagFieldNameMapper("json", true))
	api := struct {
		Eval func(string) string `json:"eval"`
	}{Eval: func(source string) string { return "data:" + source }}
	if err := registry.Register("api", api); err != nil {
		t.Fatal(err)
	}
	env, err := registry.Freeze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p, ds, err := goya.Compile(context.Background(), goya.Project{Files: map[string]string{"/main.ts": `api.eval("42")==="data:42";`}}, env, nil)
	if err != nil || p == nil {
		t.Fatalf("host eval: %v %v", err, ds)
	}
	r := goya.New()
	if err := env.Bind(r); err != nil {
		t.Fatal(err)
	}
	v, err := r.RunProgram(p)
	if err != nil || !v.ToBoolean() {
		t.Fatalf("host callback: %v %v", v, err)
	}
}
