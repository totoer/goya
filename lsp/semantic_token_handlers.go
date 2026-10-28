package lsp

import (
	"context"
	"encoding/json"
	"github.com/totoer/goya/lsp/protocol"
)

func semanticTokenRequestPreparers(s *Session) map[string]requestPreparer {
	handlers := map[string]requestPreparer{}
	for _, full := range []bool{true, false} {
		method := "textDocument/semanticTokens/range"
		if full {
			method = "textDocument/semanticTokens/full"
		}
		handlers[method] = func(ctx context.Context, _ *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var params struct {
				TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
				Range        *protocol.Range                  `json:"range"`
			}
			if err := decodeObject(raw, &params); err != nil {
				return nil, err
			}
			if params.TextDocument == nil || !full && params.Range == nil {
				return nil, &Error{Code: CodeInvalidOptions, Message: method + " requires textDocument and (for range) range"}
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
				return func(context.Context) (any, error) { return protocol.SemanticTokens{Data: []uint32{}}, nil }, nil
			}
			span := params.Range
			if full {
				span = nil
			}
			work, err := s.prepareSemanticTokens(q, name, span)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) {
				result, err := work(ctx)
				if err != nil {
					return nil, err
				}
				s.mu.RLock()
				current := s.generation == result.Generation
				s.mu.RUnlock()
				if !current {
					return nil, context.Canceled
				}
				return result.Tokens, nil
			}, nil
		}
	}
	return handlers
}
