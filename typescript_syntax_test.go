package goya_test

import (
	"context"
	"testing"

	"github.com/totoer/goya"
)

func TestTypeScriptSyntaxNormalizationProfiles(t *testing.T) {
	for _, source := range []string{
		`const log:string[]=[];const o={n:3,get method(){log.push("get");return function(this:{n:number},x:number){log.push("call");return this.n+x;}}};const result=(o?.method)(2);result===5&&log.join(",")==="get,call";`,
		`const o:{n:number;method?(this:{n:number}):number}={n:3,method(){return this.n}};o.method?.()===3;`,
		`let calls=0;const o:{method?(x?:number):number}|null=null;const value=(o as {method?(x?:number):number}|null)?.method?.(++calls);value===undefined&&calls===0;`,
		`const data:{n?:number}={n:3};const deleted=delete data?.n;deleted&&!Object.hasOwn(data,"n");`,
		`const log:string[]=[];const o={get n(){log.push("get");return undefined as number|undefined},set n(v:number|undefined){log.push("set");}};o.n??=3;log.join(",")==="get,set";`,
		`let n=0;const target={value:0};target.value ||= ++n;target.value &&= ++n;target.value===2&&n===2;`,
		`const order:string[]=[];function key(){order.push("key");return "value";}class Value {[key()](){return 3;}field=(order.push("field"),1);}const value=new Value();(value as unknown as Record<string,()=>number>).value()===3&&order.join(",")==="key,field";`,
		`const value={ ["__proto__"]:3, get x(){return 2;},set x(v:number){}};Object.getPrototypeOf(value)===Object.prototype&&value.__proto__===3&&value.x===2;`,
		`class Value {accessor n=1;}const value=new Value();value.n++;value.n===2&&!Object.hasOwn(value,"n");`,
		"function tag(parts:TemplateStringsArray){return parts.raw[0]===\"a\\nb\"&&parts[0]===\"a\\nb\";}tag`a\r\nb`;",
		"function tag(parts:TemplateStringsArray){return parts[0]===undefined&&parts.raw[0]===\"\\\\uZZ\";}tag`\\uZZ`;",
	} {
		for _, target := range []string{"es2016", "es2017", "es2025", "esnext"} {
			t.Run(target, func(t *testing.T) {
				p, ds, err := goya.CompileProject(context.Background(), goya.Project{Files: map[string]string{"/main.ts": source}}, nil, goya.CompileOptions{"target": target})
				if err != nil || p == nil {
					t.Fatalf("compile %s: %v %+v", source, err, ds)
				}
				v, err := goya.New().RunProgram(p)
				if err != nil || !v.ToBoolean() {
					t.Fatalf("execute %s: %v %v", source, v, err)
				}
			})
		}
	}
}

func TestTypeScriptWithStatementNonStrictProfile(t *testing.T) {
	source := "let result=0;const scope={value:2};\n// @ts-ignore TypeScript's with warning is explicitly suppressed in this non-strict script.\nwith(scope){result=value;}\nresult===2;"
	p, ds, err := goya.CompileProject(context.Background(), goya.Project{Files: map[string]string{"/main.ts": source}}, nil, goya.CompileOptions{"strict": false})
	if err != nil || p == nil {
		t.Fatalf("compile %v %+v", err, ds)
	}
	v, err := goya.New().RunProgram(p)
	if err != nil || !v.ToBoolean() {
		t.Fatalf("with: %v %v", v, err)
	}
}

func TestTypeScriptHelperEmissionOptionsDoNotLoadExternalRuntime(t *testing.T) {
	p, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{
		"/main.ts":    `function field(_target:object,_name:string){}class Value{@field n=42;}export const result=new Value().n===42;`,
		"/tslib.d.ts": `declare module "tslib"{export function __decorate(...args:any[]):any;}`,
	}}, nil, goya.CompileOptions{"module": "commonjs", "experimentalDecorators": true, "importHelpers": true, "noEmitHelpers": true})
	if err != nil || p == nil {
		t.Fatalf("compile %v %+v", err, ds)
	}
	v, err := goya.New().RunProgram(p)
	if err != nil || !hostObject(t, v).Get("result").ToBoolean() {
		t.Fatalf("helper runtime dependency: %v", err)
	}
}
