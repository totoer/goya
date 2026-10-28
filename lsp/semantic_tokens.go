package lsp

import (
	"context"
	"slices"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

type SemanticTokenResult struct {
	Generation uint64
	Version    *int32
	Tokens     protocol.SemanticTokens
}

// SemanticTokensLegend returns an owned copy of the fixed TLS 6.0.2 legend.
// Its final type is the reference's "member", independent of client filters.
func SemanticTokensLegend() protocol.SemanticTokensLegend {
	return protocol.SemanticTokensLegend{
		TokenTypes:     []string{"class", "enum", "interface", "namespace", "typeParameter", "type", "parameter", "variable", "enumMember", "property", "function", "member"},
		TokenModifiers: []string{"declaration", "static", "async", "readonly", "defaultLibrary", "local"},
	}
}

// SemanticTokens returns relative UTF-16 token data for the complete captured
// document, or for an optional range. The range is copied before execution.
func (s *Session) SemanticTokens(ctx context.Context, name string, rng *protocol.Range) (SemanticTokenResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return SemanticTokenResult{}, err
	}
	work, err := s.prepareSemanticTokens(q, name, rng)
	if err != nil {
		return SemanticTokenResult{}, err
	}
	return work(ctx)
}
func (s *Session) prepareSemanticTokens(q *querySnapshot, name string, rng *protocol.Range) (func(context.Context) (SemanticTokenResult, error), error) {
	name, _, err := q.position(name, protocol.Position{})
	if err != nil {
		return nil, err
	}
	var span *lsproto.Range
	if rng != nil {
		_, start, err := q.position(name, rng.Start)
		if err != nil {
			return nil, err
		}
		_, end, err := q.position(name, rng.End)
		if err != nil {
			return nil, err
		}
		if start.Line > end.Line || start.Line == end.Line && start.Character > end.Character {
			return nil, &Error{Code: CodeInvalidOptions, Message: "semantic token range is reversed"}
		}
		span = &lsproto.Range{Start: lsproto.Position{Line: start.Line, Character: start.Character}, End: lsproto.Position{Line: end.Line, Character: end.Character}}
	}
	return func(ctx context.Context) (SemanticTokenResult, error) {
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return SemanticTokenResult{}, err
		}
		defer lease.release()
		ctx = lsproto.WithClientCapabilities(lease.ctx, &lsproto.ResolvedClientCapabilities{})
		result := SemanticTokenResult{Generation: q.snapshot.Generation, Version: q.version(name), Tokens: protocol.SemanticTokens{Data: []uint32{}}}
		err = lease.state.native.WithService(ctx, name, func(ctx context.Context, service *ls.LanguageService) error {
			response, err := service.ProvideReferenceSemanticTokens(ctx, nativeURI(name), span)
			if err != nil {
				return err
			}
			if response != nil && len(response.Data) > 0 {
				result.Tokens.Data = slices.Clone(response.Data)
			}
			return nil
		})
		if err != nil {
			return SemanticTokenResult{}, publicServiceError(err)
		}
		return result, lease.ctx.Err()
	}, nil
}
