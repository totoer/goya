package goya_test

import (
	"context"
	"testing"

	"github.com/totoer/goya"
)

func TestTypeScriptDynamicImports(t *testing.T) {
	for _, tc := range []struct {
		name, entry string
		files       map[string]string
		options     goya.CompileOptions
	}{
		{"literal", "/main.ts", map[string]string{"/main.ts": `const {value}=await import("./value.js");export const result=value===42;`, "/value.ts": `export const value=42;`}, nil},
		{"variable", "/main.ts", map[string]string{"/main.ts": `const name:string="./value.js";const value=await import(name);export const result=value.n===42;`, "/value.ts": `export const n=42;`}, nil},
		{"shared-static-and-dynamic", "/main.ts", map[string]string{
			"/main.ts":  `import * as stat from "./value.js";const [a,b]=await Promise.all([import("./value.js"),import("./value.js")]);a.inc();export const result=a===b&&a===stat&&stat.n===1;`,
			"/value.ts": `export let n=0;export function inc(){n++;}`,
		}, nil},
		{"async-child", "/main.ts", map[string]string{"/main.ts": `const value=await import("./value.js");export const result=value.n===42;`, "/value.ts": `await 0;export const n=42;`}, nil},
		{"namespace-then", "/main.ts", map[string]string{"/main.ts": `const value:unknown=await import("./value.js");export const result=value===99;`, "/value.ts": `await 0;export function then(resolve:(v:number)=>void){resolve(99);}`}, nil},
		{"cjs-target", "/main.mts", map[string]string{"/main.mts": `const value=await import("./value.cjs");export const result=value.default.n===42&&value.n===42;`, "/value.cts": `const value={n:42};export=value;`}, goya.CompileOptions{"module": "nodenext", "moduleResolution": "nodenext"}},
		{"cjs-erases-type-reexports", "/main.mts", map[string]string{"/main.mts": `const value=await import("./value.cjs");export const result=!Object.hasOwn(value,"Type")&&!Object.hasOwn(value,"onlyType");`, "/value.cts": `export type {Type,onlyType} from "./types.cjs";`, "/types.cts": `export interface Type{n:number};export const onlyType=1;`}, goya.CompileOptions{"module": "nodenext", "moduleResolution": "nodenext"}},
		{"cjs-source", "/main.cts", map[string]string{"/main.cts": `export const result=(async()=>{const value=await import("./value.mjs");return value.n===42;})();`, "/value.mts": `await 0;export const n=42;`}, goya.CompileOptions{"module": "nodenext", "moduleResolution": "nodenext"}},
		{"commonjs-downlevel", "/main.ts", map[string]string{"/main.ts": `export const result=(async()=>{const value=await import("./value.js");return value.n===42;})();`, "/value.ts": `export const n=42;`}, goya.CompileOptions{"module": "commonjs"}},
		{"commonjs-rewrite-variable", "/main.ts", map[string]string{"/main.ts": `export const result=(async()=>{const path:string="./value.ts";const value=await import(path);return value.n===42;})();`, "/value.ts": `export const n=42;`}, goya.CompileOptions{"module": "commonjs", "rewriteRelativeImportExtensions": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var roots []string
			if tc.name == "variable" || tc.name == "commonjs-rewrite-variable" {
				roots = []string{"/value.ts"}
			}
			p, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{tc.entry}, ModuleRoots: roots, Files: tc.files}, nil, tc.options)
			if err != nil || p == nil {
				t.Fatalf("compile %v %+v", err, ds)
			}
			value, err := goya.New().RunProgram(p)
			if err != nil {
				t.Fatal(err)
			}
			if p, ok := value.Export().(*goya.Promise); ok {
				if p.State() != goya.PromiseStateFulfilled {
					t.Fatalf("initialization %v %v", p.State(), p.Result())
				}
				value = p.Result()
			}
			result := hostObject(t, value).Get("result")
			if p, ok := result.Export().(*goya.Promise); ok {
				if p.State() != goya.PromiseStateFulfilled {
					t.Fatalf("import %v %v", p.State(), p.Result())
				}
				result = p.Result()
			}
			if !result.ToBoolean() {
				t.Fatal("dynamic import result")
			}
		})
	}
}

func TestTypeScriptDynamicResolverAndTypeErrors(t *testing.T) {
	for _, valid := range []bool{true, false} {
		calls, effects := 0, 0
		reg := goya.NewRegistry()
		registerHost(t, reg, "record", func() { effects++ })
		env := freezeHost(t, reg)
		resolver := goya.ResolverFunc(func(context.Context) (map[string]string, error) {
			calls++
			source := `record();export const value:number=42;`
			if calls > 1 {
				source = `record();export const value:number=99;`
			}
			if !valid {
				source = `record();export const value:number="wrong";`
			}
			return map[string]string{"/loaded.ts": source}, nil
		})
		p, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/loaded.ts"}, Files: map[string]string{"/main.ts": `const name:string="./loaded.js";export const result=(async()=>{const [a,b]=await Promise.all([import(name),import(name)]);return a===b&&a.value===42;})();`}, Resolver: resolver}, env, nil)
		if !valid {
			if err != nil || p != nil || !goya.HasTypeErrors(ds) || effects != 0 || calls != 1 {
				t.Fatalf("preparation gate %v %v calls %d effects %d", err, ds, calls, effects)
			}
			continue
		}
		if err != nil || p == nil || calls != 1 {
			t.Fatalf("compile %v %+v %d", err, ds, calls)
		}
		r := goya.New()
		if err := env.Bind(r); err != nil {
			t.Fatal(err)
		}
		value, err := r.RunProgram(p)
		if err != nil {
			t.Fatal(err)
		}
		promise := modulePromise(t, hostObject(t, value).Get("result"))
		if promise.State() != goya.PromiseStateFulfilled || !promise.Result().ToBoolean() {
			t.Fatalf("load %v %v", promise.State(), promise.Result())
		}
		if effects != 1 || calls != 1 {
			t.Fatalf("resolver/check gate: calls %d effects %d", calls, effects)
		}
	}
}

func TestTypeScriptDynamicImportSchedulingAndFailureCache(t *testing.T) {
	calls := 0
	reg := goya.NewRegistry()
	registerHost(t, reg, "record", func() { calls++ })
	env := freezeHost(t, reg)
	p := compileModules(t, map[string]string{
		"/main.ts":   `const events:string[]=[];const p=import("./loaded.js");Promise.resolve().then(()=>events.push("queued"));const q=p.then(()=>events.push("loaded"));await q;export const result=events.join(",")==="queued,loaded";`,
		"/loaded.ts": `export const n=1;`,
	}, nil)
	v, err := goya.New().RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	promise := modulePromise(t, v)
	if promise.State() != goya.PromiseStateFulfilled || !hostObject(t, promise.Result()).Get("result").ToBoolean() {
		t.Fatalf("job order: %v %v", promise.State(), promise.Result())
	}
	p = compileModules(t, map[string]string{
		"/main.ts":   `export const result=(async()=>{for(let i=0;i<2;i++){try{await import("./loaded.js");}catch(e){if(e!=="failure")return false;}}return true;})();`,
		"/loaded.ts": `record();await 0;throw "failure";export const n=1;`,
	}, env)
	r := goya.New()
	if err := env.Bind(r); err != nil {
		t.Fatal(err)
	}
	v, err = r.RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	promise = modulePromise(t, hostObject(t, v).Get("result"))
	if promise.State() != goya.PromiseStateFulfilled || !promise.Result().ToBoolean() || calls != 1 {
		t.Fatalf("failure cache: state %v effects %d", promise.State(), calls)
	}
}

func TestTypeScriptDynamicImportRejectsChangedLoadedDependency(t *testing.T) {
	resolutionCalls, effects := 0, 0
	reg := goya.NewRegistry()
	registerHost(t, reg, "record", func() { effects++ })
	env := freezeHost(t, reg)
	resolver := goya.ResolverFunc(func(context.Context) (map[string]string, error) {
		resolutionCalls++
		if resolutionCalls == 1 {
			return map[string]string{"/value.ts": `export const n:number=1;`}, nil
		}
		return map[string]string{"/value.ts": `export const n:number=2;`, "/later.ts": `import {n} from "./value.js";record();export const result=n;`}, nil
	})
	p, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": `import {n} from "./value.js";const name:string="./later.js";export const result=(async()=>{try{await import(name);return false;}catch(e){return e instanceof TypeError&&n===1;}})();`}, Resolver: resolver}, env, nil)
	if err != nil || p == nil {
		t.Fatalf("compile %v %+v", err, ds)
	}
	r := goya.New()
	if err := env.Bind(r); err != nil {
		t.Fatal(err)
	}
	v, err := r.RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	promise := modulePromise(t, hostObject(t, v).Get("result"))
	if promise.State() != goya.PromiseStateFulfilled || !promise.Result().ToBoolean() || effects != 0 {
		t.Fatalf("changed dependency ran: %v effects %d", promise.State(), effects)
	}
}

func TestTypeScriptDynamicHostModule(t *testing.T) {
	reg := goya.NewRegistry()
	if err := reg.RegisterModule("host:values", map[string]any{"value": float64(42)}); err != nil {
		t.Fatal(err)
	}
	env := freezeHost(t, reg)
	p, ds, err := goya.Compile(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"host:values"}, Files: map[string]string{"/main.ts": `const name:string="host:values";const a=await import(name);const b=await import(name);export const result=a===b&&a.value===42;`}}, env, nil)
	if err != nil || p == nil {
		t.Fatalf("compile %v %v", err, ds)
	}
	r := goya.New()
	if err := env.Bind(r); err != nil {
		t.Fatal(err)
	}
	v, err := r.RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	promise := modulePromise(t, v)
	if promise.State() != goya.PromiseStateFulfilled || !hostObject(t, promise.Result()).Get("result").ToBoolean() {
		t.Fatalf("host module: %v %v", promise.State(), promise.Result())
	}
}

func TestTypeScriptCanonicalModuleCacheAcrossProjects(t *testing.T) {
	counter := `export let count=0;export function inc(){count++;}`
	a, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{"/a.ts"}, Files: map[string]string{"/a.ts": `import * as counter from "./counter.js";counter.inc();export {counter};`, "/counter.ts": counter}}, nil, nil)
	if err != nil || a == nil {
		t.Fatalf("a: %v %+v", err, ds)
	}
	b, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{"/b.ts"}, Files: map[string]string{"/b.ts": `import * as counter from "./counter.js";export {counter};export const result=counter.count===1;`, "/counter.ts": counter, "/unrelated.ts": `export const unused=1;`}}, nil, nil)
	if err != nil || b == nil {
		t.Fatalf("b: %v %+v", err, ds)
	}
	r := goya.New()
	first, err := r.RunProgram(a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.RunProgram(b)
	if err != nil {
		t.Fatal(err)
	}
	if !hostObject(t, second).Get("result").ToBoolean() || hostObject(t, first).Get("counter") != hostObject(t, second).Get("counter") {
		t.Fatal("canonical module instance split by project roots or catalog")
	}
}
