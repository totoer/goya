package goya_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/totoer/goya"
)

func compileTypeScript(t *testing.T, source string, environment *goya.Environment) *goya.Program {
	t.Helper()
	program, ds, err := goya.CompileProject(context.Background(), goya.Project{Files: map[string]string{"/main.ts": source}}, environment, nil)
	if err != nil || program == nil || goya.HasTypeErrors(ds) {
		t.Fatalf("compile: %v %+v", err, ds)
	}
	return program
}

func TestTypeScriptExecutionOperations(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"numeric", `const n: number = 0x10 + 0b10 + 0o10 + 1_000 + 1e2; n === 1126 && 12n * 2n === 24n;`},
		{"strings", `const text: string = "😀\ud800"; text.length === 3 && text.charCodeAt(2) === 0xd800 && /a+/i.test("AA");`},
		{"operators", `let n: number = 3; n += 4; n <<= 1; const a = n++ + ++n; (n === 16) && (a === 30) && (n > 1 ? true : false) && ((~0 & 3) === 3);`},
		{"short-circuit", `let count = 0; function next(): number { return ++count; } let v: number | undefined; const a = v ?? next(); const b = false && next(); const c = true || next(); count === 1 && a === 1 && b === false && c === true;`},
		{"objects-arrays", `const name = "value"; const o = {name, ["a"]: 3, ...{b: 4}, method(n: number) {return this.a + n;}}; const a = [1, ...[2, 3]]; o.name === "value" && o.method(a[1]) === 5 && o.b === 4;`},
		{"templates", "const n: number = 3; `a${n}b${n + 1}c` === 'a3b4c';"},
		{"loops", `let total: number = 0; outer: for (let i = 0; i < 5; i++) { if (i === 1) continue; if (i === 4) break outer; total += i; } let n = 0; do {n++;} while (n < 2); while(n < 3) {n++;} total === 5 && n === 3;`},
		{"for-in-of", `let sum: number = 0; for (const n of [1,2,3]) sum += n; for (const key in {a:1,b:2}) sum += key.length; sum === 8;`},
		{"switch-finally", `let result: string = ""; const n: number = Number("2"); switch (n) {case 1: result = "one"; break; case 2: result = "two"; default: result += "!";} try {throw "caught";} catch (error) {result += String(error);} finally {result += "end";} result === "two!caughtend";`},
		{"closures", `function make(seed: number): () => number { let value: number = seed; return () => ++value; } const f = make(1); f() === 2 && f() === 3;`},
		{"function-hoisting", `const n: number = before(); function before(): number { return value(); function value(): number {return 4;} } n === 4;`},
		{"var-hoisting", `function f(): boolean {const initial = typeof n; if (true) {var n: number | undefined = 3;} return initial === "undefined" && n === 3;} f();`},
		{"loop-captures", `const fns: (() => number)[] = []; for (let i = 0; i < 3; i++) { fns.push(() => i); } fns[0]() === 0 && fns[1]() === 1 && fns[2]() === 2;`},
		{"tdz", `let failed: boolean = false; const read = () => value; try {read();} catch {failed = true;} let value: number = 3; failed && read() === 3;`},
		{"destructuring", `const {a: first = 1, ...rest} = {a: undefined, b: 2}; const [second, , ...tail] = [3,4,5,6]; let x: number = 0; let y: number = 0; [x,y] = [7,8]; first === 1 && rest.b === 2 && second === 3 && tail[1] === 6 && x === 7 && y === 8;`},
		{"parameters", `function f({a}: {a: number}, n: number = 2, ...rest: number[]): number {return a + n + rest.reduce((a,b)=>a+b,0);} f({a:1},undefined,...[3,4]) === 10;`},
		{"assertions", `interface Data {value: number} type Alias = Data; const a = {value: 3} as Alias; const b = <number>a!.value; const c = a satisfies Data; b === 3 && c === a;`},
		{"side-effect-order", `const events: string[] = []; let value: number = 1; const o = {get x(): number {events.push("get"); return value;}, set x(n: number) {events.push("set"); value = n;}}; function target() {events.push("target"); return o;} function key(): "x" {events.push("key"); return "x";} function right(): number {events.push("right"); return 2;} target()[key()] += right(); events.join(",") === "target,key,get,right,set" && value === 3;`},
		{"optional-chain", `const object: {value?: {n: number; method(arg?: number): number}} = {}; let calls = 0; const missing = object.value?.method(++calls as never); const indexed = object.value?.["n"]; missing === undefined && indexed === undefined && calls === 0;`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := compileTypeScript(t, tc.source, nil)
			result, err := goya.New().RunProgram(program)
			if err != nil || !result.ToBoolean() {
				t.Fatalf("execute: %v %v", result, err)
			}
		})
	}
}

func TestTypeScriptCompileAndBindingGate(t *testing.T) {
	calls := 0
	registry := goya.NewRegistry()
	registerHost(t, registry, "add", func(a, b float64) float64 { calls++; return a + b })
	registerHost(t, registry, "unused", true)
	environment := freezeHost(t, registry)
	program := compileTypeScript(t, `add(1, 2);`, environment)
	r := goya.New()
	if _, err := r.RunProgram(program); err == nil || calls != 0 {
		t.Fatal("missing API executed code")
	}
	if err := environment.Bind(r); err != nil {
		t.Fatal(err)
	}
	result, err := r.RunProgram(program)
	if err != nil || result.ToFloat() != 3 || calls != 1 {
		t.Fatalf("checked host call: %v %v", result, err)
	}
	bad := goya.NewRegistry()
	registerHost(t, bad, "add", func(a, b string) string { calls++; return a + b })
	wrong := goya.New()
	if err := freezeHost(t, bad).Bind(wrong); err != nil {
		t.Fatal(err)
	}
	var compatibility *goya.BindingCompatibilityError
	if _, err := wrong.RunProgram(program); !errors.As(err, &compatibility) || calls != 1 {
		t.Fatal("incompatible host contract reached instructions")
	}
	other := goya.NewRegistry()
	registerHost(t, other, "add", func(a, b float64) float64 { return a + b + 10 })
	registerHost(t, other, "extra", true)
	otherRuntime := goya.New()
	if err := freezeHost(t, other).Bind(otherRuntime); err != nil {
		t.Fatal(err)
	}
	result, err = otherRuntime.RunProgram(program)
	if err != nil || result.ToFloat() != 13 {
		t.Fatalf("compatible implementations/additional bindings rejected: %v %v", result, err)
	}
	invalid, ds, err := goya.CompileProject(context.Background(), goya.Project{Files: map[string]string{"/main.ts": `add(1,2); const n: number = "wrong";`}}, environment, nil)
	if err != nil || invalid != nil || !goya.HasTypeErrors(ds) || calls != 1 {
		t.Fatal("compile error created executable program")
	}
	anyProgram := compileTypeScript(t, `add("wrong" as any, 2);`, environment)
	if _, err := r.RunProgram(anyProgram); err == nil || calls != 1 {
		t.Fatal("any bypassed runtime boundary")
	}
}

func TestTypeScriptProgramCanRunConcurrently(t *testing.T) {
	program := compileTypeScript(t, `function square(n: number): number {return n*n;} square(7);`, nil)
	var group sync.WaitGroup
	for range 5 {
		group.Go(func() {
			result, err := goya.New().RunProgram(program)
			if err != nil || result.ToInteger() != 49 {
				t.Errorf("shared program: %v %v", result, err)
			}
		})
	}
	group.Wait()
}

func TestTypeScriptErasurePreservesExceptionSource(t *testing.T) {
	source := "interface Data {value: number}\ntype Alias = Data;\nfunction fail(value: Alias): never {\n  throw new Error(String(value.value));\n}\nfail({value: 42});"
	program := compileTypeScript(t, source, nil)
	_, err := goya.New().RunProgram(program)
	var exception *goya.Exception
	if !errors.As(err, &exception) {
		t.Fatalf("expected exception, got %v", err)
	}
	frames := exception.Stack()
	if len(frames) == 0 || frames[0].Position().Filename != "/main.ts" || frames[0].Position().Line != 4 {
		t.Fatalf("wrong TS source stack: %v", err)
	}
}
