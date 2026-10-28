package goya_test

import (
	"context"
	"testing"

	"github.com/totoer/goya"
)

func configuredReferenceProject() goya.Project {
	return goya.Project{ConfigFile: "/app/tsconfig.json", Files: map[string]string{
		"/settings/base.json": `{"compilerOptions":{"strict":true,"target":"es2025","module":"esnext","moduleResolution":"bundler","lib":["es2025"],"types":[]}}`,
		"/app/tsconfig.json":  `{"extends":"../settings/base.json","files":["main.ts"],"references":[{"path":"../lib"}]}`,
		"/app/main.ts":        `import {answer} from "../lib/dist/value.js"; export const result:number=answer+1;`,
		"/lib/tsconfig.json":  `{"extends":"../settings/base.json","compilerOptions":{"composite":true,"rootDir":"src","outDir":"dist"},"files":["src/value.ts"]}`,
		"/lib/src/value.ts":   `export const answer=41;`,
	}}
}

func TestVirtualConfigReferenceCompileAndExecuteWithoutOutputs(t *testing.T) {
	ctx := context.Background()
	project := configuredReferenceProject()
	ds, err := goya.Check(ctx, project, nil, nil)
	if err != nil || goya.HasTypeErrors(ds) {
		t.Fatalf("Check: %v %+v", err, ds)
	}
	p, ds, err := goya.Compile(ctx, project, nil, nil)
	if err != nil || p == nil {
		t.Fatalf("Compile: %v %+v", err, ds)
	}
	r := goya.New()
	value, err := r.RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	if value.ToObject(r).Get("result").ToInteger() != 42 {
		t.Fatalf("wrong referenced module result: %v", value)
	}
}

func TestVirtualConfigTypeErrorUsesOriginalSource(t *testing.T) {
	project := configuredReferenceProject()
	project.Files["/lib/src/value.ts"] = `export const answer:number="wrong";`
	p, ds, err := goya.Compile(context.Background(), project, nil, nil)
	if err != nil || p != nil {
		t.Fatalf("invalid graph compiled: %v %+v", err, ds)
	}
	for _, d := range ds {
		if d.Code == 2322 && d.File == "/lib/src/value.ts" && d.Range != nil {
			return
		}
	}
	t.Fatalf("missing original source diagnostic: %+v", ds)
}

func TestConfigIsExplicitAndHostOptionsTakePrecedence(t *testing.T) {
	ctx := context.Background()
	p := goya.Project{Files: map[string]string{
		"/main.ts":       `function twice(value){return value*2;}`,
		"/tsconfig.json": `{"compilerOptions":{"strict":false},"files":["main.ts"]}`,
	}}
	ds, err := goya.Check(ctx, p, nil, nil)
	if err != nil || !goya.HasTypeErrors(ds) {
		t.Fatalf("empty ConfigFile changed strict default: %v %+v", err, ds)
	}
	p.ConfigFile = "/tsconfig.json"
	ds, err = goya.Check(ctx, p, nil, nil)
	if err != nil || goya.HasTypeErrors(ds) {
		t.Fatalf("explicit config ignored: %v %+v", err, ds)
	}
	ds, err = goya.Check(ctx, p, nil, goya.CompileOptions{"strict": true})
	if err != nil || !goya.HasTypeErrors(ds) {
		t.Fatalf("host override ignored: %v %+v", err, ds)
	}
}

func TestVirtualConfigRejectsMissingAndUnsafeConfigurations(t *testing.T) {
	for _, config := range []string{"/missing.json", "file:///etc/passwd"} {
		p, ds, err := goya.Compile(context.Background(), goya.Project{ConfigFile: config, Files: map[string]string{"/main.ts": "export {};"}}, nil, nil)
		if err != nil || p != nil || !goya.HasTypeErrors(ds) {
			t.Fatalf("invalid config %s: %v %+v", config, err, ds)
		}
	}
	project := configuredReferenceProject()
	project.Files["/lib/tsconfig.json"] = `{"compilerOptions":{"composite":true,"skipLibCheck":true},"files":["src/value.ts"]}`
	p, ds, err := goya.Compile(context.Background(), project, nil, nil)
	if err != nil || p != nil || !goya.HasTypeErrors(ds) {
		t.Fatalf("reference bypassed checking gate: %v %+v", err, ds)
	}
}

func TestVirtualConfigDynamicReferenceImports(t *testing.T) {
	for _, source := range []string{
		`const value=await import("../lib/dist/value.js");export const result=value.answer+1;`,
		`const path:string="../lib/dist/value.js";const value=await import(path);export const result=value.answer+1;`,
		`import {answer} from "../lib/dist/value.js";const path:string="../lib/dist/value.js";const value=await import(path);export const result=value.answer===answer?answer+1:0;`,
	} {
		project := configuredReferenceProject()
		project.Files["/app/main.ts"] = source
		project.ModuleRoots = []string{"/lib/src/value.ts"}
		p, ds, err := goya.Compile(context.Background(), project, nil, nil)
		if err != nil || p == nil {
			t.Fatalf("compile: %v %+v", err, ds)
		}
		r := goya.New()
		value, err := r.RunProgram(p)
		if err != nil {
			t.Fatal(err)
		}
		promise, ok := value.Export().(*goya.Promise)
		if !ok {
			t.Fatalf("reference import not a promise: %v", value)
		}
		if promise.State() != goya.PromiseStateFulfilled {
			t.Fatalf("reference import %s: state=%v result=%v", source, promise.State(), promise.Result())
		}
		if promise.Result().ToObject(r).Get("result").ToInteger() != 42 {
			t.Fatalf("wrong dynamic result: %v", promise.Result())
		}
	}
}

func TestVirtualConfigReferenceAndSelectionInvalidateAnalysis(t *testing.T) {
	ctx := context.Background()
	project := configuredReferenceProject()
	ds, err := goya.Check(ctx, project, nil, nil)
	if err != nil || goya.HasTypeErrors(ds) {
		t.Fatalf("initial: %v %+v", err, ds)
	}
	project.Files["/lib/tsconfig.json"] = `{"compilerOptions":{"composite":true,"rootDir":"src","outDir":"other"},"files":["src/value.ts"]}`
	ds, err = goya.Check(ctx, project, nil, nil)
	if err != nil || !goya.HasTypeErrors(ds) {
		t.Fatalf("referenced config reused stale analysis: %v %+v", err, ds)
	}
	project = configuredReferenceProject()
	project.Files["/app/other.json"] = `{"files":["wrong.ts"]}`
	project.Files["/app/wrong.ts"] = `export const value:number="wrong";`
	ds, err = goya.Check(ctx, project, nil, nil)
	if err != nil || goya.HasTypeErrors(ds) {
		t.Fatalf("unselected config/source affected roots: %v %+v", err, ds)
	}
	project.ConfigFile = "/app/other.json"
	ds, err = goya.Check(ctx, project, nil, nil)
	if err != nil || !goya.HasTypeErrors(ds) {
		t.Fatalf("config selection reused stale analysis: %v %+v", err, ds)
	}
}
