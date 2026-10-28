package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"strconv"
	"unicode/utf8"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

// ServerOptions supplies either a caller-owned session or inputs for a new
// connection-owned session. Streams always remain owned by the caller.
type ServerOptions struct {
	Session               *Session
	SessionOptions        SessionOptions
	MaxFrameBytes         int // default 16 MiB
	Logger                *log.Logger
	MaxConcurrentRequests int // default 8, maximum 128
	MaxClientRequests     int // default 16, maximum 128
}

// ProtocolError reports a connection failure without exiting the application.
type ProtocolError struct {
	Message string
	Cause   error
}

func (e *ProtocolError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}
func (e *ProtocolError) Unwrap() error { return e.Cause }

var ErrExitWithoutShutdown = &ProtocolError{Message: "LSP exit received before shutdown"}

type rpcError = ResponseError
type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve runs LSP on supplied streams. It never closes them or exits the process.
// Context-aware transports implement ContextReader/ContextWriter; a host using
// ordinary blocking IO is responsible for unblocking it during cancellation.
func Serve(ctx context.Context, in io.Reader, out io.Writer, options ServerOptions) error {
	return serveWithHandlers(ctx, in, out, options, nil)
}

func serveWithHandlers(ctx context.Context, in io.Reader, out io.Writer, options ServerOptions, handlers map[string]requestPreparer) error {
	if ctx == nil || in == nil || out == nil {
		return &Error{Code: CodeInvalidOptions, Message: "Serve requires context, reader and writer"}
	}
	limit := options.MaxFrameBytes
	if limit == 0 {
		limit = 16 << 20
	}
	if limit < 1 {
		return &Error{Code: CodeInvalidOptions, Message: "frame byte limit must be positive"}
	}
	requestLimit := options.MaxConcurrentRequests
	if requestLimit == 0 {
		requestLimit = 8
	}
	clientLimit := options.MaxClientRequests
	if clientLimit == 0 {
		clientLimit = 16
	}
	if requestLimit < 1 || requestLimit > 128 || clientLimit < 1 || clientLimit > 128 {
		return &Error{Code: CodeInvalidOptions, Message: "connection request limits must be between 1 and 128"}
	}
	session := options.Session
	if session == nil {
		var err error
		session, err = NewSession(ctx, options.SessionOptions)
		if err != nil {
			return err
		}
		defer session.Close()
	}
	if _, err := session.Snapshot(ctx); err != nil {
		return err
	}
	registered := builtinRequestPreparers(session)
	for method, prepare := range handlers {
		if registered[method] == nil {
			registered[method] = prepare
		}
	}
	handlers = registered
	connection := newRPCConnection(ctx, out, options.Logger, requestLimit, clientLimit)
	defer connection.close()
	reader := newFrameReader(connection.ctx, in, limit)
	initialized, ready, shutdown := false, false, false
	respond := connection.respond
	for {
		data, err := reader.read()
		if err != nil {
			if fatal := connection.failure(); fatal != nil {
				return fatal
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return &ProtocolError{Message: "read LSP frame", Cause: err}
		}
		var message rpcEnvelope
		trimmed := bytes.TrimSpace(data)
		if err := json.Unmarshal(data, &message); err != nil || !utf8.Valid(data) {
			code := int32(-32700)
			if utf8.Valid(data) && json.Valid(data) {
				code = -32600
			}
			if err := respond(nil, nil, &rpcError{Code: code, Message: "Invalid JSON-RPC message"}); err != nil {
				return err
			}
			continue
		}
		if len(trimmed) == 0 || trimmed[0] != '{' || message.JSONRPC != "2.0" || !validRequestID(message.ID) {
			if err := respond(nil, nil, &rpcError{Code: -32600, Message: "Invalid Request"}); err != nil {
				return err
			}
			continue
		}
		if connection.acceptResponse(message) {
			continue
		}
		if message.Method == "" {
			if err := respond(nil, nil, &rpcError{Code: -32600, Message: "missing method"}); err != nil {
				return err
			}
			continue
		}
		request := len(message.ID) > 0
		var result any
		var rpcErr *rpcError
		switch message.Method {
		case "initialize":
			if !request || initialized || shutdown {
				rpcErr = &rpcError{Code: -32600, Message: "initialize must be the first request"}
				break
			}
			var initParams struct {
				Capabilities          json.RawMessage `json:"capabilities"`
				InitializationOptions struct {
					Locale                       string `json:"locale"`
					HostInfo                     string `json:"hostInfo"`
					CompletionDisableFilterText  bool   `json:"completionDisableFilterText"`
					SupportsHoverVerbosity       bool   `json:"supportsHoverVerbosity"`
					SupportsMoveToFileCodeAction bool   `json:"supportsMoveToFileCodeAction"`
					Preferences                  *struct {
						EditorPreferences
						DisableSuggestions *bool `json:"disableSuggestions"`
					} `json:"preferences"`
				} `json:"initializationOptions"`
			}
			if decodeObject(message.Params, &initParams) != nil || !isJSONObject(initParams.Capabilities) {
				rpcErr = &rpcError{Code: -32602, Message: "initialize requires an object"}
				break
			}
			caps, err := decodeClientCapabilities(initParams.Capabilities)
			if err != nil {
				rpcErr = rpcFailure(err)
				break
			}
			connection.capabilities = caps
			connection.workspaceConfiguration, err = workspaceConfigurationCapability(initParams.Capabilities)
			if err != nil {
				rpcErr = rpcFailure(err)
				break
			}
			connection.hoverVerbosity = initParams.InitializationOptions.SupportsHoverVerbosity
			connection.codeLensSupport, connection.codeLensRefreshSupport, err = codeLensCapabilities(initParams.Capabilities)
			if err != nil {
				rpcErr = rpcFailure(err)
				break
			}
			connection.inlayRefreshSupport, err = inlayRefreshCapability(initParams.Capabilities)
			if err != nil {
				rpcErr = rpcFailure(err)
				break
			}
			connection.hierarchicalSymbols, err = hierarchicalSymbolCapability(initParams.Capabilities)
			if err != nil {
				rpcErr = rpcFailure(err)
				break
			}
			connection.definitionLinks, err = definitionLinkCapability(initParams.Capabilities)
			if err != nil {
				rpcErr = rpcFailure(err)
				break
			}
			completion, err := completionClientCapabilities(initParams.Capabilities)
			if err != nil {
				rpcErr = rpcFailure(err)
				break
			}
			connection.completionCaps = completion
			connection.codeActionCaps, err = codeActionCapabilities(initParams.Capabilities)
			connection.codeActionCaps.SupportsMoveToFileCodeAction = initParams.InitializationOptions.SupportsMoveToFileCodeAction
			if err != nil {
				rpcErr = rpcFailure(err)
				break
			}
			pull, push, publish, version, err := diagnosticClientCapabilities(initParams.Capabilities)
			if err != nil {
				rpcErr = rpcFailure(err)
				break
			}
			connection.pullDiagnosticCaps = pull
			connection.publishDiagnosticCaps = push
			connection.publishDiagnosticsEnabled = publish
			connection.publishDiagnosticVersion = version
			var progressCaps struct {
				Window struct {
					WorkDoneProgress bool `json:"workDoneProgress"`
				} `json:"window"`
			}
			if err := json.Unmarshal(initParams.Capabilities, &progressCaps); err != nil {
				rpcErr = rpcFailure(err)
				break
			}
			connection.workDoneProgress = progressCaps.Window.WorkDoneProgress
			if initParams.InitializationOptions.Locale != "" || initParams.InitializationOptions.HostInfo != "" || initParams.InitializationOptions.CompletionDisableFilterText {
				initial, err := session.Snapshot(connection.ctx)
				if err != nil {
					rpcErr = rpcFailure(err)
					break
				}
				options := initial.EditorOptions
				options.Locale = initParams.InitializationOptions.Locale
				options.HostInfo = initParams.InitializationOptions.HostInfo
				options.CompletionDisableFilterText = initParams.InitializationOptions.CompletionDisableFilterText
				if err := session.UpdateEditorOptions(connection.ctx, options); err != nil {
					rpcErr = rpcFailure(err)
					break
				}
			}
			if preferences := initParams.InitializationOptions.Preferences; preferences != nil {
				snapshot, err := session.Snapshot(connection.ctx)
				if err != nil {
					rpcErr = rpcFailure(err)
					break
				}
				options := snapshot.EditorOptions
				options.Preferences = preferences.EditorPreferences
				if preferences.DisableSuggestions != nil {
					options.Diagnostics.DisableSuggestions = *preferences.DisableSuggestions
				}
				if err := session.UpdateEditorOptions(connection.ctx, options); err != nil {
					rpcErr = rpcFailure(err)
					break
				}
			}
			initialized = true
			result = protocol.InitializeResult{Capabilities: protocol.ServerCapabilities{Workspace: &protocol.WorkspaceServerCapabilities{FileOperations: &protocol.WorkspaceFileOperationsServerCapabilities{WillRename: &protocol.FileOperationRegistrationOptions{Filters: []protocol.FileOperationFilter{{Scheme: "file", Pattern: protocol.FileOperationPattern{Glob: "**/*.{ts,tsx,mts,cts}", Matches: "file"}}, {Scheme: "file", Pattern: protocol.FileOperationPattern{Glob: "**", Matches: "folder"}}}}}}, CodeActionProvider: &protocol.CodeActionOptions{ResolveProvider: true, CodeActionKinds: []protocol.CodeActionKind{protocol.CodeActionQuickFix, protocol.CodeActionRefactor, protocol.CodeActionSourceFixAllTS, protocol.CodeActionSourceRemoveUnusedTS, protocol.CodeActionSourceAddMissingImportsTS, protocol.CodeActionSourceOrganizeImportsTS, protocol.CodeActionSourceSortImportsTS, protocol.CodeActionSourceRemoveUnusedImportsTS}}, PositionEncoding: "utf-16", DocumentFormattingProvider: true, DocumentRangeFormattingProvider: true, CodeLensProvider: &protocol.CodeLensOptions{ResolveProvider: true}, TextDocumentSync: &protocol.TextDocumentSyncOptions{OpenClose: true, Change: 2, Save: &protocol.SaveOptions{}}, HoverProvider: true, InlayHintProvider: true, SemanticTokensProvider: &protocol.SemanticTokensOptions{Legend: SemanticTokensLegend(), Full: true, Range: true}, CallHierarchyProvider: true, FoldingRangeProvider: true, SelectionRangeProvider: true, LinkedEditingRangeProvider: true, DocumentSymbolProvider: true, WorkspaceSymbolProvider: true, RenameProvider: &protocol.RenameOptions{PrepareProvider: true}, ReferencesProvider: true, DocumentHighlightProvider: true, ExecuteCommandProvider: &protocol.ExecuteCommandOptions{Commands: []string{sourceDefinitionCommand, applyRefactoringCommand, organizeImportsCommand, applyRenameFileCommand, applyCompletionCodeActionCommand, applyCodeActionCommand, applyFixAllCodeActionCommand}}, DefinitionProvider: true, TypeDefinitionProvider: true, ImplementationProvider: true, SignatureHelpProvider: &protocol.SignatureHelpOptions{TriggerCharacters: []string{"(", ",", "<"}, RetriggerCharacters: []string{")"}}, CompletionProvider: &protocol.CompletionOptions{ResolveProvider: true, TriggerCharacters: []string{".", "\"", "'", "/", "@", "<", "#", " "}}, DiagnosticProvider: &protocol.DiagnosticProviderOptions{Identifier: "goya-typescript", InterFileDependencies: true}}, ServerInfo: &protocol.ServerInfo{Name: "goya", Version: goya.TypeScriptVersion()}}
		case "initialized":
			if initialized && !ready && !shutdown && !request && isJSONObject(message.Params) {
				if err := connection.send(connection.ctx, notification("$/typescriptVersion", protocol.TypeScriptVersionParams{Version: goya.TypeScriptVersion(), Source: "bundled"})); err != nil {
					return err
				}
				if err := connection.startDiagnostics(session); err != nil {
					return err
				}
				ready = true
			} else {
				rpcErr = &rpcError{Code: -32600, Message: "unexpected initialized"}
			}
		case "shutdown":
			if !initialized {
				rpcErr = &rpcError{Code: -32002, Message: "Server not initialized"}
				break
			}
			if !request || shutdown {
				rpcErr = &rpcError{Code: -32600, Message: "invalid shutdown"}
				break
			}
			if !noParams(message.Params) {
				rpcErr = &rpcError{Code: -32602, Message: "shutdown takes no parameters"}
				break
			}
			shutdown = true
			connection.stopDiagnostics()
			connection.cancelActiveAndWait()
		case "exit":
			if request || !noParams(message.Params) {
				rpcErr = &rpcError{Code: -32600, Message: "invalid exit"}
				break
			}
			if !shutdown {
				return ErrExitWithoutShutdown
			}
			return nil
		default:
			if !initialized || !ready {
				rpcErr = &rpcError{Code: -32002, Message: "Server not initialized"}
			} else if shutdown {
				rpcErr = &rpcError{Code: -32600, Message: "Server has shut down"}
			} else {
				if message.Method == "$/cancelRequest" {
					if request {
						rpcErr = &rpcError{Code: -32600, Message: "cancelRequest must be a notification"}
					} else {
						rpcErr = rpcFailure(connection.cancelRequest(message.Params))
					}
				} else if prepare := handlers[message.Method]; prepare != nil {
					if !request {
						rpcErr = &rpcError{Code: -32600, Message: "editor operation must be a request"}
						break
					}
					work, err := prepare(connection.ctx, connection, message.Params)
					if err != nil {
						rpcErr = rpcFailure(err)
					} else {
						if err := connection.startRequest(message.ID, work); err != nil {
							return err
						}
						continue
					}
				} else if message.Method == "goya/readDocument" {
					if !request {
						rpcErr = &rpcError{Code: -32600, Message: "goya/readDocument must be a request"}
						break
					}
					var p protocol.ReadDocumentParams
					if err := decodeObject(message.Params, &p); err != nil {
						rpcErr = &rpcError{Code: -32602, Message: err.Error()}
					} else {
						document, err := session.ReadDocument(connection.ctx, p.URI)
						if err != nil {
							rpcErr = rpcFailure(err)
						} else {
							result = document
						}
					}
				} else if message.Method == "workspace/didChangeConfiguration" {
					if request {
						rpcErr = &rpcError{Code: -32600, Message: "configuration changes must be notifications"}
					} else {
						var p struct {
							Settings WorkspaceConfiguration `json:"settings"`
						}
						if err := decodeObject(message.Params, &p); err != nil {
							rpcErr = rpcFailure(err)
						} else {
							rpcErr = rpcFailure(connection.configure(connection.ctx, session, p.Settings))
						}
					}
				} else if isSynchronizationMethod(message.Method) {
					if request {
						rpcErr = &rpcError{Code: -32600, Message: "document/file synchronization must be a notification"}
					} else if _, err := synchronize(connection.ctx, session, message.Method, message.Params); err != nil {
						rpcErr = &rpcError{Code: -32602, Message: err.Error()}
					}
				} else {
					rpcErr = &rpcError{Code: -32601, Message: "Method not found: " + message.Method}
				}
			}
		}
		if request {
			if rpcErr != nil {
				connection.log("LSP request %s %s: %s", message.ID, message.Method, rpcErr.Message)
			}
			if err := respond(message.ID, result, rpcErr); err != nil {
				return err
			}
		} else if rpcErr != nil && options.Logger != nil {
			options.Logger.Printf("LSP notification %s: %s", message.Method, rpcErr.Message)
		}
	}
}

func isJSONObject(data []byte) bool {
	data = bytes.TrimSpace(data)
	return len(data) > 0 && data[0] == '{' && json.Valid(data)
}
func noParams(data []byte) bool {
	return len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null"))
}
func validRequestID(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	id = bytes.TrimSpace(id)
	if len(id) > 0 && id[0] == '"' {
		var value string
		return json.Unmarshal(id, &value) == nil
	}
	if len(id) == 0 || bytes.ContainsAny(id, ".eE") {
		return false
	}
	// Validate arbitrarily large integer IDs without conversion to float64.
	for i, b := range id {
		if b == '-' && i == 0 {
			continue
		}
		if b < '0' || b > '9' {
			return false
		}
	}
	if _, err := strconv.ParseInt(string(id), 10, 64); err == nil {
		return true
	}
	return json.Valid(id) && len(id) > 1
}
