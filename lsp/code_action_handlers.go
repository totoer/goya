package lsp

import (
	"context"
	"encoding/json"
	"github.com/totoer/goya/lsp/protocol"
)

func codeActionCapabilities(raw json.RawMessage) (CodeActionCapabilities, error) {
	var wire struct {
		TextDocument struct {
			CodeAction CodeActionCapabilities `json:"codeAction"`
		} `json:"textDocument"`
	}
	if err := decodeObject(raw, &wire); err != nil {
		return CodeActionCapabilities{}, err
	}
	return wire.TextDocument.CodeAction, nil
}
func codeActionRequestPreparers(s *Session) map[string]requestPreparer {
	return map[string]requestPreparer{
		"textDocument/codeAction": func(ctx context.Context, c *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var params struct {
				TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
				Range        *protocol.Range                  `json:"range"`
				Context      *protocol.CodeActionContext      `json:"context"`
			}
			if err := decodeObject(raw, &params); err != nil {
				return nil, err
			}
			if params.TextDocument == nil || params.Range == nil || params.Context == nil || params.Context.Diagnostics == nil {
				return nil, &Error{Code: CodeInvalidOptions, Message: "codeAction requires textDocument, range, and context.diagnostics"}
			}
			q, err := s.captureQuery(ctx)
			if err != nil {
				return nil, err
			}
			name, err := q.name(params.TextDocument.URI)
			if err != nil {
				return nil, err
			}
			if q.version(name) == nil {
				return func(context.Context) (any, error) { return []protocol.CodeAction{}, nil }, nil
			}
			work, err := s.prepareCodeActions(q, name, *params.Range, *params.Context, &CodeActionRequestOptions{Capabilities: &c.codeActionCaps, DiagnosticCapabilities: &c.publishDiagnosticCaps})
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) {
				result, err := work(ctx)
				if err == nil {
					c.rememberRefactorCommands(s, result)
				}
				return result.Actions, err
			}, nil
		},
		"codeAction/resolve": func(ctx context.Context, c *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var action protocol.CodeAction
			if err := decodeObject(raw, &action); err != nil {
				return nil, err
			}
			if c.codeActionCaps.ResolveSupport == nil {
				return func(ctx context.Context) (any, error) { return action, s.checkContext(ctx) }, nil
			}
			work, err := s.prepareResolveCodeAction(ctx, action)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) {
				result, err := work(ctx)
				if err != nil {
					return nil, err
				}
				return resolveCodeActionProperties(action, result.Action, c.codeActionCaps), nil
			}, nil
		},
	}
}
