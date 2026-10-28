package lsp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/totoer/goya/lsp/protocol"
)

func renameRequestPreparers(s *Session) map[string]requestPreparer {
	handlers := map[string]requestPreparer{}
	for _, method := range []string{"textDocument/prepareRename", "textDocument/rename"} {
		handlers[method] = func(ctx context.Context, _ *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var params struct {
				TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
				Position     *protocol.Position               `json:"position"`
				NewName      *string                          `json:"newName"`
			}
			if err := decodeObject(raw, &params); err != nil {
				return nil, err
			}
			prepare := method == "textDocument/prepareRename"
			if params.TextDocument == nil || params.Position == nil || (!prepare && params.NewName == nil) {
				return nil, &Error{Code: CodeInvalidOptions, Message: "rename requires textDocument, position and (for rename) newName"}
			}
			q, err := s.captureQuery(ctx)
			if err != nil {
				return nil, err
			}
			name, err := q.name(params.TextDocument.URI)
			if err != nil {
				return nil, err
			}
			// TLS editor methods operate only on buffers opened by this client. The
			// typed SDK also supports closed catalog files.
			if q.version(name) == nil {
				return func(context.Context) (any, error) { return nil, nil }, nil
			}
			newName := ""
			if params.NewName != nil {
				newName = *params.NewName
			}
			work, err := s.prepareRename(q, name, *params.Position, newName, prepare)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) {
				prepared, result, err := work(ctx)
				var unavailable *RenameUnavailableError
				if prepare && errors.As(err, &unavailable) {
					return nil, &ResponseError{Code: -32603, Message: "Request textDocument/prepareRename failed with message: " + unavailable.Reason}
				}
				if err != nil {
					return nil, err
				}
				if prepare {
					return prepared.Range, nil
				}
				if result.Edits == nil {
					return nil, nil
				}
				return result.Edits.Edit, nil
			}, nil
		}
	}
	return handlers
}
