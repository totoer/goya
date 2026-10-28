package goya_test

import (
	"context"
	"testing"

	"github.com/totoer/goya"
)

func TestTypeScriptStandardJSON(t *testing.T) {
	// The pinned stable declarations still expose a two-argument reviver and
	// omit rawJSON. Optional callback parameters and explicit assertions test
	// these runtime extensions without changing the upstream declarations.
	for name, source := range map[string]string{
		"UTF16":                   `const escaped=JSON.parse('"\\ud800"');const raw=JSON.parse('"'+String.fromCharCode(0xd801)+'"');escaped.charCodeAt(0)===0xd800&&raw.charCodeAt(0)===0xd801&&JSON.stringify(escaped)==='"\\ud800"';`,
		"numbers":                 `JSON.parse("1e400")===Infinity&&JSON.parse("-1e400")===-Infinity&&Object.is(JSON.parse("-0"),-0)&&Object.is(JSON.parse("-1e-400"),-0)&&JSON.parse(" 1.25e2 \n")===125;`,
		"properties":              `const value=JSON.parse('{"__proto__":{"polluted":true},"x":1,"x":2}');Object.getPrototypeOf(value)===Object.prototype&&Object.hasOwn(value,"__proto__")&&value.__proto__.polluted&&value.x===2&&(Object.prototype as any).polluted===undefined;`,
		"primitive sources":       `const seen:Record<string,string|undefined>={};let rootContext=false;const value=JSON.parse('{"large":9007199254740993,"list":[1e2,"\\ud800"]}',function(this:any,k:string,v:any,c?:any){seen[k]=c.source;if(k==="large")return BigInt(c.source);if(k==="")rootContext=Object.getPrototypeOf(c)===Object.prototype&&!Object.hasOwn(c,"source");return v});value.large===9007199254740993n&&seen.large==="9007199254740993"&&seen["0"]==="1e2"&&seen["1"]==='"\\ud800"'&&seen.list===undefined&&rootContext;`,
		"changed primitive":       `let source=false;const value=JSON.parse('{"a":1,"b":2}',function(this:any,k:string,v:any,c?:any){if(k==="a")this.b=3;if(k==="b")source=Object.hasOwn(c,"source");return v});!source&&value.b===3;`,
		"changed parent":          `let source=false;JSON.parse('{"a":0,"b":{"n":2}}',function(this:any,k:string,v:any,c?:any){if(k==="a")this.b={n:2};if(k==="n")source=Object.hasOwn(c,"source");return v});!source;`,
		"reviver descriptors":     `let valid=false;JSON.parse("0",function(this:any,k:string,v:any,c?:any){const d=Object.getOwnPropertyDescriptor(c,"source")!;valid=arguments.length===3&&this[""]===v&&d.value==="0"&&d.writable===true&&d.enumerable===true&&d.configurable===true;return v});valid;`,
		"reviver frozen property": `const value=JSON.parse('{"a":1,"b":2}',function(this:any,k:string,v:any,c?:any){if(k==="a")Object.defineProperty(this,"b",{value:2,writable:false,configurable:false});if(k==="b")return undefined;return v});value.b===2;`,
		"invalid grammar":         `let errors=0;for(const value of ["","[1,]",'{"a":1,}',"01","1.","-.1","1e","truefalse","undefined","/*x*/1",'"\\x41"']){try{JSON.parse(value)}catch(e){if(e instanceof SyntaxError)errors++}}errors===11;`,
		"raw brand":               `const raw=(JSON as any).rawJSON("9007199254740993");const d=Object.getOwnPropertyDescriptor(raw,"rawJSON")!;(JSON as any).isRawJSON(raw)&&!(JSON as any).isRawJSON(new Proxy(raw,{}))&&Object.getPrototypeOf(raw)===null&&Object.isFrozen(raw)&&Object.prototype.toString.call(raw)==="[object Object]"&&d.enumerable&&!d.writable&&!d.configurable&&JSON.stringify({n:raw})==='{\"n\":9007199254740993}'&&JSON.stringify(new Proxy((JSON as any).rawJSON("42"),{}))==='{\"rawJSON\":\"42\"}';`,
		"raw coercion":            `(JSON as any).rawJSON({toString(){return "42"}} as any).rawJSON==="42"&&(JSON as any).rawJSON(42n as any).rawJSON==="42"&&(JSON as any).rawJSON(null as any).rawJSON==="null"&&(JSON as any).rawJSON("1e400").rawJSON==="1e400";`,
		"raw invalid":             `let errors=0;for(const value of [""," 1","1 ","{}","[]","NaN","Infinity","01","true false",NaN,Infinity]){try{(JSON as any).rawJSON(value as any)}catch(e){if(e instanceof SyntaxError)errors++}}errors===11;`,
		"raw UTF16":               `const source='"'+String.fromCharCode(0xd800)+'"';const raw=(JSON as any).rawJSON(source);raw.rawJSON===source&&JSON.stringify(raw)===source&&JSON.stringify(raw).charCodeAt(1)===0xd800;`,
		"replacer UTF16 keys":     `const a=String.fromCharCode(0xd800),b=String.fromCharCode(0xd801);const value=JSON.parse('{"\\ud800":1,"\\ud801":2}');const text=JSON.stringify(value,[a,b]);text==='{\"\\ud800\":1,\"\\ud801\":2}'&&JSON.parse(text)[b]===2&&JSON.stringify(value,[Object(a),Object(b)])===text;`,
		"indent UTF16":            `const text=JSON.stringify([1],null,"😀😀😀😀😀😀");const broken=JSON.stringify([1],null,"aaaaaaaaa😀");text==="[\n😀😀😀😀😀1\n]"&&broken.charCodeAt(11)===0xd83d&&JSON.stringify([1],null,Infinity)==="[\n          1\n]";`,
	} {
		t.Run(name, func(t *testing.T) {
			p, ds, err := goya.Compile(context.Background(), goya.Project{Files: map[string]string{"/main.ts": source}}, nil, goya.CompileOptions{"lib": []string{"esnext"}})
			if err != nil || p == nil {
				t.Fatalf("compile JSON: %v %+v", err, ds)
			}
			value, err := goya.New().RunProgram(p)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("JSON semantics: %v %v", value, err)
			}
		})
	}
}

func TestTypeScriptRawJSONGoExport(t *testing.T) {
	p := goya.MustCompile(context.Background(), goya.Project{Files: map[string]string{"/main.ts": `(JSON as any).rawJSON('"'+String.fromCharCode(0xd800)+'"');`}}, nil, goya.CompileOptions{"lib": []string{"esnext"}})
	value, err := goya.New().RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	data, err := hostObject(t, value).MarshalJSON()
	if err != nil || string(data) != `"\ud800"` {
		t.Fatalf("raw JSON UTF8 export: %q %v", data, err)
	}
}
