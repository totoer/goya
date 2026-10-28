package goya

import (
	stdcontext "context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"weak"
)

func TestCheckedFinalizationRegistry(t *testing.T) {
	for name, source := range map[string]string{
		"register and unregister":  `const calls:unknown[]=[];const registry=new FinalizationRegistry(v=>calls.push(v));const token={};const target={};registry.register(target,"a",token);registry.register(target,"b",token);const first=registry.unregister(token);first&&!registry.unregister(token)&&calls.length===0;`,
		"symbols":                  `const registry=new FinalizationRegistry(()=>{});const symbol=Symbol("target"),token=Symbol("token");registry.register(symbol,42,token);registry.unregister(token);`,
		"same token and target":    `const registry=new FinalizationRegistry(()=>{});const target={};registry.register(target,42,target);registry.unregister(target);`,
		"subclass and descriptors": `class Registry extends FinalizationRegistry<number>{}const registry=new Registry(()=>{});const p=FinalizationRegistry.prototype;const d=Object.getOwnPropertyDescriptor(p,"register")!;const c=Object.getOwnPropertyDescriptor(FinalizationRegistry,"prototype")!;registry instanceof Registry&&registry instanceof FinalizationRegistry&&Object.prototype.toString.call(registry)==="[object FinalizationRegistry]"&&p.constructor===FinalizationRegistry&&p.register.length===2&&p.unregister.length===1&&FinalizationRegistry.length===1&&!d.enumerable&&d.writable&&d.configurable&&!c.writable&&!c.enumerable&&!c.configurable;`,
		"validation":               `const registry=new FinalizationRegistry(()=>{});const target={};let errors=0;const tasks=[()=>(FinalizationRegistry as any)(()=>{}),()=>new FinalizationRegistry(null as any),()=>registry.register(1 as any,42),()=>registry.register(Symbol.for("x"),42),()=>registry.register(target,target),()=>registry.register(target,42,null as any),()=>registry.unregister(Symbol.for("x")),()=>registry.unregister(undefined as any),()=>FinalizationRegistry.prototype.register.call({},target,42),()=>FinalizationRegistry.prototype.unregister.call(new Proxy(registry,{}),target)];for(const f of tasks){try{f()}catch(e){if(e instanceof TypeError)errors++}}errors===tasks.length;`,
		"constructor order":        `let reads=0;const target=new Proxy(function(){},{get(t,k,r){if(k==="prototype")reads++;return Reflect.get(t,k,r)}});let rejected=false;try{Reflect.construct(FinalizationRegistry,[1],target)}catch(e){rejected=e instanceof TypeError}rejected&&reads===0;`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("FinalizationRegistry: %v %v", value, err)
			}
		})
	}
}
func newTestRegistry(t *testing.T, r *Runtime, callback func(FunctionCall) Value) *finalizationRegistryObject {
	t.Helper()
	object, err := r.New(r.getFinalizationRegistry(), r.newNativeFunc(callback, "cleanup", 1))
	if err != nil {
		t.Fatal(err)
	}
	return object.self.(*finalizationRegistryObject)
}
func registerCollectableFinalization(r *Runtime, registry *finalizationRegistryObject, held Value, symbol bool) weakTarget {
	var target Value = r.NewObject()
	if symbol {
		target = NewSymbol("target")
	}
	ref := makeWeakTarget(target)
	r.finalizationRegister(FunctionCall{This: registry.val, Arguments: []Value{target, held}})
	return ref
}
func TestFinalizationCleanupRunsOnOwner(t *testing.T) {
	for _, symbol := range []bool{false, true} {
		r := New()
		var delivering atomic.Bool
		calls := 0
		registry := newTestRegistry(t, r, func(call FunctionCall) Value {
			if !delivering.Load() {
				t.Error("cleanup entered VM from GC")
			}
			if !IsUndefined(call.This) || len(call.Arguments) != 1 || call.Argument(0).ToInteger() != 42 {
				t.Errorf("callback arguments: %+v", call)
			}
			calls++
			return _undefined
		})
		target := registerCollectableFinalization(r, registry, valueInt(42), symbol)
		for range 100 {
			runtime.GC()
			if calls != 0 {
				t.Fatal("cleanup ran before RunJobs")
			}
			delivering.Store(true)
			err := r.RunJobs()
			delivering.Store(false)
			if err != nil {
				t.Fatal(err)
			}
			if calls == 1 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if calls != 1 || len(registry.cells) != 0 || !IsUndefined(target.value()) {
			t.Fatalf("cleanup: calls=%d cells=%d", calls, len(registry.cells))
		}
		if err := r.RunJobs(); err != nil || calls != 1 {
			t.Fatalf("duplicate cleanup: %v %d", err, calls)
		}
		runtime.KeepAlive(registry)
	}
}
func TestFinalizationUnregisterAfterNotification(t *testing.T) {
	r := New()
	calls := 0
	registry := newTestRegistry(t, r, func(FunctionCall) Value { calls++; return _undefined })
	target, token := r.NewObject(), r.NewObject()
	r.finalizationRegister(FunctionCall{This: registry.val, Arguments: []Value{target, valueInt(42), token}})
	r.finalizations.push(finalizationNotification{registry: weak.Make(registry), id: registry.nextID})
	if !r.finalizationUnregister(FunctionCall{This: registry.val, Arguments: []Value{token}}).ToBoolean() {
		t.Fatal("unregister")
	}
	if err := r.RunJobs(); err != nil || calls != 0 || len(registry.cells) != 0 {
		t.Fatalf("cancelled notification: %v calls=%d", err, calls)
	}
	runtime.KeepAlive(target)
}
func TestFinalizationExceptionPreservesLaterJobs(t *testing.T) {
	r := New()
	calls := []int64{}
	registry := newTestRegistry(t, r, func(call FunctionCall) Value {
		n := call.Argument(0).ToInteger()
		calls = append(calls, n)
		if n == 1 {
			panic(r.NewTypeError("cleanup failed"))
		}
		return _undefined
	})
	targets := []*Object{r.NewObject(), r.NewObject()}
	for i, target := range targets {
		r.finalizationRegister(FunctionCall{This: registry.val, Arguments: []Value{target, valueInt(i + 1)}})
		r.finalizations.push(finalizationNotification{registry: weak.Make(registry), id: registry.nextID})
	}
	var exception *Exception
	if err := r.RunJobs(); !errors.As(err, &exception) {
		t.Fatalf("cleanup exception: %v", err)
	}
	if len(calls) != 1 || calls[0] != 1 || len(registry.cells) != 1 {
		t.Fatalf("failed job state: %v", calls)
	}
	if err := r.RunJobs(); err != nil || len(calls) != 2 || calls[1] != 2 || len(registry.cells) != 0 {
		t.Fatalf("later cleanup: %v %v", err, calls)
	}
	runtime.KeepAlive(targets)
}
func registerTransientRegistry(t *testing.T, r *Runtime, target *Object) weak.Pointer[Object] {
	registry := newTestRegistry(t, r, func(FunctionCall) Value { return _undefined })
	held := r.NewObject()
	r.finalizationRegister(FunctionCall{This: registry.val, Arguments: []Value{target, held}})
	return weak.Make(held)
}
func TestFinalizationDoesNotRetainDeadRegistry(t *testing.T) {
	r := New()
	target := r.NewObject()
	held := registerTransientRegistry(t, r, target)
	for range 40 {
		runtime.GC()
		if held.Value() == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if held.Value() != nil {
		t.Fatal("cleanup registration retained dead registry's held value")
	}
	runtime.KeepAlive(target)
}
func registerTransientToken(r *Runtime, registry *finalizationRegistryObject, target *Object) weak.Pointer[Object] {
	token := r.NewObject()
	r.finalizationRegister(FunctionCall{This: registry.val, Arguments: []Value{target, valueInt(42), token}})
	return weak.Make(token)
}
func TestFinalizationTokenIsWeak(t *testing.T) {
	r := New()
	registry := newTestRegistry(t, r, func(FunctionCall) Value { return _undefined })
	target := r.NewObject()
	token := registerTransientToken(r, registry, target)
	for range 40 {
		runtime.GC()
		if token.Value() == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if token.Value() != nil {
		t.Fatal("registry retained unregister token")
	}
	runtime.KeepAlive(target)
	runtime.KeepAlive(registry)
}

func registerHeldObject(r *Runtime, registry *finalizationRegistryObject, target *Object) weak.Pointer[Object] {
	held := r.NewObject()
	held.Set("value", 42)
	r.finalizationRegister(FunctionCall{This: registry.val, Arguments: []Value{target, held}})
	return weak.Make(held)
}

func TestFinalizationHeldValueIsStrongUntilUnregister(t *testing.T) {
	r := New()
	registry := newTestRegistry(t, r, func(FunctionCall) Value { return _undefined })
	target := r.NewObject()
	held := registerHeldObject(r, registry, target)
	runtime.GC()
	if held.Value() == nil {
		t.Fatal("live registry lost held value")
	}
	for id, cell := range registry.cells {
		cell.cleanup.Stop()
		delete(registry.cells, id)
	}
	for range 40 {
		runtime.GC()
		if held.Value() == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if held.Value() != nil {
		t.Fatal("removed registration retained held value")
	}
	runtime.KeepAlive(target)
	runtime.KeepAlive(registry)
}

func TestRunJobsRequiresIdleRuntime(t *testing.T) {
	r := New()
	registry := NewRegistry()
	if err := registry.Register("nestedJobs", func() bool { return r.RunJobs() != nil }); err != nil {
		t.Fatal(err)
	}
	env, err := registry.Freeze(stdcontext.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = env.Bind(r); err != nil {
		t.Fatal(err)
	}
	value, err := r.RunString("nestedJobs();")
	if err != nil || !value.ToBoolean() {
		t.Fatalf("nested jobs: %v %v", value, err)
	}
	r.Interrupt("jobs cancelled")
	var interrupted *InterruptedError
	if err = r.RunJobs(); !errors.As(err, &interrupted) {
		t.Fatalf("job interrupt: %v", err)
	}
	if err = r.RunJobs(); err != nil {
		t.Fatal(err)
	}
}
