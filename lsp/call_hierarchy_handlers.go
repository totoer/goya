package lsp

import (
	"context"
	"encoding/json"
	"github.com/totoer/goya/lsp/protocol"
)

func hierarchyRequestPreparers(s *Session) map[string]requestPreparer {
	handlers := map[string]requestPreparer{
		"textDocument/prepareCallHierarchy": func(ctx context.Context, _ *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var params struct {
				TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
				Position     *protocol.Position               `json:"position"`
			}
			if err := decodeObject(raw, &params); err != nil {
				return nil, err
			}
			if params.TextDocument == nil || params.Position == nil {
				return nil, &Error{Code: CodeInvalidOptions, Message: "prepareCallHierarchy requires textDocument and position"}
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
				return func(context.Context) (any, error) { return nil, nil }, nil
			}
			work, err := s.prepareCallHierarchy(q, name, *params.Position)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) { r, err := work(ctx); return r.Items, err }, nil
		},
	}
	for _, incoming := range []bool{true, false} {
		method := "callHierarchy/outgoingCalls"
		if incoming {
			method = "callHierarchy/incomingCalls"
		}
		handlers[method] = func(ctx context.Context, _ *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var params struct {
				Item *protocol.CallHierarchyItem `json:"item"`
			}
			if err := decodeObject(raw, &params); err != nil {
				return nil, err
			}
			if params.Item == nil {
				return nil, &Error{Code: CodeInvalidOptions, Message: method + " requires item"}
			}
			work, err := s.prepareHierarchyCalls(ctx, *params.Item, incoming)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) {
				in, out, err := work(ctx)
				if incoming {
					return in.Calls, err
				}
				return out.Calls, err
			}, nil
		}
	}
	return handlers
}
