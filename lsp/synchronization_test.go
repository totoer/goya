package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func messageFrame(t *testing.T, id any, method string, params any) string {
	t.Helper()
	message := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		message["id"] = id
	}
	if params != nil {
		message["params"] = params
	}
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return rpcFrame(string(data))
}
func documentResponses(t *testing.T, output []byte) map[string]VirtualDocument {
	t.Helper()
	result := map[string]VirtualDocument{}
	for _, response := range responseTape(t, output) {
		if response["result"] == nil {
			continue
		}
		var document VirtualDocument
		if err := json.Unmarshal(response["result"], &document); err == nil && document.URI != "" {
			result[string(response["id"])] = document
		}
	}
	return result
}

func TestWireSynchronizationUsesSharedVirtualDocuments(t *testing.T) {
	ctx := context.Background()
	uri := protocol.DocumentURI("memory:/one%20file.ts")
	base := "const emoji='😀';\r\nlet value=1;\r\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": base}}, DocumentURIs: map[string]protocol.DocumentURI{"/main.ts": uri}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var tape strings.Builder
	add := func(id any, method string, params any) { tape.WriteString(messageFrame(t, id, method, params)) }
	add(1, "initialize", map[string]any{"capabilities": map[string]any{}})
	add(nil, "initialized", map[string]any{})
	add(nil, "textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: uri, LanguageID: "typescript", Version: 1, Text: base}})
	add(nil, "textDocument/didChange", protocol.DidChangeTextDocumentParams{TextDocument: protocol.VersionedTextDocumentIdentifier{URI: uri, Version: 2}, ContentChanges: []protocol.ContentChange{
		{Range: &protocol.Range{Start: protocol.Position{Line: 1, Character: 10}, End: protocol.Position{Line: 1, Character: 11}}, Text: "2"},
		{Range: &protocol.Range{Start: protocol.Position{Line: 1, Character: 10}, End: protocol.Position{Line: 1, Character: 11}}, Text: "42"},
	}})
	add(2, "goya/readDocument", protocol.ReadDocumentParams{URI: uri})
	add(nil, "textDocument/didChange", protocol.DidChangeTextDocumentParams{TextDocument: protocol.VersionedTextDocumentIdentifier{URI: uri, Version: 2}, ContentChanges: []protocol.ContentChange{{Text: "stale"}}})
	add(3, "goya/readDocument", protocol.ReadDocumentParams{URI: uri})
	saved := "unaccepted saved text"
	add(nil, "textDocument/didSave", protocol.DidSaveTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}, Text: &saved})
	add(4, "goya/readDocument", protocol.ReadDocumentParams{URI: uri})
	// Sending a notification as a request must not mutate the shared session.
	add(5, "textDocument/didChange", protocol.DidChangeTextDocumentParams{TextDocument: protocol.VersionedTextDocumentIdentifier{URI: uri, Version: 3}, ContentChanges: []protocol.ContentChange{{Text: "must not apply"}}})
	add(6, "goya/readDocument", protocol.ReadDocumentParams{URI: uri})
	add(nil, "textDocument/didClose", protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}})
	add(7, "goya/readDocument", protocol.ReadDocumentParams{URI: uri})
	add(9, "shutdown", nil)
	add(nil, "exit", nil)
	var output, logs bytes.Buffer
	if err := Serve(ctx, strings.NewReader(tape.String()), &output, ServerOptions{Session: s, Logger: log.New(&logs, "", 0)}); err != nil {
		t.Fatal(err)
	}
	documents := documentResponses(t, output.Bytes())
	for _, id := range []string{"2", "3", "4", "6"} {
		document := documents[id]
		if document.Text != strings.Replace(base, "value=1", "value=42", 1) || document.Version == nil || *document.Version != 2 || document.URI != uri {
			t.Fatalf("response %s: %+v", id, document)
		}
	}
	if documents["7"].Text != base || documents["7"].Version != nil {
		t.Fatalf("close failed to restore base: %+v", documents["7"])
	}
	if !strings.Contains(logs.String(), "version must increase") {
		t.Fatalf("stale notification not diagnosed: %s", logs.String())
	}
	for _, response := range responseTape(t, output.Bytes()) {
		if string(response["id"]) == "5" && response["error"] == nil {
			t.Fatal("request/notification confusion accepted")
		}
	}
}

func TestWireFileEventsExplicitlyRefreshHostResolver(t *testing.T) {
	ctx := context.Background()
	calls := 0
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Resolver: goya.ResolverFunc(func(context.Context) (map[string]string, error) {
		calls++
		return map[string]string{"/main.ts": fmt.Sprintf("export const value=%d;", calls)}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	uri := protocol.DocumentURI("file:///main.ts")
	tape := messageFrame(t, 1, "initialize", map[string]any{"capabilities": map[string]any{}}) + messageFrame(t, nil, "initialized", map[string]any{}) +
		messageFrame(t, nil, "textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: uri, LanguageID: "typescript", Version: 1, Text: "export const value=99;"}}) +
		messageFrame(t, nil, "workspace/didChangeWatchedFiles", protocol.DidChangeWatchedFilesParams{Changes: []protocol.FileEvent{{URI: uri, Type: protocol.FileChanged}}}) +
		messageFrame(t, 2, "goya/readDocument", protocol.ReadDocumentParams{URI: uri}) +
		messageFrame(t, nil, "textDocument/didSave", protocol.DidSaveTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}}) +
		messageFrame(t, nil, "textDocument/didClose", protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}}) +
		messageFrame(t, 3, "goya/readDocument", protocol.ReadDocumentParams{URI: uri}) + messageFrame(t, 4, "shutdown", nil) + messageFrame(t, nil, "exit", nil)
	var output bytes.Buffer
	if err := Serve(ctx, strings.NewReader(tape), &output, ServerOptions{Session: s}); err != nil {
		t.Fatal(err)
	}
	documents := documentResponses(t, output.Bytes())
	if documents["2"].Text != "export const value=99;" || documents["3"].Text != "export const value=2;" || calls != 2 {
		t.Fatalf("file event bypassed resolver/overlay ownership: calls=%d docs=%+v", calls, documents)
	}
}
