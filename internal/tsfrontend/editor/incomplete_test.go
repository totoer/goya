package editor_test

import (
	"context"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/internal/tsfrontend/editor"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
)

func TestEditorProgramRemainsUsableWithSyntaxErrors(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, source string
		options      goya.CompileOptions
	}{
		{"/main.ts", `const item={name:"value"};item.`, nil},
		{"/main.tsx", `declare namespace JSX {interface IntrinsicElements {panel:any}} const item={name:"value"};const view=<panel label={item.`, goya.CompileOptions{"jsx": "preserve"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{tc.name: tc.source}
			w, err := editor.NewWorkspace(ctx, files, tc.options, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			var list *lsproto.CompletionList
			err = w.WithService(ctx, tc.name, func(ctx context.Context, service *ls.LanguageService) error {
				result, err := service.ProvideCompletion(ctx, lsproto.DocumentUri("file://"+tc.name), lsproto.Position{Character: uint32(len(tc.source))}, nil)
				list = result.List
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			if list != nil {
				for _, item := range list.Items {
					if item.Label == "name" {
						found = true
					}
				}
			}
			if !found {
				t.Fatalf("incomplete %s lost member completion: %+v", tc.name, list)
			}
			ds, err := w.CheckDiagnostics(ctx, tc.name)
			if err != nil || !goya.HasTypeErrors(ds) {
				t.Fatalf("editor lost diagnostics: %v %+v", err, ds)
			}
			p, compileDiagnostics, err := goya.Compile(ctx, goya.Project{Files: files}, nil, tc.options)
			if err != nil || p != nil || !goya.HasTypeErrors(compileDiagnostics) {
				t.Fatalf("editor bypassed compilation gate: %v %+v", err, compileDiagnostics)
			}
		})
	}
}
