package goya

import (
	stdcontext "context"
	"encoding/binary"
	"sync"
	"testing"
)

func TestCheckedAtomicsIntegerOperations(t *testing.T) {
	for name, source := range map[string]string{
		"all integer kinds":                 `const constructors:any[]=[Int8Array,Uint8Array,Int16Array,Uint16Array,Int32Array,Uint32Array,BigInt64Array,BigUint64Array];let passed=0;for(const C of constructors){for(const B of [ArrayBuffer,SharedArrayBuffer]){const a=new C(new B(C.BYTES_PER_ELEMENT*2));const big=C===BigInt64Array||C===BigUint64Array;const n=(v:number):any=>big?BigInt(v):v;const A:any=Atomics;if(A.store(a,1,n(7))!==n(7)||A.add(a,1,n(3))!==n(7)||A.load(a,1)!==n(10)||A.sub(a,1,n(2))!==n(10)||A.and(a,1,n(6))!==n(8)||A.or(a,1,n(3))!==n(0)||A.xor(a,1,n(1))!==n(3)||A.exchange(a,1,n(42))!==n(2)||A.compareExchange(a,1,n(42),n(9))!==n(42)||A.compareExchange(a,1,n(42),n(1))!==n(9)||A.load(a,1)!==n(9)||a[0]!==n(0))throw Error(C.name);passed++}}passed===16;`,
		"store result and wrapping":         `const a=new Int8Array(new SharedArrayBuffer(1));const stored=Atomics.store(a,0,257.9);const first=Atomics.load(a,0);const inf=Atomics.store(a,0,Infinity);const after=Atomics.load(a,0);const zero=Atomics.store(a,0,NaN);stored===257&&first===1&&inf===Infinity&&after===0&&Object.is(zero,0)&&Object.is(Atomics.store(a,0,-0),0);`,
		"bigint exactness":                  `const a=new BigInt64Array(new SharedArrayBuffer(8));const value=2n**70n+7n;const stored=Atomics.store(a,0,value);const old=Atomics.add(a,0,2n**64n+1n);stored===value&&old===7n&&Atomics.load(a,0)===8n;`,
		"large number modulo":               `const a=new Int16Array(new SharedArrayBuffer(2));const b=new Uint16Array(new ArrayBuffer(2));Atomics.store(a,0,2**63+32768);b[0]=2**63+2048;Atomics.load(a,0)===-32768&&b[0]===2048;`,
		"invalid kinds":                     `const values:any[]=[{},new DataView(new SharedArrayBuffer(8)),new Uint8ClampedArray(1),new Float16Array(1),new Float32Array(1),new Float64Array(1),new Proxy(new Int32Array(1),{})];let errors=0;for(const a of values){try{Atomics.load(a,0)}catch(e){if(e instanceof TypeError)errors++}}errors===values.length;`,
		"bounds and coercion":               `const a=new Int32Array(new SharedArrayBuffer(4));let effects=0,errors=0;for(const index of [-1,1,Infinity]){try{Atomics.store(a,index,{valueOf(){effects++;return 1}} as any)}catch(e){if(e instanceof RangeError)errors++}}errors===3&&effects===0;`,
		"resize during argument conversion": `const b=new ArrayBuffer(8,{maxByteLength:16});const a=new Int32Array(b);let rejected=false;try{Atomics.store(a,1,{valueOf(){b.resize(4);return 42}} as any)}catch(e){rejected=e instanceof RangeError}rejected&&a[0]===0;`,
		"fixed view becomes out of bounds":  `const b=new ArrayBuffer(8,{maxByteLength:16});const a=new Int32Array(b,0,2);let rejected=false;try{Atomics.store(a,0,{valueOf(){b.resize(4);return 42}} as any)}catch(e){rejected=e instanceof TypeError}rejected&&new Int32Array(b)[0]===0;`,
		"compare exchange conversion order": `const log:number[]=[];const b=new ArrayBuffer(8,{maxByteLength:16});const a=new Int32Array(b);let rejected=false;try{Atomics.compareExchange(a,1,{valueOf(){log.push(1);b.resize(4);return 0}} as any,{valueOf(){log.push(2);return 42}} as any)}catch(e){rejected=e instanceof RangeError}rejected&&log.join(",")==="1,2";`,
		"shared index witness":              `const b=new SharedArrayBuffer(4,{maxByteLength:8});const a=new Int32Array(b);let rejected=false,effects=0;try{Atomics.store(a,{valueOf(){b.grow(8);return 1}} as any,{valueOf(){effects++;return 42}} as any)}catch(e){rejected=e instanceof RangeError}rejected&&effects===0&&a.length===2;`,
		"lock free and pause":               `let effects=0;Atomics.pause({valueOf(){effects++;throw Error("must be ignored")}} as any);[1,2,4,8].every(n=>Atomics.isLockFree(n))&&[0,-1,3,16,Infinity,NaN].every(n=>!Atomics.isLockFree(n))&&effects===0&&Object.prototype.toString.call(Atomics)==="[object Atomics]";`,
	} {
		t.Run(name, func(t *testing.T) {
			p, ds, err := Compile(stdcontext.Background(), Project{Files: map[string]string{"/main.ts": source}}, nil, CompileOptions{"lib": []string{"esnext"}})
			if err != nil || p == nil {
				t.Fatalf("check Atomics: %v %+v", err, ds)
			}
			value, err := New().RunProgram(p)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("Atomics: %v %v", value, err)
			}
		})
	}
}

func TestAtomicsIndependentGoAgents(t *testing.T) {
	shared, err := NewSharedArrayBuffer(4)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	err = registry.Register("shared", HostBinding{Value: shared, TypeScript: "SharedArrayBuffer", Adapter: HostAdapterFuncs{ID: "shared-storage/v1", BindValue: func(r *Runtime, value any) (Value, error) { return r.ToValue(value.(SharedArrayBuffer)), nil }}})
	if err != nil {
		t.Fatal(err)
	}
	env, err := registry.Freeze(stdcontext.Background())
	if err != nil {
		t.Fatal(err)
	}
	program := MustCompile(stdcontext.Background(), Project{Files: map[string]string{"/main.ts": `const a=new Int32Array(shared);for(let i=0;i<5000;i++)Atomics.add(a,0,1);`}}, env, nil)
	var group sync.WaitGroup
	errors := make(chan error, 4)
	for range 4 {
		r := New()
		if err = env.Bind(r); err != nil {
			t.Fatal(err)
		}
		group.Add(1)
		go func() { defer group.Done(); _, err := r.RunProgram(program); errors <- err }()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var bytes [4]byte
	if _, err = shared.ReadAt(bytes[:], 0); err != nil {
		t.Fatal(err)
	}
	var got uint32
	if nativeEndian == littleEndian {
		got = binary.LittleEndian.Uint32(bytes[:])
	} else {
		got = binary.BigEndian.Uint32(bytes[:])
	}
	if got != 20000 {
		t.Fatalf("lost atomic increments: %d", got)
	}
}
