package goya_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/totoer/goya"
)

func TestTypeScriptSourceStacksAfterLowering(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		line         int
	}{
		{"class initializer", "interface Data{n:number}\nclass Value {\n n=(()=>{throw new Error('field')})();\n}\nnew Value();", 3},
		{"decorator", "function fail(_value:Function,_context:ClassDecoratorContext){\n throw new Error('decorator');\n}\n@fail\nclass Value{}", 2},
		{"async", "export async function fail(){\n await Promise.resolve();\n throw new Error('async');\n}", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := compileTypeScript(t, tc.source, nil)
			r := goya.New()
			v, err := r.RunProgram(p)
			if tc.name == "async" {
				if err != nil {
					t.Fatal(err)
				}
				fn, _ := goya.AssertFunction(hostObject(t, v).Get("fail"))
				v, err = fn(goya.Undefined())
				if err != nil {
					t.Fatal(err)
				}
				promise := v.Export().(*goya.Promise)
				if promise.State() != goya.PromiseStateRejected {
					t.Fatal("async did not reject")
				}
				stack := hostObject(t, promise.Result()).Get("stack").String()
				if !strings.Contains(stack, "/main.ts:3:") {
					t.Fatalf("async source stack: %s", stack)
				}
				return
			}
			var ex *goya.Exception
			if !errors.As(err, &ex) || len(ex.Stack()) == 0 {
				t.Fatalf("exception: %v", err)
			}
			pos := ex.Stack()[0].Position()
			if pos.Filename != "/main.ts" || pos.Line != tc.line {
				t.Fatalf("source stack: %v", ex)
			}
		})
	}
	p := compileModules(t, map[string]string{"/main.ts": `import "./dependency.js";export {};`, "/dependency.ts": "interface Data{n:number}\nthrow new Error('dependency');"}, nil)
	_, err := goya.New().RunProgram(p)
	var ex *goya.Exception
	if !errors.As(err, &ex) || len(ex.Stack()) == 0 || ex.Stack()[0].Position().Filename != "/dependency.ts" || ex.Stack()[0].Position().Line != 2 {
		t.Fatalf("module source stack: %v", err)
	}
	unicodeSource := `const label="😀я";throw new Error("unicode");`
	p = goya.MustCompile(context.Background(), goya.Project{Files: map[string]string{"/unicode.ts": unicodeSource}}, nil, nil)
	_, err = goya.New().RunProgram(p)
	if !errors.As(err, &ex) {
		t.Fatal(err)
	}
	pos := ex.Stack()[0].Position()
	want := len(utf16.Encode([]rune(unicodeSource[:strings.Index(unicodeSource, "new Error")]))) + 1
	if pos.Filename != "/unicode.ts" || pos.Line != 1 || pos.Column != want {
		t.Fatalf("UTF16 stack column: %+v want %d", pos, want)
	}
}
