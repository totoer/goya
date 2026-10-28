package goya_test

import (
	"context"
	"testing"

	"github.com/totoer/goya"
)

func TestTypeScriptDisposal(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		async        bool
	}{
		{"order", `const log:string[]=[];{using a={[Symbol.dispose](){log.push("a")}};using b={[Symbol.dispose](){log.push("b")}};log.push("body");}log.join(",")==="body,b,a";`, false},
		{"return", `const log:string[]=[];function f(){using a={[Symbol.dispose](){log.push("dispose")}};return 2;}f()===2&&log.join(",")==="dispose";`, false},
		{"suppressed", `let result=false;try{using a={[Symbol.dispose](){throw "dispose"}};throw "body";}catch(e){result=e instanceof SuppressedError&&e.error==="dispose"&&e.suppressed==="body"&&e instanceof Error&&!Object.keys(e).includes("error");}result;`, false},
		{"multiple-errors", `let result=false;try{using a={[Symbol.dispose](){throw "a"}};using b={[Symbol.dispose](){throw "b"}};throw "body";}catch(e){result=e instanceof SuppressedError&&e.error==="a"&&e.suppressed instanceof SuppressedError&&e.suppressed.error==="b"&&e.suppressed.suppressed==="body";}result;`, false},
		{"break-continue", `const log:number[]=[];for(const n of [1,2,3]){using a={[Symbol.dispose](){log.push(n)}};if(n===1)continue;break;}log.join(",")==="1,2";`, false},
		{"for-using", `const log:number[]=[];const sources=[1,2].map(n=>({[Symbol.dispose](){log.push(n)}}));for(using a of sources){}log.join(",")==="1,2";`, false},
		{"invalid-and-partial-init", `const log:string[]=[];let failed=false;try{using a={[Symbol.dispose](){log.push("a")}};using b={} as Disposable;}catch(e){failed=e instanceof TypeError;}failed&&log.join(",")==="a";`, false},
		{"capture-method", `let method=0;let count=0;const resource={get [Symbol.dispose](){method++;return function(this:object){if(this===resource)count++;}}};{using a=resource;Object.defineProperty(resource,Symbol.dispose,{value:()=>{count+=10}});}method===1&&count===1;`, false},
		{"sync-null", `{using a=null;using b=undefined;}true;`, false},
		{"async-order", `(async()=>{const log:string[]=[];{await using a={[Symbol.asyncDispose]:async()=>{log.push("a:start");await 0;log.push("a:end")}};using b={[Symbol.dispose](){log.push("b")}};await using c={[Symbol.asyncDispose]:async()=>{log.push("c:start");await 0;log.push("c:end")}};log.push("body");}return log.join(",")==="body,c:start,c:end,b,a:start,a:end";})()`, true},
		{"async-fallback-ignores-return", `(async()=>{let called=false;{await using a={[Symbol.dispose](){called=true;return {then(){throw "ignored"}}}};}return called;})()`, true},
		{"async-rejection", `(async()=>{let result=false;try{await using a={[Symbol.asyncDispose]:async()=>{throw "dispose"}};throw "body";}catch(e){result=e instanceof SuppressedError&&e.error==="dispose"&&e.suppressed==="body";}return result;})()`, true},
		{"null-await-turn", `(async()=>{const log:string[]=[];const p=(async()=>{using a={[Symbol.dispose](){log.push("dispose")}};await using b=null;log.push("body");})();log.push("sync");await p;return log.join(",")==="body,sync,dispose";})()`, true},
		{"iterator-dispose", `const log:string[]=[];function* f(){try{yield 1;}finally{log.push("closed")}}{using g=f();g.next();}log.join(",")==="closed";`, false},
		{"async-iterator-dispose", `(async()=>{let closed=false;async function* f(){try{yield 1;}finally{await 0;closed=true;}}{await using g=f();await g.next();}return closed;})()`, true},
	} {
		for _, target := range []string{"es2016", "es2025", "esnext"} {
			t.Run(tc.name+"/"+target, func(t *testing.T) {
				p, ds, err := goya.CompileProject(context.Background(), goya.Project{Files: map[string]string{"/main.ts": tc.source}}, nil, goya.CompileOptions{"target": target, "lib": []string{"es2025", "esnext.disposable"}})
				if err != nil || p == nil {
					t.Fatalf("compile: %v %+v", err, ds)
				}
				v, err := goya.New().RunProgram(p)
				if err != nil {
					t.Fatal(err)
				}
				if tc.async {
					promise := modulePromise(t, v)
					if promise.State() != goya.PromiseStateFulfilled {
						t.Fatalf("disposal: %v %v", promise.State(), promise.Result())
					}
					v = promise.Result()
				}
				if !v.ToBoolean() {
					t.Fatalf("result %v", v)
				}
			})
		}
	}
}

func TestTypeScriptModuleAsyncDisposal(t *testing.T) {
	p, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{
		"/dep.ts":  `export const log:string[]=[];await using resource={[Symbol.asyncDispose]:async()=>{await 0;log.push("disposed")}};log.push("body");`,
		"/main.ts": `import {log} from "./dep.js";export const result=log.join(",")==="body,disposed";`,
	}}, nil, goya.CompileOptions{"lib": []string{"es2025", "esnext.disposable"}})
	if err != nil || p == nil {
		t.Fatalf("compile %v %+v", err, ds)
	}
	v, err := goya.New().RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	promise := modulePromise(t, v)
	if promise.State() != goya.PromiseStateFulfilled || !hostObject(t, promise.Result()).Get("result").ToBoolean() {
		t.Fatalf("async disposal module %v %v", promise.State(), promise.Result())
	}
}
