package goya_test

import (
	"context"
	"github.com/totoer/goya"
	"testing"
)

func TestTypeScriptFunctionConstructorsAreDisabled(t *testing.T) {
	for _, constructor := range []string{`(globalThis as any).Function`, `(function(){}).constructor`, `(async function(){}).constructor`, `(function*(){}).constructor`, `(async function*(){}).constructor`} {
		for _, call := range []string{`C(arg)`, `new C(arg)`, `C.call(null,arg)`, `C.apply(null,[arg])`, `C.bind(null,arg)()`, `Reflect.construct(C,[arg])`, `new Proxy(C,{})(arg)`} {
			source := `let effects=0;const arg={toString(){effects++;return "effects++"}};const C:any=` + constructor + `;let rejected=false;try{` + call + `}catch(e){rejected=e instanceof EvalError}rejected&&effects===0;`
			p := compileTypeScript(t, source, nil)
			value, err := goya.New().RunProgram(p)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("constructor %s via %s: %v %v", constructor, call, value, err)
			}
		}
	}
}

func TestTypeScriptFunctionConstructorDiagnostic(t *testing.T) {
	for _, source := range []string{`Function("return 1");`, `new Function("return 1");`, `globalThis.Function("return 1");`} {
		project := goya.Project{Files: map[string]string{"/main.ts": source}}
		check, err := goya.Check(context.Background(), project, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		p, ds, err := goya.Compile(context.Background(), project, nil, nil)
		if err != nil || p != nil || len(check) != 1 || len(ds) != 1 || check[0].Namespace != "goya" || check[0].Code != 1114 || ds[0].Code != check[0].Code || check[0].Range == nil || ds[0].Range == nil || *check[0].Range != *ds[0].Range {
			t.Fatalf("profile diagnostics %v %v %v", err, check, ds)
		}
	}
	p := compileTypeScript(t, `function outer(){function Function(s:string){return s.length}return Function("abc")}outer()===3;`, nil)
	value, err := goya.New().RunProgram(p)
	if err != nil || !value.ToBoolean() {
		t.Fatalf("shadowed Function %v %v", value, err)
	}
}

func TestTypeScriptOrdinaryFunctionsWithoutEval(t *testing.T) {
	for _, source := range []string{
		`function f(x:number){return ()=>arguments[0]+x}f(21)()===42;`,
		`class Value{#n=40;read(){return this.#n+2}}new Value().read()===42;`,
		`function f(this:{n:number},x:number){return this.n+x};f.call({n:40},2)===42&&f.apply({n:40},[2])===42&&f.bind({n:40},2)()===42&&f instanceof Function;`,
		`function* f(){yield 42}f().next().value===42;`,
	} {
		p := compileTypeScript(t, source, nil)
		value, err := goya.New().RunProgram(p)
		if err != nil || !value.ToBoolean() {
			t.Fatalf("function semantics %v %v", value, err)
		}
	}
	p := compileTypeScript(t, `export const result=(async()=>{async function* f(){yield await Promise.resolve(42)}return (await f().next()).value===42})();`, nil)
	value, err := goya.New().RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	promise := modulePromise(t, hostObject(t, value).Get("result"))
	if promise.State() != goya.PromiseStateFulfilled || !promise.Result().ToBoolean() {
		t.Fatalf("async generator %v %v", promise.State(), promise.Result())
	}
}

func TestTypeScriptFunctionToStringProxy(t *testing.T) {
	for name, source := range map[string]string{
		"revoked callable": `
			const handle = Proxy.revocable(() => 1, {});
			handle.revoke();
			Function.prototype.toString.call(handle.proxy) === "function () { [native code] }";
		`,
		"nested callable": `
			const handle = Proxy.revocable(() => 1, {});
			const outer = new Proxy(handle.proxy, {});
			const before = Function.prototype.toString.call(outer);
			handle.revoke();
			before === "function () { [native code] }" && Function.prototype.toString.call(outer) === before;
		`,
		"revoked non-callable": `
			const handle = Proxy.revocable({}, {});
			handle.revoke();
			let caught = false;
			try { Function.prototype.toString.call(handle.proxy); }
			catch (e) { caught = e instanceof TypeError; }
			caught;
		`,
	} {
		t.Run(name, func(t *testing.T) {
			p := compileTypeScript(t, source, nil)
			value, err := goya.New().RunProgram(p)
			if err != nil {
				t.Fatal(err)
			}
			if !value.ToBoolean() {
				t.Fatalf("unexpected result: %v", value)
			}
		})
	}
}
