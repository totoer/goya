package goya_test

import (
	"context"
	"testing"

	"github.com/totoer/goya"
)

func TestTypeScriptDisposableStacks(t *testing.T) {
	for name, source := range map[string]string{
		"sync order":                      `const log:string[]=[];const s=new DisposableStack();const value={ [Symbol.dispose](){log.push("use")}};const returned=s.use(value);const adopted=s.adopt(42,v=>log.push(String(v)));s.defer(()=>log.push("defer"));const before=!s.disposed;s.dispose();s.dispose();export const result=before&&s.disposed&&returned===value&&adopted===42&&log.join(",")==="defer,42,use";`,
		"sync move":                       `const log:number[]=[];class Derived extends DisposableStack{}const s=new Derived();s.defer(()=>log.push(42));const moved=s.move();s.dispose();const before=s.disposed&&!moved.disposed&&!(moved instanceof Derived);moved.dispose();let rejected=0;for(const f of [()=>s.defer(()=>{}),()=>s.adopt(1,()=>{}),()=>s.use(null),()=>s.move()]){try{f()}catch(e){if(e instanceof ReferenceError)rejected++}}export const result=before&&log.join(",")==="42"&&rejected===4;`,
		"capture method":                  `let got=0;let called=0;const value={get [Symbol.dispose](){got++;return function(this:unknown){if(this===value)called++}}};const s=new DisposableStack();s.use(value);s.use(null);s.use(undefined);s.dispose();export const result=got===1&&called===1;`,
		"sync suppression":                `const s=new DisposableStack();const first={},second={};s.defer(()=>{throw first});s.defer(()=>{throw second});let error:any;try{s.dispose()}catch(e){error=e}export const result=error instanceof SuppressedError&&error.error===first&&error.suppressed===second&&s.disposed;`,
		"reentrant disposal":              `const s=new DisposableStack();let calls=0;s.defer(()=>{calls++;s.dispose()});s.dispose();export const result=calls===1&&s.disposed;`,
		"sync descriptor":                 `const p=DisposableStack.prototype;const d=Object.getOwnPropertyDescriptor(p,"disposed")!;const f=Object.getOwnPropertyDescriptor(p,"dispose")!;export const result=p[Symbol.dispose]===p.dispose&&!d.enumerable&&d.configurable&&d.set===undefined&&!f.enumerable&&f.writable&&f.configurable&&p.dispose.length===0&&p.use.length===1&&p.adopt.length===2&&p.defer.length===1&&p.move.length===0&&Object.prototype.toString.call(new DisposableStack())==="[object DisposableStack]";`,
		"sync brand and validation":       `let errors=0;const s=new DisposableStack();for(const f of [()=>DisposableStack.prototype.dispose.call({}),()=>DisposableStack.prototype.use.call(new AsyncDisposableStack(),null),()=>(DisposableStack as unknown as ()=>DisposableStack)(),()=>DisposableStack.prototype.dispose.call(new Proxy(s,{})),()=>s.use(1 as any),()=>s.adopt(1,null as any),()=>s.defer(null as any)]){try{f()}catch(e){if(e instanceof TypeError)errors++}}export const result=errors===7;`,
		"async order":                     `const log:string[]=[];const s=new AsyncDisposableStack();s.use({async [Symbol.asyncDispose](){log.push("use");await Promise.resolve();log.push("used")}});s.adopt(42,async v=>{log.push(String(v));await Promise.resolve();log.push("adopted")});s.defer(async()=>{log.push("defer");await Promise.resolve();log.push("deferred")});const p=s.disposeAsync();const disposed=s.disposed;await p;await s.disposeAsync();export const result=disposed&&log.join(",")==="defer,deferred,42,adopted,use,used";`,
		"async fallback ignores thenable": `let touched=0;const s=new AsyncDisposableStack();s.use({[Symbol.dispose](){return {get then(){touched++;throw new Error("must not adopt")}}}});await s.disposeAsync();export const result=touched===0;`,
		"async null fallback":             `let calls=0;const s=new AsyncDisposableStack();s.use({[Symbol.asyncDispose]:null,[Symbol.dispose](){calls++}} as any);await s.disposeAsync();export const result=calls===1;`,
		"async suppression":               `const s=new AsyncDisposableStack();const first={},second={};s.defer(async()=>{throw first});s.defer(async()=>{throw second});let error:any;try{await s.disposeAsync()}catch(e){error=e}export const result=error instanceof SuppressedError&&error.error===first&&error.suppressed===second&&s.disposed;`,
		"async move and brand":            `let calls=0;const s=new AsyncDisposableStack();s.defer(()=>{calls++});const moved=s.move();await s.disposeAsync();await moved[Symbol.asyncDispose]();let sync=false;let pending:Promise<void>|undefined;try{pending=AsyncDisposableStack.prototype.disposeAsync.call({})}catch{sync=true}let rejected=false;try{await pending}catch(e){rejected=e instanceof TypeError}export const result=s.disposed&&moved.disposed&&calls===1&&!sync&&rejected&&AsyncDisposableStack.prototype[Symbol.asyncDispose]===AsyncDisposableStack.prototype.disposeAsync;`,
		"using stack":                     `const log:number[]=[];{using s=new DisposableStack();s.defer(()=>log.push(42))}export const result=log[0]===42;`,
		"await using stack":               `let value=0;{await using s=new AsyncDisposableStack();s.defer(async()=>{await Promise.resolve();value=42})}export const result=Number(value)===42;`,
	} {
		t.Run(name, func(t *testing.T) {
			p, ds, err := goya.Compile(context.Background(), goya.Project{Files: map[string]string{"/main.ts": source}}, nil, goya.CompileOptions{"lib": []string{"esnext"}})
			if err != nil || p == nil {
				t.Fatalf("compile stacks: %v %+v", err, ds)
			}
			value, err := goya.New().RunProgram(p)
			if err != nil {
				t.Fatal(err)
			}
			if promise, ok := value.Export().(*goya.Promise); ok {
				if promise.State() != goya.PromiseStateFulfilled {
					t.Fatalf("stack initialization: %v %v", promise.State(), promise.Result())
				}
				value = promise.Result()
			}
			if !hostObject(t, value).Get("result").ToBoolean() {
				t.Fatal("disposable stack semantics")
			}
		})
	}
}
