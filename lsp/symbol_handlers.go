package lsp

import (
	"context"
	"encoding/json"

	"github.com/totoer/goya/lsp/protocol"
)

func hierarchicalSymbolCapability(raw json.RawMessage) (bool, error) {
	var caps struct {
		TextDocument struct {
			DocumentSymbol struct {
				Hierarchical bool `json:"hierarchicalDocumentSymbolSupport"`
			} `json:"documentSymbol"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(raw, &caps); err != nil {
		return false, &Error{Code: CodeInvalidOptions, Message: "invalid document symbol capability", Cause: err}
	}
	return caps.TextDocument.DocumentSymbol.Hierarchical, nil
}
func symbolRequestPreparers(s *Session) map[string]requestPreparer {
	return map[string]requestPreparer{
		"textDocument/documentSymbol": func(ctx context.Context, c *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var params struct {
				TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
			}
			if err := decodeObject(raw, &params); err != nil {
				return nil, err
			}
			if params.TextDocument == nil {
				return nil, &Error{Code: CodeInvalidOptions, Message: "documentSymbol requires textDocument"}
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
				return func(context.Context) (any, error) { return []protocol.DocumentSymbol{}, nil }, nil
			}
			work, err := s.prepareDocumentSymbols(q, name)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) {
				result, err := work(ctx)
				if c.hierarchicalSymbols {
					return result.Symbols, err
				}
				return result.Flat(), err
			}, nil
		},
		"workspace/symbol": func(ctx context.Context, _ *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var params struct {
				Query *string `json:"query"`
			}
			if err := decodeObject(raw, &params); err != nil {
				return nil, err
			}
			if params.Query == nil {
				return nil, &Error{Code: CodeInvalidOptions, Message: "workspace/symbol requires query"}
			}
			q, err := s.captureQuery(ctx)
			if err != nil {
				return nil, err
			}
			work := s.prepareWorkspaceSymbols(q, *params.Query)
			return func(ctx context.Context) (any, error) { result, err := work(ctx); return result.Symbols, err }, nil
		},
	}
}
