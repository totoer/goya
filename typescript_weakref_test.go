package goya

import (
	stdcontext "context"
	"runtime"
	"testing"
	"time"
)

func TestCheckedWeakRef(t *testing.T) {
	for name, source := range map[string]string{
		"objects and symbols":          `const target={value:42};const symbol=Symbol("key");const a=new WeakRef(target);const b=new WeakRef(symbol);new WeakMap([[symbol,42]]);new WeakSet([symbol]);a.deref()===target&&b.deref()===symbol;`,
		"subclass":                     `class Ref extends WeakRef<object>{}const target={};const ref=new Ref(target);ref instanceof Ref&&ref instanceof WeakRef&&ref.deref()===target;`,
		"brands":                       `let errors=0;const ref=new WeakRef({});for(const f of [()=>WeakRef.prototype.deref.call({}),()=>WeakRef.prototype.deref.call(WeakRef.prototype),()=>WeakRef.prototype.deref.call(new Proxy(ref,{})),()=>(WeakRef as any)({})]){try{f()}catch(e){if(e instanceof TypeError)errors++}}errors===4;`,
		"invalid targets":              `let errors=0;for(const value of [undefined,null,1,true,"x",1n,Symbol.for("registered")]){try{new WeakRef(value as any)}catch(e){if(e instanceof TypeError)errors++}}errors===7;`,
		"descriptors":                  `const p=WeakRef.prototype;const d=Object.getOwnPropertyDescriptor(p,"deref")!;const c=Object.getOwnPropertyDescriptor(WeakRef,"prototype")!;const tag=Object.getOwnPropertyDescriptor(p,Symbol.toStringTag)!;Object.prototype.toString.call(new WeakRef({}))==="[object WeakRef]"&&p.constructor===WeakRef&&WeakRef.length===1&&p.deref.length===0&&!d.enumerable&&d.writable&&d.configurable&&!c.writable&&!c.enumerable&&!c.configurable&&tag.value==="WeakRef"&&!tag.writable&&tag.configurable;`,
		"constructor validation order": `let reads=0;const Target=new Proxy(function(){},{get(t,k,r){if(k==="prototype")reads++;return Reflect.get(t,k,r)}});let rejected=false;try{Reflect.construct(WeakRef,[1],Target)}catch(e){rejected=e instanceof TypeError}rejected&&reads===0;`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("WeakRef: %v %v", value, err)
			}
		})
	}
}

func TestCheckedWeakRefJobLifetime(t *testing.T) {
	r := New()
	registry := NewRegistry()
	if err := registry.Register("collect", func() { runtime.GC(); runtime.GC() }); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("keptCount", func() int32 { return int32(len(r.keptObjects)) }); err != nil {
		t.Fatal(err)
	}
	env, err := registry.Freeze(stdcontext.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = env.Bind(r); err != nil {
		t.Fatal(err)
	}
	p := MustCompile(stdcontext.Background(), Project{Files: map[string]string{"/main.ts": `
const ref=new WeakRef({value:42});collect();
if(ref.deref()?.value!==42||ref.deref()?.value!==42||keptCount()!==1)throw Error("construction lifetime");
const target={};const live=new WeakRef(target);
Promise.resolve().then(()=>{if(keptCount()!==0)throw Error("top-level not cleared");if(live.deref()!==target)throw Error("deref");collect();if(live.deref()!==target||keptCount()!==1)throw Error("deref lifetime")}).then(()=>{if(keptCount()!==0)throw Error("promise job not cleared")});
`}}, env, nil)
	if len(r.keptObjects) != 0 {
		t.Fatal("kept targets survived execution")
	}
	// Rejected promise callbacks do not become RunProgram errors.
	r.SetPromiseRejectionTracker(func(p *Promise, operation PromiseRejectionOperation) {
		if operation == PromiseRejectionReject {
			t.Errorf("job assertion: %v", p.Result())
		}
	})
	if _, err = r.RunProgram(p); err != nil {
		t.Fatal(err)
	}
}

func newCollectableWeakRef(r *Runtime, symbol bool) *weakRefObject {
	var target Value = r.NewObject()
	if symbol {
		target = NewSymbol("collectable")
	}
	object, err := r.New(r.getWeakRef(), target)
	if err != nil {
		panic(err)
	}
	return object.self.(*weakRefObject)
}

func TestWeakRefDoesNotRetainTarget(t *testing.T) {
	for _, symbol := range []bool{false, true} {
		r := New()
		ref := newCollectableWeakRef(r, symbol)
		for range 40 {
			runtime.GC()
			if IsUndefined(ref.target.value()) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !IsUndefined(ref.target.value()) {
			t.Fatal("weak target retained after job")
		}
		if value := r.weakRefDeref(FunctionCall{This: ref.val}); !IsUndefined(value) || len(r.keptObjects) != 0 {
			t.Fatalf("cleared deref: %v", value)
		}
		runtime.KeepAlive(ref)
	}
}

func TestWeakKeysRejectRegisteredSymbolFromAnotherRealm(t *testing.T) {
	symbol, err := New().RunString(`Symbol.for("key");`)
	if err != nil {
		t.Fatal(err)
	}
	r := New()
	if r.canBeHeldWeakly(symbol) || r.getWeakMapRefs(symbol, true) != nil {
		t.Fatal("foreign registered symbol accepted as weak key")
	}
	if _, err := r.New(r.getWeakRef(), symbol); err == nil {
		t.Fatal("foreign registered symbol accepted as WeakRef target")
	}
}
