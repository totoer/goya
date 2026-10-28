package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestSemanticTokensCaptureRangeGenerationAndOwnLegendAndData(t *testing.T) {
	ctx := context.Background()
	text := "// 😀\r\nexport const value = 1;\r\nvalue;\r\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": text}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/main.ts", 5, text); err != nil {
		t.Fatal(err)
	}
	q, err := s.captureQuery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	span := &protocol.Range{Start: protocol.Position{Line: 2}, End: protocol.Position{Line: 2, Character: 5}}
	work, err := s.prepareSemanticTokens(q, "/main.ts", span)
	if err != nil {
		t.Fatal(err)
	}
	span.Start.Line = 99
	if err := s.ChangeDocument(ctx, "/main.ts", 6, []protocol.ContentChange{{Text: "// 😀\r\nexport function run() {}\r\nrun();\r\n"}}); err != nil {
		t.Fatal(err)
	}
	old, err := work(ctx)
	if err != nil || old.Generation != q.snapshot.Generation || old.Version == nil || *old.Version != 5 || len(old.Tokens.Data) != 5 || old.Tokens.Data[0] != 2 || old.Tokens.Data[3] != 7 {
		t.Fatalf("captured range: %+v %v", old, err)
	}
	current, err := s.SemanticTokens(ctx, "/main.ts", nil)
	if err != nil || current.Generation <= old.Generation || current.Version == nil || *current.Version != 6 || len(current.Tokens.Data) != 10 || current.Tokens.Data[3] != 10 {
		t.Fatalf("current: %+v %v", current, err)
	}
	current.Tokens.Data[0] = 99
	again, err := s.SemanticTokens(ctx, "/main.ts", nil)
	if err != nil || again.Tokens.Data[0] == 99 {
		t.Fatalf("data aliases native state: %+v %v", again, err)
	}
	legend := SemanticTokensLegend()
	legend.TokenTypes[0] = "tampered"
	legend.TokenModifiers[0] = "tampered"
	next := SemanticTokensLegend()
	if next.TokenTypes[0] != "class" || next.TokenTypes[11] != "member" || next.TokenModifiers[0] != "declaration" {
		t.Fatal(next)
	}
	if _, err := s.SemanticTokens(ctx, "/main.ts", &protocol.Range{Start: protocol.Position{Line: 2}, End: protocol.Position{Line: 1}}); !errors.Is(err, &Error{Code: CodeInvalidOptions}) {
		t.Fatalf("reversed range: %v", err)
	}
	if _, err := s.SemanticTokens(ctx, "/main.ts", &protocol.Range{Start: protocol.Position{Line: 0, Character: 4}, End: protocol.Position{Line: 1}}); !errors.Is(err, &Error{Code: CodeInvalidDocument}) {
		t.Fatalf("split surrogate: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.SemanticTokens(cancelled, "/main.ts", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.SemanticTokens(ctx, "/main.ts", nil); err != nil {
		t.Fatal("recovery", err)
	}
}

func TestSemanticTokensWireDiscardsSupersededSnapshotAndValidatesParams(t *testing.T) {
	ctx := context.Background()
	text := "export const value=1;\nvalue;\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": text}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/main.ts", 1, text); err != nil {
		t.Fatal(err)
	}
	handlers := semanticTokenRequestPreparers(s)
	params, _ := json.Marshal(protocol.DocumentSymbolParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}})
	work, err := handlers["textDocument/semanticTokens/full"](ctx, nil, params)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Text: "\n" + text}}); err != nil {
		t.Fatal(err)
	}
	if _, err := work(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("wire returned superseded data: %v", err)
	}
	if _, err := handlers["textDocument/semanticTokens/range"](ctx, nil, params); !errors.Is(err, &Error{Code: CodeInvalidOptions}) {
		t.Fatalf("missing range: %v", err)
	}
	// The fixed legend and its data indices are independent of client support.
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s})
	c.send(t, "full", "textDocument/semanticTokens/full", protocol.DocumentSymbolParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}})
	message := c.read(t)
	var tokens protocol.SemanticTokens
	if err := json.Unmarshal(message["result"], &tokens); err != nil || len(tokens.Data) != 10 {
		t.Fatalf("wire tokens: %+v %v", message, err)
	}
	direct, err := s.SemanticTokens(ctx, "/main.ts", nil)
	if err != nil || !reflect.DeepEqual(tokens, direct.Tokens) {
		t.Fatalf("facades differ: %+v %+v %v", tokens, direct, err)
	}
	c.finish(t)
}
