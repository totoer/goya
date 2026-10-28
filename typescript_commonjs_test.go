package goya_test

import (
	"context"
	"testing"

	"github.com/totoer/goya"
)

func TestTypeScriptModuleAssignments(t *testing.T) {
	for _, tc := range []struct {
		name, entry string
		files       map[string]string
		options     goya.CompileOptions
	}{
		{"export-object", "/main.cts", map[string]string{
			"/main.cts":  `import value = require("./value.cjs");export const result=value.n===42;`,
			"/value.cts": `const value={n:42};export = value;`,
		}, nil},
		{"export-callable", "/main.cts", map[string]string{
			"/main.cts":  `import value = require("./value.cjs");export const result=value(40)===42&&value.offset===2;`,
			"/value.cts": `function value(n:number){return n+value.offset;}namespace value{export const offset=2;}export = value;`,
		}, nil},
		{"named-live-update", "/main.cts", map[string]string{
			"/main.cts":  `import value = require("./value.cjs");value.inc();export const result=value.n===1;`,
			"/value.cts": `export let n=0;export function inc(){n++;}`,
		}, nil},
		{"cycle-and-position", "/main.cts", map[string]string{
			"/main.cts": `export const events:string[]=[];events.push("a:start");import b = require("./b.cjs");events.push("a:end");export const result=b.n===1&&events.join(",")==="a:start,b,a:end";`,
			"/b.cts":    `import a = require("./main.cjs");a.events.push("b");export const n=1;`,
		}, nil},
		{"esm-default-and-named", "/main.mts", map[string]string{
			"/main.mts":  `import value,{n} from "./value.cjs";export const result=value.n===42&&n===42;`,
			"/value.cts": `const value={n:42};export = value;`,
		}, nil},
		{"esm-cjs-snapshot", "/main.mts", map[string]string{
			"/main.mts":  `import value,{n,inc} from "./value.cjs";const before=n;inc();export const result=n===before&&value.n===1;`,
			"/value.cts": `export let n=0;export function inc(){n++;}`,
		}, nil},
		{"cjs-default-and-namespace", "/main.cts", map[string]string{
			"/main.cts":  `import value from "./value.cjs";import * as whole from "./value.cjs";export const result=value.n===42&&whole.n===42&&(whole as unknown as {default:unknown}).default===value;`,
			"/value.cts": `const value={n:42};export = value;`,
		}, nil},
		{"cjs-reexport", "/main.cts", map[string]string{
			"/main.cts":   `import * as values from "./bridge.cjs";values.inc();export const result=values.n===1;`,
			"/bridge.cts": `export * from "./value.cjs";`,
			"/value.cts":  `export let n=0;export function inc(){n++;}`,
		}, nil},
		{"esm-import-assignment", "/main.mts", map[string]string{
			"/main.mts":  `import value = require("./value.cjs");export const result=value.n===42;`,
			"/value.cts": `const value={n:42};export = value;`,
		}, nil},
		{"require-esm", "/main.cts", map[string]string{
			"/main.cts":  `import value,{count,inc} from "./value.mjs";inc();export const result=value===4&&count===1;`,
			"/value.mts": `export default 4;export let count=0;export function inc(){count++;}`,
		}, nil},
		{"preserve", "/main.ts", map[string]string{
			"/main.ts":  `import value = require("./value.js");export const result=value.n===42;`,
			"/value.ts": `const value={n:42};export = value;`,
		}, goya.CompileOptions{"module": "preserve"}},
		{"internal-alias", "/main.cts", map[string]string{"/main.cts": `namespace Values{export const n=42;}import alias=Values.n;export const result=alias===42;`}, nil},
		{"rewrite-extensions", "/main.cts", map[string]string{
			"/main.cts":  `import value = require("./value.cts");export const result=value.n===42;`,
			"/value.cts": `export const n=42;`,
		}, goya.CompileOptions{"module": "nodenext", "moduleResolution": "nodenext", "rewriteRelativeImportExtensions": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.options
			if opts == nil {
				opts = goya.CompileOptions{"module": "nodenext", "moduleResolution": "nodenext"}
			}
			p, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{tc.entry}, Files: tc.files}, nil, opts)
			if err != nil || p == nil {
				t.Fatalf("compile %v %+v", err, ds)
			}
			r := goya.New()
			v, err := r.RunProgram(p)
			if err != nil {
				t.Fatal(err)
			}
			if !hostObject(t, v).Get("result").ToBoolean() {
				t.Fatalf("module result %v", v)
			}
			for _, name := range []string{"require", "exports", "module"} {
				if r.Get(name) != nil {
					t.Fatalf("implicit global %s", name)
				}
			}
			again, err := r.RunProgram(p)
			if err != nil || again != v {
				t.Fatalf("repeated initialization: %v %v", again, err)
			}
		})
	}
}

func TestTypeScriptCommonJSHostPreflight(t *testing.T) {
	calls := 0
	reg := goya.NewRegistry()
	registerHost(t, reg, "record", func() { calls++ })
	if err := reg.RegisterModule("host:api", map[string]any{"add": func(a, b float64) float64 { return a + b }}); err != nil {
		t.Fatal(err)
	}
	env := freezeHost(t, reg)
	project := goya.Project{EntryPoints: []string{"/main.cts"}, Files: map[string]string{"/main.cts": `record();import api=require("host:api");export const result=api.add(40,2)===42;`}}
	p, ds, err := goya.CompileProject(context.Background(), project, env, goya.CompileOptions{"module": "nodenext", "moduleResolution": "nodenext"})
	if err != nil || p == nil {
		t.Fatalf("compile %v %+v", err, ds)
	}
	wrong := goya.NewRegistry()
	registerHost(t, wrong, "record", func() { calls++ })
	if err := wrong.RegisterModule("host:api", map[string]any{"add": func(a, b string) string { return a + b }}); err != nil {
		t.Fatal(err)
	}
	r := goya.New()
	if err := freezeHost(t, wrong).Bind(r); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunProgram(p); err == nil || calls != 0 {
		t.Fatalf("incompatible CommonJS API reached effects: %v %d", err, calls)
	}
	r = goya.New()
	if err := env.Bind(r); err != nil {
		t.Fatal(err)
	}
	v, err := r.RunProgram(p)
	if err != nil || !hostObject(t, v).Get("result").ToBoolean() || calls != 1 {
		t.Fatalf("host require: %v %d", err, calls)
	}
}

func TestTypeScriptCommonJSStillChecksWholeGraph(t *testing.T) {
	reg := goya.NewRegistry()
	calls := 0
	registerHost(t, reg, "record", func() { calls++ })
	env := freezeHost(t, reg)
	p, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{"/main.cts"}, Files: map[string]string{
		"/main.cts":  `record();import value=require("./value.cjs");export const result=value.n;`,
		"/value.cts": `record();export const n:number="wrong";`,
	}}, env, goya.CompileOptions{"module": "nodenext", "moduleResolution": "nodenext"})
	if err != nil || p != nil || !goya.HasTypeErrors(ds) || calls != 0 {
		t.Fatalf("unchecked CommonJS graph %v %+v %d", err, ds, calls)
	}
}

func TestTypeScriptCommonJSDoesNotProvideUserRequire(t *testing.T) {
	p, ds, err := goya.CompileProject(context.Background(), goya.Project{Files: map[string]string{"/main.cts": `declare const require:(name:string)=>unknown;export const result=require("external");`}}, nil, goya.CompileOptions{"module": "nodenext", "moduleResolution": "nodenext"})
	if err != nil || p == nil {
		t.Fatalf("compile %v %+v", err, ds)
	}
	if _, err := goya.New().RunProgram(p); err == nil {
		t.Fatal("user require gained implicit capability")
	}
}
