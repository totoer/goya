package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestReferenceQueriesOwnOptionsResultsAndCapturedGeneration(t *testing.T) {
	ctx := context.Background()
	main := "import {value} from './helper';\nvalue;\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": main, "/helper.ts": "export let value=1;\n"}}})
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
	options := &ReferenceOptions{IncludeDeclaration: false}
	work, err := s.prepareReferences(q, "/helper.ts", protocol.Position{Line: 0, Character: 13}, options)
	if err != nil {
		t.Fatal(err)
	}
	options.IncludeDeclaration = true
	if err := s.OpenDocument(ctx, "/helper.ts", 1, "export let value=1;\nvalue++;\n"); err != nil {
		t.Fatal(err)
	}
	old, err := work(ctx)
	if err != nil || old.Generation != q.snapshot.Generation || old.Version != nil || len(old.Locations) != 2 {
		t.Fatalf("captured references: %+v %v", old, err)
	}
	for _, location := range old.Locations {
		if location.URI != "file:///main.ts" {
			t.Fatalf("mutated options included declaration: %+v", old)
		}
	}
	current, err := s.References(ctx, "/helper.ts", protocol.Position{Line: 0, Character: 13}, nil)
	if err != nil || current.Generation <= old.Generation || current.Version == nil || *current.Version != 1 || len(current.Locations) != 4 {
		t.Fatalf("updated references: %+v %v", current, err)
	}
	current.Locations[0].Range.Start.Line = 99
	again, err := s.References(ctx, "/helper.ts", protocol.Position{Line: 0, Character: 13}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, location := range again.Locations {
		if location.Range.Start.Line == 99 {
			t.Fatal("returned locations alias compiler state")
		}
	}
	highlights, err := s.DocumentHighlights(ctx, "/helper.ts", protocol.Position{Line: 1, Character: 2})
	if err != nil || len(highlights.Highlights) != 2 || highlights.Version == nil || *highlights.Version != 1 {
		t.Fatalf("highlights: %+v %v", highlights, err)
	}
	*highlights.Highlights[0].Kind = protocol.HighlightWrite
	highlights.Highlights[0].Range.Start.Line = 99
	next, err := s.DocumentHighlights(ctx, "/helper.ts", protocol.Position{Line: 1, Character: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, highlight := range next.Highlights {
		if highlight.Kind == nil || *highlight.Kind != protocol.HighlightRead || highlight.Range.Start.Line == 99 {
			t.Fatal("highlight DTO aliases retained data")
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.References(cancelled, "/main.ts", protocol.Position{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestReferencesFromFrozenGoDeclarationDoNotCallBindings(t *testing.T) {
	ctx := context.Background()
	calls := 0
	registry := goya.NewRegistry()
	if err := registry.Register("host", func() float64 { calls++; return 42 }); err != nil {
		t.Fatal(err)
	}
	environment, err := registry.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": "const value=host();\nhost();\n"}}, Environment: environment, DocumentURIs: map[string]protocol.DocumentURI{"/main.ts": "memory:///workspace/main.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const name = "/.goya/host-api.d.ts"
	text := environment.TypeDeclarations()[name]
	position, err := PositionAt(text, strings.Index(text, "host:"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.References(ctx, name, position, &ReferenceOptions{IncludeDeclaration: false})
	if err != nil || len(result.Locations) != 2 || calls != 0 {
		t.Fatalf("host references: %+v %v calls=%d", result, err, calls)
	}
	for _, location := range result.Locations {
		if location.URI != "memory:///workspace/main.ts" {
			t.Fatal(location)
		}
	}
	uri, err := s.DocumentURI(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s})
	c.send(t, "host", "textDocument/references", protocol.ReferenceParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}, Position: position, Context: protocol.ReferenceContext{IncludeDeclaration: false}})
	message := c.read(t)
	var locations []protocol.Location
	if err := json.Unmarshal(message["result"], &locations); err != nil || len(locations) != 2 || calls != 0 {
		t.Fatalf("wire host references: %s %v calls=%d", message["result"], err, calls)
	}
	c.send(t, "bad", "textDocument/references", map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: uri}, "position": position})
	message = c.read(t)
	var failure ResponseError
	if err := json.Unmarshal(message["error"], &failure); err != nil || failure.Code != -32602 {
		t.Fatalf("missing context accepted: %+v %v", message, err)
	}
	c.finish(t)
}
