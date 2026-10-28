package lsp

import (
	"context"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

func sourceKindIntersects(a, b protocol.CodeActionKind) bool {
	return a == b || strings.HasPrefix(string(a), string(b)+".") || strings.HasPrefix(string(b), string(a)+".") || a == "" || b == ""
}
func sourceActionKinds(only *[]protocol.CodeActionKind) []protocol.CodeActionKind {
	if only == nil || len(*only) == 0 {
		return nil
	}
	result := []protocol.CodeActionKind{}
	for _, kind := range []protocol.CodeActionKind{protocol.CodeActionSourceFixAllTS, protocol.CodeActionSourceRemoveUnusedTS, protocol.CodeActionSourceAddMissingImportsTS} {
		for _, requested := range *only {
			if sourceKindIntersects(protocol.CodeActionSource, requested) && sourceKindIntersects(kind, requested) {
				result = append(result, kind)
				break
			}
		}
	}
	return result
}

type sourceFix struct {
	codes []int32
	name  string
}

func sourceFixMatch(diagnostic protocol.Diagnostic, fix sourceFix) bool {
	if diagnostic.CodeString != nil {
		return false
	}
	for _, code := range fix.codes {
		if code == diagnostic.Code {
			return true
		}
	}
	return false
}
func sourceNativeDiagnostic(diagnostic protocol.Diagnostic) *lsproto.Diagnostic {
	code := diagnostic.Code
	return &lsproto.Diagnostic{Range: lsproto.Range{Start: lsproto.Position(diagnostic.Range.Start), End: lsproto.Position(diagnostic.Range.End)}, Code: &lsproto.IntegerOrString{Integer: &code}, Message: lsproto.StringOrMarkupContent{String: &diagnostic.Message}}
}
func (q *querySnapshot) sourceIndividualEdits(ctx context.Context, service *ls.LanguageService, name string, diagnostics []protocol.Diagnostic, fixes []sourceFix) (*protocol.WorkspaceEdit, error) {
	edit := &protocol.WorkspaceEdit{DocumentChanges: []protocol.DocumentChange{}}
	for _, diagnostic := range diagnostics {
		for _, candidate := range fixes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !sourceFixMatch(diagnostic, candidate) {
				continue
			}
			actions, err := service.ProvideReferenceCodeFixes(ctx, nativeURI(name), sourceNativeDiagnostic(diagnostic))
			if err != nil {
				return nil, err
			}
			matched := false
			for _, action := range actions {
				if action.FixName == candidate.name {
					changes, _, err := q.codeFixChanges(action.Changes)
					if err != nil {
						return nil, err
					}
					if changes != nil {
						edit.DocumentChanges = append(edit.DocumentChanges, changes.DocumentChanges...)
					}
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
	}
	return edit, ctx.Err()
}
func (q *querySnapshot) sourceCombinedEdits(ctx context.Context, service *ls.LanguageService, name string, diagnostics []protocol.Diagnostic, fixes []sourceFix) (*protocol.WorkspaceEdit, error) {
	for _, diagnostic := range diagnostics {
		for _, candidate := range fixes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !sourceFixMatch(diagnostic, candidate) {
				continue
			}
			actions, err := service.ProvideReferenceCodeFixes(ctx, nativeURI(name), sourceNativeDiagnostic(diagnostic))
			if err != nil {
				return nil, err
			}
			for _, action := range actions {
				if action.FixName == candidate.name {
					changes := action.Changes
					if action.FixID != "" {
						changes, err = service.ProvideReferenceCombinedCodeFix(ctx, nativeURI(name), action.FixID)
						if err != nil {
							return nil, err
						}
					}
					edit, _, err := q.codeFixChanges(changes)
					if err != nil {
						return nil, err
					}
					if edit == nil {
						edit = &protocol.WorkspaceEdit{DocumentChanges: []protocol.DocumentChange{}}
					}
					return edit, nil
				}
			}
		}
	}
	return nil, ctx.Err()
}
func (q *querySnapshot) sourceCodeActions(ctx context.Context, service *ls.LanguageService, name string, only *[]protocol.CodeActionKind, diagnostics []protocol.Diagnostic, ready bool) ([]codeActionTarget, error) {
	if !ready {
		return nil, nil
	}
	actions := []codeActionTarget{}
	for _, kind := range sourceActionKinds(only) {
		title := ""
		switch kind {
		case protocol.CodeActionSourceFixAllTS:
			title = "Fix all fixable JS/TS issues"
		case protocol.CodeActionSourceRemoveUnusedTS:
			title = "Remove all unused code"
		case protocol.CodeActionSourceAddMissingImportsTS:
			title = "Add all missing imports"
		}
		action := protocol.CodeAction{Title: title, Kind: kind}
		if len(diagnostics) > 0 {
			var err error
			switch kind {
			case protocol.CodeActionSourceFixAllTS:
				action.Edit, err = q.sourceIndividualEdits(ctx, service, name, diagnostics, []sourceFix{{codes: []int32{2420}, name: "fixClassIncorrectlyImplementsInterface"}, {codes: []int32{1308}, name: "fixAwaitInSyncFunction"}})
				if err == nil {
					var combined *protocol.WorkspaceEdit
					combined, err = q.sourceCombinedEdits(ctx, service, name, diagnostics, []sourceFix{{codes: []int32{7027}, name: "fixUnreachableCode"}})
					if combined != nil {
						action.Edit.DocumentChanges = append(action.Edit.DocumentChanges, combined.DocumentChanges...)
					}
				}
			case protocol.CodeActionSourceRemoveUnusedTS:
				action.Edit, err = q.sourceCombinedEdits(ctx, service, name, diagnostics, []sourceFix{{codes: []int32{6196, 6133}, name: "unusedIdentifier"}})
			case protocol.CodeActionSourceAddMissingImportsTS:
				action.Edit, err = q.sourceCombinedEdits(ctx, service, name, diagnostics, []sourceFix{{codes: []int32{2552, 2304}, name: "import"}})
			}
			if err != nil {
				return nil, err
			}
		}
		actions = append(actions, codeActionTarget{name: name, action: action})
	}
	return actions, ctx.Err()
}
