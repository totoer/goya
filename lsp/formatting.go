package lsp

import (
	"context"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

type FormattingResult struct {
	Generation uint64
	Version    *int32
	URI        protocol.DocumentURI
	TextEdits  []protocol.TextEdit
	Edits      *BoundEdits
}

func (s *Session) FormatDocument(ctx context.Context, name string, options *protocol.FormattingOptions) (FormattingResult, error) {
	return s.Formatting(ctx, name, nil, options)
}
func (s *Session) FormatRange(ctx context.Context, name string, span protocol.Range, options *protocol.FormattingOptions) (FormattingResult, error) {
	return s.Formatting(ctx, name, &span, options)
}

// Formatting proposes edits for the captured original source. Nil span formats
// the document; nil options asks the optional document-settings provider.
func (s *Session) Formatting(ctx context.Context, name string, span *protocol.Range, options *protocol.FormattingOptions) (FormattingResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return FormattingResult{}, err
	}
	work, err := s.prepareFormatting(q, name, span, options)
	if err != nil {
		return FormattingResult{}, err
	}
	return work(ctx)
}
func (s *Session) prepareFormatting(q *querySnapshot, name string, span *protocol.Range, options *protocol.FormattingOptions) (func(context.Context) (FormattingResult, error), error) {
	name, _, err := q.position(name, protocol.Position{})
	if err != nil {
		return nil, err
	}
	var nativeSpan *lsproto.Range
	if span != nil {
		_, start, err := q.position(name, span.Start)
		if err != nil {
			return nil, err
		}
		_, end, err := q.position(name, span.End)
		if err != nil {
			return nil, err
		}
		if start.Line > end.Line || start.Line == end.Line && start.Character > end.Character {
			return nil, &Error{Code: CodeInvalidOptions, Message: "formatting range is reversed"}
		}
		nativeSpan = &lsproto.Range{Start: lsproto.Position{Line: start.Line, Character: start.Character}, End: lsproto.Position{Line: end.Line, Character: end.Character}}
	}
	ownOptions := cloneFormattingOptions(options)
	return func(ctx context.Context) (FormattingResult, error) {
		settings, err := s.formattingSettings(ctx, q, name, ownOptions)
		if err != nil {
			return FormattingResult{}, err
		}
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return FormattingResult{}, err
		}
		defer lease.release()
		result := FormattingResult{Generation: q.snapshot.Generation, Version: q.version(name), URI: sourceURI(name, q.snapshot.Environment, q.mappings), TextEdits: []protocol.TextEdit{}}
		ctx = lsproto.WithClientCapabilities(lease.ctx, &lsproto.ResolvedClientCapabilities{})
		err = lease.state.native.WithService(ctx, name, func(ctx context.Context, service *ls.LanguageService) error {
			edits, err := service.ProvideReferenceFormatting(ctx, nativeURI(name), settings, nativeSpan)
			if err != nil {
				return err
			}
			for _, edit := range edits {
				result.TextEdits = append(result.TextEdits, protocol.TextEdit{Range: publicRange(edit.Range), NewText: edit.NewText})
			}
			return nil
		})
		if err != nil {
			return FormattingResult{}, publicServiceError(err)
		}
		if err = lease.ctx.Err(); err != nil {
			return FormattingResult{}, err
		}
		if len(result.TextEdits) > 0 {
			s.mu.Lock()
			defer s.mu.Unlock()
			if err = s.checkLocked(ctx); err != nil {
				return FormattingResult{}, err
			}
			if s.generation != q.snapshot.Generation {
				return FormattingResult{}, ErrContentModified
			}
			bound, err := s.bindEditsLocked(protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{result.URI: result.TextEdits}})
			if err != nil {
				return FormattingResult{}, err
			}
			result.Edits = &bound
		}
		return result, nil
	}, nil
}
