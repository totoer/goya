package lsp

import (
	"context"
	"encoding/json"
	"github.com/totoer/goya/lsp/protocol"
)

func structureRequestPreparers(s *Session) map[string]requestPreparer {
	handlers := map[string]requestPreparer{}
	for method, kind := range map[string]structureKind{"textDocument/foldingRange": structureFolding, "textDocument/selectionRange": structureSelection, "textDocument/linkedEditingRange": structureLinked} {
		handlers[method] = func(ctx context.Context, _ *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var params struct {
				TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
				Position     *protocol.Position               `json:"position"`
				Positions    *[]protocol.Position             `json:"positions"`
			}
			if err := decodeObject(raw, &params); err != nil {
				return nil, err
			}
			if params.TextDocument == nil || kind == structureSelection && params.Positions == nil || kind == structureLinked && params.Position == nil {
				return nil, &Error{Code: CodeInvalidOptions, Message: method + " requires textDocument and its position/positions"}
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
				if kind == structureFolding {
					return nil, &ResponseError{Code: -32603, Message: "Request textDocument/foldingRange failed with message: The document should be opened for foldingRanges', file: " + string(params.TextDocument.URI)}
				}
				return func(context.Context) (any, error) { return nil, nil }, nil
			}
			var positions []protocol.Position
			if kind == structureSelection {
				positions = *params.Positions
			} else if kind == structureLinked {
				positions = []protocol.Position{*params.Position}
			}
			work, err := s.prepareStructure(q, name, positions, kind)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) {
				r, err := work(ctx)
				switch kind {
				case structureFolding:
					return r.folding.Ranges, err
				case structureSelection:
					return r.selection.Ranges, err
				default:
					return r.linked.Ranges, err
				}
			}, nil
		}
	}
	return handlers
}
