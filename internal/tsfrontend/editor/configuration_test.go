package editor_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/totoer/goya/internal/tsfrontend"
	"github.com/totoer/goya/internal/tsfrontend/editor"
)

func TestEditorAndCheckNormalizeTheSameConfiguration(t *testing.T) {
	ctx := context.Background()
	for _, options := range []tsfrontend.Options{nil, {"strict": false}, {"strict": true}} {
		files := map[string]string{
			"/base.json":   `{"compilerOptions":{"strict":false}}`,
			"/config.json": `{"extends":"./base.json","files":["main.ts"]}`,
			"/main.ts":     `declare function host(value:number):number; function f(value){return value;} host("wrong"); document.title;`,
		}
		w, err := editor.NewWorkspace(ctx, files, options, "/config.json", nil)
		if err != nil {
			t.Fatal(err)
		}
		editorDiagnostics, err := w.CheckDiagnostics(ctx, "/main.ts")
		w.Close()
		if err != nil {
			t.Fatal(err)
		}
		checkDiagnostics, err := tsfrontend.Check(ctx, tsfrontend.Project{ConfigFile: "/config.json", Files: files}, nil, options)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(editorDiagnostics, checkDiagnostics) {
			t.Fatalf("options=%v editor=%+v Check=%+v", options, editorDiagnostics, checkDiagnostics)
		}
	}
}
