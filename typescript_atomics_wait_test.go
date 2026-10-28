package goya

import (
	stdcontext "context"
	"errors"
	"runtime"
	"testing"
	"time"
	"weak"
)

func TestCheckedAtomicsWaitValidation(t *testing.T) {
	for name, source := range map[string]string{
		"immediate":                   `const a=new Int32Array(new SharedArrayBuffer(8));const b=new BigInt64Array(new SharedArrayBuffer(8));Atomics.wait(a,0,1)==="not-equal"&&Atomics.wait(a,0,0,0)==="timed-out"&&Atomics.wait(b,0,1n)==="not-equal"&&Atomics.wait(b,0,0n,-Infinity)==="timed-out";`,
		"async immediate":             `const a=new Int32Array(new SharedArrayBuffer(4));const one=Atomics.waitAsync(a,0,1);const two=Atomics.waitAsync(a,0,0,0);!one.async&&one.value==="not-equal"&&!two.async&&two.value==="timed-out"&&Object.keys(one).join(",")==="async,value";`,
		"shared before index":         `let effects=0,errors=0;const index={valueOf(){effects++;return 0}};for(const f of [()=>Atomics.wait(new Int32Array(1),index as any,0),()=>Atomics.waitAsync(new Int32Array(1),index as any,0)]){try{f()}catch(e){if(e instanceof TypeError)errors++}}errors===2&&effects===0;`,
		"wait kinds":                  `let errors=0;for(const a of [new Uint32Array(new SharedArrayBuffer(4)),new Int16Array(new SharedArrayBuffer(4)),new BigUint64Array(new SharedArrayBuffer(8)),new Proxy(new Int32Array(new SharedArrayBuffer(4)),{})]){for(const f of [()=>Atomics.wait(a as any,0,0,0),()=>Atomics.waitAsync(a as any,0,0,0),()=>Atomics.notify(a as any,0)]){try{f()}catch(e){if(e instanceof TypeError)errors++}}}errors===12;`,
		"notify ordinary conversions": `const log:string[]=[];const b=new ArrayBuffer(8,{maxByteLength:8});const a=new Int32Array(b);const n=Atomics.notify(a,{valueOf(){log.push("index");return 1}} as any,{valueOf(){log.push("count");b.resize(0);return 1}} as any);n===0&&log.join(",")==="index,count";`,
		"wait conversions":            `const log:string[]=[];const a=new Int32Array(new SharedArrayBuffer(4));const value=Atomics.wait(a,{valueOf(){log.push("index");return 0}} as any,{valueOf(){log.push("value");return 1}} as any,{valueOf(){log.push("timeout");return NaN}} as any);value==="not-equal"&&log.join(",")==="index,value,timeout";`,
		"growth length witness":       `const b=new SharedArrayBuffer(4,{maxByteLength:8});let effects=0,rejected=false;try{Atomics.wait(new Int32Array(b),{valueOf(){b.grow(8);return 1}} as any,{valueOf(){effects++;return 0}} as any,0)}catch(e){rejected=e instanceof RangeError}rejected&&effects===0;`,
		"descriptors":                 `Atomics.wait.length===4&&Atomics.waitAsync.length===4&&Atomics.notify.length===3&&Object.getOwnPropertyDescriptor(Atomics,"wait")!.enumerable===false;`,
	} {
		t.Run(name, func(t *testing.T) {
			p, ds, err := Compile(stdcontext.Background(), Project{Files: map[string]string{"/main.ts": source}}, nil, CompileOptions{"lib": []string{"esnext"}})
			if err != nil || p == nil {
				t.Fatalf("wait check: %v %+v", err, ds)
			}
			value, err := New().RunProgram(p)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("wait: %v %v", value, err)
			}
		})
	}
}

func TestAtomicsSameAgentNotifyOrdering(t *testing.T) {
	r := New()
	p := MustCompile(stdcontext.Background(), Project{Files: map[string]string{"/main.ts": `const a=new Int32Array(new SharedArrayBuffer(4));const results:string[]=[];const first=Atomics.waitAsync(a,0,0);const second=Atomics.waitAsync(a,0,0);if(!first.async||!second.async)throw Error("async");first.value.then(v=>results.push("first:"+v));second.value.then(v=>results.push("second:"+v));if(Atomics.notify(a,0,1)!==1)throw Error("count");Promise.resolve().then(()=>results.push("promise"));if(Atomics.notify(a,0)!==1||Atomics.notify(a,0)!==0)throw Error("remaining");results;`}}, nil, nil)
	value, err := r.RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := value.String(); got != "first:ok,promise,second:ok" {
		t.Fatalf("notify job order: %s", got)
	}
	if len(r.waitPromises) != 0 {
		t.Fatal("settled capabilities retained")
	}
}

func atomicsWaitEnvironment(t *testing.T, shared SharedArrayBuffer) *Environment {
	t.Helper()
	registry := NewRegistry()
	if err := registry.Register("shared", HostBinding{Value: shared, TypeScript: "SharedArrayBuffer", Adapter: HostAdapterFuncs{ID: "wait-storage/v1", BindValue: func(r *Runtime, value any) (Value, error) { return r.ToValue(value.(SharedArrayBuffer)), nil }}}); err != nil {
		t.Fatal(err)
	}
	env, err := registry.Freeze(stdcontext.Background())
	if err != nil {
		t.Fatal(err)
	}
	return env
}
func awaitWaiters(t *testing.T, shared SharedArrayBuffer, count int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		shared.storage.waitMu.Lock()
		n := len(shared.storage.waiters[0])
		shared.storage.waitMu.Unlock()
		if n == count {
			return
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("waiter registration did not reach %d", count)
}

func TestAtomicsWaitIndependentGoAgents(t *testing.T) {
	shared, err := NewSharedArrayBuffer(8)
	if err != nil {
		t.Fatal(err)
	}
	env := atomicsWaitEnvironment(t, shared)
	waiterProgram := MustCompile(stdcontext.Background(), Project{Files: map[string]string{"/wait.ts": `Atomics.wait(new Int32Array(shared),0,0);`}}, env, nil)
	notifier := New()
	if err = env.Bind(notifier); err != nil {
		t.Fatal(err)
	}
	done := make([]chan string, 3)
	for i := range done {
		r := New()
		if err = env.Bind(r); err != nil {
			t.Fatal(err)
		}
		done[i] = make(chan string, 1)
		go func() {
			value, err := r.RunProgram(waiterProgram)
			if err != nil {
				done[i] <- err.Error()
			} else {
				done[i] <- value.String()
			}
		}()
		awaitWaiters(t, shared, i+1)
	}
	for i := range done {
		value, err := notifier.RunString("Atomics.notify(new BigInt64Array(shared),0,1);")
		if err != nil || value.ToInteger() != 1 {
			t.Fatalf("notify: %v %v", value, err)
		}
		select {
		case result := <-done[i]:
			if result != "ok" {
				t.Fatal(result)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("FIFO waiter was not woken")
		}
	}
	awaitWaiters(t, shared, 0)
}

func TestAtomicsWaitInterruptAndReuse(t *testing.T) {
	shared, err := NewSharedArrayBuffer(4)
	if err != nil {
		t.Fatal(err)
	}
	env := atomicsWaitEnvironment(t, shared)
	r := New()
	if err = env.Bind(r); err != nil {
		t.Fatal(err)
	}
	p := MustCompile(stdcontext.Background(), Project{Files: map[string]string{"/wait.ts": `Atomics.wait(new Int32Array(shared),0,0);`}}, env, nil)
	done := make(chan error, 1)
	go func() { _, err := r.RunProgram(p); done <- err }()
	awaitWaiters(t, shared, 1)
	r.Interrupt("blocked wait")
	select {
	case err := <-done:
		var interrupted *InterruptedError
		if !errors.As(err, &interrupted) || interrupted.Value() != "blocked wait" {
			t.Fatalf("interrupt: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("blocked wait ignored interrupt")
	}
	awaitWaiters(t, shared, 0)
	value, err := r.RunString("40+2;")
	if err != nil || value.ToInteger() != 42 {
		t.Fatalf("reuse: %v %v", value, err)
	}
}

func TestAtomicsWaitAsyncOwnerDelivery(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "notify", true: "timeout"}[timeout], func(t *testing.T) {
			shared, err := NewSharedArrayBuffer(4)
			if err != nil {
				t.Fatal(err)
			}
			env := atomicsWaitEnvironment(t, shared)
			r := New()
			if err = env.Bind(r); err != nil {
				t.Fatal(err)
			}
			duration := "Infinity"
			expected := "ok"
			if timeout {
				duration = "20"
				expected = "timed-out"
			}
			p := MustCompile(stdcontext.Background(), Project{Files: map[string]string{"/wait.ts": `const results:string[]=[];const w=Atomics.waitAsync(new Int32Array(shared),0,0,` + duration + `);if(!w.async)throw Error("expected async");w.value.then(v=>results.push(v));results;`}}, env, nil)
			value, err := r.RunProgram(p)
			if err != nil {
				t.Fatal(err)
			}
			if !timeout {
				notifier := New()
				if err = env.Bind(notifier); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { _, err := notifier.RunString("Atomics.notify(new Int32Array(shared),0);"); done <- err }()
				if err = <-done; err != nil {
					t.Fatal(err)
				}
			}
			awaitWaiters(t, shared, 0)
			if value.String() != "" {
				t.Fatal("another agent executed the owner continuation")
			}
			if err = r.RunJobs(); err != nil {
				t.Fatal(err)
			}
			if got := value.String(); got != expected {
				t.Fatalf("owner delivery: %q want %q", got, expected)
			}
		})
	}
}

func newAbandonedWaitAgent(t *testing.T, shared SharedArrayBuffer) weak.Pointer[Runtime] {
	t.Helper()
	env := atomicsWaitEnvironment(t, shared)
	r := New()
	if err := env.Bind(r); err != nil {
		t.Fatal(err)
	}
	p := MustCompile(stdcontext.Background(), Project{Files: map[string]string{"/wait.ts": `Atomics.waitAsync(new Int32Array(shared),0,0);`}}, env, nil)
	if _, err := r.RunProgram(p); err != nil {
		t.Fatal(err)
	}
	return weak.Make(r)
}
func TestAtomicsWaitAsyncDoesNotRetainAgent(t *testing.T) {
	shared, err := NewSharedArrayBuffer(4)
	if err != nil {
		t.Fatal(err)
	}
	agent := newAbandonedWaitAgent(t, shared)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		runtime.Gosched()
		shared.storage.waitMu.Lock()
		n := len(shared.storage.waiters[0])
		shared.storage.waitMu.Unlock()
		if agent.Value() == nil && n == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("waiter retained an abandoned agent or its shared-memory registration")
}
