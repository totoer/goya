package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func applyFormattingCopy(t *testing.T, text string, edits []protocol.TextEdit) string {
	t.Helper()
	type change struct {
		start, end, index int
		replacement       string
	}
	changes := make([]change, 0, len(edits))
	for index, edit := range edits {
		start, err := OffsetAt(text, edit.Range.Start)
		if err != nil {
			t.Fatal(err)
		}
		end, err := OffsetAt(text, edit.Range.End)
		if err != nil {
			t.Fatal(err)
		}
		changes = append(changes, change{start, end, index, edit.NewText})
	}
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].start != changes[j].start {
			return changes[i].start > changes[j].start
		}
		return changes[i].index > changes[j].index
	})
	for _, c := range changes {
		text = text[:c.start] + c.replacement + text[c.end:]
	}
	return text
}
func TestFormattingProviderOwnsEditsAndChecksFormattedCopy(t *testing.T) {
	ctx := context.Background()
	source := "export function sum(a:number,b:number){\nreturn a+b;\n}\n"
	size := uint32(3)
	spaces := false
	calls := 0
	var session *Session
	provider := func(ctx context.Context, uri protocol.DocumentURI) (*protocol.DocumentFormattingOptions, error) {
		calls++
		if uri != "memory:///main.ts" {
			t.Fatalf("scope: %s", uri)
		}
		// Reentrant session reads prove the callback is outside state locks.
		if _, err := session.ReadDocument(ctx, uri); err != nil {
			return nil, err
		}
		if _, err := session.FoldingRanges(ctx, "/main.ts"); err != nil {
			return nil, err
		}
		return &protocol.DocumentFormattingOptions{TabSize: &size, InsertSpaces: &spaces}, nil
	}
	var err error
	session, err = NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": source}}, DocumentURIs: map[string]protocol.DocumentURI{"/main.ts": "memory:///main.ts"}, FormattingOptionsProvider: provider})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	before, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.FormatDocument(ctx, "/main.ts", nil)
	if err != nil || result.Edits == nil || len(result.TextEdits) == 0 || result.Version != nil || calls != 1 {
		t.Fatalf("format: %+v calls=%d %v", result, calls, err)
	}
	text := applyFormattingCopy(t, source, result.TextEdits)
	if !strings.Contains(text, "\n\treturn") {
		t.Fatalf("document settings ignored: %q", text)
	}
	if ds, err := goya.Check(ctx, goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": text}}, nil, goya.CompileOptions{}); err != nil || goya.HasTypeErrors(ds) {
		t.Fatalf("formatted copy: %+v %v", ds, err)
	}
	if err = session.ValidateEdits(ctx, *result.Edits); err != nil {
		t.Fatal(err)
	}
	result.TextEdits[0].NewText = "tampered"
	if err = session.ValidateEdits(ctx, *result.Edits); err != nil {
		t.Fatalf("result slices alias proposal: %v", err)
	}
	after, err := session.Snapshot(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("format mutated session: %v", err)
	}
	// Explicit request options override the document provider, as on the wire.
	explicit := &protocol.FormattingOptions{TabSize: 2, InsertSpaces: true}
	again, err := session.FormatDocument(ctx, "/main.ts", explicit)
	if err != nil || calls != 1 {
		t.Fatalf("explicit callback: %d %v", calls, err)
	}
	if formatted := applyFormattingCopy(t, source, again.TextEdits); !strings.Contains(formatted, "\n  return") {
		t.Fatalf("request options ignored: %q", formatted)
	}
	if err = session.OpenDocument(ctx, "/main.ts", 1, source); err != nil {
		t.Fatal(err)
	}
	q, err := session.captureQuery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	work, err := session.prepareFormatting(q, "/main.ts", nil, explicit)
	if err != nil {
		t.Fatal(err)
	}
	explicit.TabSize = 8
	explicit.InsertSpaces = false
	if err = session.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Text: source}}); err != nil {
		t.Fatal(err)
	}
	if _, err = work(ctx); !errors.Is(err, ErrContentModified) {
		t.Fatalf("stale format: %v", err)
	}
	if err = session.ValidateEdits(ctx, *again.Edits); !errors.Is(err, ErrContentModified) {
		t.Fatalf("stale proposal: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = session.FormatDocument(cancelled, "/main.ts", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = session.FormatRange(ctx, "/main.ts", protocol.Range{Start: protocol.Position{Line: 1}}, &protocol.FormattingOptions{TabSize: 2, InsertSpaces: true}); !errors.Is(err, &Error{Code: CodeInvalidOptions}) {
		t.Fatalf("reversed range: %v", err)
	}
}
func TestFormattingSessionCloseCancelsProvider(t *testing.T) {
	ctx := context.Background()
	started := make(chan struct{})
	done := make(chan error, 1)
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "export const x=1;\n"}}, FormattingOptionsProvider: func(ctx context.Context, _ protocol.DocumentURI) (*protocol.DocumentFormattingOptions, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, err := s.FormatDocument(ctx, "/main.ts", nil); done <- err }()
	<-started
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("provider cancellation: %v", err)
	}
}
func TestFormattingWireConfigurationCacheAndExplicitOptions(t *testing.T) {
	ctx := context.Background()
	source := "export function sum(a:number,b:number){\nreturn a+b;\n}\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": source}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := startTestConnection(t, nil, 8, 1, testClientSetup{session: s, capabilities: map[string]any{"workspace": map[string]any{"configuration": true}}})
	open := func(version int32) {
		c.send(t, nil, "textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: "file:///main.ts", LanguageID: "typescript", Version: version, Text: source}})
	}
	hover := func(id string) {
		c.send(t, id, "textDocument/hover", protocol.HoverParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, Position: protocol.Position{Character: 18}})
	}
	open(1)
	hover("first")
	request := c.read(t)
	if string(request["method"]) != `"workspace/configuration"` {
		t.Fatalf("configuration: %+v", request)
	}
	var params struct {
		Items []struct{ ScopeURI, Section string }
	}
	if err = json.Unmarshal(request["params"], &params); err != nil || len(params.Items) != 1 || params.Items[0].ScopeURI != "file:///main.ts" || params.Items[0].Section != "formattingOptions" {
		t.Fatalf("scope: %+v %v", params, err)
	}
	c.send(t, "barrier", "goya/readDocument", protocol.ReadDocumentParams{URI: "file:///main.ts"})
	if response := c.read(t); string(response["id"]) != `"barrier"` {
		t.Fatal(response)
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": []any{map[string]any{"tabSize": 3, "insertSpaces": false, "semicolons": "remove"}}})
	if err = writeFrame(ctx, c.client, body); err != nil {
		t.Fatal(err)
	}
	if response := c.read(t); string(response["id"]) != `"first"` {
		t.Fatal(response)
	}
	c.send(t, "format", "textDocument/formatting", map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, "options": protocol.FormattingOptions{TabSize: 2, InsertSpaces: true}})
	response := c.read(t)
	var edits []protocol.TextEdit
	if string(response["id"]) != `"format"` || json.Unmarshal(response["result"], &edits) != nil {
		t.Fatalf("explicit formatting queried configuration: %+v", response)
	}
	if formatted := applyFormattingCopy(t, source, edits); !strings.Contains(formatted, "\n  return") {
		t.Fatalf("explicit options: %q", formatted)
	}
	hover("second")
	if response := c.read(t); string(response["id"]) != `"second"` {
		t.Fatalf("uncached config: %+v", response)
	}
	// TLS keeps the formatting-options cache even after closing a document.
	c.send(t, nil, "textDocument/didClose", protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}})
	open(2)
	hover("reopened")
	if response := c.read(t); string(response["id"]) != `"reopened"` {
		t.Fatalf("cache lifecycle: %+v", response)
	}
	c.finish(t)
}

func TestFormattingReadonlyHostAndInvalidOptionsLeaveSourceUntouched(t *testing.T) {
	ctx := context.Background()
	calls, binds := 0, 0
	registry := goya.NewRegistry()
	if err := registry.Register("host", goya.HostBinding{Value: func(value any) any { calls++; return value }, TypeScript: "(payload: Payload) => Payload", Adapter: goya.HostAdapterFuncs{ID: "format-host/v1", BindValue: func(r *goya.Runtime, value any) (goya.Value, error) { binds++; return r.ToValue(value), nil }}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.DeclareTypes("/types.d.ts", "interface Payload{value:number}\n"); err != nil {
		t.Fatal(err)
	}
	env, err := registry.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source := "export const result=host({value:1});\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": source}}, Environment: env})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	targets, err := s.TypeDefinition(ctx, "/main.ts", protocol.Position{Character: 15})
	if err != nil || len(targets.Targets) != 1 {
		t.Fatalf("host: %+v %v", targets, err)
	}
	doc, err := s.ReadDocument(ctx, targets.Targets[0].TargetURI)
	if err != nil || !doc.ReadOnly {
		t.Fatalf("readonly: %+v %v", doc, err)
	}
	name, err := s.DocumentName(ctx, doc.URI)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.FormatDocument(ctx, name, &protocol.FormattingOptions{TabSize: 2, InsertSpaces: true}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("readonly formatting: %v", err)
	}
	for _, options := range []*protocol.FormattingOptions{{TabSize: 0}, {TabSize: 1 << 20}} {
		if _, err = s.FormatDocument(ctx, "/main.ts", options); err == nil {
			t.Fatal("invalid formatting options accepted")
		}
	}
	negative := int32(-1)
	if err = s.Configure(ctx, WorkspaceConfiguration{TypeScript: &TypeScriptConfiguration{Format: &protocol.FormatCodeSettings{IndentSize: &negative}}}); !errors.Is(err, &Error{Code: CodeInvalidOptions}) {
		t.Fatalf("invalid settings: %v", err)
	}
	snapshot, err := s.Snapshot(ctx)
	if err != nil || snapshot.Project.Files["/main.ts"] != source || calls != 0 || binds != 0 {
		t.Fatalf("formatter effects: calls=%d binds=%d %v", calls, binds, err)
	}
}
func TestFormattingWireCancelsConfigurationAndRecovers(t *testing.T) {
	ctx := context.Background()
	source := "export const value=1;\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": source}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := startTestConnection(t, nil, 8, 1, testClientSetup{session: s, capabilities: map[string]any{"workspace": map[string]any{"configuration": true}}})
	c.send(t, nil, "textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: "file:///main.ts", LanguageID: "typescript", Version: 1, Text: source}})
	hover := func(id string) {
		c.send(t, id, "textDocument/hover", protocol.HoverParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, Position: protocol.Position{Character: 15}})
	}
	hover("cancelled")
	first := c.read(t)
	if string(first["method"]) != `"workspace/configuration"` {
		t.Fatal(first)
	}
	c.send(t, nil, "$/cancelRequest", map[string]any{"id": "cancelled"})
	for {
		message := c.read(t)
		if string(message["method"]) == `"$/cancelRequest"` {
			continue
		}
		assertResponseCode(t, message, `"cancelled"`, -32800)
		break
	}
	hover("recovered")
	request := c.read(t)
	if string(request["method"]) != `"workspace/configuration"` {
		t.Fatalf("cancelled request polluted cache: %+v", request)
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": []any{nil}})
	if err = writeFrame(ctx, c.client, body); err != nil {
		t.Fatal(err)
	}
	if response := c.read(t); string(response["id"]) != `"recovered"` {
		t.Fatalf("unavailable config fallback: %+v", response)
	}
	c.finish(t)
}
