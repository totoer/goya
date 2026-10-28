package goya

import "testing"

func TestCheckedRegExpStaticState(t *testing.T) {
	for name, source := range map[string]string{
		"initial":                 `RegExp.input===""&&RegExp.$1===""&&RegExp.lastMatch===""&&RegExp.lastParen==="";`,
		"success failure aliases": `/(a)(b)/d.exec("xabz");const before=RegExp.lastMatch;const left=RegExp.leftContext,right=RegExp.rightContext;const failed=/q/.exec("none");RegExp.input==="xabz"&&RegExp.$1==="a"&&RegExp.$2==="b"&&RegExp.$9===""&&before==="ab"&&RegExp.lastMatch===before&&RegExp["$&"]==="ab"&&RegExp.lastParen==="b"&&RegExp["$+"]==="b"&&left==="x"&&right==="z"&&failed===null;`,
		"input coercion":          `/(a)/.test("a");RegExp.input={toString(){return "changed"}} as any;RegExp.$_==="changed"&&RegExp.$1==="a";`,
		"global matcher":          `"a1a2".match(/a(\d)/g);RegExp.$1==="2"&&RegExp.lastMatch==="a2"&&RegExp.leftContext==="a1";`,
		"replacement collection before callbacks": `const seen:string[]=[];"a1a2".replace(/a(\d)/g,()=>{seen.push(RegExp.$1);return "x"});seen.join(",")==="2,2";`,
		"split limit":                 `"a1b2c".split(/(\d)/,2);RegExp.$1==="1"&&RegExp.lastMatch==="1";`,
		"reset missing captures":      `/(a)(b)/.test("ab");/(x)/.test("x");RegExp.$1==="x"&&RegExp.$2===""&&RegExp.lastParen==="x";`,
		"brands descriptors subclass": `const get=Object.getOwnPropertyDescriptor(RegExp,"$1")!;let errors=0;class R extends RegExp{}for(const read of [()=>get.get!.call({}),()=>R.$1]){try{read()}catch(e){if(e instanceof TypeError)errors++}}new R("(a)").test("a");try{RegExp.$1}catch(e){if(e instanceof TypeError)errors++}try{new R("a").compile("b")}catch(e){if(e instanceof TypeError)errors++}/(b)/.test("b");errors===4&&RegExp.$1==="b"&&get.set===undefined&&get.configurable&&!get.enumerable;`,
		"constructor prototype":       `class R extends RegExp{}const subclass=new R("a","v");const native=new RegExp(subclass);Object.getPrototypeOf(native)===RegExp.prototype&&native.unicodeSets&&native.source==="a";`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("RegExp statics: %v %v", value, err)
			}
		})
	}
}

func TestCheckedRegExpIndices(t *testing.T) {
	for name, source := range map[string]string{
		"unicode named optional":         `const re=/(?<head>😀)(a)?/du;const m=re.exec("x😀")!;m.indices![0]!.join(",")==="1,3"&&m.indices![1]!.join(",")==="1,3"&&m.indices![2]===undefined&&m.indices!.groups!.head===m.indices![1]&&Object.getPrototypeOf(m.indices!.groups)===null&&re.hasIndices&&re.unicode&&re.flags==="du";`,
		"disabled":                       `const m=/a/.exec("a")!;!("indices" in m)&&!/a/.hasIndices&&RegExp.prototype.hasIndices===undefined;`,
		"lookbehind":                     `const m=/(?<=([ab]+)([bc]+))$/d.exec("abc")!;m.indices![1]!.join(",")==="0,1"&&m.indices![2]!.join(",")==="1,3";`,
		"lookahead then earlier capture": `const m=/(?=aa(b))(a)/d.exec("aab")!;m[1]==="b"&&m[2]==="a"&&m.indices![1]!.join(",")==="2,3"&&m.indices![2]!.join(",")==="0,1";`,
		"matchAll copies":                `const re=/(?<a>a)/dg;const matches=[..."aa".matchAll(re)];matches.length===2&&matches[0].indices!.groups!.a.join(",")==="0,1"&&matches[1].indices![0]!.join(",")==="1,2"&&matches[0].indices!==matches[1].indices&&re.lastIndex===0;`,
		"flags getter order":             `const seen:string[]=[];const getter=Object.getOwnPropertyDescriptor(RegExp.prototype,"flags")!.get!;const flags=getter.call(new Proxy({},{get(_,key){seen.push(String(key));return true}}));flags==="dgimsuvy"&&seen.join(",")==="hasIndices,global,ignoreCase,multiline,dotAll,unicode,unicodeSets,sticky";`,
		"brands descriptor":              `const get=Object.getOwnPropertyDescriptor(RegExp.prototype,"hasIndices")!;let errors=0;for(const v of [{},null,new Proxy(/a/d,{})]){try{get.get!.call(v)}catch(e){if(e instanceof TypeError)errors++}}errors===3&&get.set===undefined&&get.configurable&&!get.enumerable&&/a/d.toString()==="/a/d";`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("RegExp indices: %v %v", value, err)
			}
		})
	}
}
