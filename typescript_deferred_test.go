package goya_test

import (
	"context"
	"testing"

	"github.com/totoer/goya"
)

func TestTypeScriptDeferredModules(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"get", `const before=log.length;const value=lazy.answer;export const result=before===0&&value===42&&log.join(",")==="value";`},
		{"missing-get", `const before=log.length;const value=(lazy as unknown as {missing?:number}).missing;export const result=before===0&&value===undefined&&log.join(",")==="value";`},
		{"has", `const before=log.length;const value="answer" in lazy;export const result=before===0&&value&&log.join(",")==="value";`},
		{"has-own", `const before=log.length;const value=Object.hasOwn(lazy,"missing");export const result=before===0&&!value&&log.join(",")==="value";`},
		{"own-keys", `const before=log.length;const value=Reflect.ownKeys(lazy);export const result=before===0&&value.includes("answer")&&log.join(",")==="value";`},
		{"own-symbols", `const before=log.length;const value=Object.getOwnPropertySymbols(lazy);export const result=before===0&&value.includes(Symbol.toStringTag)&&log.join(",")==="value";`},
		{"descriptor", `const before=log.length;const value=Object.getOwnPropertyDescriptor(lazy,"answer");export const result=before===0&&value?.value===42&&log.join(",")==="value";`},
		{"delete", `const before=log.length;const value=Reflect.deleteProperty(lazy,"answer");export const result=before===0&&!value&&log.join(",")==="value";`},
		{"define", `const before=log.length;const value=Reflect.defineProperty(lazy,"answer",{get(){return 1}});export const result=before===0&&!value&&log.join(",")==="value";`},
		{"symbol-and-then-no-evaluation", `const raw=lazy as unknown as {then:unknown};const tag=(lazy as unknown as {[Symbol.toStringTag]:string})[Symbol.toStringTag];const then=raw.then;const has="then" in lazy;const deleted=Reflect.deleteProperty(lazy,"then");const defined=Reflect.defineProperty(lazy,"then",{});const changed=Reflect.set(lazy,"answer",0);export const result=tag==="Deferred Module"&&then===undefined&&!has&&deleted&&!defined&&!changed&&Object.getPrototypeOf(lazy)===null&&!Object.isExtensible(lazy)&&log.length===0;`},
		{"identity", `import defer * as other from "./value.js";const before=log.length;const n=lazy.answer;const normal=await import("./value.js");export const result=lazy===other&&lazy!==normal&&before===0&&n===42&&normal.answer===42&&log.length===1;`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := compileModules(t, map[string]string{"/main.ts": `import {log} from "./state.js";import defer * as lazy from "./value.js";` + tc.source, "/state.ts": `export const log:string[]=[];`, "/value.ts": `import {log} from "./state.js";log.push("value");export const answer=42;`}, nil)
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
				t.Fatal("deferred semantics")
			}
		})
	}
}

func TestTypeScriptDeferredAsyncPreload(t *testing.T) {
	p := compileModules(t, map[string]string{
		"/main.ts":  `import {log} from "./state.js";import defer * as c from "./c.js";const before=log.join(",");const n=c.answer;export const result=before==="e,d:start,d:end"&&n===42&&log.join(",")==="e,d:start,d:end,f,c";`,
		"/state.ts": `export const log:string[]=[];`,
		"/c.ts":     `import "./d.js";import "./f.js";import {log} from "./state.js";log.push("c");export const answer=42;`,
		"/d.ts":     `import "./e.js";import {log} from "./state.js";log.push("d:start");await 0;log.push("d:end");`,
		"/e.ts":     `import {log} from "./state.js";log.push("e");`,
		"/f.ts":     `import {log} from "./state.js";log.push("f");`,
	}, nil)
	v, err := goya.New().RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	promise := modulePromise(t, v)
	if promise.State() != goya.PromiseStateFulfilled || !hostObject(t, promise.Result()).Get("result").ToBoolean() {
		t.Fatalf("preload: %v %v", promise.State(), promise.Result())
	}
}

func TestTypeScriptDeferredFailureAndReexport(t *testing.T) {
	calls := 0
	reg := goya.NewRegistry()
	registerHost(t, reg, "record", func() { calls++ })
	env := freezeHost(t, reg)
	p := compileModules(t, map[string]string{
		"/main.ts":   `import {lazy} from "./bridge.js";let errors=0;for(let i=0;i<2;i++){try{lazy.answer;}catch(e){if(e==="failure")errors++;}}export const result=errors===2;`,
		"/bridge.ts": `import defer * as lazy from "./value.js";export {lazy};`,
		"/value.ts":  `record();export const answer=42;throw "failure";`,
	}, env)
	r := goya.New()
	if err := env.Bind(r); err != nil {
		t.Fatal(err)
	}
	v, err := r.RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	if !hostObject(t, v).Get("result").ToBoolean() || calls != 1 {
		t.Fatalf("deferred failure cache effects %d", calls)
	}
}

func TestTypeScriptDeferredCycleReadiness(t *testing.T) {
	calls := 0
	reg := goya.NewRegistry()
	registerHost(t, reg, "record", func() { calls++ })
	env := freezeHost(t, reg)
	p := compileModules(t, map[string]string{
		"/main.ts":  `import defer * as lazy from "./value.js";let denied=false;try{lazy.answer;}catch(e){denied=e instanceof TypeError;}export const result=denied;export function read(){return lazy.answer;}`,
		"/value.ts": `import "./main.js";record();export const answer=42;`,
	}, env)
	r := goya.New()
	if err := env.Bind(r); err != nil {
		t.Fatal(err)
	}
	v, err := r.RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	ns := hostObject(t, v)
	if !ns.Get("result").ToBoolean() || calls != 0 {
		t.Fatal("reentrant deferred cycle executed")
	}
	read, _ := goya.AssertFunction(ns.Get("read"))
	answer, err := read(goya.Undefined())
	if err != nil || answer.ToInteger() != 42 || calls != 1 {
		t.Fatalf("ready cycle: %v %v %d", answer, err, calls)
	}
}

func TestTypeScriptDeferredGraphTypeChecking(t *testing.T) {
	p, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": `import defer * as lazy from "./value.js";export const result=true;export {lazy};`, "/value.ts": `export const answer:number="wrong";`}}, nil, nil)
	if err != nil || p != nil || !goya.HasTypeErrors(ds) {
		t.Fatalf("deferred graph escaped checking: %v %+v", err, ds)
	}
}

func TestTypeScriptDynamicDeferredImport(t *testing.T) {
	p := compileModules(t, map[string]string{
		"/main.ts":  `import {log} from "./state.js";const a=await import.defer("./value.js") as {answer:number};const before=log.length;const b=await import.defer("./value.js");const n=a.answer;const ordinary=await import("./value.js");export const result=before===0&&a===b&&a!==ordinary&&n===42&&log.join(",")==="value";`,
		"/state.ts": `export const log:string[]=[];`,
		"/value.ts": `import {log} from "./state.js";log.push("value");export const answer=42;export function then(resolve:(value:unknown)=>void){resolve(7);}`,
	}, nil)
	// The ordinary import resolves its exported then function to 7. The deferred
	// namespace excludes then and can be fulfilled without evaluating the module.
	v, err := goya.New().RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	promise := modulePromise(t, v)
	if promise.State() != goya.PromiseStateFulfilled || !hostObject(t, promise.Result()).Get("result").ToBoolean() {
		t.Fatalf("dynamic defer: %v %v", promise.State(), promise.Result())
	}
}

func TestTypeScriptDeferredAlreadyFailedModule(t *testing.T) {
	calls := 0
	reg := goya.NewRegistry()
	registerHost(t, reg, "record", func() { calls++ })
	env := freezeHost(t, reg)
	p := compileModules(t, map[string]string{
		"/main.ts":  `try{await import("./value.js");}catch{}const lazy=await import.defer("./value.js");let errors=0;for(let i=0;i<2;i++){try{lazy.answer;}catch(e){if(e==="failure")errors++;}}export const result=errors===2&&Object.prototype.toString.call(lazy)==="[object Deferred Module]";`,
		"/value.ts": `record();export const answer=42;throw "failure";`,
	}, env)
	r := goya.New()
	if err := env.Bind(r); err != nil {
		t.Fatal(err)
	}
	v, err := r.RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	promise := modulePromise(t, v)
	if promise.State() != goya.PromiseStateFulfilled || !hostObject(t, promise.Result()).Get("result").ToBoolean() || calls != 1 {
		t.Fatalf("cached deferred failure %v effects %d", promise.State(), calls)
	}
}

func TestTypeScriptDeferredAsyncPreloadRejection(t *testing.T) {
	var effects []string
	reg := goya.NewRegistry()
	registerHost(t, reg, "record", func(value string) { effects = append(effects, value) })
	env := freezeHost(t, reg)
	p := compileModules(t, map[string]string{
		"/main.ts":  `import defer * as lazy from "./value.js";record("main");export {lazy};`,
		"/value.ts": `import "./async.js";record("deferred");export const answer=42;`,
		"/async.ts": `record("async");await Promise.reject("failure");export {};`,
	}, env)
	r := goya.New()
	if err := env.Bind(r); err != nil {
		t.Fatal(err)
	}
	v, err := r.RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	promise := modulePromise(t, v)
	if promise.State() != goya.PromiseStateRejected || promise.Result().String() != "failure" || len(effects) != 1 || effects[0] != "async" {
		t.Fatalf("preload rejection %v effects %v", promise.State(), effects)
	}
}
