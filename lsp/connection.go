package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/totoer/goya/lsp/protocol"
	"io"
	"log"
	"sync"
)

// ResponseError retains an LSP/JSON-RPC error returned by the connected client.
type ResponseError struct {
	Code    int32           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *ResponseError) Error() string { return fmt.Sprintf("LSP response %d: %s", e.Code, e.Message) }

type requestWork func(context.Context) (any, error)
type requestPreparer func(context.Context, *rpcConnection, json.RawMessage) (requestWork, error)
type clientReply struct {
	result json.RawMessage
	err    error
}
type rpcConnection struct {
	issuedRefactors           []issuedWireRefactor
	refactorCommandsIssued    bool
	codeActionCaps            CodeActionCapabilities
	workspaceConfiguration    bool
	formattingCache           map[protocol.DocumentURI]*protocol.DocumentFormattingOptions
	formattingCacheKeys       []protocol.DocumentURI
	inlayRefreshSupport       bool
	codeLensSupport           bool
	codeLensRefreshSupport    bool
	refreshCtx                context.Context
	refreshCancel             context.CancelFunc
	refreshStopped            bool
	refreshPending            map[string]bool
	refreshRunning            bool
	refreshWG                 sync.WaitGroup
	ctx                       context.Context
	cancel                    context.CancelFunc
	out                       io.Writer
	logger                    *log.Logger
	writeGate                 chan struct{}
	mu                        sync.Mutex
	active                    map[string]context.CancelFunc
	pendingResponses          int
	pending                   map[string]chan clientReply
	nextClientID              uint64
	requestLimit, clientLimit int
	wg                        sync.WaitGroup
	closed                    bool
	fatal                     error
	capabilities              ClientCapabilities
	hoverVerbosity            bool
	definitionLinks           bool
	hierarchicalSymbols       bool
	completionCaps            CompletionCapabilities
	pullDiagnosticCaps        DiagnosticCapabilities
	publishDiagnosticCaps     DiagnosticCapabilities
	publishDiagnosticsEnabled bool
	publishDiagnosticVersion  bool
	workDoneProgress          bool
	diagnosticCancel          context.CancelFunc
	diagnosticWG              sync.WaitGroup
	progressWG                sync.WaitGroup
	progressSlots             chan struct{}
}

func newRPCConnection(ctx context.Context, out io.Writer, logger *log.Logger, requestLimit, clientLimit int) *rpcConnection {
	ctx, cancel := context.WithCancel(ctx)
	refreshCtx, refreshCancel := context.WithCancel(ctx)
	c := &rpcConnection{formattingCache: map[protocol.DocumentURI]*protocol.DocumentFormattingOptions{}, refreshCtx: refreshCtx, refreshCancel: refreshCancel, refreshPending: make(map[string]bool), ctx: ctx, cancel: cancel, out: out, logger: logger, writeGate: make(chan struct{}, 1), active: map[string]context.CancelFunc{}, pending: map[string]chan clientReply{}, requestLimit: requestLimit, clientLimit: clientLimit, progressSlots: make(chan struct{}, 2)}
	c.writeGate <- struct{}{}
	return c
}
func (c *rpcConnection) log(format string, args ...any) {
	if c.logger != nil {
		c.logger.Printf(format, args...)
	}
}
func (c *rpcConnection) close() {
	c.mu.Lock()
	c.closed = true
	for _, cancel := range c.active {
		cancel()
	}
	c.mu.Unlock()
	c.cancel()
	c.stopDiagnostics()
	c.stopRefresh()
	c.wg.Wait()
}
func (c *rpcConnection) cancelActiveAndWait() {
	c.stopRefresh()
	c.mu.Lock()
	for _, cancel := range c.active {
		cancel()
	}
	c.mu.Unlock()
	c.wg.Wait()
}
func (c *rpcConnection) failure() error { c.mu.Lock(); defer c.mu.Unlock(); return c.fatal }

func (c *rpcConnection) send(ctx context.Context, value any) error {
	_, err := c.sendChecked(ctx, value, nil)
	return err
}

// Check generation after obtaining the writer, so queued obsolete diagnostics
// cannot overwrite a newer document's publication.
func (c *rpcConnection) sendChecked(ctx context.Context, value any, current func() bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-c.ctx.Done():
		return false, c.ctx.Err()
	case <-c.writeGate:
	}
	defer func() { c.writeGate <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if current != nil && !current() {
		return false, nil
	}
	// Complete a started frame under the connection context, even if the
	// individual request cancels. A partial frame ends this connection.
	if err := writeFrame(c.ctx, c.out, data); err != nil {
		c.mu.Lock()
		if c.fatal == nil {
			c.fatal = err
		}
		c.mu.Unlock()
		c.cancel()
		return false, err
	}
	return true, nil
}
func (c *rpcConnection) respond(id json.RawMessage, result any, rpcErr *rpcError) error {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	if rpcErr == nil {
		return c.send(c.ctx, struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Result  any             `json:"result"`
		}{"2.0", id, result})
	}
	return c.send(c.ctx, rpcResponse{JSONRPC: "2.0", ID: id, Error: rpcErr})
}
func requestKey(id json.RawMessage) string {
	id = bytes.TrimSpace(id)
	if len(id) > 0 && id[0] == '"' {
		var s string
		_ = json.Unmarshal(id, &s)
		return "s:" + s
	}
	// Valid JSON integers have no leading zeros or '+' prefix. Only -0 needs
	// canonicalization; preserve large IDs without expensive big-integer parsing.
	if bytes.Equal(id, []byte("-0")) {
		id = []byte("0")
	}
	return "n:" + string(id)
}

func rpcFailure(err error) *rpcError {
	if err == nil {
		return nil
	}
	var response *ResponseError
	if errors.As(err, &response) {
		return response
	}
	code := int32(-32603)
	var typed *Error
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code = -32800
	case errors.Is(err, ErrContentModified), errors.Is(err, ErrResultExpired):
		code = -32801
	case errors.As(err, &typed):
		switch typed.Code {
		case CodeInvalidOptions, CodeInvalidDocument, CodeInvalidProject, CodeInvalidHandle, CodeReadOnly, CodeUnsupportedClient:
			code = -32602
		case CodeClosed:
			code = -32600
		case CodeRequestLimit:
			code = -32803
		}
	}
	return &rpcError{Code: code, Message: err.Error()}
}

func (c *rpcConnection) startRequest(id json.RawMessage, work requestWork) error {
	key := requestKey(id)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return context.Canceled
	}
	if _, exists := c.active[key]; exists {
		c.mu.Unlock()
		return c.respond(id, nil, &rpcError{Code: -32600, Message: "duplicate active request ID"})
	}
	if len(c.active) >= c.requestLimit || len(c.active)+c.pendingResponses >= 2*c.requestLimit {
		c.mu.Unlock()
		return c.respond(id, nil, &rpcError{Code: -32000, Message: "concurrent request limit reached"})
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.active[key] = cancel
	c.wg.Add(1)
	c.mu.Unlock()
	go func() {
		defer func() { cancel(); c.mu.Lock(); c.pendingResponses--; c.mu.Unlock(); c.wg.Done() }()
		var result any
		err := ctx.Err()
		if err == nil {
			result, err = work(ctx)
		}
		if ctx.Err() != nil {
			result = nil
			err = ctx.Err()
		}
		// Complete work before emitting its response so a client can immediately
		// reuse admission after receiving it. Separately bound blocked responses.
		c.mu.Lock()
		delete(c.active, key)
		c.pendingResponses++
		c.mu.Unlock()
		if err != nil {
			c.log("LSP request %s: %v", id, err)
		}
		_ = c.respond(id, result, rpcFailure(err))
	}()
	return nil
}
func (c *rpcConnection) cancelRequest(params json.RawMessage) error {
	var p struct {
		ID json.RawMessage `json:"id"`
	}
	if err := decodeObject(params, &p); err != nil {
		return err
	}
	if len(p.ID) == 0 || !validRequestID(p.ID) {
		return &Error{Code: CodeInvalidOptions, Message: "cancelRequest requires an integer or string id"}
	}
	c.mu.Lock()
	cancel := c.active[requestKey(p.ID)]
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// requestClient never holds session state or a checker lease. The reader loop
// continues accepting the matching response while a request worker awaits it.
func (c *rpcConnection) requestClient(ctx context.Context, method string, params any, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return context.Canceled
	}
	if len(c.pending) >= c.clientLimit {
		c.mu.Unlock()
		return &Error{Code: CodeInvalidOptions, Message: "pending client request limit reached"}
	}
	c.nextClientID++
	id, _ := json.Marshal(fmt.Sprintf("goya:%d", c.nextClientID))
	key := requestKey(id)
	reply := make(chan clientReply, 1)
	c.pending[key] = reply
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, key); c.mu.Unlock() }()
	if err := c.send(ctx, struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  any             `json:"params"`
	}{"2.0", id, method, params}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return c.ctx.Err()
	case response := <-reply:
		if response.err != nil {
			return response.err
		}
		if result == nil {
			return nil
		}
		if err := json.Unmarshal(response.result, result); err != nil {
			return &Error{Code: CodeInvalidOptions, Message: "invalid client response result", Cause: err}
		}
		return nil
	}
}
func (c *rpcConnection) acceptResponse(message rpcEnvelope) bool {
	if message.Method != "" || len(message.ID) == 0 || (len(message.Result) == 0 && message.Error == nil) {
		return false
	}
	c.mu.Lock()
	reply := c.pending[requestKey(message.ID)]
	c.mu.Unlock()
	if reply == nil {
		c.log("LSP response for unknown or cancelled client request %s", message.ID)
		return true
	}
	response := clientReply{result: message.Result}
	if message.Error != nil {
		response.err = message.Error
	}
	if message.Error != nil && len(message.Result) > 0 {
		response.err = &Error{Code: CodeInvalidOptions, Message: "client response contains both result and error"}
	}
	select {
	case reply <- response:
	default:
		c.log("duplicate client response %s", message.ID)
	}
	return true
}
