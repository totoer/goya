package goya_test

import (
	"testing"

	"github.com/totoer/goya"
)

func modulePromise(t *testing.T, value goya.Value) *goya.Promise {
	t.Helper()
	p, ok := value.Export().(*goya.Promise)
	if !ok {
		t.Fatalf("expected module Promise, got %v", value)
	}
	return p
}

func TestTypeScriptModuleTopLevelAwait(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{"value", map[string]string{"/main.ts": `export const result=(await Promise.resolve(42))===42;`}},
		{"dependency-order", map[string]string{
			"/log.ts":  `export const events:string[]=[];`,
			"/a.ts":    `import {events} from "./log.js";events.push("a:start");await 0;events.push("a:end");export const a=1;`,
			"/b.ts":    `import {events} from "./log.js";events.push("b:start");await 0;events.push("b:end");export const b=2;`,
			"/main.ts": `import {a} from "./a.js";import {b} from "./b.js";import {events} from "./log.js";events.push("main");export const result=a+b===3&&events.join(",")==="a:start,b:start,a:end,b:end,main";`,
		}},
		{"cycle", map[string]string{
			"/main.ts": `import {b} from "./b.js";export let a=1;await 0;a++;export const result=b===2;export function read(){return a;}`,
			"/b.ts":    `import {read} from "./main.js";export const b=2;export function later(){return read();}`,
		}},
		{"top-level-for-await", map[string]string{"/main.ts": `let sum=0;for await(const n of [Promise.resolve(1),2]){sum+=n;}export const result=sum===3;`}},
		{"nested-function-not-tla", map[string]string{"/main.ts": `async function f(){await 0;}await f();export const result=true;`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := compileModules(t, tc.files, nil)
			r := goya.New()
			v, err := r.RunProgram(p)
			if err != nil {
				t.Fatal(err)
			}
			promise := modulePromise(t, v)
			if promise.State() != goya.PromiseStateFulfilled {
				t.Fatalf("module state %v result %v", promise.State(), promise.Result())
			}
			if !hostObject(t, promise.Result()).Get("result").ToBoolean() {
				t.Fatal("incorrect module result")
			}
			again, err := r.RunProgram(p)
			if err != nil {
				t.Fatal(err)
			}
			second := modulePromise(t, again)
			if second.State() != goya.PromiseStateFulfilled || second.Result() != promise.Result() {
				t.Fatal("module initialized twice")
			}
		})
	}
}

func TestTypeScriptModuleAwaitPendingAndSharedDependencies(t *testing.T) {
	var release func()
	var events []string
	reg := goya.NewRegistry()
	registerHost(t, reg, "hold", func(resolve func()) {
		if release != nil {
			t.Fatal("duplicate initialization")
		}
		release = resolve
	})
	registerHost(t, reg, "record", func(event string) { events = append(events, event) })
	env := freezeHost(t, reg)
	p := compileModules(t, map[string]string{
		"/main.ts": `import {a} from "./a.js";import {b} from "./b.js";record("main");export const result=a+b===4;`,
		"/a.ts":    `import {value} from "./gate.js";record("a");export const a=value;`,
		"/b.ts":    `import {value} from "./gate.js";record("b");export const b=value;`,
		"/gate.ts": `record("gate:start");await new Promise<void>(resolve=>hold(resolve));record("gate:end");export const value=2;`,
	}, env)
	r := goya.New()
	if err := env.Bind(r); err != nil {
		t.Fatal(err)
	}
	v, err := r.RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	first := modulePromise(t, v)
	again, err := r.RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	second := modulePromise(t, again)
	if release == nil || first.State() != goya.PromiseStatePending || second.State() != goya.PromiseStatePending || len(events) != 1 || events[0] != "gate:start" {
		t.Fatalf("premature initialization: %v", events)
	}
	release()
	for _, promise := range []*goya.Promise{first, second} {
		if promise.State() != goya.PromiseStateFulfilled || !hostObject(t, promise.Result()).Get("result").ToBoolean() {
			t.Fatalf("continuation: %v %v", promise.State(), promise.Result())
		}
	}
	if len(events) != 5 || events[1] != "gate:end" || events[4] != "main" {
		t.Fatalf("dependency order %v", events)
	}
}

func TestTypeScriptModuleAwaitRejectionStopsDependents(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		var events []string
		reg := goya.NewRegistry()
		registerHost(t, reg, "record", func(event string) { events = append(events, event) })
		env := freezeHost(t, reg)
		files := map[string]string{
			"/main.ts": `import "./a.js";import "./c.js";record("main");export const result=true;`,
			"/a.ts":    `record("a");await Promise.reject("failure");record("after");export const a=1;`,
			"/c.ts":    `import "./a.js";record("c");`,
		}
		if cycle {
			files["/a.ts"] = `import "./b.js";record("a");await Promise.reject("failure");export const a=1;`
			files["/b.ts"] = `import "./a.js";record("b");`
			files["/c.ts"] = `import "./b.js";record("c");`
		}
		p := compileModules(t, files, env)
		r := goya.New()
		if err := env.Bind(r); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			v, err := r.RunProgram(p)
			if err != nil {
				t.Fatal(err)
			}
			promise := modulePromise(t, v)
			if promise.State() != goya.PromiseStateRejected || promise.Result().String() != "failure" {
				t.Fatalf("rejection %v %v", promise.State(), promise.Result())
			}
		}
		want := 1
		if cycle {
			want = 2
		}
		if len(events) != want {
			t.Fatalf("dependent or repeated effects: %v", events)
		}
	}
}

func TestTypeScriptModuleNestedAsyncKeepsSyncEvaluation(t *testing.T) {
	p := compileModules(t, map[string]string{"/main.ts": `export async function f(){await 0;return 1;}export const result=true;`}, nil)
	v, err := goya.New().RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Export().(*goya.Promise); ok {
		t.Fatal("nested await made module asynchronous")
	}
	if !hostObject(t, v).Get("result").ToBoolean() {
		t.Fatal("module result")
	}
}
