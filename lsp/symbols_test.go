package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestSymbolsClosedConfiguredFilesCaptureSourcesURIsAndOwnResults(t *testing.T) {
	ctx := context.Background()
	text := "export class Zoo { zebra() {} alpha() {} }\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{ConfigFile: "/custom.project.json", Files: map[string]string{
		"/a-outside.ts": "export const Outside=1;\n", "/z.ts": text,
		"/custom.project.json": `{"compilerOptions":{"strict":true},"files":["z.ts"]}`,
	}}, DocumentURIs: map[string]protocol.DocumentURI{"/z.ts": "memory:///old.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	q, err := s.captureQuery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	docWork, err := s.prepareDocumentSymbols(q, "/z.ts")
	if err != nil {
		t.Fatal(err)
	}
	workspaceWork := s.prepareWorkspaceSymbols(q, "Zoo")
	if err := s.OpenDocument(ctx, "/z.ts", 2, "\nexport class Added {}\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDocumentURIs(ctx, map[string]protocol.DocumentURI{"/z.ts": "file:///C:/Workspace/z.ts"}); err != nil {
		t.Fatal(err)
	}
	old, err := docWork(ctx)
	if err != nil || old.Generation != q.snapshot.Generation || old.Version != nil || old.URI != "memory:///old.ts" || len(old.Symbols) != 1 || old.Symbols[0].Name != "Zoo" || len(old.Symbols[0].Children) != 2 || old.Symbols[0].Children[0].Name != "alpha" {
		t.Fatalf("captured closed outline: %+v %v", old, err)
	}
	search, err := workspaceWork(ctx)
	if err != nil || search.Generation != q.snapshot.Generation || len(search.Symbols) != 1 || search.Symbols[0].Location.URI != "memory:///old.ts" {
		t.Fatalf("captured closed search: %+v %v", search, err)
	}
	flat := old.Flat()
	if len(flat) != 3 || flat[1].ContainerName == nil || *flat[1].ContainerName != "Zoo" {
		t.Fatal(flat)
	}
	*flat[1].ContainerName = "tampered"
	flat[0].Location.Range.Start.Line = 99
	if old.Symbols[0].Name != "Zoo" || old.Symbols[0].Range.Start.Line != 0 {
		t.Fatal("flat DTO aliases outline")
	}
	old.Symbols[0].Children[0].Name = "tampered"
	search.Symbols[0].Name = "tampered"
	current, err := s.DocumentSymbols(ctx, "/z.ts")
	if err != nil || current.Generation <= old.Generation || current.Version == nil || *current.Version != 2 || len(current.Symbols) != 1 || current.Symbols[0].Name != "Added" || current.URI != "file:///C:/Workspace/z.ts" {
		t.Fatalf("current outline: %+v %v", current, err)
	}
	current.Symbols[0].Name = "tampered"
	for _, name := range []string{"Added", "Zoo", "Outside"} {
		result, err := s.WorkspaceSymbols(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if name == "Added" {
			if len(result.Symbols) != 1 || result.Symbols[0].Location.URI != "file:///C:/Workspace/z.ts" {
				t.Fatal(result)
			}
		} else if len(result.Symbols) != 0 {
			t.Fatalf("unexpected %s: %+v", name, result)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.WorkspaceSymbols(cancelled, "Added"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.DocumentSymbols(cancelled, "/z.ts"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := s.CloseDocument(ctx, "/z.ts"); err != nil {
		t.Fatal(err)
	}
	restored, err := s.WorkspaceSymbols(ctx, "Zoo")
	if err != nil || len(restored.Symbols) != 1 {
		t.Fatalf("close restored base: %+v %v", restored, err)
	}
}

func TestSymbolQueriesReadFrozenGoLibraryAndWireClosedFallback(t *testing.T) {
	ctx := context.Background()
	calls := 0
	registry := goya.NewRegistry()
	if err := registry.Register("host", func() float64 { calls++; return 1 }); err != nil {
		t.Fatal(err)
	}
	environment, err := registry.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": "export const count=host();"}}, Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, name := range []string{"/.goya/host-api.d.ts", libraryPrefix + "lib.es5.d.ts"} {
		result, err := s.DocumentSymbols(ctx, name)
		if err != nil || len(result.Symbols) == 0 {
			t.Fatalf("frozen outline %s: %+v %v", name, result, err)
		}
		doc, err := s.ReadDocument(ctx, result.URI)
		if err != nil || !doc.ReadOnly {
			t.Fatalf("frozen outline document: %+v %v", doc, err)
		}
	}
	if calls != 0 {
		t.Fatal("symbol query executed Go")
	}
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s})
	c.send(t, "closed", "textDocument/documentSymbol", protocol.DocumentSymbolParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}})
	if message := c.read(t); string(message["result"]) != "[]" {
		t.Fatalf("wire closed outline: %+v", message)
	}
	c.send(t, "bad", "workspace/symbol", map[string]any{})
	message := c.read(t)
	var failure ResponseError
	if err := json.Unmarshal(message["error"], &failure); err != nil || failure.Code != -32602 {
		t.Fatalf("missing query: %+v %v", message, err)
	}
	c.finish(t)
}
