package lsp

import (
	"context"
	"encoding/json"
	"github.com/totoer/goya/lsp/protocol"
)

func inlayHintRequestPreparers(s *Session) map[string]requestPreparer {
	return map[string]requestPreparer{"textDocument/inlayHint": func(ctx context.Context, c *rpcConnection, raw json.RawMessage) (requestWork, error) {
		var params struct {
			TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
			Range        *protocol.Range                  `json:"range"`
		}
		if err := decodeObject(raw, &params); err != nil {
			return nil, err
		}
		if params.TextDocument == nil || params.Range == nil {
			return nil, &Error{Code: CodeInvalidOptions, Message: "inlayHint requires textDocument and range"}
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
			return func(ctx context.Context) (any, error) {
				err := c.send(ctx, notification("window/showMessage", map[string]any{"type": 1, "message": "Inlay Hints request failed. File not opened in the editor."}))
				return []protocol.InlayHint{}, err
			}, nil
		}
		work, err := s.prepareInlayHints(q, name, *params.Range)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) (any, error) { r, err := work(ctx); return r.Hints, err }, nil
	}}
}
