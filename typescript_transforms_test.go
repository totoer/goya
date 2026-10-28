package goya_test

import (
	"context"
	"testing"

	"github.com/totoer/goya"
)

func TestTypeScriptRuntimeTransforms(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"enum", `enum Color {Red, Green = 4, Blue = Green + 2, Text = "text"} Color.Red === 0 && Color[Color.Blue] === "Blue" && Color.Text === "text" && !("text" in Color);`},
		{"computed-enum", `let calls = 0; function next(): number {return ++calls;} enum Value {First = next(), Second = next()} Value.First === 1 && Value.Second === 2 && calls === 2;`},
		{"const-enum", `const enum Flag {First = 1 << 4, Second = First | 2} const n: number = Flag.Second; n === 18;`},
		{"namespace", `namespace Values {export const answer = 40;} namespace Values {export function add(n: number): number {return answer + n;}} Values.add(2) === 42;`},
		{"namespace-merging", `function value(): number {return 3;} namespace value {export const label = "label";} enum NumberValue {One = 1} namespace NumberValue {export const other = 2;} value() === 3 && value.label === "label" && NumberValue.One + NumberValue.other === 3;`},
		{"nested-namespace", `namespace Outer.Inner {export const value = 3;} Outer.Inner.value === 3;`},
		{"parameter-properties", `class Point {constructor(public x: number, readonly y: number = 2) {}} const p = new Point(3); p.x === 3 && p.y === 2;`},
		{"derived-parameter-properties", `class Base {value = 1;} class Derived extends Base {constructor(public other: number) {super();}} const p = new Derived(3); p.value === 1 && p.other === 3;`},
		{"class-initialization", `const events: string[] = []; class Base {base = (events.push("base-field"), 1); constructor() {events.push("base-body");}} class Derived extends Base {static first = (events.push("static-field"), 1); static {events.push("static-block");} field = (events.push("derived-field"), 2); constructor() {super();events.push("derived-body");}} new Derived(); events.join(",") === "static-field,static-block,base-field,base-body,derived-field,derived-body";`},
		{"private-members", `class Private {#field = 3; static #static = 4; get value(): number {return this.#field;} static get value(): number {return this.#static;} static has(v: object): boolean {return #field in v;}} const value = new Private(); value.value === 3 && Private.value === 4 && Private.has(value) && !Private.has({});`},
		{"super-and-brand", `class Base {value(): number {return 2;}} class Derived extends Base {#field = 3; value(): number {return super.value() + this.#field;} } const p = new Derived(); let failed = false; try {p.value.call({});} catch {failed = true;} p.value() === 5 && failed;`},
		{"class-expression", `const C = class Named {static value = 3; field = Named.value;}; new C().field === 3;`},
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

func TestTypeScriptClassFieldOption(t *testing.T) {
	source := "const values: number[] = []; class Base {set value(n: number) {values.push(n);}} class Derived extends Base {\n// @ts-expect-error TS permits suppressing the property/accessor conflict.\nvalue = 3; } new Derived(); values.length;"
	for _, define := range []bool{true, false} {
		program, ds, err := goya.CompileProject(context.Background(), goya.Project{Files: map[string]string{"/main.ts": source}}, nil, goya.CompileOptions{"useDefineForClassFields": define})
		if err != nil || program == nil {
			t.Fatalf("define=%v: %v %+v", define, err, ds)
		}
		result, err := goya.New().RunProgram(program)
		want := int64(1)
		if define {
			want = 0
		}
		if err != nil || result.ToInteger() != want {
			t.Fatalf("define=%v: %v %v", define, result, err)
		}
	}
}

func TestTypeScriptImportedEnumsAndConstEnumOptions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		options   goya.CompileOptions
		preserved bool
	}{
		{"default", nil, false}, {"preserve", goya.CompileOptions{"preserveConstEnums": true}, true}, {"isolated", goya.CompileOptions{"isolatedModules": true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{
				"/main.ts":   `import {Color, Flag} from "./values.js"; import * as values from "./values.js"; export const result = Color[Color.Blue] === "Blue" && Color.Text === "text" && Flag.Mask === 18; export const preserved = Object.hasOwn(values,"Flag");`,
				"/values.ts": `export enum Color {Red, Blue = 4, Text = "text"} export const enum Flag {Bit = 1 << 4, Mask = Bit | 2} export const marker = true;`,
			}}, nil, tc.options)
			if err != nil || program == nil {
				t.Fatalf("compile enum options: %v %+v", err, ds)
			}
			value, err := goya.New().RunProgram(program)
			if err != nil {
				t.Fatal(err)
			}
			namespace := hostObject(t, value)
			if !namespace.Get("result").ToBoolean() || namespace.Get("preserved").ToBoolean() != tc.preserved {
				t.Fatal("enum emit/inlining policy differs from checking options")
			}
		})
	}
}
