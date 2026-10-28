package goya_test

import (
	"context"
	"testing"

	"github.com/totoer/goya"
)

func TestTypeScriptJSONModules(t *testing.T) {
	for _, tc := range []struct{ name, source, data string }{
		{"default", `import value from "./value.json" with {type:"json"};export const result=value.answer===42;`, `{"answer":42}`},
		{"identity", `import value from "./value.json" with {type:"json"};const again=await import("./value.json",{with:{type:"json"}});value.answer++;export const result=value===again.default&&again.default.answer===43;`, `{"answer":42}`},
		{"proto-data", `import value from "./value.json" with {type:"json"};export const result=Object.hasOwn(value,"__proto__")&&Object.getPrototypeOf(value)===Object.prototype&&value.__proto__.answer===42;`, `{"__proto__":{"answer":42}}`},
		{"unicode", `import value from "./value.json" with {type:"json"};export const result=value.text.length===2&&value.text.charCodeAt(0)===0xd800&&value.text[1]==="x";`, `{"text":"\ud800x"}`},
		{"numbers", `import value from "./value.json" with {type:"json"};export const result=Object.is(value.zero,-0)&&value.large===Infinity;`, `{"zero":-0,"large":1e309}`},
		{"array", `import value from "./value.json" with {type:"json"};export const result=value.length===3&&value[0]===1&&value[1]===2&&value[2]===3;`, `[1,2,3]`},
		{"scalar", `import value from "./value.json" with {type:"json"};export const result=value===42;`, `42`},
		{"null", `import value from "./value.json" with {type:"json"};export const result=value===null;`, `null`},
		{"duplicate-keys", `import value from "./value.json" with {type:"json"};export const result=value.answer===42;`, `{"answer":1,"answer":42}`},
		{"deferred", `import defer * as lazy from "./value.json" with {type:"json"};export const result=(lazy as unknown as {default:{answer:number}}).default.answer===42;`, `{"answer":42}`},
		{"reexport", `import {config} from "./bridge.js";export const result=config.answer===42;`, `{"answer":42}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": tc.source, "/value.json": tc.data, "/bridge.ts": `export {default as config} from "./value.json" with {type:"json"};`}}, nil, goya.CompileOptions{"resolveJsonModule": true})
			if err != nil || p == nil {
				t.Fatalf("compile %v %+v", err, ds)
			}
			v, err := goya.New().RunProgram(p)
			if err != nil {
				t.Fatal(err)
			}
			if promise, ok := v.Export().(*goya.Promise); ok {
				if promise.State() != goya.PromiseStateFulfilled {
					t.Fatalf("initialization %v %v", promise.State(), promise.Result())
				}
				v = promise.Result()
			}
			if !hostObject(t, v).Get("result").ToBoolean() {
				t.Fatal("JSON result")
			}
		})
	}
}

func TestTypeScriptInvalidJSONDoesNotCompileOrRun(t *testing.T) {
	calls := 0
	reg := goya.NewRegistry()
	registerHost(t, reg, "record", func() { calls++ })
	env := freezeHost(t, reg)
	for _, data := range []string{`{"answer":}`, `{"answer":42,}`, `/* comment */ {"answer":42}`, `{answer:42}`, `{"answer":NaN}`, ""} {
		project := goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": `import value from "./value.json" with {type:"json"};record();export const result=value;`, "/value.json": data}}
		ds, err := goya.Check(context.Background(), project, env, goya.CompileOptions{"resolveJsonModule": true})
		if err != nil || !goya.HasTypeErrors(ds) {
			t.Fatalf("invalid JSON passed Check: %q %v %+v", data, err, ds)
		}
		p, ds, err := goya.CompileProject(context.Background(), project, env, goya.CompileOptions{"resolveJsonModule": true})
		if err != nil || p != nil || !goya.HasTypeErrors(ds) || calls != 0 {
			t.Fatalf("invalid JSON compiled: %q %v %+v", data, err, ds)
		}
	}
}

func TestTypeScriptStaticImportAttributePolicy(t *testing.T) {
	for _, tc := range []struct{ source, file, data string }{
		{`import value from "./value.json";export const result=value;`, "/value.json", `{"answer":42}`},
		{`import value from "./value.json" with {type:"css"};export const result=value;`, "/value.json", `{"answer":42}`},
		{`import value from "./value.json" with {type:"json",custom:"value"};export const result=value;`, "/value.json", `{"answer":42}`},
		{`import value from "./value.js" with {type:"json"};export const result=value;`, "/value.ts", `export default 42;`},
	} {
		p, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": tc.source, tc.file: tc.data}}, nil, goya.CompileOptions{"resolveJsonModule": true})
		if err != nil || p != nil || !goya.HasTypeErrors(ds) {
			t.Fatalf("invalid attributes compiled: %v %+v", err, ds)
		}
	}
}

func TestTypeScriptCommonJSJSONRequire(t *testing.T) {
	p, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{"/main.cts"}, Files: map[string]string{"/main.cts": `import value=require("./value.json");export const result=value.answer===42;`, "/value.json": `{"answer":42}`}}, nil, goya.CompileOptions{"resolveJsonModule": true, "module": "nodenext", "moduleResolution": "nodenext"})
	if err != nil || p == nil {
		t.Fatalf("compile %v %+v", err, ds)
	}
	v, err := goya.New().RunProgram(p)
	if err != nil || !hostObject(t, v).Get("result").ToBoolean() {
		t.Fatalf("JSON require %v", err)
	}
}

func TestTypeScriptDynamicJSONAttributePolicy(t *testing.T) {
	p, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/value.json"}, Files: map[string]string{
		"/main.ts":    `const path:string="./value.json";const a=await import(path,{with:{type:"json"}});let errors=0;for(const options of [undefined,{with:{type:"css"}},{with:{type:1}},{with:null}]){try{await import(path,options as ImportCallOptions); }catch(e){if(e instanceof TypeError)errors++;}}export const result=a.default.answer===42&&errors===4;`,
		"/value.json": `{"answer":42}`,
	}}, nil, goya.CompileOptions{"resolveJsonModule": true})
	if err != nil || p == nil {
		t.Fatalf("compile %v %+v", err, ds)
	}
	v, err := goya.New().RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	promise := modulePromise(t, v)
	if promise.State() != goya.PromiseStateFulfilled || !hostObject(t, promise.Result()).Get("result").ToBoolean() {
		t.Fatalf("dynamic JSON %v %v", promise.State(), promise.Result())
	}
}

func TestTypeScriptDynamicImportAttributeEvaluationOrder(t *testing.T) {
	p, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/value.json"}, Files: map[string]string{
		"/main.ts":    `const events:string[]=[];const path:string="./value.json";const options={get with(){events.push("with");return {get type(){events.push("type");return "json";}}}};const promise=import(path,options);events.push("sync");await promise;let rejected=false;try{await import(path,{with:{get first(){events.push("first");return 1;},get second(){events.push("second");return "json";}}} as unknown as ImportCallOptions);}catch(e){rejected=e instanceof TypeError;}export const result=rejected&&events.join(",")==="with,type,sync,first,second";`,
		"/value.json": `{"answer":42}`,
	}}, nil, goya.CompileOptions{"resolveJsonModule": true})
	if err != nil || p == nil {
		t.Fatalf("compile %v %+v", err, ds)
	}
	v, err := goya.New().RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	promise := modulePromise(t, v)
	if promise.State() != goya.PromiseStateFulfilled || !hostObject(t, promise.Result()).Get("result").ToBoolean() {
		t.Fatalf("attributes order %v %v", promise.State(), promise.Result())
	}
}
