package lsp

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestExtractSymbolRenameAfterAcceptedUnicodeHostChanges(t *testing.T) {
	for _, test := range []struct {
		kind, action, newName string
		index                 int
	}{
		{"refactor.extract.function", "function_scope_1", "newFunction", 1},
		{"refactor.extract.constant", "constant_scope_0", "newLocal", 0},
	} {
		t.Run(test.action, func(t *testing.T) {
			ctx := context.Background()
			text := "// 😀 extraction\r\nexport function run(input: number) {\r\n    return input\u2028+\u20291;\r\n}\r\n"
			project := goya.Project{Files: map[string]string{"/main.ts": text}, EntryPoints: []string{"/main.ts"}}
			s, err := NewSession(ctx, SessionOptions{Project: project, CompilerOptions: goya.CompileOptions{"strict": true, "target": "es2025", "module": "esnext", "moduleResolution": "bundler"}, DocumentURIs: map[string]protocol.DocumentURI{"/main.ts": "catalog:///user/main.ts"}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			offset := strings.Index(text, "input\u2028")
			start, _ := PositionAt(text, offset)
			end, _ := PositionAt(text, strings.Index(text, "1;")+1)
			kind := protocol.CodeActionKind(test.kind)
			choices, err := s.ApplicableRefactors(ctx, "/main.ts", protocol.Range{Start: start, End: end}, &RefactorRequestOptions{Kind: &kind})
			if err != nil || len(choices.Refactors) != 1 || len(choices.Refactors[0].Actions) <= test.index {
				t.Fatalf("choices: %+v %v", choices, err)
			}
			action := choices.Refactors[0].Actions[test.index]
			if action.Name != test.action || action.Range == nil || action.Range.End != end {
				t.Fatalf("affected source coordinates: %+v", action)
			}
			first, err := OffsetAt(text, action.Range.Start)
			if err != nil || strings.TrimSpace(text[first:offset]) != "" || action.Range.Start.Line != start.Line {
				t.Fatalf("unstable Unicode source range: %+v %v", action.Range, err)
			}
			// The public result is owned by the caller, including nested coordinates.
			action.Range.End = protocol.Position{}
			before, err := s.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.ApplyRefactor(ctx, RefactorSelection{Handle: choices.Handle, ActionIndex: test.index}, nil)
			if err != nil || result.Edits == nil || result.Rename == nil {
				t.Fatalf("extraction: %+v %v", result, err)
			}
			changed := text
			for _, change := range result.Edits.Edit.DocumentChanges {
				if change.TextDocumentEdit == nil {
					t.Fatal("unexpected resource")
				}
				changed = applyFormattingCopy(t, changed, change.TextDocumentEdit.Edits)
			}
			at, err := OffsetAt(changed, result.Rename.Position)
			if err != nil || !strings.HasPrefix(changed[at:], test.newName) || result.Rename.URI != "catalog:///user/main.ts" {
				t.Fatalf("prospective rename: %+v %v\n%s", result.Rename, err, changed)
			}
			after, err := s.Snapshot(ctx)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("unaccepted extraction mutated state: %v", err)
			}
			project.Files = maps.Clone(project.Files)
			project.Files["/main.ts"] = changed
			if err := s.UpdateProject(ctx, project); err != nil {
				t.Fatal(err)
			}
			if err := s.ValidateEdits(ctx, *result.Edits); !errors.Is(err, ErrContentModified) {
				t.Fatalf("old proposal: %v", err)
			}
			if _, err := s.ApplyRefactor(ctx, RefactorSelection{Handle: choices.Handle, ActionIndex: test.index}, nil); !errors.Is(err, ErrContentModified) {
				t.Fatalf("old selection: %v", err)
			}
			renamed, err := s.Rename(ctx, "/main.ts", result.Rename.Position, "extractedValue")
			if err != nil || renamed.Edits == nil {
				t.Fatalf("subsequent rename: %+v %v", renamed, err)
			}
			for _, change := range renamed.Edits.Edit.DocumentChanges {
				project.Files["/main.ts"] = applyFormattingCopy(t, project.Files["/main.ts"], change.TextDocumentEdit.Edits)
			}
			for _, edits := range renamed.Edits.Edit.Changes {
				project.Files["/main.ts"] = applyFormattingCopy(t, project.Files["/main.ts"], edits)
			}
			diagnostics, err := goya.Check(ctx, project, before.Environment, before.CompilerOptions)
			if err != nil || goya.HasTypeErrors(diagnostics) || strings.Contains(project.Files["/main.ts"], test.newName) {
				t.Fatalf("extraction then rename: %+v %v\n%s", diagnostics, err, project.Files["/main.ts"])
			}
		})
	}
}
