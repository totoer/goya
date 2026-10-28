package goya_test

import (
	"context"
	"testing"

	"github.com/totoer/goya"
)

func compileModules(t *testing.T, files map[string]string, environment *goya.Environment) *goya.Program {
	t.Helper()
	program, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, Files: files}, environment, nil)
	if err != nil || program == nil {
		t.Fatalf("compile modules: %v %+v", err, ds)
	}
	return program
}

func TestTypeScriptModuleImportShorthandProperties(t *testing.T) {
	for _, tc := range []struct {
		name     string
		source   string
		expected map[string]int64
	}{
		{
			name:     "named import with unused function",
			source:   `import {value, read} from "./dependency"; export const result = {value};`,
			expected: map[string]int64{"value": 42},
		},
		{
			name:     "aliased import",
			source:   `import {value as renamed} from "./dependency"; export const result = {renamed};`,
			expected: map[string]int64{"renamed": 42},
		},
		{
			name:     "default import",
			source:   `import value from "./dependency"; export const result = {value};`,
			expected: map[string]int64{"value": 42},
		},
		{
			name:     "namespace import",
			source:   `import * as dependency from "./dependency"; export const result = {dependency}.dependency;`,
			expected: map[string]int64{"value": 42},
		},
		{
			name:     "shadowed import",
			source:   `import {value} from "./dependency"; function make(value: number) {return {value};} export const result = make(7);`,
			expected: map[string]int64{"value": 7},
		},
		{
			name:     "live import with snapshot properties",
			source:   `import {value, increment} from "./dependency"; const before = {value}; increment(); const after = {value}; export const result = {before: before.value, after: after.value};`,
			expected: map[string]int64{"before": 42, "after": 43},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := compileModules(t, map[string]string{
				"/main.ts":       tc.source,
				"/dependency.ts": `export let value = 42; export default 42; export function read() {return value;} export function increment() {value++;}`,
			}, nil)
			value, err := goya.New().RunProgram(program)
			if err != nil {
				t.Fatal(err)
			}
			result := hostObject(t, hostObject(t, value).Get("result"))
			for name, expected := range tc.expected {
				if actual := result.Get(name).ToInteger(); actual != expected {
					t.Fatalf("result.%s = %d, want %d", name, actual, expected)
				}
			}
		})
	}
}

func TestTypeScriptModulesExecuteLiveImports(t *testing.T) {
	program := compileModules(t, map[string]string{
		"/main.ts":     `import {value, increment, unbound} from "./counter.js"; import * as counter from "./counter.js"; import {renamed} from "./reexport.js"; const initial = value; increment(); export const result = initial === 0 && value === 1 && counter.value === 1 && renamed === 1 && unbound(); export {counter};`,
		"/counter.ts":  `export let value: number = 0; export function increment(): void {value++;} export function unbound(this: unknown): boolean {return this === undefined;}`,
		"/reexport.ts": `export {value as renamed} from "./counter.js";`,
	}, nil)
	r := goya.New()
	result, err := r.RunProgram(program)
	if err != nil {
		t.Fatal(err)
	}
	namespace := hostObject(t, result)
	if !namespace.Get("result").ToBoolean() {
		t.Fatal("imports did not retain live bindings")
	}
	counter := hostObject(t, namespace.Get("counter"))
	if counter.Get("value").ToInteger() != 1 {
		t.Fatal("namespace value")
	}
	again, err := r.RunProgram(program)
	if err != nil || again != result || counter.Get("value").ToInteger() != 1 {
		t.Fatal("module initialization repeated", err)
	}
}

func TestTypeScriptModuleCyclesAndInstantiation(t *testing.T) {
	program := compileModules(t, map[string]string{
		"/main.ts": `import {getA, initialized, value} from "./a.js"; export const result = initialized && getA() === 3 && value === 3;`,
		"/a.ts":    `import {callA} from "./b.js"; export function getA(): number {return 3;} export var value: number = callA(); export const initialized = true;`,
		"/b.ts":    `import {getA} from "./a.js"; export function callA(): number {return getA();}`,
	}, nil)
	value, err := goya.New().RunProgram(program)
	if err != nil || !hostObject(t, value).Get("result").ToBoolean() {
		t.Fatalf("cyclic hoisted functions: %v", err)
	}
	bad := compileModules(t, map[string]string{
		"/main.ts": `import {value} from "./a.js"; export const result = value;`,
		"/a.ts":    `import {read} from "./b.js"; export let value: number = read();`,
		"/b.ts":    `import {value} from "./a.js"; export function read(): number {return value;}`,
	}, nil)
	r := goya.New()
	if _, err := r.RunProgram(bad); err == nil {
		t.Fatal("cyclic lexical TDZ was lost")
	}
	if _, err := r.RunProgram(bad); err == nil {
		t.Fatal("failed module initialization was forgotten")
	}
}

func TestTypeScriptModuleNamespaceSemantics(t *testing.T) {
	program := compileModules(t, map[string]string{
		"/main.ts":   `import * as namespace from "./values.js"; const descriptor = Object.getOwnPropertyDescriptor(namespace,"value")!; const proto = Object.getPrototypeOf(namespace); const before = descriptor.value; namespace.increment(); const after = Object.getOwnPropertyDescriptor(namespace,"value")!.value; let failed = false; try {(namespace as any).value = 42;} catch {failed = true;} export const result = descriptor.writable === true && descriptor.enumerable === true && descriptor.configurable === false && descriptor.get === undefined && before === 0 && after === 1 && proto === null && !Object.isExtensible(namespace) && failed && Object.prototype.toString.call(namespace) === "[object Module]";`,
		"/values.ts": `export let value = 0; export function increment(): void {value++;}`,
	}, nil)
	value, err := goya.New().RunProgram(program)
	if err != nil || !hostObject(t, value).Get("result").ToBoolean() {
		t.Fatalf("namespace descriptors: %v", err)
	}
}

func TestTypeScriptModuleFactoryDoesNotExposeArguments(t *testing.T) {
	program := compileModules(t, map[string]string{"/main.ts": "// @ts-ignore\nexport const kind = typeof arguments;\nexport const topThis = this;"}, nil)
	value, err := goya.New().RunProgram(program)
	if err != nil {
		t.Fatal(err)
	}
	namespace := hostObject(t, value)
	if namespace.Get("kind").String() != "undefined" || !goya.IsUndefined(namespace.Get("topThis")) {
		t.Fatal("module exposed its private factory frame")
	}
}

func TestTypeScriptHostModulePreflight(t *testing.T) {
	calls := 0
	registry := goya.NewRegistry()
	if err := registry.RegisterModule("host:math", map[string]any{"add": func(a, b float64) float64 { calls++; return a + b }}); err != nil {
		t.Fatal(err)
	}
	environment := freezeHost(t, registry)
	program := compileModules(t, map[string]string{"/main.ts": `import {add} from "host:math"; export const result = add(1,2);`}, environment)
	r := goya.New()
	if _, err := r.RunProgram(program); err == nil || calls != 0 {
		t.Fatal("missing host module executed initializers")
	}
	if err := environment.Bind(r); err != nil {
		t.Fatal(err)
	}
	value, err := r.RunProgram(program)
	if err != nil || hostObject(t, value).Get("result").ToInteger() != 3 || calls != 1 {
		t.Fatalf("host module: %v", err)
	}
}

func TestTypeScriptModuleDefaultAndStarExports(t *testing.T) {
	program := compileModules(t, map[string]string{
		"/main.ts":     `import f from "./function.js"; import C from "./class.js"; import arrow from "./arrow.js"; import * as star from "./star.js"; import * as same from "./same.js"; export const result = f() === 3 && f.name === "default" && C.name === "default" && C.seen === "default" && new C().value === 4 && arrow.name === "default" && arrow() === 5 && star.value === 6 && !("default" in star) && same.shared === 6;`,
		"/function.ts": `export default function(): number {return 3;}`,
		"/class.ts":    `export default class {static seen: string; static {this.seen = this.name;} value = 4;}`,
		"/arrow.ts":    `export default (): number => 5;`,
		"/values.ts":   `export const value = 6; export {value as alternate}; export default 7;`,
		"/star.ts":     `export * from "./values.js";`,
		"/one.ts":      `export {value as shared} from "./values.js";`,
		"/two.ts":      `export {alternate as shared} from "./values.js";`,
		"/same.ts":     `export * from "./one.js"; export * from "./two.js";`,
	}, nil)
	value, err := goya.New().RunProgram(program)
	if err != nil || !hostObject(t, value).Get("result").ToBoolean() {
		t.Fatalf("default/star exports: %v", err)
	}
}

func TestTypeScriptModuleFailureDoesNotRepeatEffects(t *testing.T) {
	calls := 0
	registry := goya.NewRegistry()
	registerHost(t, registry, "notify", func() { calls++ })
	environment := freezeHost(t, registry)
	program := compileModules(t, map[string]string{
		"/main.ts":   `import "./failed.js"; notify(); export {};`,
		"/failed.ts": `notify(); throw new Error("failed"); export {};`,
	}, environment)
	r := goya.New()
	if err := environment.Bind(r); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := r.RunProgram(program); err == nil {
			t.Fatal("module failure was lost")
		}
	}
	if calls != 1 {
		t.Fatalf("failed dependency ran again or its parent ran: %d", calls)
	}
}

func TestTypeScriptModuleDependencyEvaluationOrder(t *testing.T) {
	program := compileModules(t, map[string]string{
		"/main.ts":   `export {one} from "./one.js"; import "./two.js"; import {events} from "./events.js"; events.push("main"); export const result = events.join(",") === "one,two,main";`,
		"/one.ts":    `import {events} from "./events.js"; events.push("one"); export const one = 1;`,
		"/two.ts":    `import {events} from "./events.js"; events.push("two"); export {};`,
		"/events.ts": `export const events: string[] = [];`,
	}, nil)
	value, err := goya.New().RunProgram(program)
	if err != nil || !hostObject(t, value).Get("result").ToBoolean() {
		t.Fatalf("module request ordering: %v", err)
	}
}

func TestTypeScriptStarExportAmbiguityPropagates(t *testing.T) {
	program := compileModules(t, map[string]string{
		"/main.ts":      `import * as namespace from "./outer.js"; export const result = !("value" in namespace);`,
		"/one.ts":       `export const value = 1;`,
		"/two.ts":       `export const value = 2;`,
		"/ambiguous.ts": "export * from './one.js';\n// @ts-ignore preserve ordinary ESM ambiguity semantics\nexport * from './two.js';",
		"/outer.ts":     "export * from './ambiguous.js';\n// @ts-ignore preserve ordinary ESM ambiguity semantics\nexport * from './one.js';",
	}, nil)
	value, err := goya.New().RunProgram(program)
	if err != nil || !hostObject(t, value).Get("result").ToBoolean() {
		t.Fatalf("ambiguous star export survived: %v", err)
	}
}

func TestTypeScriptTypeOnlyModuleHasNoExecutionArtifact(t *testing.T) {
	program := compileModules(t, map[string]string{
		"/main.ts": `import type {Data} from "./data.js"; const value: Data = {value:3}; export const result = value.value === 3;`,
		"/data.ts": `export interface Data {value: number} import.meta; throw new Error("must not execute");`,
	}, nil)
	value, err := goya.New().RunProgram(program)
	if err != nil || !hostObject(t, value).Get("result").ToBoolean() {
		t.Fatalf("type-only dependency was lowered or evaluated: %v", err)
	}
}

func TestTypeScriptAmbientModuleAvailabilityBeforeEffects(t *testing.T) {
	calls := 0
	registry := goya.NewRegistry()
	registerHost(t, registry, "notify", func() { calls++ })
	if err := registry.DeclareTypes("/ambient.d.ts", `declare module "host:absent" {export const value: number;} declare module "host:types" {export interface Data {value: number}}`); err != nil {
		t.Fatal(err)
	}
	environment := freezeHost(t, registry)
	program := compileModules(t, map[string]string{"/main.ts": `import {value} from "host:absent"; notify(); export const result = value;`}, environment)
	r := goya.New()
	if err := environment.Bind(r); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunProgram(program); err == nil || calls != 0 {
		t.Fatal("missing ambient implementation was detected after effects")
	}
	types := compileModules(t, map[string]string{"/main.ts": `import type {Data} from "host:types"; const value: Data = {value:3}; export const result = value.value === 3;`}, environment)
	value, err := goya.New().RunProgram(types)
	if err != nil || !hostObject(t, value).Get("result").ToBoolean() {
		t.Fatalf("type-only ambient module required implementation: %v", err)
	}
}

func TestTypeScriptHostModuleContractsAndExtraExports(t *testing.T) {
	registry := goya.NewRegistry()
	if err := registry.RegisterModule("host:math", map[string]any{"default": func(a, b float64) float64 { return a + b }, "odd-name": float64(2)}); err != nil {
		t.Fatal(err)
	}
	environment := freezeHost(t, registry)
	program := compileModules(t, map[string]string{"/main.ts": `import add, {"odd-name" as number} from "host:math"; export const result = add(number,1);`}, environment)
	compatible := goya.NewRegistry()
	if err := compatible.RegisterModule("host:math", map[string]any{"default": func(a, b float64) float64 { return a + b + 10 }, "odd-name": float64(3), "extra": true}); err != nil {
		t.Fatal(err)
	}
	r := goya.New()
	if err := freezeHost(t, compatible).Bind(r); err != nil {
		t.Fatal(err)
	}
	value, err := r.RunProgram(program)
	if err != nil || hostObject(t, value).Get("result").ToInteger() != 14 {
		t.Fatalf("compatible module implementation rejected: %v", err)
	}
	wrong := goya.NewRegistry()
	if err := wrong.RegisterModule("host:math", map[string]any{"default": func(a, b string) string { return a + b }, "odd-name": float64(2)}); err != nil {
		t.Fatal(err)
	}
	bad := goya.New()
	if err := freezeHost(t, wrong).Bind(bad); err != nil {
		t.Fatal(err)
	}
	if _, err := bad.RunProgram(program); err == nil {
		t.Fatal("incompatible module export executed")
	}
}

func TestTypeScriptCyclicVarIsInitializedToUndefined(t *testing.T) {
	program := compileModules(t, map[string]string{
		"/main.ts": `import {result} from "./a.js"; export {result};`,
		"/a.ts":    `import {seen} from "./b.js"; export var value = 1; export const result = seen === undefined;`,
		"/b.ts":    `import {value} from "./a.js"; export const seen = value;`,
	}, nil)
	value, err := goya.New().RunProgram(program)
	if err != nil || !hostObject(t, value).Get("result").ToBoolean() {
		t.Fatalf("cyclic var lost initial undefined value: %v", err)
	}
}

func TestTypeScriptNamespaceNumericKeysAndForeignSet(t *testing.T) {
	program := compileModules(t, map[string]string{
		"/main.ts":   `import * as namespace from "./values.js"; const receiver = {}; export const result = Object.keys(namespace).join(",") === "10,2" && Reflect.set(namespace,"2",42,receiver) === false && !("2" in receiver);`,
		"/values.ts": `const two = 2, ten = 10; export {two as "2", ten as "10"};`,
	}, nil)
	value, err := goya.New().RunProgram(program)
	if err != nil || !hostObject(t, value).Get("result").ToBoolean() {
		t.Fatalf("namespace numeric key semantics: %v", err)
	}
}
