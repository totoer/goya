package goya_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/totoer/goya"
)

type jsxProps struct {
	Value goya.Optional[int32] `ts:"value"`
}

const jsxDeclarations = `declare namespace JSX {type Element = string; interface IntrinsicElements {box: {value: number; children?: string | string[]};}}`

func TestTypeScriptRegisteredJSXFactoryModes(t *testing.T) {
	for _, mode := range []string{"react", "preserve", "react-native"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			registry := goya.NewRegistry()
			registry.SetFieldNameMapper(goya.TagFieldNameMapper("ts", false))
			registerHost(t, registry, "h", func(tag string, props *jsxProps, children ...string) string {
				calls++
				value := int32(0)
				if props != nil && props.Value.Present {
					value = props.Value.Value
				}
				return fmt.Sprintf("%s:%d:%s", tag, value, strings.Join(children, "|"))
			})
			registerHost(t, registry, "fragment", "fragment")
			if err := registry.DeclareTypes("/jsx.d.ts", jsxDeclarations); err != nil {
				t.Fatal(err)
			}
			environment := freezeHost(t, registry)
			program, ds, err := goya.CompileProject(context.Background(), goya.Project{Files: map[string]string{"/main.tsx": `const node = <><box value={42}/><box value={3}/></>; export const result = node === "fragment:0:box:42:|box:3:";`}}, environment, goya.CompileOptions{"jsx": mode, "jsxFactory": "h", "jsxFragmentFactory": "fragment"})
			if err != nil || program == nil {
				t.Fatalf("JSX factory compile: %v %+v", err, ds)
			}
			if calls != 0 {
				t.Fatal("compilation invoked JSX factory")
			}
			r := goya.New()
			if err := environment.Bind(r); err != nil {
				t.Fatal(err)
			}
			value, err := r.RunProgram(program)
			if err != nil || !hostObject(t, value).Get("result").ToBoolean() || calls != 3 {
				t.Fatalf("JSX factory execution: %v calls=%d", err, calls)
			}
			invalid, ds, err := goya.CompileProject(context.Background(), goya.Project{Files: map[string]string{"/main.tsx": `export const node = <box value="wrong"/>;`}}, environment, goya.CompileOptions{"jsx": mode, "jsxFactory": "h"})
			if err != nil || invalid != nil || !goya.HasTypeErrors(ds) || calls != 3 {
				t.Fatal("invalid JSX props created a program or invoked Go")
			}
		})
	}
}

func TestTypeScriptJSXMissingFactoryRejectedBeforeEffects(t *testing.T) {
	calls := 0
	registry := goya.NewRegistry()
	registerHost(t, registry, "notify", func() { calls++ })
	if err := registry.DeclareTypes("/jsx.d.ts", jsxDeclarations); err != nil {
		t.Fatal(err)
	}
	environment := freezeHost(t, registry)
	program, ds, err := goya.CompileProject(context.Background(), goya.Project{Files: map[string]string{"/main.tsx": `notify(); export const node = <box value={3}/>;`}}, environment, goya.CompileOptions{"jsx": "preserve"})
	if err != nil || program == nil {
		t.Fatalf("checking preserved JSX: %v %+v", err, ds)
	}
	r := goya.New()
	if err := environment.Bind(r); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunProgram(program); err == nil || calls != 0 {
		t.Fatal("missing JSX runtime detected after effects")
	}
}

func TestTypeScriptAutomaticJSXRuntimeModules(t *testing.T) {
	for _, mode := range []string{"react-jsx", "react-jsxdev"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			registry := goya.NewRegistry()
			adapter := goya.HostAdapterFuncs{ID: "jsx-runtime/v1", BindValue: func(r *goya.Runtime, _ any) (goya.Value, error) {
				return r.ToValue(func(call goya.FunctionCall) goya.Value {
					if len(call.Arguments) < 2 || !goya.IsString(call.Argument(0)) {
						panic(r.NewTypeError("expected a tag and props"))
					}
					props, ok := call.Argument(1).(*goya.Object)
					if !ok {
						panic(r.NewTypeError("expected props object"))
					}
					calls++
					return r.ToValue(call.Argument(0).String() + ":" + props.Get("value").String())
				}), nil
			}}
			exports := map[string]any{"jsx": goya.HostBinding{TypeScript: "(tag: string,props: {value: number},key?: unknown) => string", Adapter: adapter}, "jsxs": goya.HostBinding{TypeScript: "(tag: string,props: {value: number},key?: unknown) => string", Adapter: adapter}, "jsxDEV": goya.HostBinding{TypeScript: "(...args: any[]) => string", Adapter: adapter}}
			specifier := "view/jsx-runtime"
			if mode == "react-jsxdev" {
				specifier = "view/jsx-dev-runtime"
			}
			if err := registry.RegisterModule(specifier, exports); err != nil {
				t.Fatal(err)
			}
			if err := registry.DeclareTypes("/jsx.d.ts", fmt.Sprintf(`declare module %q {export namespace JSX {type Element = string; interface IntrinsicElements {box: {value: number};}}}`, specifier)); err != nil {
				t.Fatal(err)
			}
			environment := freezeHost(t, registry)
			program, ds, err := goya.CompileProject(context.Background(), goya.Project{Files: map[string]string{"/main.tsx": `export const result = <box value={42}/> === "box:42";`}}, environment, goya.CompileOptions{"jsx": mode, "jsxImportSource": "view"})
			if err != nil || program == nil {
				t.Fatalf("automatic JSX compile: %v %+v", err, ds)
			}
			r := goya.New()
			if err := environment.Bind(r); err != nil {
				t.Fatal(err)
			}
			value, err := r.RunProgram(program)
			if err != nil || !hostObject(t, value).Get("result").ToBoolean() || calls != 1 {
				t.Fatalf("automatic JSX execution: %v calls=%d", err, calls)
			}
		})
	}
}
