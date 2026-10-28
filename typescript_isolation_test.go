package goya_test

import (
	"context"
	"sync"
	"testing"

	"github.com/totoer/goya"
)

type isolationAccount struct{ Value int32 }

func (a *isolationAccount) Bump() int32 { a.Value++; return a.Value }

func TestTypeScriptParallelModuleAndHostViewIsolation(t *testing.T) {
	makeEnv := func(seed int32) (*goya.Environment, *isolationAccount) {
		a := &isolationAccount{Value: seed}
		registry := goya.NewRegistry()
		registerHost(t, registry, "account", a)
		return freezeHost(t, registry), a
	}
	compileEnv, _ := makeEnv(0)
	p := goya.MustCompile(context.Background(), goya.Project{Files: map[string]string{"/main.ts": `export let count=0;export const view=account;export function next(){count++;return account!.Bump()+count}`}}, compileEnv, nil)
	var group sync.WaitGroup
	for i := range 5 {
		group.Go(func() {
			env, account := makeEnv(int32(i * 10))
			r := goya.New()
			if err := env.Bind(r); err != nil {
				t.Error(err)
				return
			}
			value, err := r.RunProgram(p)
			if err != nil {
				t.Error(err)
				return
			}
			namespace := hostObject(t, value)
			if namespace.Get("view") != r.Get("account") {
				t.Error("host view identity lost")
			}
			fn, _ := goya.AssertFunction(namespace.Get("next"))
			first, err := fn(goya.Undefined())
			if err != nil || first.ToInteger() != int64(i*10+2) {
				t.Errorf("first: %v %v", first, err)
				return
			}
			again, err := r.RunProgram(p)
			if err != nil || again != value {
				t.Errorf("instance identity: %v", err)
				return
			}
			second, err := fn(goya.Undefined())
			if err != nil || second.ToInteger() != int64(i*10+4) || account.Value != int32(i*10+2) || namespace.Get("count").ToInteger() != 2 {
				t.Errorf("isolated state: %v %v %+v", second, err, account)
			}
		})
	}
	group.Wait()
}

func TestTypeScriptParallelStandardLibraryIsolation(t *testing.T) {
	program := goya.MustCompile(context.Background(), goya.Project{Files: map[string]string{"/main.ts": `
 const format=new Intl.NumberFormat("de",{minimumFractionDigits:2});
 const segmenter=new Intl.Segmenter("en",{granularity:"grapheme"});
 const pattern=new RegExp("[[a-z]--[aeiou]]+","vi");
 const matches=pattern.exec("BBA");
 export const valid=format.format(1234.5)==="1.234,50" &&
  [...segmenter.segment("👩‍💻a")].length===2 && matches?.[0]==="BB";
 export let count=0;
 export function next(){return ++count;}
 `}}, nil, nil)
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			for range 3 {
				runtime := goya.New()
				value, err := runtime.RunProgram(program)
				if err != nil {
					t.Error(err)
					return
				}
				namespace := hostObject(t, value)
				if !namespace.Get("valid").ToBoolean() {
					t.Error("standard library state differs between runtimes")
					return
				}
				next, _ := goya.AssertFunction(namespace.Get("next"))
				for expected := int64(1); expected <= 2; expected++ {
					result, err := next(goya.Undefined())
					if err != nil || result.ToInteger() != expected {
						t.Errorf("module state: %v %v want %d", result, err, expected)
						return
					}
				}
			}
		})
	}
	group.Wait()
}
