package goya

import (
	stdcontext "context"
	"fmt"
	"strings"
	"testing"
)

func compileResolutionProject(t *testing.T, project Project, options CompileOptions) *Program {
	t.Helper()
	p, ds, err := Compile(stdcontext.Background(), project, nil, options)
	if err != nil || p == nil {
		t.Fatalf("compile: %v %+v", err, ds)
	}
	return p
}

func resolutionLoader(t *testing.T, r *Runtime, p *Program) Callable {
	t.Helper()
	v, err := r.RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := AssertFunction(v.(*Object).Get("load"))
	if !ok {
		t.Fatal("missing loader")
	}
	return f
}

func resolutionPromise(t *testing.T, load Callable, args ...Value) *Promise {
	t.Helper()
	v, err := load(Undefined(), args...)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := v.Export().(*Promise)
	if !ok {
		t.Fatalf("not a promise: %v", v)
	}
	return p
}

func resolvedNamespace(t *testing.T, r *Runtime, load Callable, name string) *Object {
	t.Helper()
	p := resolutionPromise(t, load, r.ToValue(name))
	if p.State() != PromiseStateFulfilled {
		t.Fatalf("import %q: state=%v result=%v", name, p.State(), p.Result())
	}
	return p.Result().(*Object)
}

func TestModuleResolutionDynamicPaths(t *testing.T) {
	for _, commonJS := range []bool{false, true} {
		t.Run(fmt.Sprintf("commonjs=%v", commonJS), func(t *testing.T) {
			options := CompileOptions{}
			if commonJS {
				options["module"] = "commonjs"
			}
			p := compileResolutionProject(t, Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/plugin.ts"}, Files: map[string]string{
				"/main.ts":   `export const load=(name:string)=>import(name);`,
				"/plugin.ts": `(globalThis as any).starts=((globalThis as any).starts??0)+1;export const state={value:0};`,
			}}, options)
			r := New()
			load := resolutionLoader(t, r, p)
			if r.dynamicResolutions != nil {
				t.Fatal("cache allocated before import")
			}
			first := resolvedNamespace(t, r, load, "./plugin")
			instance := r.moduleInstances["/plugin.ts"]
			for i := 1; i <= 400; i++ {
				ns := resolvedNamespace(t, r, load, strings.Repeat("./", i)+"plugin")
				if (!commonJS && ns != first) || ns.Get("state") != first.Get("state") || r.moduleInstances["/plugin.ts"] != instance {
					t.Fatal("module identity changed")
				}
				checkResolutionCache(t, r.dynamicResolutions)
			}
			if len(r.dynamicResolutions.entries) != 1 || r.Get("starts").ToInteger() != 1 {
				t.Fatalf("entries=%d starts=%v", len(r.dynamicResolutions.entries), r.Get("starts"))
			}
		})
	}
}

func TestModuleResolutionDynamicAliases(t *testing.T) {
	for _, commonJS := range []bool{false, true} {
		for _, long := range []bool{false, true} {
			t.Run(fmt.Sprintf("commonjs=%v/long=%v", commonJS, long), func(t *testing.T) {
				calls := 0
				options := CompileOptions{"paths": map[string]any{"@flat/*": []string{"/plugin.ts"}}}
				if commonJS {
					options["module"] = "commonjs"
				}
				p := compileResolutionProject(t, Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/plugin.ts"}, Files: map[string]string{
					"/main.ts": `export const load=(name:string)=>import(name);`,
				}, Resolver: ResolverFunc(func(stdcontext.Context) (map[string]string, error) {
					calls++
					return map[string]string{"/plugin.ts": `(globalThis as any).starts=((globalThis as any).starts??0)+1;export const state={value:0};`}, nil
				})}, options)
				r := New()
				load := resolutionLoader(t, r, p)
				first := resolvedNamespace(t, r, load, "@flat/first")
				for i := 0; i < moduleResolutionCacheEntries+20; i++ {
					name := fmt.Sprintf("@flat/%d", i)
					if long {
						name += strings.Repeat("x", 2048)
					}
					ns := resolvedNamespace(t, r, load, name)
					if ns.Get("state") != first.Get("state") {
						t.Fatal("alias split module")
					}
					checkResolutionCache(t, r.dynamicResolutions)
				}
				c := r.dynamicResolutions
				phase := moduleResolutionImport
				if commonJS {
					phase = moduleResolutionRequire
				}
				key := preparedResolutionKey(p.typescript, "/main.ts", "@flat/first", phase, nil)
				if c.entries[key] != nil || (long && len(c.entries) >= c.limit) || (!long && len(c.entries) != c.limit) {
					t.Fatal("expected budget was not exercised")
				}
				before, count := c.bytes, len(c.entries)
				for i := 0; i < 2; i++ {
					resolvedNamespace(t, r, load, "@flat/"+strings.Repeat("z", moduleResolutionCacheBytes+1))
					checkResolutionCache(t, c)
				}
				if c.bytes != before || len(c.entries) != count {
					t.Fatal("oversized alias retained or evicted entries")
				}
				again := resolvedNamespace(t, r, load, "@flat/first")
				if (!commonJS && again != first) || again.Get("state") != first.Get("state") || calls != 1 || r.Get("starts").ToInteger() != 1 {
					t.Fatalf("reimport: resolver=%d starts=%v", calls, r.Get("starts"))
				}
			})
		}
	}
}

func assertResolutionEvicted(t *testing.T, r *Runtime, p *Program, name string, phase moduleResolutionPhase, attrs map[string]string) {
	t.Helper()
	checkResolutionCache(t, r.dynamicResolutions)
	if r.dynamicResolutions.entries[preparedResolutionKey(p.typescript, "/main.ts", name, phase, attrs)] != nil {
		t.Fatalf("resolution %q was not evicted", name)
	}
}

func TestModuleResolutionDynamicEvictionState(t *testing.T) {
	for _, commonJS := range []bool{false, true} {
		t.Run(fmt.Sprintf("commonjs=%v", commonJS), func(t *testing.T) {
			options := CompileOptions{"paths": map[string]any{"@flat/*": []string{"/plugin.ts"}}}
			phase := moduleResolutionImport
			if commonJS {
				options["module"] = "commonjs"
				phase = moduleResolutionRequire
			}
			p := compileResolutionProject(t, Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/plugin.ts"}, Files: map[string]string{
				"/main.ts":   `export const events:string[]=[];export const load=(name:string)=>{const p=import(name);Promise.resolve().then(()=>events.push("queued"));return p.then(m=>{events.push("loaded");return m;});};`,
				"/plugin.ts": `(globalThis as any).starts=((globalThis as any).starts??0)+1;export let count=0;export function inc(){count++;}export const state={value:0};`,
			}}, options)
			r := New()
			r.dynamicResolutions = &moduleResolutionCache{limit: 1, maxBytes: moduleResolutionCacheBytes}
			load := resolutionLoader(t, r, p)
			a := resolvedNamespace(t, r, load, "@flat/first")
			inc, _ := AssertFunction(a.Get("inc"))
			if _, err := inc(Undefined()); err != nil {
				t.Fatal(err)
			}
			if err := a.Get("state").(*Object).Set("value", 42); err != nil {
				t.Fatal(err)
			}
			resolvedNamespace(t, r, load, "@flat/other")
			assertResolutionEvicted(t, r, p, "@flat/first", phase, nil)
			b := resolvedNamespace(t, r, load, "@flat/first")
			if (!commonJS && a != b) || b.Get("state") != a.Get("state") || b.Get("state").(*Object).Get("value").ToInteger() != 42 || b.Get("count").ToInteger() != 1 || r.Get("starts").ToInteger() != 1 {
				t.Fatal("eviction reset module state")
			}
			// ESM must preserve promise-job ordering on both the first load and miss.
			if !commonJS {
				events := r.moduleInstances["/main.ts"].namespace.Get("events").(*Object)
				if events.String() != "queued,loaded,queued,loaded,queued,loaded" {
					t.Fatalf("job order %s", events)
				}
			}
		})
	}
}

func TestModuleResolutionDynamicEvictionPendingAndFailure(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%v", failure), func(t *testing.T) {
			plugin := `(globalThis as any).starts=((globalThis as any).starts??0)+1;`
			if failure {
				plugin += `throw {sentinel:true};export const value=42;`
			} else {
				plugin += `await new Promise<void>(resolve=>{(globalThis as any).release=resolve;});export const value=42;`
			}
			p := compileResolutionProject(t, Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/plugin.ts"}, Files: map[string]string{
				"/main.ts": `export const load=(name:string)=>import(name);`, "/plugin.ts": plugin,
			}}, CompileOptions{"paths": map[string]any{"@flat/*": []string{"/plugin.ts"}}})
			r := New()
			r.dynamicResolutions = &moduleResolutionCache{limit: 1, maxBytes: moduleResolutionCacheBytes}
			load := resolutionLoader(t, r, p)
			a := resolutionPromise(t, load, r.ToValue("@flat/first"))
			instance := r.moduleInstances["/plugin.ts"]
			evaluation := instance.evaluation
			b := resolutionPromise(t, load, r.ToValue("@flat/other"))
			assertResolutionEvicted(t, r, p, "@flat/first", moduleResolutionImport, nil)
			c := resolutionPromise(t, load, r.ToValue("@flat/first"))
			want := PromiseStateRejected
			if !failure {
				for _, promise := range []*Promise{a, b, c} {
					if promise.State() != PromiseStatePending {
						t.Fatalf("expected pending initialization: %v", promise.State())
					}
				}
				if instance.evaluation != evaluation {
					t.Fatal("pending evaluation replaced")
				}
				release, _ := AssertFunction(r.Get("release"))
				if _, err := release(Undefined()); err != nil {
					t.Fatal(err)
				}
				want = PromiseStateFulfilled
			}
			for _, promise := range []*Promise{a, b, c} {
				if promise.State() != want || promise.Result() != a.Result() {
					t.Fatalf("shared initialization/failure lost: %v %v", promise.State(), promise.Result())
				}
			}
			if r.moduleInstances["/plugin.ts"] != instance || r.Get("starts").ToInteger() != 1 {
				t.Fatal("module reinitialized")
			}
		})
	}
}

func TestModuleResolutionDynamicEvictionJSON(t *testing.T) {
	p := compileResolutionProject(t, Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/value.json"}, Files: map[string]string{
		"/main.ts":    `export const load=(name:string,options?:ImportCallOptions)=>import(name,options);`,
		"/value.json": `{"answer":42}`,
	}}, CompileOptions{"resolveJsonModule": true, "paths": map[string]any{"@flat/*": []string{"/value.json"}}})
	r := New()
	r.dynamicResolutions = &moduleResolutionCache{limit: 1, maxBytes: moduleResolutionCacheBytes}
	load := resolutionLoader(t, r, p)
	options := r.ToValue(map[string]any{"with": map[string]any{"type": "json"}})
	a := resolutionPromise(t, load, r.ToValue("@flat/first"), options)
	resolutionPromise(t, load, r.ToValue("@flat/other"), options)
	attrs := map[string]string{"type": "json"}
	assertResolutionEvicted(t, r, p, "@flat/first", moduleResolutionImport, attrs)
	b := resolutionPromise(t, load, r.ToValue("@flat/first"), options)
	if a.State() != PromiseStateFulfilled || b.State() != PromiseStateFulfilled || a.Result() != b.Result() {
		t.Fatal("JSON namespace lost after eviction")
	}
	for _, name := range []string{"@flat/first", "@flat/other"} {
		bad := resolutionPromise(t, load, r.ToValue(name))
		if bad.State() != PromiseStateRejected || bad.Result().(*Object).Get("name").String() != "TypeError" {
			t.Fatal("JSON attributes bypassed")
		}
	}
	checkResolutionCache(t, r.dynamicResolutions)
}

func TestModuleResolutionDynamicEvictionDeferred(t *testing.T) {
	p := compileResolutionProject(t, Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/plugin.ts"}, Files: map[string]string{
		"/main.ts":   `export const load=(name:string)=>import.defer(name);`,
		"/plugin.ts": `(globalThis as any).starts=((globalThis as any).starts??0)+1;export const value=42;`,
	}}, CompileOptions{"paths": map[string]any{"@flat/*": []string{"/plugin.ts"}}})
	r := New()
	r.dynamicResolutions = &moduleResolutionCache{limit: 1, maxBytes: moduleResolutionCacheBytes}
	load := resolutionLoader(t, r, p)
	a := resolvedNamespace(t, r, load, "@flat/first")
	resolvedNamespace(t, r, load, "@flat/other")
	assertResolutionEvicted(t, r, p, "@flat/first", moduleResolutionDefer, nil)
	b := resolvedNamespace(t, r, load, "@flat/first")
	if a != b || r.Get("starts") != nil {
		t.Fatal("deferred module evaluated or namespace replaced")
	}
	if a.Get("value").ToInteger() != 42 || b.Get("value").ToInteger() != 42 || r.Get("starts").ToInteger() != 1 {
		t.Fatal("deferred evaluation changed")
	}
}

func TestModuleResolutionDynamicUnknown(t *testing.T) {
	r := New()
	load, _ := AssertFunction(r.newNativeFunc(func(c FunctionCall) Value {
		return r.dynamicImport(nil, "/main.ts", c, false)
	}, "import", 1))
	promise := resolutionPromise(t, load, r.ToValue("./plugin"))
	if promise.State() != PromiseStateRejected || !strings.Contains(promise.Result().String(), "requires a prepared program") || r.dynamicResolutions != nil {
		t.Fatal("unprepared import did not reject without caching")
	}
	p := compileResolutionProject(t, Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{
		"/main.ts": `export const load=(name:string)=>import(name);`,
	}}, nil)
	load = resolutionLoader(t, r, p)
	for i := 0; i < 20; i++ {
		promise = resolutionPromise(t, load, r.ToValue(fmt.Sprintf("./missing%d", i)))
		if promise.State() != PromiseStateRejected || promise.Result().(*Object).Get("name").String() != "TypeError" || r.dynamicResolutions != nil {
			t.Fatal("unknown import did not reject without caching")
		}
	}
}

func TestModuleResolutionCommonJSProtocols(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		p := compileResolutionProject(t, Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/value.json"}, Files: map[string]string{
			"/main.ts": `export const load=(name:string)=>import(name);`, "/value.json": `{"answer":42}`,
		}}, CompileOptions{"module": "commonjs", "resolveJsonModule": true, "paths": map[string]any{"@flat/*": []string{"/value.json"}}})
		r := New()
		r.dynamicResolutions = &moduleResolutionCache{limit: 1, maxBytes: moduleResolutionCacheBytes}
		load := resolutionLoader(t, r, p)
		a := resolvedNamespace(t, r, load, "@flat/first")
		resolvedNamespace(t, r, load, "@flat/other")
		assertResolutionEvicted(t, r, p, "@flat/first", moduleResolutionRequire, nil)
		b := resolvedNamespace(t, r, load, "@flat/first")
		if a.Get("default") != b.Get("default") || a.Get("answer").ToInteger() != 42 {
			t.Fatal("CommonJS JSON exports changed")
		}
	})
	t.Run("async-graph", func(t *testing.T) {
		// Exercise the synchronous fallback with a prepared async target. Compiling
		// that target as CommonJS would itself reject top-level await.
		p := compileResolutionProject(t, Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/async.ts"}, Files: map[string]string{
			"/main.ts":  `export const load=(name:string)=>import(name);`,
			"/async.ts": `(globalThis as any).ran=true;await 0;export const value=42;`,
		}}, nil)
		r := New()
		resolutionLoader(t, r, p)
		from := r.moduleInstances["/main.ts"]
		load, _ := AssertFunction(r.newNativeFunc(func(c FunctionCall) Value {
			return r.requireModule(from, c.Argument(0).String())
		}, "require", 1))
		for i := 0; i < 2; i++ {
			_, err := load(Undefined(), r.ToValue("./async.js"))
			if err == nil || !strings.Contains(err.Error(), "cannot synchronously require an async module graph") || r.Get("ran") != nil {
				t.Fatalf("async require: %v", err)
			}
		}
	})
}

func TestModuleResolutionSharedRuntimeBudget(t *testing.T) {
	var programs []*Program
	for _, commonJS := range []bool{false, true} {
		prefix := "/esm"
		options := CompileOptions{}
		if commonJS {
			prefix, options["module"] = "/cjs", "commonjs"
		}
		options["paths"] = map[string]any{"@flat/*": []string{prefix + "/plugin.ts"}}
		programs = append(programs, compileResolutionProject(t, Project{EntryPoints: []string{prefix + "/main.ts"}, ModuleRoots: []string{prefix + "/plugin.ts"}, Files: map[string]string{
			prefix + "/main.ts":   `export const load=(name:string)=>import(name);`,
			prefix + "/plugin.ts": `export const state={};`,
		}}, options))
	}
	for _, maxBytes := range []int{moduleResolutionCacheBytes, 200} {
		r := New()
		r.dynamicResolutions = &moduleResolutionCache{limit: 3, maxBytes: maxBytes}
		loads := []Callable{resolutionLoader(t, r, programs[0]), resolutionLoader(t, r, programs[1])}
		first := []*Object{resolvedNamespace(t, r, loads[0], "@flat/first"), resolvedNamespace(t, r, loads[1], "@flat/first")}
		for i := 0; i < 12; i++ {
			for j, load := range loads {
				ns := resolvedNamespace(t, r, load, fmt.Sprintf("@flat/%d", i))
				if ns.Get("state") != first[j].Get("state") {
					t.Fatal("mixed loaders changed identity")
				}
				checkResolutionCache(t, r.dynamicResolutions)
			}
		}
		for j, p := range programs {
			phase := moduleResolutionImport
			if j == 1 {
				phase = moduleResolutionRequire
			}
			if r.dynamicResolutions.entries[preparedResolutionKey(p.typescript, p.typescript.roots[0], "@flat/first", phase, nil)] != nil {
				t.Fatal("mixed programs did not evict old entry")
			}
			resolvedNamespace(t, r, loads[j], "@flat/first")
		}
		other := New()
		load := resolutionLoader(t, other, programs[0])
		ns := resolvedNamespace(t, other, load, "@flat/first")
		if other.dynamicResolutions == r.dynamicResolutions || ns.Get("state") == first[0].Get("state") || len(other.dynamicResolutions.entries) != 1 {
			t.Fatal("runtime state shared")
		}
	}
}

func TestModuleResolutionContextIsolation(t *testing.T) {
	t.Run("importers", func(t *testing.T) {
		p := compileResolutionProject(t, Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/left/plugin.ts", "/right/plugin.ts"}, Files: map[string]string{
			"/main.ts":         `import {load as left} from "./left/loader.js";import {load as right} from "./right/loader.js";export const load=(side:string)=>(side==="left"?left:right)("./plugin");`,
			"/left/loader.ts":  `export const load=(name:string)=>import(name);`,
			"/right/loader.ts": `export const load=(name:string)=>import(name);`,
			"/left/plugin.ts":  `export const value=1;`, "/right/plugin.ts": `export const value=2;`,
		}}, nil)
		r := New()
		load := resolutionLoader(t, r, p)
		for i := 0; i < 2; i++ {
			for j, name := range []string{"left", "right"} {
				if ns := resolvedNamespace(t, r, load, name); ns.Get("value").ToInteger() != int64(j+1) {
					t.Fatal("importer context lost")
				}
			}
		}
		if len(r.dynamicResolutions.entries) != 2 {
			t.Fatal("importer cache keys merged")
		}
	})
	t.Run("programs", func(t *testing.T) {
		r := New()
		var loads []Callable
		for _, target := range []string{"/a.ts", "/b.ts"} {
			p := compileResolutionProject(t, Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{target}, Files: map[string]string{
				"/main.ts": `((name:string)=>import(name));`,
				"/a.ts":    `export const value=1;`, "/b.ts": `export const value=2;`,
			}}, CompileOptions{"paths": map[string]any{"@chosen": []string{target}}})
			v, err := r.RunProgram(p)
			if err != nil {
				t.Fatal(err)
			}
			load, ok := AssertFunction(v)
			if !ok {
				t.Fatal("script loader missing")
			}
			loads = append(loads, load)
		}
		for i := 0; i < 2; i++ {
			for j, load := range loads {
				if ns := resolvedNamespace(t, r, load, "@chosen"); ns.Get("value").ToInteger() != int64(j+1) {
					t.Fatal("program permission context lost")
				}
			}
		}
		before := len(r.dynamicResolutions.entries)
		bad := resolutionPromise(t, loads[1], r.ToValue("./a.js"))
		if bad.State() != PromiseStateRejected || bad.Result().(*Object).Get("name").String() != "TypeError" || len(r.dynamicResolutions.entries) != before {
			t.Fatal("loaded target granted access to another program")
		}
	})
	t.Run("blocked-package", func(t *testing.T) {
		p := compileResolutionProject(t, Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/node_modules/pkg/plugin.ts"}, Files: map[string]string{
			"/main.ts":                       `export const load=(name:string)=>import(name);`,
			"/node_modules/pkg/package.json": `{"name":"pkg","exports":{"./*":"./plugin.ts","./private/*":null}}`,
			"/node_modules/pkg/plugin.ts":    `export const value=42;`,
		}}, nil)
		r := New()
		load := resolutionLoader(t, r, p)
		resolvedNamespace(t, r, load, "pkg/plugin")
		for _, name := range []string{"pkg/private/secret", "pkg/x/../plugin", "pkg/./plugin"} {
			bad := resolutionPromise(t, load, r.ToValue(name))
			if bad.State() != PromiseStateRejected || bad.Result().(*Object).Get("name").String() != "TypeError" || len(r.dynamicResolutions.entries) != 1 {
				t.Fatalf("blocked package alias %q acquired cached permission", name)
			}
		}
	})
}
