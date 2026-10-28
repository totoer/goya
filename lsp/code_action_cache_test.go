package lsp

import (
	"context"
	"testing"

	"github.com/totoer/goya/lsp/protocol"
)

func TestNewCodeActionRequestExpiresEarlierResolveSelectors(t *testing.T) {
	ctx := context.Background()
	s := actionTestSession(t, nil)
	first := enumerateTestActions(t, s)
	before, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []protocol.CodeActionKind{protocol.CodeActionQuickFix}
	empty, err := s.CodeActions(ctx, "/main.ts", actionTestRange, protocol.CodeActionContext{Only: &kinds, Diagnostics: []protocol.Diagnostic{}}, nil)
	if err != nil || len(empty.Actions) != 0 {
		t.Fatalf("empty enumeration: %+v, %v", empty, err)
	}
	resolved, err := s.ResolveCodeAction(ctx, first.Actions[1])
	if err != nil || resolved.Edits != nil || resolved.Action.Edit != nil || resolved.Action.Title != first.Actions[1].Title {
		t.Fatalf("expired reference selector must remain unresolved: %+v, %v", resolved, err)
	}
	after, err := s.Snapshot(ctx)
	if err != nil || after.Generation != before.Generation || after.Project.Files["/main.ts"] != before.Project.Files["/main.ts"] {
		t.Fatalf("cache expiration changed the source snapshot: %v", err)
	}
}
