package lsp

import (
	"context"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

type InlayHintResult struct {
	Generation uint64
	Version    *int32
	Hints      []protocol.InlayHint
}

// InlayHints computes owned labels and original source locations for a captured
// range. Hints are presentation only and never apply edits or invoke Go.
func (s *Session) InlayHints(ctx context.Context, name string, span protocol.Range) (InlayHintResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return InlayHintResult{}, err
	}
	work, err := s.prepareInlayHints(q, name, span)
	if err != nil {
		return InlayHintResult{}, err
	}
	return work(ctx)
}
func (s *Session) prepareInlayHints(q *querySnapshot, name string, span protocol.Range) (func(context.Context) (InlayHintResult, error), error) {
	name, start, err := q.position(name, span.Start)
	if err != nil {
		return nil, err
	}
	_, end, err := q.position(name, span.End)
	if err != nil {
		return nil, err
	}
	if start.Line > end.Line || start.Line == end.Line && start.Character > end.Character {
		return nil, &Error{Code: CodeInvalidOptions, Message: "inlay hint range is reversed"}
	}
	nativeRange := lsproto.Range{Start: lsproto.Position{Line: start.Line, Character: start.Character}, End: lsproto.Position{Line: end.Line, Character: end.Character}}
	interactive := preferenceEnabled(q.snapshot.EditorOptions.Preferences.InteractiveInlayHints)
	return func(ctx context.Context) (InlayHintResult, error) {
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return InlayHintResult{}, err
		}
		defer lease.release()
		ctx = lsproto.WithClientCapabilities(lease.ctx, &lsproto.ResolvedClientCapabilities{})
		result := InlayHintResult{Generation: q.snapshot.Generation, Version: q.version(name), Hints: []protocol.InlayHint{}}
		err = lease.state.native.WithService(ctx, name, func(ctx context.Context, service *ls.LanguageService) error {
			response, err := service.ProvideReferenceInlayHints(ctx, &lsproto.InlayHintParams{TextDocument: lsproto.TextDocumentIdentifier{Uri: nativeURI(name)}, Range: nativeRange}, interactive)
			if err != nil {
				return err
			}
			if response.InlayHints != nil {
				for _, hint := range *response.InlayHints {
					public := protocol.InlayHint{Position: protocol.Position{Line: hint.Position.Line, Character: hint.Position.Character}}
					if hint.Kind != nil {
						kind := protocol.InlayHintKind(*hint.Kind)
						public.Kind = &kind
					}
					public.PaddingLeft = hint.PaddingLeft != nil && *hint.PaddingLeft
					public.PaddingRight = hint.PaddingRight != nil && *hint.PaddingRight
					if hint.Label.String != nil {
						value := *hint.Label.String
						public.Label.String = &value
					} else if hint.Label.InlayHintLabelParts != nil {
						var plain strings.Builder
						parts := make([]protocol.InlayHintLabelPart, 0, len(*hint.Label.InlayHintLabelParts))
						for _, part := range *hint.Label.InlayHintLabelParts {
							plain.WriteString(part.Value)
							label := protocol.InlayHintLabelPart{Value: part.Value}
							if part.Location != nil {
								label.Location = &protocol.Location{URI: sourceURI(part.Location.Uri.FileName(), q.snapshot.Environment, q.mappings), Range: publicRange(part.Location.Range)}
							}
							parts = append(parts, label)
						}
						if interactive {
							public.Label.Parts = parts
						} else {
							text := plain.String()
							public.Label.String = &text
						}
					}
					result.Hints = append(result.Hints, public)
				}
			}
			return nil
		})
		if err != nil {
			return InlayHintResult{}, publicServiceError(err)
		}
		return result, lease.ctx.Err()
	}, nil
}
