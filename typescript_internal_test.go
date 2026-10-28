package goya

import (
	stdcontext "context"
	"errors"
	"testing"
	"time"

	"github.com/totoer/goya/ast"
	"github.com/totoer/goya/internal/tsfrontend"
)

func TestCheckedNativeJobChainCanBeInterrupted(t *testing.T) {
	p := MustCompile(stdcontext.Background(), Project{Files: map[string]string{"/main.ts": `1;`}}, nil, nil)
	r := New()
	started := make(chan struct{})
	done := make(chan error, 1)
	var next func()
	next = func() { r.enqueuePromiseJob(next) }
	r.enqueuePromiseJob(func() { close(started); next() })
	go func() { _, err := r.RunProgram(p); done <- err }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		r.Interrupt("timeout")
		t.Fatal("job did not start")
	}
	r.Interrupt("native jobs")
	select {
	case err := <-done:
		var interrupted *InterruptedError
		if !errors.As(err, &interrupted) {
			t.Fatalf("native jobs: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("native jobs ignored interrupt")
	}
}

func TestCheckedBackendRejectsUnverifiedIR(t *testing.T) {
	for _, tree := range []*tsfrontend.ExecutionIR{nil, {}} {
		program, _, err := compileCheckedIR(tree, nil)
		if err == nil || program != nil {
			t.Fatal("unverified IR reached bytecode generation")
		}
	}
}

func TestRunProgramRejectsUncheckedOperations(t *testing.T) {
	r := New()
	for _, p := range []*Program{nil, {}, mustCompileVMFixture("unchecked.js", `throw "must not execute";`, false)} {
		value, err := r.RunProgram(p)
		if value != nil || err == nil {
			t.Fatalf("unchecked operations accepted: %v %v", value, err)
		}
		if _, ok := err.(*Exception); ok {
			t.Fatal("unchecked body executed")
		}
	}
}

func TestCheckedBytecodeDoesNotRetainMutableIR(t *testing.T) {
	analysis, ds, err := tsfrontend.Analyze(stdcontext.Background(), tsfrontend.Project{Files: map[string]string{"/main.ts": `const value: number = 3; value;`}}, nil, nil)
	if err != nil || analysis == nil {
		t.Fatalf("analysis: %v %+v", err, ds)
	}
	lowered, ds, err := analysis.Lower(stdcontext.Background())
	if err != nil || lowered == nil {
		t.Fatalf("lowering: %v %+v", err, ds)
	}
	program, ds, err := compileCheckedIR(lowered, nil)
	if err != nil || program == nil {
		t.Fatalf("bytecode: %v %+v", err, ds)
	}
	if err := lowered.Visit(func(module *tsfrontend.IRModule) error {
		declaration := module.Tree().Body[0].(*ast.LexicalDeclaration)
		declaration.List[0].Initializer.(*ast.NumberLiteral).Value = float64(99)
		module.Tree().Body = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := New().RunProgram(program)
	if err != nil || result.ToInteger() != 3 {
		t.Fatalf("mutated IR changed compiled code: %v %v", result, err)
	}
}

func TestNativeTypeScriptHelpersHaveImplementations(t *testing.T) {
	r := New()
	for _, name := range tsfrontend.NativeHelperNames() {
		if exception := r.Try(func() {
			if _, ok := AssertFunction(r.typeScriptHelper(name)); !ok {
				t.Errorf("helper %s is not callable", name)
			}
		}); exception != nil {
			t.Fatalf("helper registry drift: %s: %v", name, exception)
		}
	}
	for _, name := range r.GlobalObject().GetOwnPropertyNames() {
		if len(name) > 0 && name[0] == 0 {
			t.Fatal("private helper was installed as a global")
		}
	}
}
