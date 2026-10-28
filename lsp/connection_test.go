package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
	"log"
	"net"
	"strings"
	"testing"
	"time"
)

type testConnection struct {
	client             net.Conn
	reader             *frameReader
	done               chan error
	logs               *bytes.Buffer
	captureDiagnostics bool
}

type testClientSetup struct {
	session            *Session
	capabilities       any
	captureDiagnostics bool
}

func startTestConnection(t *testing.T, handlers map[string]requestPreparer, requestLimit, clientLimit int, setups ...testClientSetup) *testConnection {
	t.Helper()
	client, server := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	deadline := time.Now().Add(10 * time.Second)
	_ = client.SetDeadline(deadline)
	_ = server.SetDeadline(deadline)
	logs := &bytes.Buffer{}
	done := make(chan error, 1)
	session := testSession(t, nil)
	var capabilities any = map[string]any{}
	if len(setups) > 0 {
		if setups[0].session != nil {
			session = setups[0].session
		}
		if setups[0].capabilities != nil {
			capabilities = setups[0].capabilities
		}
	}
	go func() {
		done <- serveWithHandlers(ctx, server, server, ServerOptions{Session: session, Logger: log.New(logs, "APP LOG: ", 0), MaxConcurrentRequests: requestLimit, MaxClientRequests: clientLimit}, handlers)
	}()
	t.Cleanup(func() { cancel(); client.Close(); server.Close() })
	c := &testConnection{client: client, reader: newFrameReader(ctx, client, 16<<20), done: done, logs: logs}
	if len(setups) > 0 {
		c.captureDiagnostics = setups[0].captureDiagnostics
	}
	c.send(t, 1, "initialize", map[string]any{"capabilities": capabilities})
	c.read(t)
	c.send(t, nil, "initialized", map[string]any{})
	version := c.read(t)
	assertVersionNotification(t, version)
	return c
}
func (c *testConnection) send(t *testing.T, id any, method string, params any) {
	t.Helper()
	if _, err := c.client.Write([]byte(messageFrame(t, id, method, params))); err != nil {
		t.Fatal(err)
	}
}
func (c *testConnection) read(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	for {
		data, err := c.reader.read()
		if err != nil {
			t.Fatal(err)
		}
		var message map[string]json.RawMessage
		if err := json.Unmarshal(data, &message); err != nil {
			t.Fatal(err)
		}
		if !c.captureDiagnostics && string(message["method"]) == `"textDocument/publishDiagnostics"` {
			continue
		}
		return message
	}
}
func (c *testConnection) finish(t *testing.T) {
	t.Helper()
	c.send(t, "shutdown", "shutdown", nil)
	response := c.read(t)
	if string(response["id"]) != `"shutdown"` {
		t.Fatalf("shutdown response: %+v", response)
	}
	c.send(t, nil, "exit", nil)
	if err := receiveError(t, c.done); err != nil {
		t.Fatal(err)
	}
}
func assertResponseCode(t *testing.T, message map[string]json.RawMessage, id string, code int32) {
	t.Helper()
	var rpcErr ResponseError
	if err := json.Unmarshal(message["error"], &rpcErr); err != nil {
		t.Fatal(err)
	}
	if string(message["id"]) != id || rpcErr.Code != code {
		t.Fatalf("response: %+v error=%+v", message, rpcErr)
	}
}

func TestWireCancelContinuesAndBoundsRequestWork(t *testing.T) {
	entered := make(chan struct{}, 1)
	handlers := map[string]requestPreparer{"test/slow": func(context.Context, *rpcConnection, json.RawMessage) (requestWork, error) {
		return func(ctx context.Context) (any, error) { entered <- struct{}{}; <-ctx.Done(); return nil, ctx.Err() }, nil
	}}
	c := startTestConnection(t, handlers, 1, 1)
	c.send(t, "slow", "test/slow", map[string]any{})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	c.send(t, "overflow", "test/slow", map[string]any{})
	assertResponseCode(t, c.read(t), `"overflow"`, -32000)
	c.send(t, nil, "$/cancelRequest", map[string]any{"id": "slow"})
	assertResponseCode(t, c.read(t), `"slow"`, -32800)
	c.send(t, 3, "unknown/method", map[string]any{})
	assertResponseCode(t, c.read(t), "3", -32601)
	c.send(t, 4, "goya/readDocument", map[string]any{"uri": "file:///main.ts"})
	response := c.read(t)
	if response["error"] != nil {
		t.Fatalf("request after cancellation failed: %+v", response)
	}
	c.finish(t)
	if !strings.Contains(c.logs.String(), "APP LOG:") || !strings.Contains(c.logs.String(), "unknown/method") {
		t.Fatalf("separate logger missing errors: %s", c.logs.String())
	}
}

func TestBidirectionalClientRequestAndMatchingResponse(t *testing.T) {
	handlers := map[string]requestPreparer{"test/client": func(_ context.Context, c *rpcConnection, _ json.RawMessage) (requestWork, error) {
		return func(ctx context.Context) (any, error) {
			var result []struct {
				TabSize int `json:"tabSize"`
			}
			err := c.requestClient(ctx, "workspace/configuration", map[string]any{"items": []any{map[string]any{"section": "typescript.format"}}}, &result)
			return result, err
		}, nil
	}}
	c := startTestConnection(t, handlers, 2, 1)
	c.send(t, "outer", "test/client", map[string]any{})
	clientRequest := c.read(t)
	if string(clientRequest["method"]) != `"workspace/configuration"` {
		t.Fatalf("client request: %+v", clientRequest)
	}
	// The only client-request slot is occupied; another worker fails without
	// blocking the reader that must receive the first client's response.
	c.send(t, "overflow", "test/client", map[string]any{})
	assertResponseCode(t, c.read(t), `"overflow"`, -32602)
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": clientRequest["id"], "result": []any{map[string]any{"tabSize": 4}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(context.Background(), c.client, body); err != nil {
		t.Fatal(err)
	}
	response := c.read(t)
	if string(response["id"]) != `"outer"` || string(response["result"]) != `[{"tabSize":4}]` {
		t.Fatalf("client result not delivered: %+v", response)
	}
	c.finish(t)
}

func TestCancelledClientRequestReleasesPendingState(t *testing.T) {
	handlers := map[string]requestPreparer{"test/client": func(_ context.Context, c *rpcConnection, _ json.RawMessage) (requestWork, error) {
		return func(ctx context.Context) (any, error) {
			var result any
			return nil, c.requestClient(ctx, "workspace/configuration", map[string]any{}, &result)
		}, nil
	}}
	c := startTestConnection(t, handlers, 1, 1)
	c.send(t, 2, "test/client", map[string]any{})
	clientRequest := c.read(t)
	c.send(t, nil, "$/cancelRequest", map[string]any{"id": 2})
	assertResponseCode(t, c.read(t), "2", -32800)
	// A late response is ignored, and the next request can occupy the slot.
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": clientRequest["id"], "result": nil})
	if err := writeFrame(context.Background(), c.client, body); err != nil {
		t.Fatal(err)
	}
	c.send(t, 3, "test/client", map[string]any{})
	next := c.read(t)
	body, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": next["id"], "error": map[string]any{"code": -32800, "message": "client declined"}})
	if err := writeFrame(context.Background(), c.client, body); err != nil {
		t.Fatal(err)
	}
	assertResponseCode(t, c.read(t), "3", -32800)
	c.finish(t)
}

func TestInvalidJSONAndParamsLeaveConnectionUsable(t *testing.T) {
	tape := rpcFrame(`{`) + messageFrame(t, 1, "initialize", map[string]any{"capabilities": map[string]any{}}) + messageFrame(t, nil, "initialized", map[string]any{}) +
		messageFrame(t, 2, "goya/readDocument", map[string]any{"uri": 42}) + messageFrame(t, 3, "goya/readDocument", map[string]any{"uri": "file:///main.ts"}) + messageFrame(t, 4, "shutdown", nil) + messageFrame(t, nil, "exit", nil)
	var output, logs bytes.Buffer
	s := testSession(t, nil)
	if err := Serve(context.Background(), strings.NewReader(tape), &output, ServerOptions{Session: s, Logger: log.New(&logs, "APP LOG: ", 0)}); err != nil {
		t.Fatal(err)
	}
	responses := responseTape(t, output.Bytes())
	if len(responses) != 5 {
		t.Fatalf("responses: %s", output.Bytes())
	}
	assertResponseCode(t, responses[0], "null", -32700)
	assertResponseCode(t, responses[2], "2", -32602)
	if responses[3]["error"] != nil || string(responses[3]["id"]) != "3" {
		t.Fatalf("connection poisoned: %+v", responses[3])
	}
	if bytes.Contains(output.Bytes(), []byte("APP LOG:")) || !bytes.Contains(logs.Bytes(), []byte("APP LOG:")) {
		t.Fatal("logger contaminated or bypassed protocol")
	}
	var brokenOutput bytes.Buffer
	err := Serve(context.Background(), strings.NewReader("Content-Length: 50\r\n\r\nshort"), &brokenOutput, ServerOptions{Session: s})
	var framing *ProtocolError
	if !errors.As(err, &framing) {
		t.Fatalf("malformed framing: %v", err)
	}
	if _, err := s.Snapshot(context.Background()); err != nil {
		t.Fatal("malformed frame closed caller session")
	}
}

func assertVersionNotification(t *testing.T, message map[string]json.RawMessage) {
	t.Helper()
	var params protocol.TypeScriptVersionParams
	if string(message["method"]) != `"$/typescriptVersion"` || message["id"] != nil || json.Unmarshal(message["params"], &params) != nil || params.Version != goya.TypeScriptVersion() || params.Source != "bundled" {
		t.Fatalf("embedded version notification: %+v", message)
	}
}
