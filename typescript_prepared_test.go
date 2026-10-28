package goya_test

import (
	"context"
	"strings"
	"testing"

	"github.com/totoer/goya"
)

func TestPreparedProgramFrozenInputs(t *testing.T) {
	goya.SetCompilationCacheLimits(0, 0)
	defer goya.SetCompilationCacheLimits(2, 16<<20)
	calls := 0
	var effects []string
	registry := goya.NewRegistry()
	if err := registry.Register("record", func(name string) { effects = append(effects, name) }); err != nil {
		t.Fatal(err)
	}
	env, err := registry.Freeze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resolved := map[string]string{"/plugins/fast.ts": `record("fast");export const value=42;`, "/plugins/safe.ts": `record("safe");export const value=43;`}
	const main = `const name:string="@chosen";export const result=(async()=>{const m=await import(name);return m.value})();const unknown:string="./unprepared.js";export const rejected=(async()=>{try{await import(unknown);return false}catch(e){return e instanceof TypeError}})();`
	files := map[string]string{"/main.ts": main}
	roots := []string{"/plugins/fast.ts", "/plugins/safe.ts"}
	paths := map[string]any{"@chosen": []string{"/plugins/fast.ts"}}
	options := goya.CompileOptions{"paths": paths}
	project := goya.Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: roots, Files: files, Resolver: goya.ResolverFunc(func(context.Context) (map[string]string, error) { calls++; return resolved, nil })}
	program, ds, err := goya.Compile(context.Background(), project, env, options)
	if err != nil || program == nil {
		t.Fatalf("compile %v %v", err, ds)
	}
	files["/main.ts"] = `throw new Error("mutated source");`
	resolved["/plugins/fast.ts"] = `record("fast");export const value=99;`
	roots[0] = "/missing.ts"
	paths["@chosen"] = []string{"/plugins/safe.ts"}
	check := func(program *goya.Program, want int64) {
		t.Helper()
		r := goya.New()
		if err := env.Bind(r); err != nil {
			t.Fatal(err)
		}
		value, err := r.RunProgram(program)
		if err != nil {
			t.Fatal(err)
		}
		promise := modulePromise(t, hostObject(t, value).Get("result"))
		if promise.State() != goya.PromiseStateFulfilled || promise.Result().ToInteger() != want {
			t.Fatalf("prepared result %v %v, want %d", promise.State(), promise.Result(), want)
		}
		unknown := modulePromise(t, hostObject(t, value).Get("rejected"))
		if unknown.State() != goya.PromiseStateFulfilled || !unknown.Result().ToBoolean() {
			t.Fatalf("unknown target %v %v", unknown.State(), unknown.Result())
		}
	}
	check(program, 42)
	if calls != 1 {
		t.Fatalf("execution reread resolver %d times", calls)
	}
	files["/main.ts"] = main
	roots[0] = "/plugins/fast.ts"
	paths["@chosen"] = []string{"/plugins/fast.ts"}
	updated, ds, err := goya.Compile(context.Background(), project, env, options)
	if err != nil || updated == nil {
		t.Fatalf("new compile %v %v", err, ds)
	}
	check(updated, 99)
	check(program, 42)
	if calls != 2 {
		t.Fatalf("resolver calls %d", calls)
	}
	if len(effects) != 3 || effects[0] != "fast" || effects[1] != "fast" || effects[2] != "fast" {
		t.Fatalf("lazy module effects %v", effects)
	}
}

func TestPreparedWholeGraphContracts(t *testing.T) {
	effects := 0
	makeEnv := func(global, api bool, used, unused any, extra bool) *goya.Environment {
		reg := goya.NewRegistry()
		if err := reg.Register("start", func() { effects++ }); err != nil {
			t.Fatal(err)
		}
		if global {
			if err := reg.Register("hostValue", float64(42)); err != nil {
				t.Fatal(err)
			}
		}
		if api {
			if err := reg.RegisterModule("api", map[string]any{"used": used, "unused": unused}); err != nil {
				t.Fatal(err)
			}
		}
		if extra {
			if err := reg.RegisterModule("extra", map[string]any{"n": float64(1)}); err != nil {
				t.Fatal(err)
			}
		}
		env, err := reg.Freeze(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return env
	}
	env := makeEnv(true, true, float64(1), "unused", false)
	project := goya.Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/plugin.ts"}, Files: map[string]string{
		"/main.ts":   `start();export const result=1;`,
		"/plugin.ts": `import {used} from "api";export const n=used;export const later=()=>import("./child.js");`,
		"/child.ts":  `export function read(){return hostValue;}`,
	}}
	program, ds, err := goya.Compile(context.Background(), project, env, nil)
	if err != nil || program == nil {
		t.Fatalf("compile %v %v", err, ds)
	}
	globalFound, usedFound := false, false
	for _, binding := range program.Manifest().HostBindings {
		if binding.Module == "" && binding.Name == "hostValue" {
			globalFound = true
		}
		if binding.Module == "api" && binding.Name == "used" {
			usedFound = true
		}
		if binding.Module == "api" && binding.Name == "unused" {
			t.Fatal("static named import widened to unused export")
		}
	}
	if !globalFound || !usedFound {
		t.Fatalf("incomplete manifest %+v", program.Manifest().HostBindings)
	}
	for _, actual := range []*goya.Environment{makeEnv(false, true, float64(1), false, false), makeEnv(true, false, float64(1), false, false), makeEnv(true, true, "wrong", false, false)} {
		r := goya.New()
		if err := actual.Bind(r); err != nil {
			t.Fatal(err)
		}
		if _, err := r.RunProgram(program); err == nil {
			t.Fatal("incomplete lazy contract accepted")
		}
		if effects != 0 {
			t.Fatalf("startup effects %d", effects)
		}
	}
	for i := 0; i < 2; i++ {
		r := goya.New()
		if err := makeEnv(true, true, float64(1), false, true).Bind(r); err != nil {
			t.Fatal(err)
		}
		if _, err := r.RunProgram(program); err != nil {
			t.Fatal(err)
		}
	}
	if effects != 2 {
		t.Fatalf("separate runtime startup effects %d", effects)
	}
	amb, ds, err := goya.Compile(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"ambient-api"}, Files: map[string]string{"/main.ts": `export const n=1;`, "/api.d.ts": `declare module "ambient-api" {export const value:number}`}}, nil, nil)
	if err != nil || amb == nil {
		t.Fatalf("ambient compile %v %v", err, ds)
	}
	if _, err := goya.New().RunProgram(amb); err == nil {
		t.Fatal("ambient-only implementation accepted")
	}
}

func TestPreparedProgramKeepsSourceMetadata(t *testing.T) {
	goya.SetCompilationCacheLimits(0, 0)
	defer goya.SetCompilationCacheLimits(2, 16<<20)
	files := map[string]string{"/main.ts": `export const result=import("./plugin.js");`, "/plugin.ts": `export function fail(value:number){throw new Error("sentinel");} export const text=fail.toString();`}
	program, ds, err := goya.Compile(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, Files: files}, nil, nil)
	if err != nil || program == nil {
		t.Fatalf("compile %v %v", err, ds)
	}
	clear(files)
	value, err := goya.New().RunProgram(program)
	if err != nil {
		t.Fatal(err)
	}
	promise := modulePromise(t, hostObject(t, value).Get("result"))
	if promise.State() != goya.PromiseStateFulfilled {
		t.Fatalf("module %v", promise.Result())
	}
	ns := hostObject(t, promise.Result())
	if !strings.Contains(ns.Get("text").String(), "value:number") {
		t.Fatal("original function source lost")
	}
	f, ok := goya.AssertFunction(ns.Get("fail"))
	if !ok {
		t.Fatal("missing exported function")
	}
	if _, err := f(goya.Undefined(), goya.Undefined()); err == nil || !strings.Contains(err.Error(), "/plugin.ts:1:") {
		t.Fatalf("source-level stack %v", err)
	}
}

func TestPreparedRootsDoNotStart(t *testing.T) {
	for _, configured := range []bool{false, true} {
		for _, script := range []bool{false, true} {
			main := `export const result=!Object.hasOwn(globalThis,"pluginEffects");`
			if script {
				main = `!Object.hasOwn(globalThis,"pluginEffects");`
			}
			project := goya.Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/plugin.ts"}, Files: map[string]string{
				"/main.ts":   main,
				"/plugin.ts": `Object.defineProperty(globalThis,"pluginEffects",{value:true});export const value=42;`,
			}}
			if configured {
				project.ConfigFile = "/tsconfig.json"
				project.Files[project.ConfigFile] = `{"files":["main.ts"]}`
			}
			program, ds, err := goya.Compile(context.Background(), project, nil, nil)
			if err != nil || program == nil {
				t.Fatalf("compile configured=%v script=%v: %v %v", configured, script, err, ds)
			}
			value, err := goya.New().RunProgram(program)
			if err != nil {
				t.Fatal(err)
			}
			if !script {
				value = hostObject(t, value).Get("result")
			}
			if !value.ToBoolean() {
				t.Fatal("prepared module initialized before import")
			}
		}
	}
}

func TestPreparedStartupOverlap(t *testing.T) {
	program, ds, err := goya.Compile(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/main.ts", "main.ts"}, Files: map[string]string{
		"/main.ts": `(globalThis as any).starts=((globalThis as any).starts??0)+1;export const result=(globalThis as any).starts;`,
	}}, nil, nil)
	if err != nil || program == nil {
		t.Fatalf("compile %v %v", err, ds)
	}
	value, err := goya.New().RunProgram(program)
	if err != nil {
		t.Fatal(err)
	}
	if got := hostObject(t, value).Get("result").ToInteger(); got != 1 {
		t.Fatalf("starts %d", got)
	}
}

func TestPreparedPluginImport(t *testing.T) {
	for _, commonJS := range []bool{false, true} {
		var events []string
		registry := goya.NewRegistry()
		if err := registry.Register("record", func(name string) { events = append(events, name) }); err != nil {
			t.Fatal(err)
		}
		env, err := registry.Freeze(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		project := goya.Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/plugins/fast.ts", "/plugins/safe.ts"}, Files: map[string]string{
			"/main.ts":         `const mode:string="fast";export const result=(async()=>{const m=await import("./plugins/"+mode+".js");return m.value===42;})();`,
			"/plugins/fast.ts": `record("fast");export const value=42;`,
			"/plugins/safe.ts": `record("safe");export const value=43;`,
		}}
		var options goya.CompileOptions
		if commonJS {
			options = goya.CompileOptions{"module": "commonjs"}
		}
		program, ds, err := goya.Compile(context.Background(), project, env, options)
		if err != nil || program == nil {
			t.Fatalf("compile %v %v", err, ds)
		}
		if len(events) != 0 {
			t.Fatal("compile executed plugin")
		}
		r := goya.New()
		if err := env.Bind(r); err != nil {
			t.Fatal(err)
		}
		value, err := r.RunProgram(program)
		if err != nil {
			t.Fatal(err)
		}
		promise := modulePromise(t, hostObject(t, value).Get("result"))
		if promise.State() != goya.PromiseStateFulfilled || !promise.Result().ToBoolean() {
			t.Fatalf("import commonjs=%v: %v %v", commonJS, promise.State(), promise.Result())
		}
		if len(events) != 1 || events[0] != "fast" {
			t.Fatalf("plugin effects %v", events)
		}
	}
}

func TestPreparedUnknownTargets(t *testing.T) {
	registry := goya.NewRegistry()
	if err := registry.RegisterModule("host:extra", map[string]any{"value": float64(1)}); err != nil {
		t.Fatal(err)
	}
	env, err := registry.Freeze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	program, ds, err := goya.Compile(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{
		"/main.ts":   `import type {T} from "./type.js";const data:T={n:1};export const result=(async()=>{for(const name of ["./hidden.js","./type.js","./missing.js","unknown","host:extra","data:text/javascript,export default 42"]){try{await import(name);return false}catch(e){if(!(e instanceof TypeError))return false}}return data.n===1})();`,
		"/hidden.ts": `export const value=1;`,
		"/type.ts":   `export interface T{n:number};export const value=2;`,
	}}, env, nil)
	if err != nil || program == nil {
		t.Fatalf("compile %v %v", err, ds)
	}
	r := goya.New()
	if err := env.Bind(r); err != nil {
		t.Fatal(err)
	}
	value, err := r.RunProgram(program)
	if err != nil {
		t.Fatal(err)
	}
	promise := modulePromise(t, hostObject(t, value).Get("result"))
	if promise.State() != goya.PromiseStateFulfilled || !promise.Result().ToBoolean() {
		t.Fatalf("catalog authorization %v %v", promise.State(), promise.Result())
	}
	r = goya.New()
	if err := env.Bind(r); err != nil {
		t.Fatal(err)
	}
	// A module loaded by an earlier Program does not grant access to this one.
	preload := compileModules(t, map[string]string{"/main.ts": `export {value} from "./hidden.js";`, "/hidden.ts": `export const value=1;`}, env)
	if _, err := r.RunProgram(preload); err != nil {
		t.Fatal(err)
	}
	// Use a distinct entry name so the module cache's code-compatibility guard
	// does not reject the test before the import authorization is exercised.
	program, ds, err = goya.Compile(context.Background(), goya.Project{EntryPoints: []string{"/app.ts"}, Files: map[string]string{
		"/app.ts":    `export const result=(async()=>{for(const name of ["./hidden.js","host:extra","data:text/javascript,export default 42"]){try{await import(name);return false}catch(e){if(!(e instanceof TypeError))return false}}return true})();`,
		"/hidden.ts": `export const value=1;`,
	}}, env, nil)
	if err != nil || program == nil {
		t.Fatalf("compile %v %v", err, ds)
	}
	value, err = r.RunProgram(program)
	if err != nil {
		t.Fatal(err)
	}
	promise = modulePromise(t, hostObject(t, value).Get("result"))
	if promise.State() != goya.PromiseStateFulfilled || !promise.Result().ToBoolean() {
		t.Fatalf("authorization %v %v", promise.State(), promise.Result())
	}
}

func TestPreparedDynamicHostContract(t *testing.T) {
	effects := 0
	makeEnvironment := func(value any) *goya.Environment {
		reg := goya.NewRegistry()
		if err := reg.RegisterModule("api", map[string]any{"value": value}); err != nil {
			t.Fatal(err)
		}
		if err := reg.Register("record", func() { effects++ }); err != nil {
			t.Fatal(err)
		}
		env, err := reg.Freeze(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return env
	}
	for _, computed := range []bool{false, true} {
		project := goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": `record();const m=await import("api");export const result=m.value+1;`}}
		if computed {
			project.ModuleRoots = []string{"api"}
			project.Files["/main.ts"] = `record();const name:string="api";const m=await import(name);export const result=m.value+1;`
		}
		program, ds, err := goya.Compile(context.Background(), project, makeEnvironment(float64(42)), nil)
		if err != nil || program == nil {
			t.Fatalf("compile %v %v", err, ds)
		}
		r := goya.New()
		if err := makeEnvironment("bad").Bind(r); err != nil {
			t.Fatal(err)
		}
		if _, err := r.RunProgram(program); err == nil {
			t.Fatal("incompatible dynamic namespace accepted")
		}
		if effects != 0 {
			t.Fatalf("startup effects %d", effects)
		}
	}
}

func TestPreparedCacheCannotDelegateImportPermissions(t *testing.T) {
	effects := 0
	reg := goya.NewRegistry()
	if err := reg.Register("record", func() { effects++ }); err != nil {
		t.Fatal(err)
	}
	env, err := reg.Freeze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	const loader = `export const load=(name:string)=>import(name);`
	a, ds, err := goya.Compile(context.Background(), goya.Project{EntryPoints: []string{"/a.ts"}, ModuleRoots: []string{"/secret.ts"}, Files: map[string]string{
		"/a.ts": `import * as loader from "./loader.js";export {loader};`, "/loader.ts": loader, "/secret.ts": `export const value=42;`,
	}}, env, nil)
	if err != nil || a == nil {
		t.Fatalf("compile A %v %v", err, ds)
	}
	for _, script := range []bool{false, true} {
		main := `import * as loader from "./loader.js";record();export const result=loader.load("./secret.js");`
		roots := []string(nil)
		if script {
			main = `record();`
			roots = []string{"/loader.ts"}
		}
		b, ds, err := goya.Compile(context.Background(), goya.Project{EntryPoints: []string{"/b.ts"}, ModuleRoots: roots, Files: map[string]string{"/b.ts": main, "/loader.ts": loader}}, env, nil)
		if err != nil || b == nil {
			t.Fatalf("compile B %v %v", err, ds)
		}
		r := goya.New()
		if err := env.Bind(r); err != nil {
			t.Fatal(err)
		}
		if _, err := r.RunProgram(a); err != nil {
			t.Fatal(err)
		}
		if _, err := r.RunProgram(b); err == nil {
			t.Fatal("cached loader carried another program's import permissions")
		}
		if effects != 0 {
			t.Fatalf("incompatible cached authority detected after effects: %d", effects)
		}
	}
}

func BenchmarkPreparedPluginPackage(b *testing.B) {
	goya.SetCompilationCacheLimits(0, 0)
	defer goya.SetCompilationCacheLimits(2, 16<<20)
	calls := 0
	project := goya.Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/plugins/fast.ts", "/plugins/safe.ts"}, Files: map[string]string{
		"/main.ts": `export async function load(){const name:string="./plugins/fast.js";const m=await import(name);return m.value;}`,
	}, Resolver: goya.ResolverFunc(func(context.Context) (map[string]string, error) {
		calls++
		return map[string]string{"/plugins/fast.ts": `export const value=42;`, "/plugins/safe.ts": `export const value=43;`}, nil
	})}
	b.Run("Compile", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			p, ds, err := goya.Compile(context.Background(), project, nil, nil)
			if err != nil || p == nil {
				b.Fatalf("compile %v %v", err, ds)
			}
		}
	})
	b.Run("RepeatedImport", func(b *testing.B) {
		p, ds, err := goya.Compile(context.Background(), project, nil, nil)
		if err != nil || p == nil {
			b.Fatalf("compile %v %v", err, ds)
		}
		value, err := goya.New().RunProgram(p)
		if err != nil {
			b.Fatal(err)
		}
		load, ok := goya.AssertFunction(value.(*goya.Object).Get("load"))
		if !ok {
			b.Fatal("missing loader")
		}
		if _, err := load(goya.Undefined()); err != nil {
			b.Fatal(err)
		}
		captured := calls
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			v, err := load(goya.Undefined())
			if err != nil {
				b.Fatal(err)
			}
			promise := v.Export().(*goya.Promise)
			if promise.State() != goya.PromiseStateFulfilled || promise.Result().ToInteger() != 42 {
				b.Fatal("import failed")
			}
		}
		b.StopTimer()
		if calls != captured {
			b.Fatal("repeated import entered resolver")
		}
	})
}
