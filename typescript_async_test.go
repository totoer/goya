package goya_test

import (
	"context"
	"testing"

	"github.com/totoer/goya"
)

func TestTypeScriptAsyncExecution(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"async-order", `const events: string[] = []; async function f() {events.push("start"); await 1; events.push("continued"); return 7;} const p=f();events.push("sync"); p.then(v=>v===7 && events.join(",")==="start,sync,continued");`},
		{"async-function-prototypes", `(async()=>{async function f(){}const a=async()=>1;let denied=false;try {Reflect.construct(f,[]);} catch(e){denied=e instanceof TypeError;}return denied&&Object.getPrototypeOf(f)===Object.getPrototypeOf(a)&&Object.prototype.toString.call(f)==="[object AsyncFunction]"&&!Object.hasOwn(f,"prototype");})()`},
		{"async-generator-rejected-yield", `(async()=>{async function* f(){try{yield Promise.reject("failure");} catch(e){yield String(e);}finally {await 0;}}const g=f();const a=await g.next();const b=await g.next();return a.value==="failure"&&b.done;})()`},
		{"async-generator-arguments-this", `(async()=>{const o={n:2,async *f(n:number){yield this.n+n+arguments.length;}};return (await o.f(3).next()).value===6;})()`},
		{"async-generator-delegate-missing-throw", `(async()=>{let closed=false;const source={ [Symbol.asyncIterator](){return {next:async()=>({value:1,done:false}),return:async()=>{closed=true;return {value:0,done:true};}};}};async function* f(){try {yield* source;}catch(e){yield e instanceof TypeError;}}const g=f();await g.next();const end=await g.throw("x");return closed&&end.value===true;})()`},
		{"async-catch-finally", `(async()=>{const events:string[]=[]; async function f(){try {await Promise.reject("failure");} catch(e){events.push(String(e));return 3;} finally {await 0;events.push("finally");}} return await f()===3 && events.join(",")==="failure,finally";})()`},
		{"generator", `(async()=>{function* f(){try {const v:number=yield 1;return v+1;} finally {yield 3;}} const g=f();return g.next().value===1 && g.next(4).value===3 && g.next().value===5;})()`},
		{"async-generator", `(async()=>{async function* f(){const n:number=yield Promise.resolve(1);await 0;yield n;return 9;} const g=f();const a=await g.next();const b=await g.next(4);const c=await g.next();return a.value===1&&!a.done&&b.value===4&&!b.done&&c.value===9&&c.done;})()`},
		{"async-generator-queue", `(async()=>{const events:string[]=[];async function* f(){try {yield 1;await 0;yield 2;}finally {events.push("finally");await 0;yield 3;}}const g=f();const ps=[g.next(),g.return(9 as never),g.next(),g.next()];const vs=await Promise.all(ps);return vs.map(v=>String(v.value)+":"+v.done).join(",")==="1:false,3:false,9:true,undefined:true"&&events.join(",")==="finally";})()`},
		{"async-generator-throw", `(async()=>{async function* f(){try {yield 1;} catch(e){yield String(e);}finally {await 0;yield "finally";}} const g=f();await g.next();const a=await g.throw("caught");const b=await g.next();const c=await g.next();return a.value==="caught"&&b.value==="finally"&&c.done;})()`},
		{"async-generator-delegation", `(async()=>{async function* inner(){yield Promise.resolve(2);return 3;}async function* outer(){yield* [Promise.resolve(1)];const n=yield* inner();return n+1;}const g=outer();const a=await g.next();const b=await g.next();const c=await g.next();return a.value===1&&b.value===2&&c.value===4&&c.done;})()`},
		{"for-await-close", `(async()=>{const events:number[]=[];async function* f(){try {yield 1;yield 2;}finally {await 0;events.push(3);}}for await (const n of f()){events.push(n);break;}return events.join(",")==="1,3";})()`},
		{"for-await-sync", `(async()=>{let sum=0;for await(const n of [Promise.resolve(1),2]){sum+=n;}return sum===3;})()`},
		{"async-generator-prototypes", `(async()=>{async function* f(){yield 1;}const g=f();const fp=Object.getPrototypeOf(f);const gp=Object.getPrototypeOf(f.prototype);let denied=false;try {Reflect.construct(f,[]);} catch(e){denied=e instanceof TypeError;}return Object.getPrototypeOf(g)===f.prototype&&Object.getPrototypeOf(fp)===Function.prototype&&Object.prototype.toString.call(f)==="[object AsyncGeneratorFunction]"&&Object.prototype.toString.call(g)==="[object AsyncGenerator]"&&g[Symbol.asyncIterator]()===g&&denied&&gp.constructor===fp;})()`},
		{"async-generator-super", `(async()=>{class Base {value(){return 2;}}class Derived extends Base {async *values(){yield await super.value();}}const g=new Derived().values();return (await g.next()).value===2;})()`},
		{"async-generator-super-index", `(async()=>{class Base {value(){return 2;}}class Derived extends Base {async *values(){const key="value";yield await super[key]();}}return (await new Derived().values().next()).value===2;})()`},
		{"async-super-index-write", `(async()=>{class Base {n=0;get value(){return this.n;}set value(v:number){this.n=v;}}class Derived extends Base {async *values(){const key="value";super[key]=2;yield super[key]++;yield super[key];}}const g=new Derived().values();return (await g.next()).value===2&&(await g.next()).value===3;})()`},
		{"async-generator-delegate-return", `(async()=>{let closed=false;async function* inner(){try{yield 1;}finally{await 0;closed=true;}}async function* outer(){yield* inner();}const g=outer();await g.next();const end=await g.return(7 as never);return closed&&end.done&&String(end.value)==="7";})()`},
		{"async-generator-object-rest", `(async()=>{async function* f(){const source={a:1,b:2};const {a,...rest}=source;yield {...rest,c:a};}const v=(await f().next()).value;return v?.b===2&&v.c===1;})()`},
		{"async-generator-template", "(async()=>{async function* f(){function tag(s:TemplateStringsArray,n:number){return s.raw[0]+n;}yield tag`a${1}`;}return (await f().next()).value===\"a1\";})()"},
	} {
		for _, target := range []string{"es2016", "es2017", "es2021", "es2025", "esnext"} {
			t.Run(tc.name+"/"+target, func(t *testing.T) {
				p, ds, err := goya.CompileProject(context.Background(), goya.Project{Files: map[string]string{"/main.ts": tc.source}}, nil, goya.CompileOptions{"target": target})
				if err != nil || p == nil {
					t.Fatalf("compile: %v %+v", err, ds)
				}
				v, err := goya.New().RunProgram(p)
				if err != nil {
					t.Fatal(err)
				}
				promise, ok := v.Export().(*goya.Promise)
				if !ok || promise.State() != goya.PromiseStateFulfilled || !promise.Result().ToBoolean() {
					if ok {
						t.Fatalf("promise state %v result %v", promise.State(), promise.Result())
					}
					t.Fatalf("not a promise: %v", v)
				}
			})
		}
	}
}
