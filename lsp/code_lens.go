package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

type CodeLensResult struct {
	Generation uint64
	Version    *int32
	URI        protocol.DocumentURI
	Lenses     []protocol.CodeLens
}

// NavigationContinuation lets applications consume resolved lens targets
// directly, without executing an editor command or invoking a client hook.
type NavigationContinuation struct {
	URI       protocol.DocumentURI
	Position  protocol.Position
	Locations []protocol.Location
}
type CodeLensResolveResult struct {
	Generation uint64
	Version    *int32
	Lens       protocol.CodeLens
	Navigation *NavigationContinuation
}
type codeLensTarget struct {
	name string
	span protocol.Range
	kind protocol.CodeLensKind
}

func codeLensEnabled(value *bool) bool { return value != nil && *value }

type codeLensMetadata struct{ targets []codeLensTarget }

func (s *Session) CodeLenses(ctx context.Context, name string) (CodeLensResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return CodeLensResult{}, err
	}
	work, err := s.prepareCodeLenses(q, name)
	if err != nil {
		return CodeLensResult{}, err
	}
	return work(ctx)
}
func (s *Session) prepareCodeLenses(q *querySnapshot, name string) (func(context.Context) (CodeLensResult, error), error) {
	name, _, err := q.position(name, protocol.Position{})
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (CodeLensResult, error) {
		s.mu.RLock()
		checkErr := s.checkLocked(ctx)
		s.mu.RUnlock()
		if checkErr != nil {
			return CodeLensResult{}, checkErr
		}
		result := CodeLensResult{Generation: q.snapshot.Generation, Version: q.version(name), URI: sourceURI(name, q.snapshot.Environment, q.mappings), Lenses: []protocol.CodeLens{}}
		references := q.snapshot.EditorOptions.ReferencesCodeLens != nil && codeLensEnabled(q.snapshot.EditorOptions.ReferencesCodeLens.Enabled)
		implementations := q.snapshot.EditorOptions.ImplementationsCodeLens != nil && codeLensEnabled(q.snapshot.EditorOptions.ImplementationsCodeLens.Enabled)
		if !references && !implementations {
			return result, ctx.Err()
		}
		showAll := q.snapshot.EditorOptions.ReferencesCodeLens != nil && codeLensEnabled(q.snapshot.EditorOptions.ReferencesCodeLens.ShowOnAllFunctions)
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return CodeLensResult{}, err
		}
		defer lease.release()
		ctx = lsproto.WithClientCapabilities(lease.ctx, &lsproto.ResolvedClientCapabilities{})
		var candidates []ls.ReferenceCodeLensTarget
		err = lease.state.native.WithService(ctx, name, func(ctx context.Context, service *ls.LanguageService) error {
			var err error
			candidates, err = service.ProvideReferenceCodeLensTargets(ctx, nativeURI(name), showAll)
			return err
		})
		if err != nil {
			return CodeLensResult{}, publicServiceError(err)
		}
		targets := []codeLensTarget{}
		// TLS enumerates all implementation lenses before all reference lenses.
		for _, kind := range []protocol.CodeLensKind{protocol.ImplementationsCodeLens, protocol.ReferencesCodeLens} {
			for _, candidate := range candidates {
				if kind == protocol.ImplementationsCodeLens && (!implementations || !candidate.Implementations) || kind == protocol.ReferencesCodeLens && (!references || !candidate.References) {
					continue
				}
				span := publicRange(candidate.Range)
				result.Lenses = append(result.Lenses, protocol.CodeLens{Range: span, Data: &protocol.CodeLensData{URI: result.URI, Type: kind, Index: len(targets)}})
				targets = append(targets, codeLensTarget{name: name, span: span, kind: kind})
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.checkLocked(ctx); err != nil {
			return CodeLensResult{}, err
		}
		if s.generation != q.snapshot.Generation {
			return CodeLensResult{}, ErrContentModified
		}
		if len(targets) > 0 {
			size := 64 + len(targets)*(len(name)+64)
			handle := s.retainLocked(codeLensMetadata{targets: targets}, size)
			for i := range result.Lenses {
				result.Lenses[i].Data.Handle = string(handle)
			}
		}
		return result, nil
	}, nil
}
func (s *Session) codeLensQuery(ctx context.Context, item protocol.CodeLens) (*querySnapshot, codeLensTarget, protocol.CodeLensData, error) {
	if item.Data == nil {
		return nil, codeLensTarget{}, protocol.CodeLensData{}, &Error{Code: CodeInvalidHandle, Message: "code lens lacks an issued handle"}
	}
	data := *item.Data
	s.mu.Lock()
	if err := s.checkLocked(ctx); err != nil {
		s.mu.Unlock()
		return nil, codeLensTarget{}, data, err
	}
	value, err := s.resolveLocked(ResultHandle(data.Handle))
	if err != nil {
		s.mu.Unlock()
		return nil, codeLensTarget{}, data, err
	}
	metadata, ok := value.(codeLensMetadata)
	if !ok || data.Index < 0 || data.Index >= len(metadata.targets) {
		s.mu.Unlock()
		return nil, codeLensTarget{}, data, &Error{Code: CodeInvalidHandle, Message: "invalid code lens handle or index"}
	}
	target := metadata.targets[data.Index]
	s.mu.Unlock()
	q, err := s.captureQuery(ctx)
	if err != nil {
		return nil, codeLensTarget{}, data, err
	}
	s.mu.Lock()
	_, err = s.resolveLocked(ResultHandle(data.Handle))
	s.mu.Unlock()
	if err != nil {
		return nil, codeLensTarget{}, data, err
	}
	return q, target, data, nil
}
func (s *Session) ResolveCodeLens(ctx context.Context, item protocol.CodeLens) (CodeLensResolveResult, error) {
	work, err := s.prepareResolveCodeLens(ctx, item)
	if err != nil {
		return CodeLensResolveResult{}, err
	}
	return work(ctx)
}
func (s *Session) prepareResolveCodeLens(ctx context.Context, item protocol.CodeLens) (func(context.Context) (CodeLensResolveResult, error), error) {
	q, target, data, err := s.codeLensQuery(ctx, item)
	if err != nil {
		return nil, err
	}
	uri := sourceURI(target.name, q.snapshot.Environment, q.mappings)
	data.URI = uri
	data.Type = target.kind
	return func(ctx context.Context) (CodeLensResolveResult, error) {
		ownedData := data
		result := CodeLensResolveResult{Generation: q.snapshot.Generation, Version: q.version(target.name), Lens: protocol.CodeLens{Range: target.span, Data: &ownedData}}
		locations := []protocol.Location{}
		if target.kind == protocol.ReferencesCodeLens {
			work, err := s.prepareReferences(q, target.name, target.span.Start, &ReferenceOptions{IncludeDeclaration: false})
			if err != nil {
				return CodeLensResolveResult{}, err
			}
			refs, err := work(ctx)
			if err != nil {
				return CodeLensResolveResult{}, err
			}
			locations = refs.Locations
		} else {
			work, err := s.prepareNavigation(q, target.name, target.span.Start, navigationImplementation)
			if err != nil {
				return CodeLensResolveResult{}, err
			}
			impl, err := work(ctx)
			if err != nil {
				return CodeLensResolveResult{}, err
			}
			for _, location := range impl.Locations() {
				if location.URI == uri && location.Range.Start == target.span.Start {
					continue
				}
				if location.Range.End.Line != location.Range.Start.Line {
					location.Range.End = protocol.Position{Line: location.Range.Start.Line + 1}
				}
				locations = append(locations, location)
			}
		}
		s.mu.Lock()
		if err := s.checkLocked(ctx); err != nil {
			s.mu.Unlock()
			return CodeLensResolveResult{}, err
		}
		_, err = s.resolveLocked(ResultHandle(data.Handle))
		s.mu.Unlock()
		if err != nil {
			return CodeLensResolveResult{}, err
		}
		noun := "reference"
		if target.kind == protocol.ImplementationsCodeLens {
			noun = "implementation"
		}
		if len(locations) != 1 {
			noun += "s"
		}
		command := &protocol.Command{Title: fmt.Sprintf("%d %s", len(locations), noun)}
		if len(locations) > 0 {
			command.Command = "editor.action.showReferences"
		}
		for _, arg := range []any{uri, target.span.Start, locations} {
			raw, err := json.Marshal(arg)
			if err != nil {
				return CodeLensResolveResult{}, err
			}
			command.Arguments = append(command.Arguments, raw)
		}
		result.Lens.Command = command
		// Command arguments are serialized independently and cannot alias this slice.
		result.Navigation = &NavigationContinuation{URI: uri, Position: target.span.Start, Locations: slices.Clone(locations)}
		return result, nil
	}, nil
}
