package lsp

import (
	"context"
	"encoding/json"

	"github.com/totoer/goya/lsp/protocol"
)

func completionClientCapabilities(raw json.RawMessage) (CompletionCapabilities, error) {
	var wire struct {
		TextDocument struct {
			Completion struct {
				CompletionItem CompletionCapabilities `json:"completionItem"`
			} `json:"completion"`
		} `json:"textDocument"`
	}
	if err := decodeObject(raw, &wire); err != nil {
		return CompletionCapabilities{}, err
	}
	return wire.TextDocument.Completion.CompletionItem, nil
}

func completionRequestPreparers(s *Session) map[string]requestPreparer {
	return map[string]requestPreparer{
		"textDocument/completion": func(ctx context.Context, c *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var params struct {
				TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
				Position     *protocol.Position               `json:"position"`
				Context      *protocol.CompletionContext      `json:"context"`
			}
			if err := decodeObject(raw, &params); err != nil {
				return nil, err
			}
			if params.TextDocument == nil || params.Position == nil {
				return nil, &Error{Code: CodeInvalidOptions, Message: "completion requires textDocument and position"}
			}
			q, err := s.captureQuery(ctx)
			if err != nil {
				return nil, err
			}
			name, err := q.name(params.TextDocument.URI)
			if err != nil {
				return nil, err
			}
			work, err := s.prepareCompletion(q, name, *params.Position, &CompletionRequestOptions{Context: params.Context, Capabilities: &c.completionCaps})
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) { result, err := work(ctx); return result.List, err }, nil
		},
		"completionItem/resolve": func(ctx context.Context, _ *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var item protocol.CompletionItem
			if err := decodeObject(raw, &item); err != nil {
				return nil, err
			}
			work, err := s.prepareResolveCompletion(ctx, item)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) { result, err := work(ctx); return result.Item, err }, nil
		},
	}
}
