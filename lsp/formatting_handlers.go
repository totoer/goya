package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/totoer/goya/lsp/protocol"
)

func formattingRequestPreparers(s *Session) map[string]requestPreparer {
	result := map[string]requestPreparer{}
	for _, method := range []string{"textDocument/formatting", "textDocument/rangeFormatting"} {
		result[method] = func(ctx context.Context, c *rpcConnection, raw json.RawMessage) (requestWork, error) {
			var p struct {
				TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
				Range        *protocol.Range                  `json:"range"`
				Options      *protocol.FormattingOptions      `json:"options"`
			}
			if err := decodeObject(raw, &p); err != nil {
				return nil, err
			}
			if p.TextDocument == nil || p.Options == nil || method == "textDocument/rangeFormatting" && p.Range == nil {
				return nil, &Error{Code: CodeInvalidOptions, Message: "formatting requires textDocument, options and an applicable range"}
			}
			q, err := s.captureQuery(ctx)
			if err != nil {
				return nil, err
			}
			name, err := q.name(p.TextDocument.URI)
			if err != nil {
				return nil, err
			}
			if q.version(name) == nil {
				if method == "textDocument/rangeFormatting" {
					return func(context.Context) (any, error) { return []protocol.TextEdit{}, nil }, nil
				}
				return func(context.Context) (any, error) {
					return nil, &ResponseError{Code: -32603, Message: fmt.Sprintf("Request textDocument/formatting failed with message: The document should be opened for formatting', file: %s", p.TextDocument.URI)}
				}, nil
			}
			if method == "textDocument/formatting" {
				p.Range = nil
			}
			work, err := s.prepareFormatting(q, name, p.Range, p.Options)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context) (any, error) { r, err := work(ctx); return r.TextEdits, err }, nil
		}
	}
	return result
}
