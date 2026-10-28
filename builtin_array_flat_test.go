package goya

import (
	stdcontext "context"
	"errors"
	"os"
	"os/exec"
	"runtime/debug"
	"testing"
	"time"
)

// Keep fatal Go stack overflows isolated from the test runner. Compile and
// construct inputs before reducing the Go stack so only execution is measured.
func TestArrayFlatStackSafety(t *testing.T) {
	const helperEnv = "GOYA_TEST_ARRAY_FLAT_STACK"
	if os.Getenv(helperEnv) != "1" {
		ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestArrayFlatStackSafety$", "-test.v")
		cmd.Env = append(os.Environ(), helperEnv+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("flat stack safety: %v\n%s", err, output)
		}
		return
	}

	for _, tc := range []struct {
		name, setup string
		limit       int
		overflow    bool
	}{
		{"self cycle", `var a = []; a[0] = a;`, 16, true},
		{"mutual cycle", `var a = [], b = [a]; a[0] = b;`, 16, true},
		{"default limit cycle", `var a = []; a[0] = a;`, 0, true},
		{"deep configured limit", `var a = [42]; for (var i = 0; i < 100; i++) a = [a];`, 16, true},
		{"deep iterative traversal", `var a = [42]; for (var i = 0; i < 5000; i++) a = [a];`, 0, false},
		{"deep native limit", `var a = [42]; for (var i = 0; i < 10001; i++) a = [a];`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			if _, err := r.runVMString(tc.setup); err != nil {
				t.Fatal(err)
			}
			program := mustCompileVMFixture("flat.js", `try { a.flat(Infinity); } catch (_) { 0; }`, false)
			if tc.limit != 0 {
				r.SetMaxCallStackSize(tc.limit)
			}
			old := debug.SetMaxStack(256 * 1024)
			defer debug.SetMaxStack(old)
			v, err := r.runVMProgram(program)
			if tc.overflow {
				var overflow *StackOverflowError
				if !errors.As(err, &overflow) || len(overflow.Stack()) == 0 {
					t.Fatalf("expected StackOverflowError with stack, got %v", err)
				}
			} else if err != nil || v.(*Object).Get("0").ToInteger() != 42 || v.(*Object).Get("length").ToInteger() != 1 {
				t.Fatalf("deep flat result: %v, %v", v, err)
			}
			if v, err := r.runVMString(`1 + 1`); err != nil || v.ToInteger() != 2 {
				t.Fatalf("runtime reuse: %v, %v", v, err)
			}
		})
	}
}

func TestArrayFlatInterrupt(t *testing.T) {
	for _, method := range []string{"flat", "flatMap"} {
		for _, nested := range []bool{false, true} {
			name := method + "/sparse"
			if nested {
				name = method + "/nested"
			}
			t.Run(name, func(t *testing.T) {
				r := New()
				a := r.NewArray()
				// The interrupt originates in a native getter. No JS bytecode
				// runs between setting it and continuing the native traversal.
				if err := a.DefineAccessorProperty("0", r.ToValue(func() Value {
					r.Interrupt("cancel flat")
					return intToValue(42)
				}), nil, FLAG_TRUE, FLAG_TRUE); err != nil {
					t.Fatal(err)
				}
				if err := a.Set("length", 1<<30); err != nil {
					t.Fatal(err)
				}
				if nested {
					a = r.NewArray(a)
				}
				fn, ok := AssertFunction(a.Get(method))
				if !ok {
					t.Fatal("missing method")
				}
				arg := r.ToValue(func(call FunctionCall) Value { return call.Argument(0) })
				if method == "flat" {
					arg = _positiveInf
				}
				_, err := fn(a, arg)
				var interrupted *InterruptedError
				if !errors.As(err, &interrupted) || interrupted.Value() != "cancel flat" {
					t.Fatalf("expected InterruptedError, got %v", err)
				}
				r.ClearInterrupt()
				if v, err := r.runVMString(`[1, [2]].flat().join()`); err != nil || v.String() != "1,2" {
					t.Fatalf("runtime reuse: %v, %v", v, err)
				}
			})
		}
	}
}

func TestArrayFlatCheckedTypeScript(t *testing.T) {
	r := New()
	r.SetMaxCallStackSize(16)
	_, err := r.RunString(`const a: any[] = []; a[0] = a; a.flat(Infinity);`)
	var overflow *StackOverflowError
	if !errors.As(err, &overflow) {
		t.Fatalf("expected StackOverflowError, got %v", err)
	}
	if v, err := r.RunString(`[1, [2, [3]]].flat(Infinity).join()`); err != nil || v.String() != "1,2,3" {
		t.Fatalf("runtime reuse after checked execution: %v, %v", v, err)
	}
}

func TestArrayFlatTraversalSemantics(t *testing.T) {
	const script = `
	// Repeated references must be visited again; finite cycles are valid.
	var shared = [1, , 2];
	assert.sameValue([shared, shared].flat(Infinity).join(), "1,2,1,2");
	var cycle = [1]; cycle[1] = cycle;
	var finite = cycle.flat(2);
	assert.sameValue(finite.length, 4);
	assert.sameValue(finite[3], cycle);
	assert.sameValue(cycle.flat(0)[1], cycle);
	assert.sameValue(cycle.flat(-1)[1], cycle);
	// Accessors can break a cycle, so identity alone cannot reject one.
	var reads = 0, dynamic = [];
	Object.defineProperty(dynamic, "0", { get() { return ++reads < 4 ? dynamic : 42; } });
	assert.sameValue(dynamic.flat(Infinity).join(), "42");
	assert.sameValue(reads, 4);
	// Read lengths on descent, snapshot them, then resume the parent in order.
	var log = [], child = [1, 2], parent = [child, 3];
	Object.defineProperty(child, "0", { get() {
		log.push("child"); child.push(4); parent[1] = 5; return 1;
	} });
	Object.defineProperty(parent, "1", { get() { log.push("parent"); return 5; } });
	assert.sameValue(parent.flat(Infinity).join(), "1,2,5");
	assert.sameValue(log.join(), "child,parent");
	// Only root elements are mapped, with the original arguments and this.
	var context = {}, calls = [], input = [1, , 2];
	var result = input.flatMap(function(value, index, source) {
		assert.sameValue(this, context);
		assert.sameValue(source, input);
		calls.push(index);
		return [value, , [value + 10]];
	}, context);
	assert.sameValue(calls.join(), "0,2");
	assert.sameValue(result.length, 4);
	assert.sameValue(result[0], 1);
	assert.sameValue(result[1][0], 11);
	assert.sameValue(result[2], 2);
	assert.sameValue(result[3][0], 12);
	// Species writes remain interleaved with source reads.
	var events = [], source = [[1, 2], 3];
	Object.defineProperty(source, "1", { get() { events.push("read"); return 3; } });
	source.constructor = { [Symbol.species]: function() {
		return new Proxy({}, { defineProperty(target, key, descriptor) {
			events.push(key + ":" + descriptor.value);
			return Reflect.defineProperty(target, key, descriptor);
		} });
	} };
	var output = source.flat(Infinity);
	assert.sameValue(events.join(), "0:1,1:2,read,2:3");
	assert.sameValue(output[2], 3);
	`
	testScriptWithTestLib(script, _undefined, t)
}
