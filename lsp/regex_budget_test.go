package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestAutoImportRegexBudgetFailsWholeRequestAndReleasesLease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	helper := strings.Repeat("a", 80) + "!"
	s, err := NewSession(ctx, SessionOptions{MaxConcurrentRequests: 1, Project: goya.Project{ConfigFile: "/tsconfig.json", Files: map[string]string{
		"/tsconfig.json":     `{"files":["main.ts","` + helper + `.ts"]}`,
		"/main.ts":           "export const marker=1;\nappe(42);\n",
		"/" + helper + ".ts": "export function append(value:number):number{return value;}\n",
	}}, EditorOptions: EditorOptions{Preferences: EditorPreferences{AutoImportSpecifierExcludeRegexes: []string{`^\./(a+)+$`}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	result, err := s.Completion(ctx, "/main.ts", protocol.Position{Line: 1, Character: 4}, nil)
	if !errors.Is(err, ErrRequestLimit) || result.List != nil {
		t.Fatalf("expected complete request failure, got %+v %v", result, err)
	}
	// The matcher must release the checker and snapshot, including its sole
	// admission slot; a separate operation on the same generation still works.
	hover, err := s.Hover(ctx, "/main.ts", protocol.Position{Line: 0, Character: 15}, nil)
	if err != nil || hover.Hover == nil {
		t.Fatalf("budget failure poisoned session: %+v %v", hover, err)
	}
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s})
	c.send(t, "limited", "textDocument/completion", protocol.CompletionParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, Position: protocol.Position{Line: 1, Character: 4}})
	message := c.read(t)
	var failure ResponseError
	if err := json.Unmarshal(message["error"], &failure); err != nil || failure.Code != -32803 {
		t.Fatalf("wire budget failure: %s %v", message["error"], err)
	}
	c.send(t, "usable", "textDocument/hover", protocol.HoverParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, Position: protocol.Position{Line: 0, Character: 15}})
	message = c.read(t)
	if string(message["id"]) != `"usable"` || len(message["result"]) == 0 || string(message["result"]) == "null" {
		t.Fatalf("connection failed after budget error: %+v", message)
	}
	c.finish(t)
}
