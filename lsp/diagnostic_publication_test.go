package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/totoer/goya/lsp/protocol"
)

func publishedDiagnostics(t *testing.T, c *testConnection) protocol.PublishDiagnosticsParams {
	t.Helper()
	message := c.read(t)
	if string(message["method"]) != `"textDocument/publishDiagnostics"` {
		t.Fatalf("unexpected diagnostic transcript frame: %+v", message)
	}
	var result protocol.PublishDiagnosticsParams
	if err := json.Unmarshal(message["params"], &result); err != nil {
		t.Fatal(err)
	}
	if result.Diagnostics == nil {
		t.Fatal("diagnostics must be an array")
	}
	return result
}

func TestWireAutomaticDiagnosticsAndClearing(t *testing.T) {
	for _, versionSupport := range []bool{false, true} {
		t.Run(map[bool]string{false: "without versions", true: "with versions"}[versionSupport], func(t *testing.T) {
			s := testSession(t, nil)
			var caps any = map[string]any{}
			if versionSupport {
				caps = map[string]any{"textDocument": map[string]any{"publishDiagnostics": map[string]any{"versionSupport": true}}}
			}
			c := startTestConnection(t, nil, 8, 16, testClientSetup{session: s, capabilities: caps, captureDiagnostics: true})
			c.send(t, nil, "workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"diagnostics": map[string]any{"eagerClear": true}}})
			c.send(t, nil, "textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: "file:///main.ts", LanguageID: "typescript", Version: 1, Text: `export const value:number="wrong";`}})
			d := publishedDiagnostics(t, c)
			if len(d.Diagnostics) != 1 || d.Diagnostics[0].Code != 2322 || (d.Version != nil) != versionSupport {
				t.Fatalf("initial publication: %+v", d)
			}
			c.send(t, nil, "textDocument/didChange", protocol.DidChangeTextDocumentParams{TextDocument: protocol.VersionedTextDocumentIdentifier{URI: d.URI, Version: 2}, ContentChanges: []protocol.ContentChange{{Text: "export const value:number=42;"}}})
			clear := publishedDiagnostics(t, c)
			if len(clear.Diagnostics) != 0 {
				t.Fatalf("eager clear: %+v", clear)
			}
			d = publishedDiagnostics(t, c)
			if len(d.Diagnostics) != 0 || (versionSupport && (d.Version == nil || *d.Version != 2)) {
				t.Fatalf("corrected publication: %+v", d)
			}
			c.send(t, nil, "textDocument/didClose", protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: d.URI}})
			clear = publishedDiagnostics(t, c)
			if len(clear.Diagnostics) != 0 || clear.Version != nil {
				t.Fatalf("close clear: %+v", clear)
			}
			c.finish(t)
		})
	}
}

func testDiagnosticPublisher(t *testing.T, s *Session, w *DiagnosticStream, progress bool) (*rpcConnection, *testConnection) {
	t.Helper()
	client, server := net.Pipe()
	_ = client.SetDeadline(time.Now().Add(10 * time.Second))
	_ = server.SetDeadline(time.Now().Add(10 * time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	c := newRPCConnection(ctx, server, nil, 8, 16)
	c.publishDiagnosticVersion = true
	c.workDoneProgress = progress
	c.runDiagnosticPublisher(ctx, cancel, s, w)
	t.Cleanup(func() { cancel(); client.Close(); server.Close(); c.close() })
	return c, &testConnection{client: client, reader: newFrameReader(ctx, client, 16<<20), captureDiagnostics: true}
}

func TestWireLateOldDiagnosticTranscript(t *testing.T) {
	ctx := context.Background()
	s := testSession(t, nil)
	if err := s.OpenDocument(ctx, "/main.ts", 1, "old error"); err != nil {
		t.Fatal(err)
	}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	w, err := s.watchDiagnostics(ctx, nil, slowDiagnosticAnalyzer(entered, release, finished))
	if err != nil {
		t.Fatal(err)
	}
	_, c := testDiagnosticPublisher(t, s, w, false)
	<-entered
	if err := s.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Text: "corrected"}}); err != nil {
		t.Fatal(err)
	}
	d := publishedDiagnostics(t, c)
	if d.Version == nil || *d.Version != 2 || len(d.Diagnostics) != 0 {
		t.Fatal(d)
	}
	close(release)
	released = true
	<-finished
	if err := s.ChangeDocument(ctx, "/main.ts", 3, []protocol.ContentChange{{Text: "still corrected"}}); err != nil {
		t.Fatal(err)
	}
	d = publishedDiagnostics(t, c)
	if d.Version == nil || *d.Version != 3 || len(d.Diagnostics) != 0 {
		t.Fatal(d)
	}
}

func TestDiagnosticPublicationRechecksGenerationAfterWriterGate(t *testing.T) {
	s := testSession(t, nil)
	snapshot, _ := s.Snapshot(context.Background())
	event := DiagnosticEvent{Kind: DiagnosticEventResult, Generation: snapshot.Generation, URI: "file:///main.ts"}
	var output bytes.Buffer
	c := newRPCConnection(context.Background(), &output, nil, 8, 16)
	defer c.close()
	<-c.writeGate
	done := make(chan error, 1)
	go func() {
		sent, err := c.sendChecked(c.ctx, notification("textDocument/publishDiagnostics", protocol.PublishDiagnosticsParams{URI: event.URI, Diagnostics: []protocol.Diagnostic{}}), func() bool { return s.diagnosticEventCurrent(event) })
		if sent {
			t.Error("obsolete frame written")
		}
		done <- err
	}()
	if err := s.OpenDocument(context.Background(), "/main.ts", 1, "corrected"); err != nil {
		t.Fatal(err)
	}
	c.writeGate <- struct{}{}
	if err := receiveError(t, done); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatal(output.String())
	}
}

func TestDiagnosticWorkDoneProgressAndUnresponsiveClient(t *testing.T) {
	for _, acknowledge := range []bool{false, true} {
		t.Run(map[bool]string{false: "unresponsive", true: "acknowledged"}[acknowledge], func(t *testing.T) {
			s := testSession(t, nil)
			ctx := context.Background()
			if err := s.OpenDocument(ctx, "/main.ts", 1, "export {};"); err != nil {
				t.Fatal(err)
			}
			release := make(chan struct{})
			defer close(release)
			w, err := s.watchDiagnostics(ctx, nil, func(ctx context.Context, q *querySnapshot, name string, _ *DiagnosticOptions) (DiagnosticResult, error) {
				select {
				case <-release:
				case <-ctx.Done():
					return DiagnosticResult{}, ctx.Err()
				}
				return DiagnosticResult{Generation: q.snapshot.Generation, URI: "file:///main.ts", Version: q.version(name), Items: []protocol.Diagnostic{}}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			connection, c := testDiagnosticPublisher(t, s, w, true)
			request := c.read(t)
			if string(request["method"]) != `"window/workDoneProgress/create"` {
				t.Fatal(request)
			}
			if acknowledge {
				if !connection.acceptResponse(rpcEnvelope{ID: request["id"], Result: json.RawMessage("null")}) {
					t.Fatal("progress reply ignored")
				}
				begin := c.read(t)
				var progress protocol.ProgressParams
				if err := json.Unmarshal(begin["params"], &progress); err != nil {
					t.Fatal(err)
				}
				if string(begin["method"]) != `"$/progress"` || progress.Value.(map[string]any)["kind"] != "begin" {
					t.Fatal(begin)
				}
			}
			release <- struct{}{}
			if d := publishedDiagnostics(t, c); len(d.Diagnostics) != 0 {
				t.Fatal(d)
			}
			if acknowledge {
				end := c.read(t)
				var progress protocol.ProgressParams
				if err := json.Unmarshal(end["params"], &progress); err != nil {
					t.Fatal(err)
				}
				if string(end["method"]) != `"$/progress"` || progress.Value.(map[string]any)["kind"] != "end" {
					t.Fatal(end)
				}
			}
		})
	}
}
