package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestCodeLensOwnedTargetsOpaqueDataAndNoGoExecution(t *testing.T) {
	ctx := context.Background()
	calls, binds := 0, 0
	registry := goya.NewRegistry()
	if err := registry.Register("host", goya.HostBinding{Value: func(value any) any { calls++; return value }, TypeScript: "(payload: Payload) => Payload", Adapter: goya.HostAdapterFuncs{ID: "lens-host/v1", BindValue: func(r *goya.Runtime, value any) (goya.Value, error) { binds++; return r.ToValue(value), nil }}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.DeclareTypes("/payload.d.ts", "interface Payload { total: number; }\n"); err != nil {
		t.Fatal(err)
	}
	environment, err := registry.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	text := "export function target() {}\nexport function caller() { target(); }\ncaller();\nconst payload: Payload = host({total:1});\nfunction local() {}\n"
	on := true
	options := SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": text}}, Environment: environment, DocumentURIs: map[string]protocol.DocumentURI{"/main.ts": "memory:///main.ts"}, EditorOptions: EditorOptions{ReferencesCodeLens: &ReferencesCodeLensSettings{Enabled: &on}}}
	s, err := NewSession(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	on = false
	if err = s.OpenDocument(ctx, "/main.ts", 1, text); err != nil {
		t.Fatal(err)
	}
	lenses, err := s.CodeLenses(ctx, "/main.ts")
	if err != nil || len(lenses.Lenses) != 2 || lenses.Version == nil || *lenses.Version != 1 || lenses.URI != "memory:///main.ts" {
		t.Fatalf("lenses: %+v %v", lenses, err)
	}
	var item protocol.CodeLens
	for _, lens := range lenses.Lenses {
		if lens.Range.Start.Line == 0 {
			item = lens
		}
	}
	if item.Data == nil {
		t.Fatal("missing target lens")
	}
	issuedRange := item.Range
	item.Range.Start.Line = 99
	item.Data.URI = "file:///forged.ts"
	item.Data.Type = protocol.ImplementationsCodeLens
	resolved, err := s.ResolveCodeLens(ctx, item)
	if err != nil || resolved.Lens.Range != issuedRange || resolved.Lens.Command == nil || resolved.Lens.Command.Title != "1 reference" || resolved.Lens.Data.Type != protocol.ReferencesCodeLens || resolved.Lens.Data.URI != "memory:///main.ts" || resolved.Navigation == nil || len(resolved.Navigation.Locations) != 1 || resolved.Navigation.Locations[0].URI != "memory:///main.ts" {
		t.Fatalf("resolve: %+v %v", resolved, err)
	}
	resolved.Navigation.Locations[0].Range.Start.Line = 99
	resolved.Lens.Data.URI = "file:///mutated.ts"
	resolved.Lens.Command.Arguments[2][0] = 'x'
	again, err := s.ResolveCodeLens(ctx, item)
	if err != nil || again.Navigation.Locations[0].Range.Start.Line == 99 || again.Lens.Data.URI != "memory:///main.ts" || !json.Valid(again.Lens.Command.Arguments[2]) {
		t.Fatalf("owned result: %+v %v", again, err)
	}
	work, err := s.prepareResolveCodeLens(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := NewSession(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	if _, err = foreign.ResolveCodeLens(ctx, item); !errors.Is(err, &Error{Code: CodeInvalidHandle}) {
		t.Fatalf("foreign: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.ResolveCodeLens(cancelled, item); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = s.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Text: "\n" + text}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveCodeLens(ctx, item); !errors.Is(err, ErrContentModified) {
		t.Fatalf("stale: %v", err)
	}
	if _, err = work(ctx); !errors.Is(err, ErrContentModified) {
		t.Fatalf("captured stale resolve: %v", err)
	}
	fresh, err := s.CodeLenses(ctx, "/main.ts")
	if err != nil || len(fresh.Lenses) != 2 || fresh.Generation <= lenses.Generation {
		t.Fatalf("recovery: %+v %v", fresh, err)
	}
	// Read-only host declaration navigation and lens queries need no VM/binding.
	defs, err := s.TypeDefinition(ctx, "/main.ts", protocol.Position{Line: 4, Character: 8})
	if err != nil || len(defs.Targets) != 1 {
		t.Fatalf("host type: %+v %v", defs, err)
	}
	doc, err := s.ReadDocument(ctx, defs.Targets[0].TargetURI)
	if err != nil || !doc.ReadOnly {
		t.Fatalf("frozen target: %+v %v", doc, err)
	}
	name, err := s.DocumentName(ctx, defs.Targets[0].TargetURI)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := s.CodeLenses(ctx, name)
	if err != nil || len(frozen.Lenses) == 0 {
		t.Fatalf("frozen lenses: %+v %v", frozen, err)
	}
	if calls != 0 || binds != 0 {
		t.Fatalf("lens invoked Go: %d %d", calls, binds)
	}
}

func TestCodeLensSettingsCloneBatchRetentionAndDisabledClose(t *testing.T) {
	ctx := context.Background()
	text := "export function target() {}\nfunction local() {}\ntarget();\n"
	on := true
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": text}}, EditorOptions: EditorOptions{ReferencesCodeLens: &ReferencesCodeLensSettings{Enabled: &on}}, ResultCacheLimits: &RetentionLimits{MaxEntries: 1, MaxBytes: 1 << 16}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, err := s.CodeLenses(ctx, "/main.ts")
	if err != nil || len(first.Lenses) != 1 || first.Version != nil {
		t.Fatalf("closed SDK: %+v %v", first, err)
	}
	second, err := s.CodeLenses(ctx, "/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveCodeLens(ctx, first.Lenses[0]); !errors.Is(err, ErrResultExpired) {
		t.Fatalf("eviction: %v", err)
	}
	if _, err = s.ResolveCodeLens(ctx, second.Lenses[0]); err != nil {
		t.Fatal(err)
	}
	bad := second.Lenses[0]
	data := *bad.Data
	bad.Data = &data
	bad.Data.Index = 99
	if _, err = s.ResolveCodeLens(ctx, bad); !errors.Is(err, &Error{Code: CodeInvalidHandle}) {
		t.Fatalf("index: %v", err)
	}
	settings := &ReferencesCodeLensSettings{Enabled: &on, ShowOnAllFunctions: &on}
	if err = s.Configure(ctx, WorkspaceConfiguration{TypeScript: &TypeScriptConfiguration{ReferencesCodeLens: settings}}); err != nil {
		t.Fatal(err)
	}
	on = false
	settings.ShowOnAllFunctions = nil
	all, err := s.CodeLenses(ctx, "/main.ts")
	if err != nil || len(all.Lenses) != 2 {
		t.Fatalf("configuration ownership: %+v %v", all, err)
	}
	if all.Lenses[0].Data.Handle != all.Lenses[1].Data.Handle || len(s.results.entries) != 1 {
		t.Fatal("per-item metadata retention")
	}
	if err = s.Configure(ctx, WorkspaceConfiguration{}); err != nil {
		t.Fatal(err)
	}
	disabled, err := s.CodeLenses(ctx, "/main.ts")
	if err != nil || len(disabled.Lenses) != 0 {
		t.Fatalf("reset settings: %+v %v", disabled, err)
	}
	q, err := s.captureQuery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	work, err := s.prepareCodeLenses(q, "/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = work(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("disabled work after Close: %v", err)
	}
}

func TestCodeLensAndInlayRefreshSerializeAndShutdownCancels(t *testing.T) {
	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "export const value=1;\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := startTestConnection(t, nil, 8, 1, testClientSetup{session: s, capabilities: map[string]any{"workspace": map[string]any{"codeLens": map[string]any{"refreshSupport": true}, "inlayHint": map[string]any{"refreshSupport": true}}}})
	configure := func(value bool) {
		c.send(t, nil, "workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"typescript": map[string]any{"inlayHints": map[string]any{"includeInlayVariableTypeHints": value}, "referencesCodeLens": map[string]any{"enabled": value}}}})
	}
	configure(true)
	first := c.read(t)
	if string(first["method"]) != `"workspace/inlayHint/refresh"` {
		t.Fatal(first)
	}
	for i := 0; i < 30; i++ {
		configure(i%2 == 0)
	}
	c.send(t, "barrier", "goya/readDocument", protocol.ReadDocumentParams{URI: "file:///main.ts"})
	if m := c.read(t); string(m["id"]) != `"barrier"` {
		t.Fatalf("blocked reader: %+v", m)
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": first["id"], "result": nil})
	if err = writeFrame(ctx, c.client, body); err != nil {
		t.Fatal(err)
	}
	second := c.read(t)
	if string(second["method"]) != `"workspace/codeLens/refresh"` || len(second["id"]) == 0 {
		t.Fatalf("lost second refresh kind: %+v", second)
	}
	c.finish(t)
}

func TestCodeLensWireStaleInvalidAndRecovery(t *testing.T) {
	ctx := context.Background()
	text := "export function target() {}\ntarget();\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": text}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s, capabilities: map[string]any{"textDocument": map[string]any{"codeLens": map[string]any{}}}})
	c.send(t, nil, "workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"typescript": map[string]any{"referencesCodeLens": map[string]any{"enabled": true}}}})
	c.send(t, nil, "textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: "file:///main.ts", LanguageID: "typescript", Version: 1, Text: text}})
	c.send(t, "lenses", "textDocument/codeLens", map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: "file:///main.ts"}})
	var lenses []protocol.CodeLens
	message := c.read(t)
	if err = json.Unmarshal(message["result"], &lenses); err != nil || len(lenses) != 1 {
		t.Fatalf("wire lenses: %+v %v", message, err)
	}
	c.send(t, nil, "textDocument/didChange", protocol.DidChangeTextDocumentParams{TextDocument: protocol.VersionedTextDocumentIdentifier{URI: "file:///main.ts", Version: 2}, ContentChanges: []protocol.ContentChange{{Text: "\n" + text}}})
	c.send(t, "stale", "codeLens/resolve", lenses[0])
	assertResponseCode(t, c.read(t), `"stale"`, -32801)
	c.send(t, "bad", "codeLens/resolve", protocol.CodeLens{})
	assertResponseCode(t, c.read(t), `"bad"`, -32602)
	c.send(t, "missing", "textDocument/codeLens", map[string]any{})
	assertResponseCode(t, c.read(t), `"missing"`, -32602)
	c.send(t, "again", "textDocument/codeLens", map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: "file:///main.ts"}})
	message = c.read(t)
	if err = json.Unmarshal(message["result"], &lenses); err != nil || len(lenses) != 1 || lenses[0].Range.Start.Line != 1 {
		t.Fatalf("recovery: %+v %v", message, err)
	}
	c.finish(t)
}
