package lsp

import (
	"context"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

// NavigationResult owns detached target data for one captured generation.
// A query with no matching targets returns an empty slice.
type NavigationResult struct {
	Generation uint64
	Version    *int32
	Targets    []protocol.LocationLink
}

// Locations returns the selection locations used by clients without link
// support. The returned slice does not share mutable data with Targets.
func (r NavigationResult) Locations() []protocol.Location {
	if r.Targets == nil {
		return nil
	}
	result := make([]protocol.Location, len(r.Targets))
	for i, target := range r.Targets {
		result[i] = protocol.Location{URI: target.TargetURI, Range: target.TargetSelectionRange}
	}
	return result
}

type navigationKind uint8

const (
	navigationDefinition navigationKind = iota
	navigationTypeDefinition
	navigationImplementation
	navigationSourceDefinition
)

// Definition finds declarations at an original-source UTF-16 position.
func (s *Session) Definition(ctx context.Context, name string, position protocol.Position) (NavigationResult, error) {
	return s.navigate(ctx, name, position, navigationDefinition)
}

// TypeDefinition finds declarations of the symbol's type.
func (s *Session) TypeDefinition(ctx context.Context, name string, position protocol.Position) (NavigationResult, error) {
	return s.navigate(ctx, name, position, navigationTypeDefinition)
}

// Implementation searches the active program, including referenced source redirects.
func (s *Session) Implementation(ctx context.Context, name string, position protocol.Position) (NavigationResult, error) {
	return s.navigate(ctx, name, position, navigationImplementation)
}

// SourceDefinition finds concrete source targets and excludes ambient/failed-alias fallbacks.
func (s *Session) SourceDefinition(ctx context.Context, name string, position protocol.Position) (NavigationResult, error) {
	return s.navigate(ctx, name, position, navigationSourceDefinition)
}
func (s *Session) navigate(ctx context.Context, name string, position protocol.Position, kind navigationKind) (NavigationResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return NavigationResult{}, err
	}
	work, err := s.prepareNavigation(q, name, position, kind)
	if err != nil {
		return NavigationResult{}, err
	}
	return work(ctx)
}

func (s *Session) prepareNavigation(q *querySnapshot, name string, position protocol.Position, kind navigationKind) (func(context.Context) (NavigationResult, error), error) {
	name, position, err := q.position(name, position)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (NavigationResult, error) {
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return NavigationResult{}, err
		}
		defer lease.release()
		caps := &lsproto.ResolvedClientCapabilities{}
		caps.TextDocument.Definition.LinkSupport = true
		caps.TextDocument.TypeDefinition.LinkSupport = true
		caps.TextDocument.Implementation.LinkSupport = true
		ctx = lsproto.WithClientCapabilities(lease.ctx, caps)
		result := NavigationResult{Generation: q.snapshot.Generation, Version: q.version(name), Targets: []protocol.LocationLink{}}
		run := func(ctx context.Context, service *ls.LanguageService, search ls.CrossProjectOrchestrator) error {
			var response lsproto.LocationOrLocationsOrDefinitionLinksOrNull
			var err error
			pos := lsproto.Position{Line: position.Line, Character: position.Character}
			switch kind {
			case navigationDefinition:
				response, err = service.ProvideDefinition(ctx, nativeURI(name), pos)
			case navigationTypeDefinition:
				response, err = service.ProvideTypeDefinition(ctx, nativeURI(name), pos)
			case navigationSourceDefinition:
				response, err = service.ProvideReferenceSourceDefinition(ctx, nativeURI(name), pos)
			case navigationImplementation:
				response, err = service.ProvideImplementations(ctx, &lsproto.ImplementationParams{TextDocument: lsproto.TextDocumentIdentifier{Uri: nativeURI(name)}, Position: pos}, search)
			}
			if err != nil {
				return err
			}
			if response.DefinitionLinks != nil {
				result.Targets = make([]protocol.LocationLink, 0, len(*response.DefinitionLinks))
				for _, link := range *response.DefinitionLinks {
					target := protocol.LocationLink{TargetURI: sourceURI(link.TargetUri.FileName(), q.snapshot.Environment, q.mappings), TargetRange: publicRange(link.TargetRange), TargetSelectionRange: publicRange(link.TargetSelectionRange)}
					if link.OriginSelectionRange != nil {
						origin := publicRange(*link.OriginSelectionRange)
						target.OriginSelectionRange = &origin
					}
					result.Targets = append(result.Targets, target)
				}
			} else {
				var locations []lsproto.Location
				if response.Locations != nil {
					locations = *response.Locations
					result.Targets = []protocol.LocationLink{}
				}
				if response.Location != nil {
					locations = []lsproto.Location{*response.Location}
				}
				for _, location := range locations {
					r := publicRange(location.Range)
					result.Targets = append(result.Targets, protocol.LocationLink{TargetURI: sourceURI(location.Uri.FileName(), q.snapshot.Environment, q.mappings), TargetRange: r, TargetSelectionRange: r})
				}
			}
			return nil
		}
		// The reference implementation request searches the active project's
		// program, including its source redirects, rather than every project
		// that happens to contain the declaration.
		err = lease.state.native.WithService(ctx, name, func(ctx context.Context, service *ls.LanguageService) error { return run(ctx, service, nil) })
		if err != nil {
			return NavigationResult{}, publicServiceError(err)
		}
		return result, lease.ctx.Err()
	}, nil
}
