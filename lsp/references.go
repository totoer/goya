package lsp

import (
	"context"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

type ReferenceOptions struct{ IncludeDeclaration bool }
type ReferenceResult struct {
	Generation uint64
	Version    *int32
	Locations  []protocol.Location
}
type DocumentHighlightResult struct {
	Generation uint64
	Version    *int32
	Highlights []protocol.DocumentHighlight
}

// References searches related projects in the captured virtual workspace.
// Nil options include declaration locations, as does an explicit true value.
func (s *Session) References(ctx context.Context, name string, position protocol.Position, options *ReferenceOptions) (ReferenceResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return ReferenceResult{}, err
	}
	work, err := s.prepareReferences(q, name, position, options)
	if err != nil {
		return ReferenceResult{}, err
	}
	return work(ctx)
}
func (s *Session) prepareReferences(q *querySnapshot, name string, position protocol.Position, options *ReferenceOptions) (func(context.Context) (ReferenceResult, error), error) {
	name, position, err := q.position(name, position)
	if err != nil {
		return nil, err
	}
	include := options == nil || options.IncludeDeclaration
	return func(ctx context.Context) (ReferenceResult, error) {
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return ReferenceResult{}, err
		}
		defer lease.release()
		ctx = lsproto.WithClientCapabilities(lease.ctx, &lsproto.ResolvedClientCapabilities{})
		result := ReferenceResult{Generation: q.snapshot.Generation, Version: q.version(name), Locations: []protocol.Location{}}
		err = lease.state.native.WithCrossProjectService(ctx, name, func(ctx context.Context, service *ls.LanguageService, search ls.CrossProjectOrchestrator) error {
			response, err := service.ProvideReferenceLocations(ctx, &lsproto.ReferenceParams{TextDocument: lsproto.TextDocumentIdentifier{Uri: nativeURI(name)}, Position: lsproto.Position{Line: position.Line, Character: position.Character}, Context: &lsproto.ReferenceContext{IncludeDeclaration: include}}, search)
			if err != nil {
				return err
			}
			if response.Locations != nil {
				for _, location := range *response.Locations {
					result.Locations = append(result.Locations, protocol.Location{URI: sourceURI(location.Uri.FileName(), q.snapshot.Environment, q.mappings), Range: publicRange(location.Range)})
				}
			}
			return nil
		})
		if err != nil {
			return ReferenceResult{}, publicServiceError(err)
		}
		return result, lease.ctx.Err()
	}, nil
}

// DocumentHighlights returns semantic and syntactic spans with the reference
// server's highlight-kind mapping in
// the request document, without changing sources or invoking Go bindings.
func (s *Session) DocumentHighlights(ctx context.Context, name string, position protocol.Position) (DocumentHighlightResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return DocumentHighlightResult{}, err
	}
	work, err := s.prepareDocumentHighlights(q, name, position)
	if err != nil {
		return DocumentHighlightResult{}, err
	}
	return work(ctx)
}
func (s *Session) prepareDocumentHighlights(q *querySnapshot, name string, position protocol.Position) (func(context.Context) (DocumentHighlightResult, error), error) {
	name, position, err := q.position(name, position)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (DocumentHighlightResult, error) {
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return DocumentHighlightResult{}, err
		}
		defer lease.release()
		ctx = lsproto.WithClientCapabilities(lease.ctx, &lsproto.ResolvedClientCapabilities{})
		result := DocumentHighlightResult{Generation: q.snapshot.Generation, Version: q.version(name), Highlights: []protocol.DocumentHighlight{}}
		err = lease.state.native.WithService(ctx, name, func(ctx context.Context, service *ls.LanguageService) error {
			response, err := service.ProvideReferenceDocumentHighlights(ctx, nativeURI(name), lsproto.Position{Line: position.Line, Character: position.Character})
			if err != nil {
				return err
			}
			if response.DocumentHighlights != nil {
				for _, highlight := range *response.DocumentHighlights {
					item := protocol.DocumentHighlight{Range: publicRange(highlight.Range)}
					if highlight.Kind != nil {
						kind := protocol.DocumentHighlightKind(*highlight.Kind)
						item.Kind = &kind
					}
					result.Highlights = append(result.Highlights, item)
				}
			}
			return nil
		})
		if err != nil {
			return DocumentHighlightResult{}, publicServiceError(err)
		}
		return result, lease.ctx.Err()
	}, nil
}
