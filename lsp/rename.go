package lsp

import (
	"context"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

// RenameUnavailableError explains why a symbol cannot be renamed. A rename
// request itself returns no proposal for an ineligible symbol, as does TLS.
type RenameUnavailableError struct{ Reason string }

func (e *RenameUnavailableError) Error() string { return e.Reason }

type PrepareRenameResult struct {
	Generation uint64
	Version    *int32
	Range      *protocol.Range
}
type RenameResult struct {
	Generation uint64
	Version    *int32
	Edits      *BoundEdits
}

// PrepareRename validates the symbol and the frozen declaration boundary.
// It does not reserve a name or mutate the captured catalog.
func (s *Session) PrepareRename(ctx context.Context, name string, position protocol.Position) (PrepareRenameResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return PrepareRenameResult{}, err
	}
	work, err := s.prepareRename(q, name, position, "", true)
	if err != nil {
		return PrepareRenameResult{}, err
	}
	prepared, _, err := work(ctx)
	return prepared, err
}

// Rename proposes generation-bound edits. Like the reference server, it
// substitutes newName verbatim; the host can Check a copy before accepting it.
func (s *Session) Rename(ctx context.Context, name string, position protocol.Position, newName string) (RenameResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return RenameResult{}, err
	}
	work, err := s.prepareRename(q, name, position, newName, false)
	if err != nil {
		return RenameResult{}, err
	}
	_, result, err := work(ctx)
	return result, err
}

func (q *querySnapshot) editable(name string) bool {
	if strings.HasPrefix(name, libraryPrefix) {
		return false
	}
	_, frozen := q.snapshot.Environment.TypeDeclarations()[name]
	return !frozen
}

func (s *Session) prepareRename(q *querySnapshot, name string, position protocol.Position, newName string, prepare bool) (func(context.Context) (PrepareRenameResult, RenameResult, error), error) {
	name, position, err := q.position(name, position)
	if err != nil {
		return nil, err
	}
	if !q.editable(name) {
		return nil, ErrReadOnly
	}
	return func(ctx context.Context) (PrepareRenameResult, RenameResult, error) {
		if !prepare {
			var formatErr error
			ctx, formatErr = s.withDocumentFormatting(ctx, q, name)
			if formatErr != nil {
				return PrepareRenameResult{}, RenameResult{}, formatErr
			}
		}
		prepared := PrepareRenameResult{Generation: q.snapshot.Generation, Version: q.version(name)}
		result := RenameResult{Generation: q.snapshot.Generation, Version: q.version(name)}
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return prepared, result, err
		}
		defer lease.release()
		// Native module eligibility assumes a file-operation UI. TLS exposes the
		// same span-only rename independently of that capability; we return
		// only text changes and leave actual file operations to the host.
		capabilities := &lsproto.ResolvedClientCapabilities{}
		capabilities.Workspace.WorkspaceEdit.DocumentChanges = true
		capabilities.Workspace.WorkspaceEdit.ResourceOperations = []lsproto.ResourceOperationKind{lsproto.ResourceOperationKindRename}
		ctx = lsproto.WithClientCapabilities(lease.ctx, capabilities)
		var edit *protocol.WorkspaceEdit
		err = lease.state.native.WithCrossProjectService(ctx, name, func(ctx context.Context, service *ls.LanguageService, search ls.CrossProjectOrchestrator) error {
			uri := nativeURI(name)
			pos := lsproto.Position{Line: position.Line, Character: position.Character}
			info := service.GetRenameInfo(ctx, "", uri, pos)
			if !info.CanRename {
				if prepare {
					return &RenameUnavailableError{Reason: info.LocalizedErrorMessage}
				}
				return nil
			}
			if prepare {
				r := publicRange(info.TriggerSpan)
				prepared.Range = &r
			}
			requestedName := newName
			if prepare {
				requestedName = "__goya_rename_probe__"
			}
			response, err := service.ProvideRename(ctx, &lsproto.RenameParams{TextDocument: lsproto.TextDocumentIdentifier{Uri: uri}, Position: pos, NewName: requestedName}, search)
			if err != nil {
				return err
			}
			if response.WorkspaceEdit == nil || response.WorkspaceEdit.Changes == nil {
				return nil
			}
			edit = &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{}}
			for uri, edits := range *response.WorkspaceEdit.Changes {
				if len(edits) == 0 {
					continue
				}
				if !q.editable(uri.FileName()) {
					return ErrReadOnly
				}
				target := sourceURI(uri.FileName(), q.snapshot.Environment, q.mappings)
				for _, e := range edits {
					edit.Changes[target] = append(edit.Changes[target], protocol.TextEdit{Range: publicRange(e.Range), NewText: e.NewText})
				}
			}
			return nil
		})
		if err != nil {
			return prepared, result, publicServiceError(err)
		}
		if err := lease.ctx.Err(); err != nil {
			return prepared, result, err
		}
		if !prepare && edit != nil && len(edit.Changes) > 0 {
			s.mu.Lock()
			defer s.mu.Unlock()
			if err := s.checkLocked(ctx); err != nil {
				return prepared, result, err
			}
			if s.generation != q.snapshot.Generation {
				return prepared, result, ErrContentModified
			}
			bound, err := s.bindEditsLocked(*edit)
			if err != nil {
				return prepared, result, err
			}
			result.Edits = &bound
		}
		return prepared, result, nil
	}, nil
}
