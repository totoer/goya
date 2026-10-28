package goya_test

import (
	"reflect"
	"testing"

	"github.com/totoer/goya"
)

func TestTypeScriptHalfPrecisionFamilies(t *testing.T) {
	for name, source := range map[string]string{
		"rounding":                    `const above=1+2**-11+2**-40;const a=new Float16Array([1.00048828125,above,65504,65520,2**-24,2**-25,-0,NaN,Infinity,-Infinity]);a[0]===1&&a[1]===1+2**-10&&a[2]===65504&&a[3]===Infinity&&a[4]===2**-24&&a[5]===0&&Object.is(a[6],-0)&&Number.isNaN(a[7])&&a[8]===Infinity&&a[9]===-Infinity&&Math.f16round(above)===a[1]&&Object.is(Math.f16round(-0),-0);`,
		"data view endian":            `const b=new ArrayBuffer(6);const v=new DataView(b);v.setFloat16(0,1.5);v.setFloat16(2,-2,true);v.setFloat16(4,-0);v.getUint16(0)===0x3e00&&v.getUint16(2,true)===0xc000&&v.getFloat16(0)===1.5&&v.getFloat16(2,true)===-2&&Object.is(v.getFloat16(4),-0);`,
		"methods":                     `const a=new Float16Array([3,1,2,NaN,-0,0]);const sorted=a.toSorted();const b=a.slice(0,3).map(v=>v*2);const c=b.subarray(1);c[0]=42;const d=Float16Array.from([1,2],v=>v+.5);const e=Float16Array.of(1,2).with(0,3);sorted[0]===0&&Object.is(sorted[0],-0)&&sorted[1]===0&&Number.isNaN(sorted[5])&&b.join(",")==="6,42,4"&&d.join(",")==="1.5,2.5"&&e.join(",")==="3,2"&&a.includes(NaN)&&a.indexOf(NaN)===-1&&a.indexOf(1.00001)===-1&&a.lastIndexOf(0)===5;`,
		"buffer constructor and copy": `const b=new ArrayBuffer(8);const a=new Float16Array(b,2,2);a.set([1.5,2.5]);const copy=new Float32Array(a);const roundtrip=new Float16Array(copy);a.byteOffset===2&&a.byteLength===4&&a.length===2&&ArrayBuffer.isView(a)&&new Uint16Array(b)[1]===0x3e00&&roundtrip.join(",")==="1.5,2.5"&&Float16Array.BYTES_PER_ELEMENT===2&&a.BYTES_PER_ELEMENT===2&&Object.prototype.toString.call(a)==="[object Float16Array]";`,
		"range brands coercion":       `let rejected=0;for(const f of [()=>new Float16Array(new ArrayBuffer(4),1),()=>new DataView(new ArrayBuffer(1)).getFloat16(0),()=>DataView.prototype.getFloat16.call({} as DataView,0),()=>Math.f16round(1n as any)]){try{f()}catch(e){if(e instanceof TypeError||e instanceof RangeError)rejected++}}rejected===4&&Math.f16round("1.5" as any)===1.5;`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := goya.New().RunProgram(compileTypeScript(t, source, nil))
			if err != nil || !value.ToBoolean() {
				t.Fatalf("float16: %v %v", value, err)
			}
		})
	}
	r := goya.New()
	value, err := r.RunProgram(compileTypeScript(t, `new Float16Array([1.5,42]);`, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(value.Export(), []float64{1.5, 42}) {
		t.Fatalf("half-precision numeric export: %#v", value.Export())
	}
}

func TestTypeScriptTypedArrayIntrinsicIdentity(t *testing.T) {
	for _, source := range []string{
		`Object.defineProperty(Float16Array,"name",{get(){throw new Error("must not read constructor name")}});const values=new Float16Array([1.5]);Object.prototype.toString.call(values)==="[object Float16Array]"&&new Float32Array(values)[0]===1.5;`,
		`class Derived extends BigInt64Array{}const values=new Derived(new BigInt64Array([42n]));values[0]===42n&&Object.prototype.toString.call(values)==="[object BigInt64Array]";`,
		`class Wrong extends Float16Array{static get [Symbol.species](){return BigInt64Array as unknown as Float16ArrayConstructor}}const source=new Wrong(0);let errors=0;for(const f of [()=>source.map(v=>v),()=>source.filter(()=>true),()=>source.slice(),()=>source.subarray()]){try{f()}catch(e){if(e instanceof TypeError)errors++}}errors===4;`,
		`let errors=0;for(const f of [()=>new Float16Array(new BigInt64Array(0) as any),()=>new BigInt64Array(new Float16Array(0) as any),()=>new Float16Array(0).set(new BigInt64Array(0) as any),()=>new BigInt64Array(0).set(new Float16Array(0) as any)]){try{f()}catch(e){if(e instanceof TypeError)errors++}}errors===4;`,
	} {
		value, err := goya.New().RunProgram(compileTypeScript(t, source, nil))
		if err != nil || !value.ToBoolean() {
			t.Fatalf("typed array identity: %v %v", value, err)
		}
	}
}
