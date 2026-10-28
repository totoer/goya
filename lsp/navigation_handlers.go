package lsp

import (
	"context"
	"encoding/json"

	"github.com/totoer/goya/lsp/protocol"
)

func definitionLinkCapability(raw json.RawMessage) (bool, error) {
	var wire struct {
		TextDocument struct {
			Definition struct {
				LinkSupport bool `json:"linkSupport"`
			} `json:"definition"`
		} `json:"textDocument"`
	}
	if err := decodeObject(raw, &wire); err != nil {
		return false, err
	}
	return wire.TextDocument.Definition.LinkSupport, nil
}
func navigationRequestPreparers(s *Session) map[string]requestPreparer {
	result := map[string]requestPreparer{}
	for method, kind := range map[string]navigationKind{"textDocument/definition": navigationDefinition, "textDocument/typeDefinition": navigationTypeDefinition, "textDocument/implementation": navigationImplementation} {
		result[method] = func(ctx context.Context, c *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var params struct {
				TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
				Position     *protocol.Position               `json:"position"`
			}
			if err := decodeObject(raw, &params); err != nil {
				return nil, err
			}
			if params.TextDocument == nil || params.Position == nil {
				return nil, &Error{Code: CodeInvalidOptions, Message: method + " requires textDocument and position"}
			}
			q, err := s.captureQuery(ctx)
			if err != nil {
				return nil, err
			}
			name, err := q.name(params.TextDocument.URI)
			if err != nil {
				return nil, err
			}
			work, err := s.prepareNavigation(q, name, *params.Position, kind)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) {
				r, err := work(ctx)
				if kind == navigationDefinition && c.definitionLinks {
					return r.Targets, err
				}
				return r.Locations(), err
			}, nil
		}
	}
	return result
}
