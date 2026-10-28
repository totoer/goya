package editor

import (
	"context"
	"reflect"
	"testing"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
)

func TestReferenceSelectionDoesNotRetainSyntheticParents(t *testing.T) {
	ctx := context.Background()
	w, err := NewWorkspace(ctx, map[string]string{"/main.ts": "const values = [1, 2, 3];\nfunction get() { return values[1]; }\n"}, nil, "", []string{"/main.ts"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx = lsproto.WithClientCapabilities(ctx, &lsproto.ResolvedClientCapabilities{})
	err = w.WithService(ctx, "/main.ts", func(ctx context.Context, service *ls.LanguageService) error {
		params := &lsproto.SelectionRangeParams{TextDocument: lsproto.TextDocumentIdentifier{Uri: "file:///main.ts"}, Positions: []lsproto.Position{{Line: 0, Character: 20}, {Line: 1, Character: 33}}}
		if _, err := service.ProvideReferenceSelectionRanges(ctx, params); err != nil {
			return err
		}
		file := service.GetProgram().GetSourceFile("/main.ts")
		count := reflect.ValueOf(file).Elem().FieldByName("tokenCache").Len()
		for i := 0; i < 100; i++ {
			if _, err := service.ProvideReferenceSelectionRanges(ctx, params); err != nil {
				return err
			}
		}
		after := reflect.ValueOf(file).Elem().FieldByName("tokenCache").Len()
		if after != count {
			t.Fatalf("selection retained request-local AST parents: token cache grew from %d to %d", count, after)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
