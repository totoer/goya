package goya_test

import (
	"context"
	"slices"
	"testing"

	"github.com/totoer/goya"
)

func TestTypeScriptStandardDecoratorInitializersAndContext(t *testing.T) {
	program := compileTypeScript(t, `
const events: string[] = [];
let saved: (() => void) | undefined;
function method(value: any, context: ClassMethodDecoratorContext<any, any>): any {
  events.push("decorate-method:" + String(context.name));
  context.addInitializer(function() { events.push("method-extra"); });
  saved = () => context.addInitializer(function() {});
  return function(this: any, ...args: any[]) { return value.apply(this, args) + 1; };
}
function field(value: undefined, context: ClassFieldDecoratorContext<any, number>) {
  events.push("decorate-field:" + String(context.name));
  context.addInitializer(function() { events.push("field-extra:" + context.access.get(this)); });
  return function(this: any, initial: number) { events.push("field-init:" + initial); return initial + 1; };
}
class Example { @method call(): number {return 3;} @field value = 4; }
const example = new Example();
let denied = false; try { saved!(); } catch { denied = true; }
events.join(",") === "decorate-method:call,decorate-field:value,method-extra,field-init:4,field-extra:5" && example.call() === 4 && example.value === 5 && denied;
`, nil)
	result, err := goya.New().RunProgram(program)
	if err != nil || !result.ToBoolean() {
		t.Fatalf("decorator context/initializers: %v %v", result, err)
	}
}

func TestTypeScriptDecoratorApplicationOrderAndAccessors(t *testing.T) {
	program := compileTypeScript(t, `
const events: string[] = [];
function decorator(label: string): any { events.push("evaluate:" + label); return function(value: any, context: any): any { events.push("apply:" + label); if (context.kind === "accessor") return {get(this: any) {return value.get.call(this) + 1;}, set: value.set, init(initial: number) {return initial + 2;}}; }; }
class Example { @decorator("first") @decorator("second") accessor value = 3; }
const example = new Example();
events.join(",") === "evaluate:first,evaluate:second,apply:second,apply:first" && example.value === 9;
`, nil)
	result, err := goya.New().RunProgram(program)
	if err != nil || !result.ToBoolean() {
		t.Fatalf("accessor decorator order: %v %v", result, err)
	}
}

func TestTypeScriptStandardDecoratorMetadataAndReplacement(t *testing.T) {
	program, ds, err := goya.CompileProject(context.Background(), goya.Project{Files: map[string]string{"/main.ts": `
function metadata(value: any, context: ClassDecoratorContext): any { context.metadata.answer = 42; return class extends value {replaced = true;}; }
@metadata class Example {}
export const result = (new Example() as any).replaced === true && (Example[Symbol.metadata] as any).answer === 42;
`}}, nil, goya.CompileOptions{"lib": []string{"es2025", "esnext.decorators"}})
	if err != nil || program == nil {
		t.Fatalf("metadata compile: %v %+v", err, ds)
	}
	result, err := goya.New().RunProgram(program)
	if err != nil || !hostObject(t, result).Get("result").ToBoolean() {
		t.Fatalf("decorator metadata/replacement: %v", err)
	}
}

func TestTypeScriptClassProfiles(t *testing.T) {
	for _, target := range []string{"es2016", "es2021", "es2022", "es2025", "esnext"} {
		t.Run(target, func(t *testing.T) {
			program, ds, err := goya.CompileProject(context.Background(), goya.Project{Files: map[string]string{"/main.ts": `
class Base {value(): number {return 1;}}
class Derived extends Base {#field = 2; static #static = 3; static {this.#static++;} get field(): number {return this.#field;} set field(value: number) {this.#field = value;} static check(object: object): boolean {return #field in object;} static value(): number {return this.#static;} value(): number {return super.value() + this.#field;}}
const value = new Derived(); value.field = 4;
export const result = value.field === 4 && value.value() === 5 && Derived.value() === 4 && Derived.check(value) && !Derived.check({});
`}}, nil, goya.CompileOptions{"target": target})
			if err != nil || program == nil {
				t.Fatalf("class profile: %v %+v", err, ds)
			}
			result, err := goya.New().RunProgram(program)
			if err != nil || !hostObject(t, result).Get("result").ToBoolean() {
				t.Fatalf("class profile execution: %v", err)
			}
		})
	}
}

func TestTypeScriptESNextDecoratorNormalization(t *testing.T) {
	program, ds, err := goya.CompileProject(context.Background(), goya.Project{Files: map[string]string{"/main.ts": `function field(_value: undefined,_context: ClassFieldDecoratorContext): any {return function(value: number) {return value+1;};} class Example {@field value = 3;} export const result = new Example().value === 4;`}}, nil, goya.CompileOptions{"target": "esnext"})
	if err != nil || program == nil {
		t.Fatalf("ESNext decorator lowering: %v %+v", err, ds)
	}
	value, err := goya.New().RunProgram(program)
	if err != nil || !hostObject(t, value).Get("result").ToBoolean() {
		t.Fatalf("ESNext decorator execution: %v", err)
	}
}

func TestTypeScriptLegacyMetadataUsesRegisteredHook(t *testing.T) {
	var records []string
	registry := goya.NewRegistry()
	registerHost(t, registry, "record", func(key, value string) { records = append(records, key+":"+value) })
	environment := freezeHost(t, registry)
	program, ds, err := goya.CompileProject(context.Background(), goya.Project{Files: map[string]string{"/main.ts": `
const Reflect = {metadata(key: string, value: any) {return function(_target: any, _name?: string) {record(key, Array.isArray(value) ? value.map((item: any) => item.name).join(",") : value.name);};}};
function decorated(...args: any[]): void {record("decorated",String(args.length));}
function parameter(_target: any, _name: string | undefined, index: number): void {record("parameter",String(index));}
@decorated class Example {
  @decorated field: number = 1;
  @decorated inferred = 2;
  constructor(public value: string) {}
  @decorated method(@parameter value: string): boolean {return value.length > 0;}
}
export const result = new Example("value").method("yes");
`}}, environment, goya.CompileOptions{"experimentalDecorators": true, "emitDecoratorMetadata": true})
	if err != nil || program == nil {
		t.Fatalf("legacy metadata compile: %v %+v", err, ds)
	}
	if len(records) != 0 {
		t.Fatal("metadata hook executed during compilation")
	}
	r := goya.New()
	if err := environment.Bind(r); err != nil {
		t.Fatal(err)
	}
	value, err := r.RunProgram(program)
	if err != nil || !hostObject(t, value).Get("result").ToBoolean() {
		t.Fatalf("legacy metadata execution: %v", err)
	}
	for _, expected := range []string{"design:type:Number", "design:type:Object", "design:type:Function", "design:paramtypes:String", "design:returntype:Boolean", "parameter:0", "decorated:1", "decorated:3"} {
		if !slices.Contains(records, expected) {
			t.Fatalf("metadata %q missing: %v", expected, records)
		}
	}
}

func TestTypeScriptInvalidDecoratorResultRejected(t *testing.T) {
	for _, source := range []string{
		`function bad(_value: any,_context: any): any {return 3;} class Example {@bad method() {}}`,
		`function bad(_value: any,_context: any): any {return function(){};} class Example {@bad accessor value = 1;}`,
	} {
		program := compileTypeScript(t, source, nil)
		if _, err := goya.New().RunProgram(program); err == nil {
			t.Fatal("invalid decorator return contract was accepted")
		}
	}
}
