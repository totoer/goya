package lsp

import (
	"context"
	"encoding/json"

	"github.com/totoer/goya/lsp/protocol"
)

func builtinRequestPreparers(s *Session) map[string]requestPreparer {
	handlers := map[string]requestPreparer{"textDocument/hover": func(ctx context.Context, c *rpcConnection, params json.RawMessage) (requestWork, error) {
		var p struct {
			TextDocument   *protocol.TextDocumentIdentifier `json:"textDocument"`
			Position       *protocol.Position               `json:"position"`
			VerbosityLevel *int32                           `json:"verbosityLevel"`
		}
		if err := decodeObject(params, &p); err != nil {
			return nil, err
		}
		if p.TextDocument == nil || p.Position == nil {
			return nil, &Error{Code: CodeInvalidOptions, Message: "hover requires textDocument and position"}
		}
		q, err := s.captureQuery(ctx)
		if err != nil {
			return nil, err
		}
		name, err := q.name(p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		options := &HoverOptions{SupportsVerbosity: c.hoverVerbosity}
		if p.VerbosityLevel != nil {
			options.VerbosityLevel = *p.VerbosityLevel
		}
		work, err := s.prepareHover(q, name, *p.Position, options)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) (any, error) { result, err := work(ctx); return result.Hover, err }, nil
	}, "textDocument/diagnostic": func(ctx context.Context, c *rpcConnection, params json.RawMessage) (requestWork, error) {
		var p struct {
			TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
			Identifier   string                           `json:"identifier"`
		}
		if err := decodeObject(params, &p); err != nil {
			return nil, err
		}
		if p.TextDocument == nil {
			return nil, &Error{Code: CodeInvalidOptions, Message: "diagnostic requires textDocument"}
		}
		if p.Identifier != "" && p.Identifier != "goya-typescript" {
			return nil, &Error{Code: CodeInvalidOptions, Message: "unknown diagnostic provider identifier"}
		}
		q, err := s.captureQuery(ctx)
		if err != nil {
			return nil, err
		}
		name, err := q.name(p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		work, err := s.prepareDiagnostics(q, name, &DiagnosticOptions{Capabilities: &c.pullDiagnosticCaps})
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) (any, error) {
			result, err := work(ctx)
			if err == nil {
				s.noteDiagnosticsReady(name, result.Generation)
			}
			return protocol.FullDocumentDiagnosticReport{Kind: "full", Items: result.Items}, err
		}, nil
	}}
	for method, prepare := range completionRequestPreparers(s) {
		handlers[method] = prepare
	}
	for method, prepare := range signatureHelpRequestPreparers(s) {
		handlers[method] = prepare
	}
	for method, prepare := range navigationRequestPreparers(s) {
		handlers[method] = prepare
	}
	for method, prepare := range fileRenameRequestPreparers(s) {
		handlers[method] = prepare
	}
	for method, prepare := range sourceDefinitionCommandPreparers(s) {
		handlers[method] = prepare
	}
	for method, prepare := range referenceRequestPreparers(s) {
		handlers[method] = prepare
	}
	for method, prepare := range renameRequestPreparers(s) {
		handlers[method] = prepare
	}
	for method, prepare := range symbolRequestPreparers(s) {
		handlers[method] = prepare
	}
	for method, prepare := range structureRequestPreparers(s) {
		handlers[method] = prepare
	}
	for method, prepare := range hierarchyRequestPreparers(s) {
		handlers[method] = prepare
	}
	for method, prepare := range semanticTokenRequestPreparers(s) {
		handlers[method] = prepare
	}
	for method, prepare := range inlayHintRequestPreparers(s) {
		handlers[method] = prepare
	}
	for method, prepare := range codeLensRequestPreparers(s) {
		handlers[method] = prepare
	}
	for method, prepare := range codeActionRequestPreparers(s) {
		handlers[method] = prepare
	}
	for method, prepare := range formattingRequestPreparers(s) {
		handlers[method] = prepare
	}

	// Only operations that ask the kernel for document settings use this provider.
	// Explicit formatting requests already carry their LSP formatting options.
	for _, method := range []string{"textDocument/completion", "completionItem/resolve", "textDocument/hover", "textDocument/rename", "textDocument/codeAction", "codeAction/resolve", "workspace/executeCommand", "workspace/willRenameFiles"} {
		original, ok := handlers[method]
		if !ok {
			continue
		}
		handlers[method] = func(ctx context.Context, c *rpcConnection, raw json.RawMessage) (requestWork, error) {
			work, err := original(ctx, c, raw)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) { return work(c.withFormattingProvider(ctx)) }, nil
		}
	}
	return handlers
}
