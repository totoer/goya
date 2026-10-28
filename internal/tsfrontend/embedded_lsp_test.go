package tsfrontend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/totoer/goya/internal/tsfrontend/upstream/bundled"
	nativelsp "github.com/totoer/goya/internal/tsfrontend/upstream/lsp"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/internal/tsfrontend/upstream/project/background"
)

func TestEmbeddedBackgroundQueueCloseWaitsForRegisteredWork(t *testing.T) {
	for i := 0; i < 50; i++ {
		queue := background.NewQueue()
		ctx, cancel := context.WithCancel(context.Background())
		var producers sync.WaitGroup
		for n := 0; n < 4; n++ {
			producers.Go(func() {
				for j := 0; j < 20; j++ {
					queue.Enqueue(ctx, func(ctx context.Context) { <-ctx.Done() })
				}
			})
		}
		cancel()
		queue.Close()
		queue.Wait()
		producers.Wait()
		called := false
		queue.Enqueue(context.Background(), func(context.Context) { called = true })
		queue.Wait()
		if called {
			t.Fatal("closed queue accepted work")
		}
	}
}

type embeddedTransport struct {
	ctx context.Context
	in  chan *lsproto.Message
	out chan []byte
}

func (c *embeddedTransport) Read() (*lsproto.Message, error) {
	select {
	case <-c.ctx.Done():
		return nil, io.EOF
	case m := <-c.in:
		return m, nil
	}
}
func (c *embeddedTransport) Write(m *lsproto.Message) error {
	raw, err := m.MarshalJSON()
	if err != nil {
		return err
	}
	var envelope struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	if envelope.Method != "" && len(envelope.ID) != 0 {
		// Native initialization registers a configuration watcher even when the
		// client has no configuration support. No OS watcher is supplied here.
		response := new(lsproto.Message)
		if err := response.UnmarshalJSON([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":null}`, envelope.ID))); err != nil {
			return err
		}
		select {
		case c.in <- response:
			return nil
		case <-c.ctx.Done():
			return c.ctx.Err()
		}
	}
	select {
	case c.out <- raw:
		return nil
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}
func (c *embeddedTransport) send(t *testing.T, raw string) {
	t.Helper()
	m := new(lsproto.Message)
	if err := m.UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	select {
	case c.in <- m:
	case <-c.ctx.Done():
		t.Fatal(c.ctx.Err())
	}
}
func (c *embeddedTransport) response(t *testing.T, id int) map[string]json.RawMessage {
	t.Helper()
	for {
		select {
		case raw := <-c.out:
			var value map[string]json.RawMessage
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			if string(value["id"]) == fmt.Sprint(id) {
				return value
			}
		case <-c.ctx.Done():
			t.Fatal(c.ctx.Err())
			return nil
		}
	}
}

func TestEmbeddedNativeLSPUsesOnlySuppliedSources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	transport := &embeddedTransport{ctx: ctx, in: make(chan *lsproto.Message, 32), out: make(chan []byte, 64)}
	fs, err := freezeFiles(map[string]string{
		"/__goya_virtual__/main.ts":       "const count: number = 1;\ncount;",
		"/__goya_virtual__/tsconfig.json": `{"compilerOptions":{"strict":true,"target":"es2025","lib":["es2025"],"types":[]},"files":["main.ts"]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var externalCalls atomic.Int32
	opts := &nativelsp.ServerOptions{Embedded: true, In: transport, Out: transport, Err: io.Discard, Cwd: "/__goya_virtual__", FS: bundled.WrapFS(fs), DefaultLibraryPath: bundled.LibPath(), TypingsLocation: "/__would_install_types__",
		NpmInstall: func(string, []string) ([]byte, error) {
			externalCalls.Add(1)
			return nil, fmt.Errorf("external npm hook invoked")
		},
		SetParentProcessID: func(int) { externalCalls.Add(1) },
	}
	server := nativelsp.NewServer(opts)
	if _, err := server.NpmInstall("/", nil); err == nil {
		t.Fatal("embedded server exposed npm hook")
	}
	if opts.NpmInstall == nil || opts.SetParentProcessID == nil || opts.TypingsLocation == "" {
		t.Fatal("constructor mutated caller options")
	}
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("native server failed to stop")
		}
	}()
	transport.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":123,"rootUri":"file:///__goya_virtual__","capabilities":{},"initializationOptions":{"enableTelemetry":true}}}`)
	if v := transport.response(t, 1); v["error"] != nil {
		t.Fatalf("initialize: %s", v["error"])
	}
	transport.send(t, `{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	transport.send(t, `{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///__goya_virtual__/main.ts","languageId":"typescript","version":1,"text":"const count: number = 1;\ncount;"}}}`)
	transport.send(t, `{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"file:///__goya_virtual__/main.ts"},"position":{"line":1,"character":2}}}`)
	if v := transport.response(t, 2); v["error"] != nil || string(v["result"]) == "null" {
		t.Fatalf("virtual hover: %+v", v)
	}
	transport.send(t, `{"jsonrpc":"2.0","id":3,"method":"custom/saveHeapProfile","params":{"dir":"/__must_not_write__"}}`)
	v := transport.response(t, 3)
	var e struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(v["error"], &e); err != nil || e.Code != -32601 {
		t.Fatalf("profiling command was not rejected: %+v", v)
	}
	transport.send(t, `{"jsonrpc":"2.0","id":4,"method":"custom/initializeAPISession","params":{}}`)
	v = transport.response(t, 4)
	if err := json.Unmarshal(v["error"], &e); err != nil || e.Code != -32601 {
		t.Fatalf("external API session was not rejected: %+v", v)
	}
	transport.send(t, `{"jsonrpc":"2.0","id":5,"method":"shutdown"}`)
	if v := transport.response(t, 5); v["error"] != nil {
		t.Fatalf("shutdown: %+v", v)
	}
	if externalCalls.Load() != 0 {
		t.Fatalf("external hooks called %d times", externalCalls.Load())
	}
}
