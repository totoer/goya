package goya_test

import (
	"testing"

	"github.com/totoer/goya"
)

// These operation regressions exercise the checked TS entry point, in addition
// to the historical isolated VM fixtures.
func TestTypeScriptVMOperationRegressions(t *testing.T) {
	for name, source := range map[string]string{
		"numeric":           `Object.is(-0,-0)&&!Object.is(0,-0)&&Number.isNaN(0/0)&&1/-0===-Infinity&&((-1>>>0)===4294967295)&&Math.imul(0x7fffffff,2)===-2;`,
		"bigint":            `(9007199254740993n+2n)===9007199254740995n&&BigInt.asIntN(8,255n)===-1n&&(-7n/3n)===-2n;`,
		"utf16":             `const s="😀\ud800";s.length===3&&s.charCodeAt(0)===0xd83d&&s.charCodeAt(2)===0xd800&&[...s].length===2&&!s.isWellFormed()&&s.toWellFormed()==="😀\ufffd";`,
		"property-order":    `const o={b:1,"10":2,"2":3,a:4};const s=Symbol("key");Reflect.set(o,s,5);Reflect.ownKeys(o).map(String).join(",")==="2,10,b,a,Symbol(key)";`,
		"accessor-receiver": `const base={get value(){return (this as unknown as {n:number}).n}};const child=Object.create(base);child.n=42;Reflect.get(base,"value",child)===42;`,
		"descriptor":        `const o={};Object.defineProperty(o,"n",{value:42,writable:false,configurable:false});!Reflect.set(o,"n",1)&&!Reflect.deleteProperty(o,"n")&&Reflect.get(o,"n")===42;`,
		"proxy-invariant":   `const o={};Object.defineProperty(o,"n",{value:1,configurable:false});const p=new Proxy(o,{ownKeys(){return []}});let rejected=false;try{Reflect.ownKeys(p)}catch(e){rejected=e instanceof TypeError}rejected;`,
		"sparse-array":      `const values:number[]=[];values[3]=42;let calls=0;const mapped=values.map(v=>{calls++;return v+1});mapped.length===4&&calls===1&&!(0 in mapped)&&mapped[3]===43;`,
		"stable-sort":       `const values=[{key:1,id:"a"},{key:0,id:"b"},{key:1,id:"c"}];values.sort((a,b)=>a.key-b.key).map(v=>v.id).join("")==="bac";`,
		"symbol-coercion":   `const key=Symbol("key");const o={[key]:42};Object.getOwnPropertySymbols(o)[0]===key&&o[key]===42&&String(key)==="Symbol(key)";`,
		"global-uri":        `encodeURIComponent("a 😀") === "a%20%F0%9F%98%80" && decodeURI("%3F%2F")==="%3F%2F"&&parseInt("  -0x10",0)===-16&&parseFloat("1.5x")===1.5;`,
	} {
		t.Run(name, func(t *testing.T) {
			v, err := goya.New().RunProgram(compileTypeScript(t, source, nil))
			if err != nil || !v.ToBoolean() {
				t.Fatalf("operation regression: %v %v", v, err)
			}
		})
	}
}
