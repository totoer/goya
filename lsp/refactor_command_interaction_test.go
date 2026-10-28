package lsp

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/totoer/goya/lsp/protocol"
)

func TestWireRefactorCommandAcceptedDeclinedAndCancelledWaitForHostEvents(t *testing.T) {
	for _, decision := range []string{"accepted", "declined", "cancelled"} {
		t.Run(decision, func(t *testing.T) {
			s, _, span := functionRefactorSession(t, nil)
			c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s, capabilities: map[string]any{"workspace": map[string]any{"applyEdit": true}}})
			before, _ := s.Snapshot(context.Background())
			only := []protocol.CodeActionKind{"refactor.rewrite.function.named"}
			c.send(t, "choices", "textDocument/codeAction", protocol.CodeActionParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, Range: span, Context: protocol.CodeActionContext{Diagnostics: []protocol.Diagnostic{}, Only: &only}})
			response := c.read(t)
			var actions []protocol.CodeAction
			if err := json.Unmarshal(response["result"], &actions); err != nil || len(actions) != 1 || actions[0].Command == nil {
				t.Fatalf("choices: %+v %v %+v", actions, err, response)
			}
			command := actions[0].Command
			c.send(t, "apply-refactor", "workspace/executeCommand", protocol.ExecuteCommandParams{Command: command.Command, Arguments: command.Arguments})
			request := c.read(t)
			if string(request["method"]) != `"workspace/applyEdit"` {
				t.Fatalf("missing proposal: %+v", request)
			}
			if decision == "cancelled" {
				c.send(t, nil, "$/cancelRequest", map[string]any{"id": "apply-refactor"})
				assertResponseCode(t, c.read(t), `"apply-refactor"`, -32800)
			}
			body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": protocol.ApplyWorkspaceEditResult{Applied: decision == "accepted"}})
			if err := writeFrame(context.Background(), c.client, body); err != nil {
				t.Fatal(err)
			}
			if decision != "cancelled" {
				response = c.read(t)
				if len(response["error"]) > 0 || string(response["result"]) != "null" {
					t.Fatalf("command response: %+v", response)
				}
			}
			c.send(t, "after", "goya/readDocument", map[string]any{"uri": "file:///main.ts"})
			response = c.read(t)
			if string(response["id"]) != `"after"` || len(response["error"]) > 0 {
				t.Fatalf("connection recovery: %+v", response)
			}
			after, _ := s.Snapshot(context.Background())
			if !reflect.DeepEqual(before, after) {
				t.Fatal("refactor interaction changed session without host events")
			}
			c.finish(t)
		})
	}
}
