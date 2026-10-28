package lsp

import (
	"context"
	"encoding/json"

	"github.com/totoer/goya/lsp/protocol"
)

func referenceRequestPreparers(s *Session) map[string]requestPreparer {
	return map[string]requestPreparer{
		"textDocument/references": func(ctx context.Context, _ *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var params struct {
				TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
				Position     *protocol.Position               `json:"position"`
				Context      *protocol.ReferenceContext       `json:"context"`
			}
			if err := decodeObject(raw, &params); err != nil {
				return nil, err
			}
			if params.TextDocument == nil || params.Position == nil || params.Context == nil {
				return nil, &Error{Code: CodeInvalidOptions, Message: "references requires textDocument, position and context"}
			}
			q, err := s.captureQuery(ctx)
			if err != nil {
				return nil, err
			}
			name, err := q.name(params.TextDocument.URI)
			if err != nil {
				return nil, err
			}
			work, err := s.prepareReferences(q, name, *params.Position, &ReferenceOptions{IncludeDeclaration: params.Context.IncludeDeclaration})
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) { r, err := work(ctx); return r.Locations, err }, nil
		},
		"textDocument/documentHighlight": func(ctx context.Context, _ *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var params struct {
				TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
				Position     *protocol.Position               `json:"position"`
			}
			if err := decodeObject(raw, &params); err != nil {
				return nil, err
			}
			if params.TextDocument == nil || params.Position == nil {
				return nil, &Error{Code: CodeInvalidOptions, Message: "documentHighlight requires textDocument and position"}
			}
			q, err := s.captureQuery(ctx)
			if err != nil {
				return nil, err
			}
			name, err := q.name(params.TextDocument.URI)
			if err != nil {
				return nil, err
			}
			work, err := s.prepareDocumentHighlights(q, name, *params.Position)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) { r, err := work(ctx); return r.Highlights, err }, nil
		},
	}
}
