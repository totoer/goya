package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestInlayHintsUseFrozenLocationsOwnPreferencesAndDoNotBindGo(t *testing.T) {
	ctx := context.Background()
	calls, binds := 0, 0
	registry := goya.NewRegistry()
	if err := registry.Register("host", goya.HostBinding{Value: func(value any) any { calls++; return value }, TypeScript: "(payload: Payload) => Payload", Adapter: goya.HostAdapterFuncs{ID: "inlay-host/v1", BindValue: func(r *goya.Runtime, value any) (goya.Value, error) { binds++; return r.ToValue(value), nil }}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.DeclareTypes("/payload.d.ts", "interface Payload { total: number; }\n"); err != nil {
		t.Fatal(err)
	}
	environment, err := registry.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	text := "const value = host({total: 1});\n"
	enabled := true
	options := EditorOptions{Preferences: EditorPreferences{IncludeInlayVariableTypeHints: &enabled, IncludeInlayParameterNameHints: "all"}}
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": text}}, Environment: environment, EditorOptions: options})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	enabled = false
	if err := s.OpenDocument(ctx, "/main.ts", 1, text); err != nil {
		t.Fatal(err)
	}
	end, err := PositionAt(text, len(text))
	if err != nil {
		t.Fatal(err)
	}
	span := protocol.Range{End: end}
	q, err := s.captureQuery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	work, err := s.prepareInlayHints(q, "/main.ts", span)
	if err != nil {
		t.Fatal(err)
	}
	before, err := work(ctx)
	if err != nil || len(before.Hints) != 2 || before.Version == nil || *before.Version != 1 {
		t.Fatalf("frozen hints: %+v %v", before, err)
	}
	linked := 0
	for _, hint := range before.Hints {
		for _, part := range hint.Label.Parts {
			if part.Location == nil {
				continue
			}
			linked++
			doc, err := s.ReadDocument(ctx, part.Location.URI)
			if err != nil || !doc.ReadOnly {
				t.Fatalf("hint target is not frozen: %+v %v", doc, err)
			}
		}
	}
	if linked != 2 || calls != 0 || binds != 0 {
		t.Fatalf("locations=%d calls=%d binds=%d", linked, calls, binds)
	}
	before.Hints[0].Label.Parts[0].Value = "tampered"
	before.Hints[0].Label.Parts[1].Location.URI = "file:///tampered.ts"
	again, err := work(ctx)
	if err != nil || again.Hints[0].Label.Parts[0].Value == "tampered" || again.Hints[0].Label.Parts[1].Location.URI == "file:///tampered.ts" {
		t.Fatalf("DTO mutation: %+v %v", again, err)
	}
	on, off := true, false
	if err := s.UpdateEditorOptions(ctx, EditorOptions{Preferences: EditorPreferences{InteractiveInlayHints: &off, IncludeInlayVariableTypeHints: &on, IncludeInlayParameterNameHints: "all"}}); err != nil {
		t.Fatal(err)
	}
	plain, err := s.InlayHints(ctx, "/main.ts", span)
	if err != nil || len(plain.Hints) != 2 || plain.Hints[0].Label.String == nil || plain.Generation <= before.Generation {
		t.Fatalf("plain hints: %+v %v", plain, err)
	}
	captured, err := work(ctx)
	if err != nil || captured.Hints[0].Label.Parts == nil || captured.Generation != before.Generation {
		t.Fatalf("captured preferences: %+v %v", captured, err)
	}
	override := &InlayHintSettings{IncludeInlayVariableTypeHints: &off, IncludeInlayParameterNameHints: "none"}
	if err := s.Configure(ctx, WorkspaceConfiguration{TypeScript: &TypeScriptConfiguration{InlayHints: override}}); err != nil {
		t.Fatal(err)
	}
	off = true
	override.IncludeInlayParameterNameHints = "all"
	disabled, err := s.InlayHints(ctx, "/main.ts", span)
	if err != nil || disabled.Hints == nil || len(disabled.Hints) != 0 {
		t.Fatalf("workspace override was borrowed: %+v %v", disabled, err)
	}
	if err := s.Configure(ctx, WorkspaceConfiguration{}); err != nil {
		t.Fatal(err)
	}
	restored, err := s.InlayHints(ctx, "/main.ts", span)
	if err != nil || len(restored.Hints) != 2 {
		t.Fatalf("clearing workspace overrides lost initialization prefs: %+v %v", restored, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.InlayHints(cancelled, "/main.ts", span); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.InlayHints(ctx, "/main.ts", protocol.Range{Start: end}); !errors.Is(err, &Error{Code: CodeInvalidOptions}) {
		t.Fatalf("reversed range: %v", err)
	}
	if calls != 0 || binds != 0 {
		t.Fatalf("hints executed Go: %d %d", calls, binds)
	}
}

func TestInlayRefreshCoalescesAndShutdownCancelsUnansweredRequest(t *testing.T) {
	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "export const value=1;\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := startTestConnection(t, nil, 8, 1, testClientSetup{session: s, capabilities: map[string]any{"workspace": map[string]any{"inlayHint": map[string]any{"refreshSupport": true}}}})
	configure := func(value bool) {
		c.send(t, nil, "workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"typescript": map[string]any{"inlayHints": map[string]any{"includeInlayVariableTypeHints": value}}}})
	}
	configure(true)
	message := c.read(t)
	if string(message["method"]) != `"workspace/inlayHint/refresh"` || len(message["id"]) == 0 {
		t.Fatalf("refresh: %+v", message)
	}
	// Leave this request unanswered while settings continue to change. A second
	// refresh must not consume another request slot or block incoming messages.
	for i := 0; i < 30; i++ {
		configure(i%2 == 0)
	}
	c.send(t, "barrier", "goya/readDocument", protocol.ReadDocumentParams{URI: "file:///main.ts"})
	if message := c.read(t); string(message["id"]) != `"barrier"` {
		t.Fatalf("refresh queue was not bounded: %+v", message)
	}
	c.finish(t)
}

func TestInlayHintsClosedWireMessageAndRefreshCapabilityGate(t *testing.T) {
	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "export const value=1;\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s})
	c.send(t, nil, "workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"typescript": map[string]any{"inlayHints": map[string]any{"includeInlayVariableTypeHints": true}}}})
	c.send(t, "closed", "textDocument/inlayHint", protocol.InlayHintParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, Range: protocol.Range{End: protocol.Position{Line: 1}}})
	message := c.read(t)
	var params struct {
		Type    protocol.MessageType
		Message string
	}
	if err := json.Unmarshal(message["params"], &params); err != nil || string(message["method"]) != `"window/showMessage"` || params.Type != protocol.MessageError || params.Message != "Inlay Hints request failed. File not opened in the editor." {
		t.Fatalf("closed feedback / unexpected refresh: %+v %v", message, err)
	}
	message = c.read(t)
	if string(message["id"]) != `"closed"` || string(message["result"]) != "[]" {
		t.Fatalf("closed hints: %+v", message)
	}
	c.send(t, "bad", "textDocument/inlayHint", map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: "file:///main.ts"}})
	message = c.read(t)
	var failure ResponseError
	if err := json.Unmarshal(message["error"], &failure); err != nil || failure.Code != -32602 {
		t.Fatalf("missing range: %+v %v", message, err)
	}
	c.finish(t)
}
