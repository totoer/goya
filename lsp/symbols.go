package lsp

import (
	"context"
	"slices"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

type DocumentSymbolResult struct {
	Generation uint64
	Version    *int32
	URI        protocol.DocumentURI
	Symbols    []protocol.DocumentSymbol
}
type WorkspaceSymbolResult struct {
	Generation uint64
	Symbols    []protocol.SymbolInformation
}

// Flat returns detached, depth-first SymbolInformation for clients without
// hierarchical outline support. Container names identify the immediate parent.
func (r DocumentSymbolResult) Flat() []protocol.SymbolInformation {
	result := []protocol.SymbolInformation{}
	var visit func([]protocol.DocumentSymbol, *string)
	visit = func(symbols []protocol.DocumentSymbol, parent *string) {
		for _, symbol := range symbols {
			var container *string
			if parent != nil {
				copy := *parent
				container = &copy
			}
			result = append(result, protocol.SymbolInformation{Name: symbol.Name, Kind: symbol.Kind, Location: protocol.Location{URI: r.URI, Range: symbol.Range}, ContainerName: container})
			visit(symbol.Children, &symbol.Name)
		}
	}
	visit(r.Symbols, nil)
	return result
}

// DocumentSymbols returns an owned outline, including for closed virtual files.
func (s *Session) DocumentSymbols(ctx context.Context, name string) (DocumentSymbolResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return DocumentSymbolResult{}, err
	}
	work, err := s.prepareDocumentSymbols(q, name)
	if err != nil {
		return DocumentSymbolResult{}, err
	}
	return work(ctx)
}
func (s *Session) prepareDocumentSymbols(q *querySnapshot, name string) (func(context.Context) (DocumentSymbolResult, error), error) {
	name, _, err := q.position(name, protocol.Position{})
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (DocumentSymbolResult, error) {
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return DocumentSymbolResult{}, err
		}
		defer lease.release()
		ctx = lsproto.WithClientCapabilities(lease.ctx, &lsproto.ResolvedClientCapabilities{})
		result := DocumentSymbolResult{Generation: q.snapshot.Generation, Version: q.version(name), URI: sourceURI(name, q.snapshot.Environment, q.mappings), Symbols: []protocol.DocumentSymbol{}}
		err = lease.state.native.WithService(ctx, name, func(ctx context.Context, service *ls.LanguageService) error {
			symbols, err := service.ProvideReferenceDocumentSymbols(ctx, nativeURI(name))
			if err != nil {
				return err
			}
			var convert func([]*lsproto.DocumentSymbol) []protocol.DocumentSymbol
			convert = func(symbols []*lsproto.DocumentSymbol) []protocol.DocumentSymbol {
				result := make([]protocol.DocumentSymbol, 0, len(symbols))
				for _, s := range symbols {
					children := []protocol.DocumentSymbol{}
					if s.Children != nil {
						children = convert(*s.Children)
					}
					result = append(result, protocol.DocumentSymbol{Name: s.Name, Kind: protocol.SymbolKind(s.Kind), Range: publicRange(s.Range), SelectionRange: publicRange(s.SelectionRange), Children: children})
				}
				return result
			}
			result.Symbols = convert(symbols)
			return nil
		})
		if err != nil {
			return DocumentSymbolResult{}, publicServiceError(err)
		}
		return result, lease.ctx.Err()
	}, nil
}

// WorkspaceSymbols searches the captured virtual project tree, including closed
// source files in configured projects. It never enumerates the host filesystem.
func (s *Session) WorkspaceSymbols(ctx context.Context, query string) (WorkspaceSymbolResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return WorkspaceSymbolResult{}, err
	}
	return s.prepareWorkspaceSymbols(q, query)(ctx)
}
func (s *Session) prepareWorkspaceSymbols(q *querySnapshot, query string) func(context.Context) (WorkspaceSymbolResult, error) {
	return func(ctx context.Context) (WorkspaceSymbolResult, error) {
		result := WorkspaceSymbolResult{Generation: q.snapshot.Generation, Symbols: []protocol.SymbolInformation{}}
		var anchors []string
		for name := range q.versions {
			if strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".tsx") || strings.HasSuffix(name, ".mts") || strings.HasSuffix(name, ".cts") {
				anchors = append(anchors, name)
			}
		}
		if len(anchors) == 0 {
			anchors = slices.Clone(q.snapshot.Project.EntryPoints)
		}
		if len(anchors) == 0 {
			for name := range q.snapshot.Project.Files {
				if strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".tsx") || strings.HasSuffix(name, ".mts") || strings.HasSuffix(name, ".cts") {
					anchors = append(anchors, name)
				}
			}
		}
		if len(anchors) == 0 {
			return result, ctx.Err()
		}
		slices.Sort(anchors)
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return WorkspaceSymbolResult{}, err
		}
		defer lease.release()
		ctx = lsproto.WithClientCapabilities(lease.ctx, &lsproto.ResolvedClientCapabilities{})
		symbols, err := lease.state.native.WorkspaceSymbols(ctx, anchors[0], query)
		if err != nil {
			return WorkspaceSymbolResult{}, publicServiceError(err)
		}
		for _, symbol := range symbols {
			result.Symbols = append(result.Symbols, protocol.SymbolInformation{Name: symbol.Name, Kind: protocol.SymbolKind(symbol.Kind), Location: protocol.Location{URI: sourceURI(symbol.Location.Uri.FileName(), q.snapshot.Environment, q.mappings), Range: publicRange(symbol.Location.Range)}})
		}
		return result, lease.ctx.Err()
	}
}
