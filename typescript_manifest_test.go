package goya_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/totoer/goya"
)

func TestTypeScriptPreflightTracksTypedGlobalObjectAccess(t *testing.T) {
	for name, source := range map[string]string{
		"property":     `touch();globalThis.console.log("blocked");`,
		"element":      `touch();globalThis["console"].log("blocked");`,
		"typed alias":  `const shared=globalThis;touch();shared.console.log("blocked");`,
		"constant key": `const key="console" as const;touch();globalThis[key].log("blocked");`,
	} {
		t.Run(name, func(t *testing.T) {
			effects := 0
			registry := goya.NewRegistry()
			registerHost(t, registry, "touch", func() { effects++ })
			environment := freezeHost(t, registry)
			program := goya.MustCompile(context.Background(), goya.Project{Files: map[string]string{"/main.ts": source}}, environment, goya.CompileOptions{"lib": []string{"es2025", "dom"}})
			found := false
			for _, binding := range program.Manifest().HostBindings {
				if binding.Name == "console" {
					found = true
				}
			}
			if !found {
				t.Fatal("global object access lost its required capability")
			}
			runtime := goya.New()
			if err := environment.Bind(runtime); err != nil {
				t.Fatal(err)
			}
			_, err := runtime.RunProgram(program)
			if _, ok := err.(*goya.BindingCompatibilityError); !ok || effects != 0 {
				t.Fatalf("preflight: %v effects=%d", err, effects)
			}
		})
	}
}

func TestTypeScriptUnsupportedStandardProfileFailsCompilation(t *testing.T) {
	for _, source := range []string{
		"throw new Error(\"must not execute\");Temporal.Instant.fromEpochMilliseconds(0);",
		"throw new Error(\"must not execute\");globalThis.Temporal.Instant.fromEpochMilliseconds(0);",
		"const shared=globalThis;throw new Error(\"must not execute\");shared[\"Temporal\"].Instant.fromEpochMilliseconds(0);",
	} {
		project := goya.Project{Files: map[string]string{"/main.ts": source}}
		options := goya.CompileOptions{"lib": []string{"esnext"}}
		diagnostics, err := goya.Check(context.Background(), project, nil, options)
		if err != nil || goya.HasTypeErrors(diagnostics) {
			t.Fatalf("native optional-profile checking: %v %+v", err, diagnostics)
		}
		program, diagnostics, err := goya.Compile(context.Background(), project, nil, options)
		found := false
		for _, diagnostic := range diagnostics {
			if diagnostic.Namespace == "goya" && diagnostic.Code == 1104 {
				found = true
			}
		}
		if err != nil || program != nil || !found {
			t.Fatalf("unsupported profile compiled: %v %+v %v", program, diagnostics, err)
		}
	}
}

func TestTypeScriptProgramManifestDetachedAndStable(t *testing.T) {
	makeEnvironment := func(value float64) *goya.Environment {
		r := goya.NewRegistry()
		registerHost(t, r, "answer", func() float64 { return value })
		return freezeHost(t, r)
	}
	project := goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": `const result:number=answer();result;`}}
	a := goya.MustCompile(context.Background(), project, makeEnvironment(1), nil)
	b := goya.MustCompile(context.Background(), project, makeEnvironment(2), nil)
	m := a.Manifest()
	if !reflect.DeepEqual(m, b.Manifest()) || m.TypeScriptVersion != goya.TypeScriptVersion() || m.ProfileID == "" || m.AnalysisID == "" || len(m.Sources) == 0 || len(m.HostBindings) != 1 || m.HostBindings[0].Name != "answer" || m.HostBindings[0].SchemaID == "" {
		t.Fatalf("manifest: %+v", m)
	}
	m.EntryPoints[0] = "corrupt"
	m.Sources[0].SHA256 = "corrupt"
	m.HostBindings[0].SchemaID = "corrupt"
	if !reflect.DeepEqual(a.Manifest(), b.Manifest()) {
		t.Fatal("manifest exposes mutable program state")
	}
	c := goya.MustCompile(context.Background(), project, makeEnvironment(3), goya.CompileOptions{"target": "es2024"})
	if c.Manifest().ProfileID == a.Manifest().ProfileID {
		t.Fatal("profile ignores options")
	}
}

func TestTypeScriptPreflightRequiresExternalLibraryAndSuppressedNames(t *testing.T) {
	for _, tc := range []struct {
		source  string
		options goya.CompileOptions
	}{
		{`throw new Error("must not execute"); console.log("hello");`, goya.CompileOptions{"lib": []string{"es2025", "dom"}}},
		{"throw new Error(\"must not execute\");\n// @ts-expect-error intentionally unregistered\nmissingCapability();", nil},
	} {
		p, ds, err := goya.Compile(context.Background(), goya.Project{Files: map[string]string{"/main.ts": tc.source}}, nil, tc.options)
		if err != nil || p == nil {
			t.Fatalf("compile: %v %+v", err, ds)
		}
		_, err = goya.New().RunProgram(p)
		if _, ok := err.(*goya.BindingCompatibilityError); !ok {
			t.Fatalf("external capability reached effects: %v", err)
		}
	}
}

func TestTypeScriptDefaultEnvironmentAndExplicitLogging(t *testing.T) {
	r := goya.New()
	v, err := r.RunString(`["console","require","process","readFile","load","setTimeout","setInterval","fetch"].every(name=>!Reflect.has(globalThis,name));`)
	if err != nil || !v.ToBoolean() {
		t.Fatalf("default capability: %v %v", v, err)
	}
	var messages []string
	registry := goya.NewRegistry()
	registerHost(t, registry, "log", func(s string) { messages = append(messages, s) })
	env := freezeHost(t, registry)
	if err := env.Bind(r); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunString(`log("explicit");`); err != nil || !reflect.DeepEqual(messages, []string{"explicit"}) {
		t.Fatalf("explicit logging: %v %v", messages, err)
	}
}
