package lsp

import (
	"context"
	"encoding/json"
	"maps"
	"testing"

	"github.com/totoer/goya/lsp/protocol"
)

func TestExcludedEditorCommandsRejectBeforeClientEffectsAndRecover(t *testing.T) {
	s := testSession(t, nil)
	ctx := context.Background()
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s, capabilities: map[string]any{"workspace": map[string]any{"applyEdit": true}}})
	before, _ := s.Snapshot(ctx)
	for _, command := range []string{"typescript.tsserverRequest", "_typescript.configurePlugin", "_typescript.restartTsServer", "_typescript.openTsServerLog"} {
		c.send(t, "excluded", "workspace/executeCommand", protocol.ExecuteCommandParams{Command: command, Arguments: []json.RawMessage{json.RawMessage(`{"command":"openExternalProject","arguments":{}}`)}})
		assertResponseCode(t, c.read(t), `"excluded"`, -32601)
	}
	external := json.RawMessage(`{"type":"install package","file":"/main.ts","packageName":"external"}`)
	for _, command := range []string{applyCompletionCodeActionCommand, applyCodeActionCommand} {
		var args []json.RawMessage
		if command == applyCompletionCodeActionCommand {
			raw, _ := json.Marshal([]CompletionCodeAction{{Changes: []CompletionFileCodeEdits{{FileName: "/main.ts", TextChanges: []CompletionCodeEdit{{Start: CompletionSourceLocation{1, 1}, End: CompletionSourceLocation{1, 1}, NewText: "corrupt"}}}}, Commands: []json.RawMessage{external}}})
			args = []json.RawMessage{json.RawMessage(`"/main.ts"`), raw}
		} else {
			args = []json.RawMessage{json.RawMessage(`{"action":{"changes":[{"fileName":"/main.ts","textChanges":[]}],"commands":[` + string(external) + `]}}`)}
		}
		c.send(t, "external-action", "workspace/executeCommand", protocol.ExecuteCommandParams{Command: command, Arguments: args})
		// A client request here would fail this assertion before any acknowledgement.
		assertResponseCode(t, c.read(t), `"external-action"`, -32602)
	}
	c.send(t, "recover", "goya/readDocument", map[string]any{"uri": "file:///main.ts"})
	response := c.read(t)
	if string(response["id"]) != `"recover"` || len(response["error"]) > 0 {
		t.Fatalf("command poisoned connection: %+v", response)
	}
	after, _ := s.Snapshot(ctx)
	if after.Generation != before.Generation || !maps.Equal(before.Project.Files, after.Project.Files) {
		t.Fatal("excluded command changed the catalog")
	}
	if ok, err := s.CodeActionCommands(ctx, nil); err != nil || !ok {
		t.Fatalf("empty follow-up: %v %v", ok, err)
	}
	if ok, err := s.CodeActionCommands(ctx, []json.RawMessage{external}); err == nil || ok {
		t.Fatalf("external direct command: %v %v", ok, err)
	}
	c.finish(t)
}
