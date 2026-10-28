package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func rpcFrame(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

type fragmentedInput struct {
	reader *strings.Reader
	closed bool
}

func (r *fragmentedInput) Read(p []byte) (int, error) {
	if len(p) > 3 {
		p = p[:3]
	}
	return r.reader.Read(p)
}
func (r *fragmentedInput) Close() error { r.closed = true; return nil }

type shortOutput struct {
	bytes.Buffer
	closed bool
}

func (w *shortOutput) Write(p []byte) (int, error) {
	if len(p) > 5 {
		p = p[:5]
	}
	return w.Buffer.Write(p)
}
func (w *shortOutput) Close() error { w.closed = true; return nil }

func responseTape(t *testing.T, output []byte) []map[string]json.RawMessage {
	t.Helper()
	reader := newFrameReader(context.Background(), bytes.NewReader(output), 16<<20)
	var result []map[string]json.RawMessage
	for {
		body, err := reader.read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		var message map[string]json.RawMessage
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatal(err)
		}
		if string(message["method"]) == `"$/typescriptVersion"` {
			assertVersionNotification(t, message)
			continue
		}
		result = append(result, message)
	}
	return result
}

func TestServeFragmentedLifecycleAndExactRequestIDs(t *testing.T) {
	for _, id := range []string{`1`, `"initialize request"`, `9007199254740993`, `123456789012345678901234567890`} {
		t.Run(id, func(t *testing.T) {
			s := testSession(t, nil)
			other := testSession(t, nil)
			tape := rpcFrame(`{"jsonrpc":"2.0","id":`+id+`,"method":"initialize","params":{"capabilities":{}}}`) +
				rpcFrame(`{"jsonrpc":"2.0","method":"initialized","params":{}}`) +
				rpcFrame(`{"jsonrpc":"2.0","id":"shutdown","method":"shutdown","params":null}`) +
				rpcFrame(`{"jsonrpc":"2.0","method":"exit"}`)
			input := &fragmentedInput{reader: strings.NewReader(tape)}
			output := &shortOutput{}
			if err := Serve(context.Background(), input, output, ServerOptions{Session: s}); err != nil {
				t.Fatal(err)
			}
			if input.closed || output.closed {
				t.Fatal("Serve closed caller-owned streams")
			}
			responses := responseTape(t, output.Bytes())
			if len(responses) != 2 {
				t.Fatalf("responses: %s", output.Bytes())
			}
			if string(responses[0]["id"]) != id || string(responses[1]["id"]) != `"shutdown"` || string(responses[1]["result"]) != "null" {
				t.Fatalf("IDs or null result changed: %+v", responses)
			}
			var init struct {
				Capabilities struct {
					PositionEncoding string `json:"positionEncoding"`
				}
				ServerInfo struct{ Name, Version string }
			}
			if err := json.Unmarshal(responses[0]["result"], &init); err != nil {
				t.Fatal(err)
			}
			if init.Capabilities.PositionEncoding != "utf-16" || init.ServerInfo.Name != "goya" || init.ServerInfo.Version == "" {
				t.Fatalf("initialize result: %+v", init)
			}
			if _, err := s.Snapshot(context.Background()); err != nil {
				t.Fatalf("Serve closed external session: %v", err)
			}
			if _, err := other.Snapshot(context.Background()); err != nil {
				t.Fatalf("connection affected other session: %v", err)
			}
		})
	}
}

func TestServeExitWithoutShutdownDoesNotExitApplication(t *testing.T) {
	s := testSession(t, nil)
	var output bytes.Buffer
	err := Serve(context.Background(), strings.NewReader(rpcFrame(`{"jsonrpc":"2.0","method":"exit"}`)), &output, ServerOptions{Session: s})
	if !errors.Is(err, ErrExitWithoutShutdown) {
		t.Fatalf("ungraceful exit: %v", err)
	}
	if _, err := s.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestServeRejectsLifecycleOrderAndContinues(t *testing.T) {
	tape := rpcFrame(`{"jsonrpc":"2.0","id":1,"method":"shutdown"}`) +
		rpcFrame(`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"capabilities":{}}}`) +
		rpcFrame(`{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"capabilities":{}}}`) +
		rpcFrame(`{"jsonrpc":"2.0","id":4,"method":"shutdown"}`) +
		rpcFrame(`{"jsonrpc":"2.0","method":"exit","params":null}`)
	var output bytes.Buffer
	if err := Serve(context.Background(), strings.NewReader(tape), &output, ServerOptions{}); err != nil {
		t.Fatal(err)
	}
	responses := responseTape(t, output.Bytes())
	if len(responses) != 4 {
		t.Fatalf("responses: %s", output.Bytes())
	}
	for i, want := range map[int]int32{0: -32002, 2: -32600} {
		var rpcErr rpcError
		if err := json.Unmarshal(responses[i]["error"], &rpcErr); err != nil {
			t.Fatal(err)
		}
		if rpcErr.Code != want {
			t.Fatalf("response %d: %+v", i, rpcErr)
		}
	}
}

type blockingContextInput struct {
	entered chan struct{}
	once    sync.Once
}

func (r *blockingContextInput) Read([]byte) (int, error) { panic("Serve bypassed ReadContext") }
func (r *blockingContextInput) ReadContext(ctx context.Context, p []byte) (int, error) {
	r.once.Do(func() { close(r.entered) })
	<-ctx.Done()
	return 0, ctx.Err()
}

func TestServeContextTransportCancelsBlockedRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	input := &blockingContextInput{entered: make(chan struct{})}
	var output bytes.Buffer
	s := testSession(t, nil)
	result := make(chan error, 1)
	go func() { result <- Serve(ctx, input, &output, ServerOptions{Session: s}) }()
	select {
	case <-input.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not read")
	}
	cancel()
	if err := receiveError(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("context cancellation: %v", err)
	}
	if _, err := s.Snapshot(context.Background()); err != nil {
		t.Fatal("connection cancellation changed external session")
	}
}

func TestFrameBoundsAndTruncation(t *testing.T) {
	for _, input := range []string{
		"Content-Length: 10\r\n\r\nshort",
		"Content-Length: 10\r\n\r\n",
		"Content-Length: 10\r\nContent-Length: 10\r\n\r\n0123456789",
		"Content-Length: 100\r\n\r\n",
		"Content-Length: -1\r\n\r\n",
		"Content-Type: application/vscode-jsonrpc\r\n\r\n",
	} {
		if _, err := newFrameReader(context.Background(), strings.NewReader(input), 20).read(); err == nil || err == io.EOF {
			t.Fatalf("bad frame accepted: %q %v", input, err)
		}
	}
}
