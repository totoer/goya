package lsp

import (
	"context"
	"encoding/json"
	"github.com/totoer/goya/lsp/protocol"
)

func codeLensRequestPreparers(s *Session) map[string]requestPreparer {
	return map[string]requestPreparer{
		"textDocument/codeLens": func(ctx context.Context, c *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var p struct {
				TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
			}
			if err := decodeObject(raw, &p); err != nil {
				return nil, err
			}
			if p.TextDocument == nil {
				return nil, &Error{Code: CodeInvalidOptions, Message: "codeLens requires textDocument"}
			}
			q, err := s.captureQuery(ctx)
			if err != nil {
				return nil, err
			}
			name, err := q.name(p.TextDocument.URI)
			if err != nil {
				return nil, err
			}
			if !c.codeLensSupport || q.version(name) == nil {
				return func(context.Context) (any, error) { return []protocol.CodeLens{}, nil }, nil
			}
			work, err := s.prepareCodeLenses(q, name)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) { r, err := work(ctx); return r.Lenses, err }, nil
		},
		"codeLens/resolve": func(ctx context.Context, c *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var item protocol.CodeLens
			if err := decodeObject(raw, &item); err != nil {
				return nil, err
			}
			work, err := s.prepareResolveCodeLens(ctx, item)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) { r, err := work(ctx); return r.Lens, err }, nil
		},
	}
}
