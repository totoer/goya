package lsp

import (
	"context"
	"slices"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

// OrganizeImportsMode controls whether an import proposal may remove bindings.
type OrganizeImportsMode string

const (
	OrganizeImportsAll            OrganizeImportsMode = "All"
	OrganizeImportsSortAndCombine OrganizeImportsMode = "SortAndCombine"
	OrganizeImportsRemoveUnused   OrganizeImportsMode = "RemoveUnused"
)

type ImportEditsResult struct {
	Generation uint64
	Version    *int32
	Edits      *BoundEdits
}

// OrganizeImports proposes edits against the captured catalog. It never applies
// them. SortAndCombine preserves unused imports; All may remove them.
func (s *Session) OrganizeImports(ctx context.Context, name string, mode OrganizeImportsMode) (ImportEditsResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return ImportEditsResult{}, err
	}
	work, err := s.prepareImportEdits(q, name, mode, false)
	if err != nil {
		return ImportEditsResult{}, err
	}
	return work(ctx)
}
func (s *Session) SortImports(ctx context.Context, name string) (ImportEditsResult, error) {
	return s.OrganizeImports(ctx, name, OrganizeImportsSortAndCombine)
}
func (s *Session) RemoveUnusedImports(ctx context.Context, name string) (ImportEditsResult, error) {
	return s.OrganizeImports(ctx, name, OrganizeImportsRemoveUnused)
}

// AddMissingImports computes a current proposal even when no diagnostic event
// has been consumed. Existing documents and module declarations remain immutable.
func (s *Session) AddMissingImports(ctx context.Context, name string) (ImportEditsResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return ImportEditsResult{}, err
	}
	work, err := s.prepareImportEdits(q, name, "", true)
	if err != nil {
		return ImportEditsResult{}, err
	}
	return work(ctx)
}
func importModeKind(mode OrganizeImportsMode) (lsproto.CodeActionKind, error) {
	switch mode {
	case OrganizeImportsAll:
		return lsproto.CodeActionKindSourceOrganizeImports, nil
	case OrganizeImportsSortAndCombine:
		return lsproto.CodeActionKindSourceSortImports, nil
	case OrganizeImportsRemoveUnused:
		return lsproto.CodeActionKindSourceRemoveUnusedImports, nil
	}
	return "", &Error{Code: CodeInvalidOptions, Message: "invalid organize imports mode"}
}
func (q *querySnapshot) organizeImportEdits(ctx context.Context, service *ls.LanguageService, name string, mode OrganizeImportsMode) (*protocol.WorkspaceEdit, error) {
	kind, err := importModeKind(mode)
	if err != nil {
		return nil, err
	}
	program := service.GetProgram()
	file := program.GetSourceFile(name)
	if file == nil {
		return nil, &Error{Code: CodeInvalidOptions, Message: "document is not in the selected project"}
	}
	changes := service.OrganizeImports(ctx, file, program, kind)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(changes))
	for name := range changes {
		names = append(names, name)
	}
	slices.Sort(names)
	files := make([]ls.ReferenceFileCodeChanges, 0, len(names))
	for _, name := range names {
		text, ok := q.text(name)
		if !ok {
			return nil, ErrReadOnly
		}
		edits := make([]*lsproto.TextEdit, 0, len(changes[name]))
		for _, edit := range changes[name] {
			start, err := OffsetAt(text, protocol.Position(edit.Range.Start))
			if err != nil {
				return nil, err
			}
			end, err := OffsetAt(text, protocol.Position(edit.Range.End))
			if err != nil {
				return nil, err
			}
			if start > end {
				return nil, &Error{Code: CodeInvalidOptions, Message: "reversed organize imports edit"}
			}
			// The pinned change tracker omits replacements identical to their source.
			if text[start:end] != edit.NewText {
				edits = append(edits, edit)
			}
		}
		if len(edits) > 0 {
			files = append(files, ls.ReferenceFileCodeChanges{FileName: name, TextChanges: edits})
		}
	}

	edit, _, err := q.codeFixChanges(files)
	return edit, err
}
func (s *Session) prepareImportEdits(q *querySnapshot, name string, mode OrganizeImportsMode, missing bool) (func(context.Context) (ImportEditsResult, error), error) {
	name, _, err := q.position(name, protocol.Position{})
	if err != nil {
		return nil, err
	}
	if !q.editable(name) {
		return nil, ErrReadOnly
	}
	if !missing {
		if _, err := importModeKind(mode); err != nil {
			return nil, err
		}
	}
	return func(ctx context.Context) (ImportEditsResult, error) {
		result := ImportEditsResult{Generation: q.snapshot.Generation, Version: q.version(name)}
		ctx, err := s.withDocumentFormatting(ctx, q, name)
		if err != nil {
			return result, err
		}
		var diagnostics []protocol.Diagnostic
		if missing {
			work, err := s.prepareDiagnostics(q, name, nil)
			if err != nil {
				return result, err
			}
			report, err := work(ctx)
			if err != nil {
				return result, err
			}
			diagnostics = report.Items
		}
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return result, err
		}
		defer lease.release()
		var edit *protocol.WorkspaceEdit
		err = lease.state.native.WithService(ctx, name, func(ctx context.Context, service *ls.LanguageService) error {
			var err error
			if missing {
				edit, err = q.sourceCombinedEdits(ctx, service, name, diagnostics, []sourceFix{{codes: []int32{2552, 2304}, name: "import"}})
			} else {
				edit, err = q.organizeImportEdits(ctx, service, name, mode)
			}
			return err
		})
		if err != nil {
			return result, publicServiceError(err)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.checkLocked(ctx); err != nil {
			return result, err
		}
		if s.generation != q.snapshot.Generation {
			return result, ErrContentModified
		}
		if edit != nil {
			bound, err := s.bindEditsLocked(*edit)
			if err != nil {
				return result, err
			}
			result.Edits = &bound
		}
		return result, nil
	}, nil
}

func codeActionKindContains(parent, child protocol.CodeActionKind) bool {
	return parent == "" || parent == child || len(parent) < len(child) && string(child[:len(parent)+1]) == string(parent)+"."
}
func (q *querySnapshot) organizeImportActions(ctx context.Context, service *ls.LanguageService, name string, only *[]protocol.CodeActionKind) ([]codeActionTarget, error) {
	if only == nil {
		return nil, nil
	}
	result := []codeActionTarget{}
	for _, requested := range *only {
		for _, candidate := range []struct {
			kind  protocol.CodeActionKind
			title string
			mode  OrganizeImportsMode
		}{
			// TLS uses SortAndCombine for the Organize Imports code action as well.
			{protocol.CodeActionSourceOrganizeImportsTS, "Organize Imports", OrganizeImportsSortAndCombine},
			{protocol.CodeActionSourceSortImportsTS, "Sort Imports", OrganizeImportsSortAndCombine},
			{protocol.CodeActionSourceRemoveUnusedImportsTS, "Remove Unused Imports", OrganizeImportsRemoveUnused},
		} {
			if !codeActionKindContains(requested, candidate.kind) {
				continue
			}
			edit, err := q.organizeImportEdits(ctx, service, name, candidate.mode)
			if err != nil {
				return nil, err
			}
			if edit != nil {
				result = append(result, codeActionTarget{name: name, action: protocol.CodeAction{Kind: candidate.kind, Title: candidate.title, Edit: edit}})
			}
		}
	}
	return result, ctx.Err()
}
