package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestNavigationCapturesSourceMappingVersionAndOwnsTargets(t *testing.T) {
	ctx := context.Background()
	main := "import {value} from './helper';\nvalue;\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": main, "/helper.ts": "export const value=1;\n"}}, DocumentURIs: map[string]protocol.DocumentURI{"/helper.ts": "memory://project/helper.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/main.ts", 1, main); err != nil {
		t.Fatal(err)
	}
	q, err := s.captureQuery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	work, err := s.prepareNavigation(q, "/main.ts", protocol.Position{Line: 1, Character: 2}, navigationDefinition)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.OpenDocument(ctx, "/helper.ts", 1, "\nexport const value=2;\n"); err != nil {
		t.Fatal(err)
	}
	targetURI := protocol.DocumentURI("file:///C:/Workspace/helper.ts")
	if err := s.UpdateDocumentURIs(ctx, map[string]protocol.DocumentURI{"/helper.ts": targetURI}); err != nil {
		t.Fatal(err)
	}
	old, err := work(ctx)
	if err != nil || old.Generation != q.snapshot.Generation || old.Version == nil || *old.Version != 1 || len(old.Targets) != 1 || old.Targets[0].TargetURI != "memory://project/helper.ts" || old.Targets[0].TargetSelectionRange.Start.Line != 0 {
		t.Fatalf("captured navigation: %+v %v", old, err)
	}
	current, err := s.Definition(ctx, "/main.ts", protocol.Position{Line: 1, Character: 2})
	if err != nil || current.Generation <= old.Generation || len(current.Targets) != 1 || current.Targets[0].TargetURI != targetURI || current.Targets[0].TargetSelectionRange.Start.Line != 1 {
		t.Fatalf("current navigation: %+v %v", current, err)
	}
	document, err := s.ReadDocument(ctx, targetURI)
	if err != nil || document.Name != "/helper.ts" || document.Text != "\nexport const value=2;\n" {
		t.Fatalf("mapped target not readable: %+v %v", document, err)
	}
	*current.Version = 99
	current.Targets[0].OriginSelectionRange.Start.Line = 99
	current.Targets[0].TargetSelectionRange.Start.Line = 99
	locations := old.Locations()
	locations[0].Range.Start.Line = 99
	if old.Targets[0].TargetSelectionRange.Start.Line != 0 {
		t.Fatal("flat locations alias target DTOs")
	}
	again, err := s.Definition(ctx, "/main.ts", protocol.Position{Line: 1, Character: 2})
	if err != nil || *again.Version != 1 || again.Targets[0].OriginSelectionRange.Start.Line != 1 || again.Targets[0].TargetSelectionRange.Start.Line != 1 {
		t.Fatalf("caller mutation escaped: %+v %v", again, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Implementation(cancelled, "/main.ts", protocol.Position{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNavigationWireErrorsAndBlankImplementationRemainUsable(t *testing.T) {
	s, err := NewSession(context.Background(), SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "\nconst value=1;\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s})
	for _, tc := range []struct {
		id      string
		args    []any
		message string
	}{
		{"missing", nil, "Go to Source Definition failed. Invalid position."},
		{"closed", []any{"file:///main.ts", protocol.Position{}}, "Go to Source Definition failed. File not opened in the editor."},
	} {
		c.send(t, tc.id, "workspace/executeCommand", map[string]any{"command": sourceDefinitionCommand, "arguments": tc.args})
		message := c.read(t)
		var params struct {
			Type    protocol.MessageType
			Message string
		}
		if err := json.Unmarshal(message["params"], &params); err != nil || string(message["method"]) != `"window/showMessage"` || params.Type != protocol.MessageError || params.Message != tc.message {
			t.Fatalf("command feedback: %+v %v", message, err)
		}
		response := c.read(t)
		if string(response["id"]) != `"`+tc.id+`"` || string(response["result"]) != "null" {
			t.Fatalf("invalid command result: %+v", response)
		}
	}
	c.send(t, "bad", "textDocument/definition", map[string]any{})
	response := c.read(t)
	var failure ResponseError
	if err := json.Unmarshal(response["error"], &failure); err != nil || failure.Code != -32602 {
		t.Fatalf("invalid definition request: %+v %v", response, err)
	}
	c.send(t, "empty", "textDocument/implementation", protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, Position: protocol.Position{}})
	response = c.read(t)
	if string(response["id"]) != `"empty"` || string(response["result"]) != "[]" {
		t.Fatalf("blank implementation query: %+v", response)
	}
	c.finish(t)
}
