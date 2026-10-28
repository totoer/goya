package embedded

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/totoer/goya"
	"github.com/totoer/goya/internal/lspfixture"
	"github.com/totoer/goya/lsp"
)

type Notification struct {
	Method string
	Params json.RawMessage
}
type ClientRequestHandler func(context.Context, string, json.RawMessage) (json.RawMessage, error)
type wireReply struct {
	result json.RawMessage
	err    error
}
type wireMessage struct {
	JSONRPC string             `json:"jsonrpc"`
	ID      json.RawMessage    `json:"id"`
	Method  string             `json:"method"`
	Params  json.RawMessage    `json:"params"`
	Result  json.RawMessage    `json:"result"`
	Error   *lsp.ResponseError `json:"error"`
}

// Wire owns both ends of net.Pipe (an in-memory connection, no socket/listener),
// one client reader and the Serve goroutine. Cancellation sends cancelRequest;
// Close releases these owned streams and the session, including on failure.
type Wire struct {
	ctx            context.Context
	cancel         context.CancelFunc
	client, server net.Conn
	session        *lsp.Session
	serverDone     chan error
	readerDone     chan struct{}
	writeMu        sync.Mutex
	mu             sync.Mutex
	closed         bool
	nextID         uint64
	pending        map[string]chan wireReply
	clientRequests []lspfixture.ClientRequest
	notifications  []Notification
	handler        ClientRequestHandler
	initialized    bool
	closeOnce      sync.Once
	closeErr       error
}

func NewWire(ctx context.Context, options lsp.SessionOptions, initialization *lspfixture.Initialization, handlers ...ClientRequestHandler) (*Wire, error) {
	ctx, cancel := context.WithCancel(ctx)
	session, err := lsp.NewSession(ctx, options)
	if err != nil {
		cancel()
		return nil, err
	}
	client, server := net.Pipe()
	w := &Wire{ctx: ctx, cancel: cancel, client: client, server: server, session: session, serverDone: make(chan error, 1), readerDone: make(chan struct{}), pending: map[string]chan wireReply{}}
	if len(handlers) > 0 {
		w.handler = handlers[0]
	} else {
		init := defaultInitialization(initialization)
		w.handler = func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
			if response, ok := init.ClientResponses[method]; ok {
				return slices.Clone(response), nil
			}
			if method == "workspace/applyEdit" {
				return json.RawMessage(`{"applied":false,"failureReason":"capture does not apply client edits"}`), nil
			}
			if method == "_typescript.rename" {
				return json.RawMessage("null"), nil
			}
			if method != "workspace/configuration" {
				return nil, &lsp.ResponseError{Code: -32601, Message: "fixture client method not supported"}
			}
			var request struct {
				Items []struct {
					ScopeURI string `json:"scopeUri"`
					Section  string `json:"section"`
				} `json:"items"`
			}
			if err := json.Unmarshal(params, &request); err != nil {
				return nil, err
			}
			replies := make([]json.RawMessage, 0, len(request.Items))
			for _, item := range request.Items {
				reply := init.ClientConfiguration[item.ScopeURI][item.Section]
				if len(reply) == 0 {
					reply = json.RawMessage("null")
				}
				replies = append(replies, reply)
			}
			return json.Marshal(replies)
		}
	}
	stop := context.AfterFunc(ctx, func() { client.Close(); server.Close() })
	go func() {
		err := lsp.Serve(ctx, server, server, lsp.ServerOptions{Session: session})
		server.Close()
		w.serverDone <- err
	}()
	go func() { defer close(w.readerDone); defer stop(); w.readLoop() }()
	init := defaultInitialization(initialization)
	params, err := json.Marshal(map[string]any{"capabilities": init.Capabilities, "initializationOptions": init.Options})
	if err != nil {
		w.Close()
		return nil, err
	}
	if _, err := w.Call(ctx, "initialize", params); err != nil {
		w.Close()
		return nil, err
	}
	w.initialized = true
	if err := w.Notify(ctx, "initialized", json.RawMessage(`{}`)); err != nil {
		w.Close()
		return nil, err
	}
	return w, nil
}
func mapJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
func (w *Wire) Session() *lsp.Session                { return w.session }
func (w *Wire) TypeScriptVersion() string            { return goya.TypeScriptVersion() }
func (w *Wire) SourceStringCompilationEnabled() bool { return false }
func (w *Wire) DrainNotifications() []Notification {
	w.mu.Lock()
	defer w.mu.Unlock()
	result := w.notifications
	w.notifications = nil
	return result
}

func (w *Wire) send(ctx context.Context, message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { w.client.SetWriteDeadline(time.Now()); close(interrupted) })
	if deadline, ok := ctx.Deadline(); ok {
		w.client.SetWriteDeadline(deadline)
	}
	defer func() {
		if !stop() {
			<-interrupted
		}
		w.client.SetWriteDeadline(time.Time{})
	}()
	packet := append([]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))), data...)
	for len(packet) > 0 {
		n, err := w.client.Write(packet)
		if err != nil {
			w.fail(err)
			return err
		}
		if n == 0 {
			w.fail(io.ErrShortWrite)
			return io.ErrShortWrite
		}
		packet = packet[n:]
	}
	return nil
}
func (w *Wire) Notify(ctx context.Context, method string, params json.RawMessage) error {
	return w.send(ctx, map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}
func (w *Wire) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil, io.EOF
	}
	if len(w.pending) >= 64 {
		w.mu.Unlock()
		return nil, fmt.Errorf("fixture client has too many pending requests")
	}
	w.nextID++
	id := w.nextID
	key := strconv.FormatUint(id, 10)
	reply := make(chan wireReply, 1)
	w.pending[key] = reply
	w.mu.Unlock()
	defer func() { w.mu.Lock(); delete(w.pending, key); w.mu.Unlock() }()
	if err := w.send(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		cancelCtx, cancel := context.WithTimeout(w.ctx, time.Second)
		_ = w.Notify(cancelCtx, "$/cancelRequest", mapJSON(map[string]any{"id": id}))
		cancel()
		return nil, ctx.Err()
	case <-w.ctx.Done():
		return nil, w.ctx.Err()
	case response := <-reply:
		return response.result, response.err
	}
}

func (w *Wire) fail(err error) {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		for _, reply := range w.pending {
			select {
			case reply <- wireReply{err: err}:
			default:
			}
		}
	}
	w.mu.Unlock()
	w.cancel()
}
func (w *Wire) readLoop() {
	reader := bufio.NewReaderSize(w.client, 16<<10)
	for {
		message, err := readMessage(reader)
		if err != nil {
			w.fail(err)
			return
		}
		if message.Method != "" {
			if len(message.ID) > 0 {
				w.mu.Lock()
				w.clientRequests = append(w.clientRequests, lspfixture.ClientRequest{Method: message.Method, Params: slices.Clone(message.Params)})
				w.mu.Unlock()
				var result json.RawMessage
				var responseError *lsp.ResponseError
				if w.handler == nil {
					responseError = &lsp.ResponseError{Code: -32601, Message: "fixture client method not supported"}
				} else {
					value, err := w.handler(w.ctx, message.Method, message.Params)
					result = value
					if err != nil {
						if !errors.As(err, &responseError) {
							responseError = &lsp.ResponseError{Code: -32603, Message: err.Error()}
						}
					}
				}
				response := map[string]any{"jsonrpc": "2.0", "id": message.ID}
				if responseError != nil {
					response["error"] = responseError
				} else {
					response["result"] = result
				}
				if err := w.send(w.ctx, response); err != nil {
					return
				}
			} else {
				w.mu.Lock()
				if len(w.notifications) >= 1024 {
					w.mu.Unlock()
					w.fail(fmt.Errorf("fixture notification buffer overflow"))
					return
				}
				w.notifications = append(w.notifications, Notification{Method: message.Method, Params: message.Params})
				w.mu.Unlock()
			}
			continue
		}
		w.mu.Lock()
		reply := w.pending[string(message.ID)]
		w.mu.Unlock()
		if reply != nil {
			result := wireReply{result: message.Result}
			if message.Error != nil {
				result.err = message.Error
			}
			select {
			case reply <- result:
			default:
			}
		}
	}
}

func readMessage(reader *bufio.Reader) (wireMessage, error) {
	length := -1
	size := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return wireMessage{}, err
		}
		size += len(line)
		if size > 16<<10 {
			return wireMessage{}, fmt.Errorf("fixture response header overflow")
		}
		if line == "\r\n" {
			break
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			return wireMessage{}, fmt.Errorf("invalid fixture response header")
		}
		if strings.EqualFold(key, "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || length >= 0 || n < 0 || n > 64<<20 {
				return wireMessage{}, fmt.Errorf("invalid fixture response length")
			}
			length = n
		}
	}
	if length < 0 {
		return wireMessage{}, fmt.Errorf("missing fixture response length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return wireMessage{}, err
	}
	var message wireMessage
	err := json.Unmarshal(body, &message)
	if err == nil && message.JSONRPC != "2.0" {
		err = fmt.Errorf("invalid fixture response version")
	}
	return message, err
}

func (w *Wire) Close() error {
	w.closeOnce.Do(func() {
		if w.initialized {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_, _ = w.Call(ctx, "shutdown", nil)
			_ = w.Notify(ctx, "exit", nil)
			cancel()
		}
		w.cancel()
		w.client.Close()
		w.server.Close()
		w.closeErr = <-w.serverDone
		<-w.readerDone
		_ = w.session.Close()
		if errors.Is(w.closeErr, context.Canceled) || errors.Is(w.closeErr, net.ErrClosed) {
			w.closeErr = nil
		}
	})
	return w.closeErr
}

func (w *Wire) ClientRequests() []lspfixture.ClientRequest {
	w.mu.Lock()
	defer w.mu.Unlock()
	result := slices.Clone(w.clientRequests)
	for i := range result {
		result[i].Params = slices.Clone(result[i].Params)
	}
	return result
}
