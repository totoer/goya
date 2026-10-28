package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

// The pinned TypeScript 6 provider rejects this combined proposal with
// "Changes overlap". Keep its single-action edits and reject the same invalid
// combined proposal without allowing the native tracker assertion to escape.
func TestTypeOnlyImportOverlappingFixAllReturnsTypedErrorAndRecovers(t *testing.T) {
	ctx := context.Background()
	text := "import { Shape, Other } from \"./helper\";\nexport const value: Shape = { value: 1 };\nexport const other: Other = { label: \"value\" };\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": text, "/helper.ts": "export interface Shape { value: number; }\nexport interface Other { label: string; }\n"}}, CompilerOptions: goya.CompileOptions{"verbatimModuleSyntax": true}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.OpenDocument(ctx, "/main.ts", 2, text); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Snapshot(ctx)
	diagnostics, err := s.Diagnostics(ctx, "/main.ts", nil)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []protocol.CodeActionKind{protocol.CodeActionQuickFix}
	params := protocol.CodeActionParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, Range: protocol.Range{End: protocol.Position{Line: 3}}, Context: protocol.CodeActionContext{Diagnostics: diagnostics.Items, Only: &kinds}}
	result, err := s.CodeActions(ctx, "/main.ts", params.Range, params.Context, nil)
	if err != nil {
		t.Fatal(err)
	}
	choose := func(actions []protocol.CodeAction) protocol.CodeAction {
		t.Helper()
		for _, action := range actions {
			if action.Title == "Fix all with type-only imports" {
				return action
			}
		}
		t.Fatalf("no combined action: %+v", actions)
		return protocol.CodeAction{}
	}
	resolved, err := s.ResolveCodeAction(ctx, choose(result.Actions))
	if !errors.Is(err, &Error{Code: CodeInvalidEdit}) || !strings.Contains(err.Error(), "changes overlap") || resolved.Edits != nil || resolved.Action.Edit != nil {
		t.Fatalf("invalid proposal: %+v %v", resolved, err)
	}
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s, capabilities: map[string]any{"textDocument": map[string]any{"codeAction": map[string]any{"resolveSupport": map[string]any{"properties": []string{"edit"}}}}}})
	c.send(t, "actions", "textDocument/codeAction", params)
	var actions []protocol.CodeAction
	response := c.read(t)
	if err = json.Unmarshal(response["result"], &actions); err != nil {
		t.Fatal(err)
	}
	c.send(t, "invalid-combined", "codeAction/resolve", choose(actions))
	response = c.read(t)
	assertResponseCode(t, response, `"invalid-combined"`, -32603)
	if len(response["result"]) > 0 {
		t.Fatal("invalid edits were returned")
	}
	c.send(t, "recover", "textDocument/codeAction", params)
	response = c.read(t)
	if err = json.Unmarshal(response["result"], &actions); err != nil || len(actions) == 0 {
		t.Fatalf("recovery: %v %+v", err, response)
	}
	after, _ := s.Snapshot(ctx)
	if before.Generation != after.Generation || !maps.Equal(before.Project.Files, after.Project.Files) {
		t.Fatal("failed fix mutated the catalog")
	}
}
