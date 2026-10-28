package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestCompletionCodeActionEditsOwnFirstActionAndValidateBeforeApplication(t *testing.T) {
	ctx := context.Background()
	project := goya.Project{Files: map[string]string{"/main.ts": "export const value = 1;\n", "/dep.ts": "export const other = 2;\n"}}
	s, err := NewSession(ctx, SessionOptions{Project: project, DocumentURIs: map[string]protocol.DocumentURI{"/main.ts": "catalog:///code/main.ts", "/dep.ts": "catalog:///code/dep.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actions := []CompletionCodeAction{{Description: "first", Changes: []CompletionFileCodeEdits{{FileName: "/main.ts", TextChanges: []CompletionCodeEdit{{Start: CompletionSourceLocation{1, 14}, End: CompletionSourceLocation{1, 19}, NewText: "renamed"}}}}}, {Commands: []json.RawMessage{json.RawMessage(`{"type":"install package"}`)}}}
	before, _ := s.Snapshot(ctx)
	proposal, err := s.CompletionCodeActionEdits(ctx, actions)
	if err != nil || proposal.Edits == nil {
		t.Fatalf("first action: %+v %v", proposal, err)
	}
	actions[0].Changes[0].TextChanges[0].NewText = "mutated"
	copied := before.Project
	copied.Files = maps.Clone(copied.Files)
	edits := proposal.Edits.Edit.Changes["catalog:///code/main.ts"]
	copied.Files["/main.ts"] = applyFormattingCopy(t, copied.Files["/main.ts"], edits)
	if copied.Files["/main.ts"] != "export const renamed = 1;\n" {
		t.Fatalf("detached proposal: %q", copied.Files["/main.ts"])
	}
	if ds, err := goya.Check(ctx, copied, before.Environment, before.CompilerOptions); err != nil || goya.HasTypeErrors(ds) {
		t.Fatalf("copied catalog: %+v %v", ds, err)
	}
	for _, accepted := range []bool{false, true} {
		response, err := s.ApplyEdits(ctx, *proposal.Edits, "", ClientHooks{Capabilities: ClientCapabilities{ApplyEdit: true}, ApplyEdit: func(_ context.Context, p protocol.ApplyWorkspaceEditParams) (protocol.ApplyWorkspaceEditResult, error) {
			return protocol.ApplyWorkspaceEditResult{Applied: accepted}, nil
		}})
		if err != nil || response.Applied != accepted {
			t.Fatalf("decision: %+v %v", response, err)
		}
		after, _ := s.Snapshot(ctx)
		if !maps.Equal(before.Project.Files, after.Project.Files) {
			t.Fatal("client response mutated session")
		}
	}
	for _, bad := range []CompletionCodeAction{
		{Changes: actions[0].Changes, Commands: []json.RawMessage{json.RawMessage(`{"type":"install package"}`)}},
		{Changes: []CompletionFileCodeEdits{{FileName: "/main.ts", TextChanges: []CompletionCodeEdit{{Start: CompletionSourceLocation{1, 99}, End: CompletionSourceLocation{1, 100}}}}}},
		{Changes: []CompletionFileCodeEdits{{FileName: "/main.ts", TextChanges: []CompletionCodeEdit{{Start: CompletionSourceLocation{1, 4}, End: CompletionSourceLocation{1, 9}}, {Start: CompletionSourceLocation{1, 6}, End: CompletionSourceLocation{1, 10}}}}}},
	} {
		if result, err := s.CompletionCodeActionEdits(ctx, []CompletionCodeAction{bad}); err == nil || result.Edits != nil {
			t.Fatalf("invalid action produced proposal: %+v %v", result, err)
		}
	}
	q, err := s.captureQuery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if _, err := s.completionCodeActionEdits(ctx, q, actions); !errors.Is(err, ErrContentModified) {
		t.Fatalf("captured generation: %v", err)
	}
	if err := s.ValidateEdits(ctx, *proposal.Edits); !errors.Is(err, ErrContentModified) {
		t.Fatalf("stale proposal: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.CompletionCodeActionEdits(cancelled, actions); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
