package goya_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/totoer/goya"
)

func TestTypeScriptInterruptedPendingModuleCannotResume(t *testing.T) {
	var resolve func(interface{}) error
	after := 0
	registry := goya.NewRegistry()
	registerHost(t, registry, "after", func() { after++ })
	if err := registry.Register("gate", goya.HostBinding{TypeScript: "Promise<void>", Adapter: goya.HostAdapterFuncs{ID: "pending-promise/v1", BindValue: func(r *goya.Runtime, _ any) (goya.Value, error) {
		p, fulfill, _ := r.NewPromise()
		resolve = fulfill
		return r.ToValue(p), nil
	}}}); err != nil {
		t.Fatal(err)
	}
	env := freezeHost(t, registry)
	p := goya.MustCompile(context.Background(), goya.Project{Files: map[string]string{"/pending.ts": `await gate;after();export {};`}}, env, nil)
	r := goya.New()
	if err := env.Bind(r); err != nil {
		t.Fatal(err)
	}
	v, err := r.RunProgram(p)
	if err != nil || v.Export().(*goya.Promise).State() != goya.PromiseStatePending {
		t.Fatalf("pending: %v %v", v, err)
	}
	r.Interrupt("cancel pending graph")
	_, err = r.RunString(`1;`)
	var interrupted *goya.InterruptedError
	if !errors.As(err, &interrupted) {
		t.Fatalf("interrupt: %v", err)
	}
	if err := resolve(goya.Undefined()); err != nil {
		t.Fatal(err)
	}
	if _, err = r.RunString(`1;`); err != nil || after != 0 {
		t.Fatalf("cancelled continuation resumed: %v effects %d", err, after)
	}
	_, err = r.RunProgram(p)
	if !errors.As(err, &interrupted) {
		t.Fatalf("interrupted pending instance reused: %v", err)
	}
}

func TestTypeScriptInterruptAndModuleLifecycle(t *testing.T) {
	for _, source := range []string{
		`started();for(;;){}`,
		`started();for(;;){}export {};`,
		`started();for(;;){await Promise.resolve();}export {};`,
		`started();function next():void{Promise.resolve().then(next)}next();export {};`,
	} {
		t.Run(source, func(t *testing.T) {
			started := make(chan struct{})
			done := make(chan error, 1)
			registry := goya.NewRegistry()
			registerHost(t, registry, "started", func() { close(started) })
			env := freezeHost(t, registry)
			program := compileTypeScript(t, source, env)
			r := goya.New()
			if err := env.Bind(r); err != nil {
				t.Fatal(err)
			}
			go func() {
				defer func() {
					if p := recover(); p != nil {
						done <- fmt.Errorf("uncaught panic: %v", p)
					}
				}()
				_, err := r.RunProgram(program)
				done <- err
			}()
			select {
			case <-started:
			case err := <-done:
				t.Fatalf("stopped before start: %v", err)
			case <-time.After(10 * time.Second):
				r.Interrupt("start timeout")
				t.Fatal("did not start")
			}
			r.Interrupt("application cancellation")
			select {
			case err := <-done:
				var interrupted *goya.InterruptedError
				if !errors.As(err, &interrupted) || interrupted.Value() != "application cancellation" {
					t.Fatalf("interrupt: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("interrupt did not stop execution")
			}
			if strings.Contains(source, "export") && strings.Contains(source, "for(") {
				_, err := r.RunProgram(program)
				var interrupted *goya.InterruptedError
				if !errors.As(err, &interrupted) {
					t.Fatalf("interrupted module reused as successful/pending: %v", err)
				}
			}
			v, err := r.RunProgram(compileTypeScript(t, `1+1;`, nil))
			if err != nil || v.ToInteger() != 2 {
				t.Fatalf("healthy program after interrupt: %v %v", v, err)
			}
		})
	}
}
