package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/totoer/goya/lsp/protocol"
)

func TestSourceActionsRequireCurrentDiagnosticsAndBindTheirGeneration(t *testing.T) {
	ctx := context.Background()
	s := actionTestSession(t, nil)
	only := []protocol.CodeActionKind{protocol.CodeActionSourceFixAllTS}
	actionContext := protocol.CodeActionContext{Only: &only, Diagnostics: []protocol.Diagnostic{}}
	query := func() CodeActionResult {
		t.Helper()
		result, err := s.CodeActions(ctx, "/main.ts", actionTestRange, actionContext, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	assertNoSource := func(result CodeActionResult) {
		t.Helper()
		for _, action := range result.Actions {
			if action.Kind == protocol.CodeActionSourceFixAllTS {
				t.Fatal("source action used diagnostics before the current generation was ready")
			}
		}
	}
	assertNoSource(query())
	if _, err := s.Diagnostics(ctx, "/main.ts", nil); err != nil {
		t.Fatal(err)
	}
	result := query()
	if len(result.Actions) != 1 || result.Actions[0].Kind != protocol.CodeActionSourceFixAllTS || result.Actions[0].IsPreferred != nil || result.Actions[0].Command != nil || len(result.Actions[0].Data) != 0 || result.Proposals[0] == nil {
		t.Fatalf("source action: %+v", result)
	}
	bound := *result.Proposals[0]
	if err := s.ValidateEdits(ctx, bound); err != nil {
		t.Fatal(err)
	}
	result.Actions[0].Edit.DocumentChanges[0].TextDocumentEdit.Edits[0].NewText = "tampered"
	if err := s.ValidateEdits(ctx, bound); err != nil {
		t.Fatal("action aliases its bound proposal:", err)
	}
	if err := s.ChangeDocument(ctx, "/main.ts", 4, []protocol.ContentChange{{Text: actionTestText}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateEdits(ctx, bound); !errors.Is(err, ErrContentModified) {
		t.Fatalf("stale source edits: %v", err)
	}
	assertNoSource(query())
	if _, err := s.Diagnostics(ctx, "/main.ts", nil); err != nil {
		t.Fatal(err)
	}
	next := query()
	if len(next.Actions) != 1 || next.Proposals[0] == nil || next.Generation == result.Generation {
		t.Fatalf("new generation: %+v", next)
	}
}

func TestWireSourceActionsBecomeAvailableAfterPushDiagnostics(t *testing.T) {
	s := actionTestSession(t, nil)
	c := startTestConnection(t, nil, 8, 16, testClientSetup{session: s, captureDiagnostics: true})
	published := publishedDiagnostics(t, c)
	if published.URI != "memory:///main.ts" || len(published.Diagnostics) != 2 {
		t.Fatalf("publication: %+v", published)
	}
	only := []protocol.CodeActionKind{protocol.CodeActionSourceFixAllTS}
	c.send(t, "source", "textDocument/codeAction", protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: published.URI}, Range: actionTestRange,
		Context: protocol.CodeActionContext{Only: &only, Diagnostics: []protocol.Diagnostic{}},
	})
	response := c.read(t)
	var actions []protocol.CodeAction
	if string(response["id"]) != `"source"` || json.Unmarshal(response["result"], &actions) != nil || len(actions) == 0 || actions[0].Kind != protocol.CodeActionSourceFixAllTS || actions[0].Edit == nil {
		t.Fatalf("source after push: %+v", response)
	}
	c.finish(t)
}
