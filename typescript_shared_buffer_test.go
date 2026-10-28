package goya

import (
	"bytes"
	stdcontext "context"
	"io"
	"sync"
	"testing"
)

func TestCheckedSharedBuffer(t *testing.T) {
	for name, source := range map[string]string{
		"basic":                      `const b=new SharedArrayBuffer(8);const a=new Uint32Array(b);a[0]=42;const d=new DataView(b);d.setUint32(4,43,true);!b.growable&&b.maxByteLength===8&&b.byteLength===8&&a[0]===42&&a[1]===43&&d.buffer===b&&Object.prototype.toString.call(b)==="[object SharedArrayBuffer]";`,
		"growth":                     `const b=new SharedArrayBuffer(4,{maxByteLength:16});const fixed=new Uint8Array(b,0,2),tracking=new Uint8Array(b),view=new DataView(b,1);tracking.set([1,2,3,4]);b.grow(8);b.grow(8);b.grow(16);b.growable&&b.byteLength===16&&b.maxByteLength===16&&fixed.length===2&&tracking.length===16&&view.byteLength===15&&tracking[3]===4&&tracking[15]===0;`,
		"partial tracking element":   `const b=new SharedArrayBuffer(3,{maxByteLength:8});const a=new Uint16Array(b);b.grow(5);a.length===2&&a.byteLength===4;`,
		"all kinds":                  `const constructors:any[]=[Int8Array,Uint8Array,Uint8ClampedArray,Int16Array,Uint16Array,Int32Array,Uint32Array,Float16Array,Float32Array,Float64Array,BigInt64Array,BigUint64Array];let count=0;for(const C of constructors){const b=new SharedArrayBuffer(C.BYTES_PER_ELEMENT*4,{maxByteLength:C.BYTES_PER_ELEMENT*8});const a=new C(b,0,4);const v=C===BigInt64Array||C===BigUint64Array?42n:42;a.fill(v);a.reverse();a.copyWithin(1,0,3);const copy=a.slice();const reversed=a.toReversed();const sorted=a.toSorted();if(a[3]!==v||copy[0]!==v||reversed[0]!==v||sorted[0]!==v||Object.prototype.toString.call(a)!=="[object "+C.name+"]")throw Error(C.name);count++}count===12;`,
		"shared copy overlap":        `const b=new SharedArrayBuffer(6);const a=new Uint8Array(b);a.set([1,2,3,4,5,6]);a.set(a.subarray(0,4),2);const first=a.join(",");a.set([1,2,3,4,5,6]);a.copyWithin(1,0,5);first==="1,2,1,2,3,4"&&a.join(",")==="1,1,2,3,4,5";`,
		"copy between kinds":         `const b=new SharedArrayBuffer(16);const a=new Uint8Array(b);a.set([1,2,3,4]);const widened=new Uint16Array(b,0,4);widened.set(a.subarray(0,4));widened.join(",")==="1,2,3,4";`,
		"ordinary and shared copies": `const a=new Uint16Array(new SharedArrayBuffer(8));a.set([1,2,3,4]);const copied=new Uint16Array(a);const back=new Uint16Array(new SharedArrayBuffer(8));back.set(copied);copied.join(",")==="1,2,3,4"&&back.join(",")==="1,2,3,4"&&! (copied.buffer instanceof SharedArrayBuffer);`,
		"unaligned data view":        `const b=new SharedArrayBuffer(24);const d=new DataView(b);d.setUint32(7,0x12345678);d.setBigInt64(13,-42n,true);d.getUint32(7)===0x12345678&&d.getBigInt64(13,true)===-42n&&new Uint8Array(b)[7]===0x12;`,
		"hex and base64":             `const a=new Uint8Array(new SharedArrayBuffer(6));const h=(a as any).setFromHex("010203");const p=(a as any).setFromBase64("BAUG");h.written===3&&p.written===3&&(a as any).toHex()==="040506000000"&&(a as any).toBase64()==="BAUGAAAA";`,
		"slice":                      `const b=new SharedArrayBuffer(4,{maxByteLength:8});new Uint8Array(b).set([1,2,3,4]);const copy=b.slice(1,3);const a=new Uint8Array(copy);a[0]=42;copy instanceof SharedArrayBuffer&&!copy.growable&&copy.byteLength===2&&a[1]===3&&new Uint8Array(b)[1]===2;`,
		"growth errors":              `const b=new SharedArrayBuffer(4,{maxByteLength:8});const fixed=new SharedArrayBuffer(4);let effects=0,errors=0;for(const f of [()=>b.grow(3),()=>b.grow(9),()=>b.grow(-1),()=>b.grow(Infinity),()=>fixed.grow({valueOf(){effects++;return 4}} as any),()=>new SharedArrayBuffer(4,{maxByteLength:3}),()=>SharedArrayBuffer.prototype.grow.call({},4)]){try{f()}catch(e){if(e instanceof TypeError||e instanceof RangeError)errors++}}errors===7&&effects===0&&b.byteLength===4;`,
		"separate brands":            `const shared=new SharedArrayBuffer(4),ordinary=new ArrayBuffer(4);let errors=0;for(const f of [()=>ArrayBuffer.prototype.slice.call(shared,0),()=>ArrayBuffer.prototype.transfer.call(shared),()=>ArrayBuffer.prototype.resize.call(shared,0),()=>SharedArrayBuffer.prototype.slice.call(ordinary,0),()=>SharedArrayBuffer.prototype.grow.call(ordinary,0),()=>(SharedArrayBuffer as any)(1)]){try{f()}catch(e){if(e instanceof TypeError)errors++}}errors===6&&!ArrayBuffer.isView(shared)&&ArrayBuffer.isView(new DataView(shared))&&ArrayBuffer.isView(new Uint8Array(shared));`,
		"extensibility":              `const b=new SharedArrayBuffer(4,{maxByteLength:8});const fixed=new Uint8Array(b,0,2),tracking=new Uint8Array(b);Reflect.preventExtensions(fixed)&&!Reflect.preventExtensions(tracking);`,
		"callback growth snapshot":   `const b=new SharedArrayBuffer(2,{maxByteLength:4});const a=new Uint8Array(b);a.set([1,2]);const seen:number[]=[];a.forEach((v,i)=>{seen.push(v);if(i===0)b.grow(4)});seen.join(",")==="1,2"&&a.length===4;`,
		"slice species identity":     `const b=new SharedArrayBuffer(4);Object.defineProperty(b,"constructor",{value:{[Symbol.species]:function(){return b}}});let rejected=false;try{b.slice(0)}catch(e){rejected=e instanceof TypeError}rejected;`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("shared buffer: %v %v", value, err)
			}
		})
	}
}

func TestSharedBufferGoOwnership(t *testing.T) {
	shared, err := NewSharedArrayBuffer(8, 16)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := shared.WriteAt([]byte{1, 2, 3, 4}, 2); n != 4 || err != nil {
		t.Fatalf("write %d %v", n, err)
	}
	first, second := New(), New()
	a, b := first.ToValue(shared).(*Object), second.ToValue(shared).(*Object)
	if a == b || a != first.ToValue(shared) || b != second.ToValue(shared) {
		t.Fatal("shared realm identity")
	}
	if _, ok := a.Export().(SharedArrayBuffer); !ok {
		t.Fatal("shared buffer export lost storage handle")
	}
	view, err := first.New(first.getUint8Array(), a)
	if err != nil {
		t.Fatal(err)
	}
	copy := view.Export().([]byte)
	copy[2] = 42
	var actual [4]byte
	if n, err := shared.ReadAt(actual[:], 2); n != 4 || err != nil || !bytes.Equal(actual[:], []byte{1, 2, 3, 4}) {
		t.Fatalf("copied export: %v %d %v", actual, n, err)
	}
	if err = shared.Grow(16); err != nil {
		t.Fatal(err)
	}
	if length := first.typedArrayProto_getLength(FunctionCall{This: view}).ToInteger(); length != 16 {
		t.Fatalf("Go growth not reflected: %d", length)
	}
	if n, err := shared.ReadAt(make([]byte, 2), 15); n != 1 || err != io.EOF {
		t.Fatalf("read boundary: %d %v", n, err)
	}
	if n, err := shared.WriteAt([]byte{1, 2}, 15); n != 1 || err != io.ErrShortWrite {
		t.Fatalf("write boundary: %d %v", n, err)
	}
}

func TestSharedBufferIndependentGoAgents(t *testing.T) {
	shared, err := NewSharedArrayBuffer(4, 64)
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
	writer := MustCompile(stdcontext.Background(), Project{Files: map[string]string{"/writer.ts": `const a=new Uint32Array(shared);for(let i=0;i<20000;i++){a[0]=0xaaaaaaaa;a[0]=0x55555555;if(i%8===0){a.fill(0xaaaaaaaa);a.reverse();a.sort()}}`}}, env, nil)
	reader := MustCompile(stdcontext.Background(), Project{Files: map[string]string{"/reader.ts": `const a=new Uint32Array(shared);for(let i=0;i<40000;i++){const value=a[0];if(value!==0&&value!==0xaaaaaaaa&&value!==0x55555555)throw Error("torn integer");if(a.length<1||a.length>16)throw Error("invalid tracking length")}`}}, env, nil)
	errors := make(chan error, 3)
	var group sync.WaitGroup
	for _, program := range []*Program{writer, reader} {
		r := New()
		if err = env.Bind(r); err != nil {
			t.Fatal(err)
		}
		group.Add(1)
		go func() { defer group.Done(); _, err := r.RunProgram(program); errors <- err }()
	}
	group.Add(1)
	go func() {
		defer group.Done()
		for n := 8; n <= 64; n += 4 {
			if err := shared.Grow(n); err != nil {
				errors <- err
				return
			}
		}
		errors <- nil
	}()
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}
