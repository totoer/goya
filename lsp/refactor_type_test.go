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

func TestExtractTypeRenameContinuationAfterAcceptedHostChanges(t *testing.T) {
	for _, action := range []string{"Extract to type alias", "Extract to interface"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			text := "// 😀 type extraction\r\nlet value: { item: string };\r\n"
			project := goya.Project{Files: map[string]string{"/main.ts": text}, EntryPoints: []string{"/main.ts"}}
			s, err := NewSession(ctx, SessionOptions{Project: project, CompilerOptions: goya.CompileOptions{"strict": true, "target": "es2025", "module": "esnext", "moduleResolution": "bundler"}, DocumentURIs: map[string]protocol.DocumentURI{"/main.ts": "catalog:///user/main.ts"}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			start, _ := PositionAt(text, strings.Index(text, "{ item"))
			end, _ := PositionAt(text, strings.Index(text, "};")+1)
			kind := protocol.CodeActionKind("refactor.extract.type")
			choices, err := s.ApplicableRefactors(ctx, "/main.ts", protocol.Range{Start: start, End: end}, &RefactorRequestOptions{Kind: &kind})
			if err != nil || len(choices.Refactors) != 1 || len(choices.Refactors[0].Actions) != 2 {
				t.Fatalf("extract choices: %+v %v", choices, err)
			}
			actionIndex := 0
			if action == "Extract to interface" {
				actionIndex = 1
			}
			span := choices.Refactors[0].Actions[actionIndex].Range
			if span == nil || span.Start != start || span.End != end {
				t.Fatalf("affected source range: %+v", span)
			}
			// Caller-owned ranges cannot change the privately retained operation.
			span.Start = protocol.Position{}
			before, err := s.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.ApplyRefactor(ctx, RefactorSelection{Handle: choices.Handle, ActionIndex: actionIndex}, nil)
			if err != nil || result.Edits == nil || result.Rename == nil {
				t.Fatalf("extract proposal: %+v %v", result, err)
			}
			changed := text
			for _, change := range result.Edits.Edit.DocumentChanges {
				if change.TextDocumentEdit == nil {
					t.Fatal("unexpected resource")
				}
				changed = applyFormattingCopy(t, changed, change.TextDocumentEdit.Edits)
			}
			at, err := OffsetAt(changed, result.Rename.Position)
			if err != nil || !strings.HasPrefix(changed[at:], "NewType") || result.Rename.URI != "catalog:///user/main.ts" {
				t.Fatalf("rename does not point at the prospective name: %+v %v\n%s", result.Rename, err, changed)
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
				t.Fatalf("accepted update did not invalidate old proposal: %v", err)
			}
			renamed, err := s.Rename(ctx, "/main.ts", result.Rename.Position, "ValueShape")
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
			if err != nil || goya.HasTypeErrors(diagnostics) || strings.Contains(project.Files["/main.ts"], "NewType") {
				t.Fatalf("extraction then rename: %+v %v\n%s", diagnostics, err, project.Files["/main.ts"])
			}
		})
	}
}
