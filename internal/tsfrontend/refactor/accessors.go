package refactor

import (
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/lsp/protocol"
)

const accessorName = "Generate 'get' and 'set' accessors"
const accessorKind = "refactor.rewrite.property.generateAccessors"

var accessorProvider = provider{name: accessorName, kinds: []string{accessorKind}, available: availableAccessors, edits: editAccessors}

func accessorInfo(r *request, considerEmpty bool) (*ast.Node, string) {
	token := astnav.GetTokenAtPosition(r.file, r.span.Pos())
	var declaration *ast.Node
	if token.Parent != nil {
		declaration = ancestor(token.Parent, func(node *ast.Node) bool {
			return ast.IsPropertyDeclaration(node) || ast.IsPropertyAssignment(node) || node.Parent != nil && ast.IsParameterPropertyDeclaration(node, node.Parent)
		})
	}
	if declaration == nil || declaration.Name() == nil || !(overlapsSpan(r, declaration.Name()) || r.span.Pos() == r.span.End() && considerEmpty) {
		return nil, diagnostics.Could_not_find_property_for_which_to_generate_accessor.Localize(locale.FromContext(r.ctx))
	}
	name := declaration.Name()
	if !ast.IsIdentifier(name) && !ast.IsStringLiteral(name) {
		return nil, diagnostics.Name_is_not_valid.Localize(locale.FromContext(r.ctx))
	}
	meaning := ast.ModifierFlagsAccessibilityModifier | ast.ModifierFlagsStatic | ast.ModifierFlagsReadonly
	if (declaration.ModifierFlags()&ast.ModifierFlagsModifier)|meaning != meaning {
		return nil, diagnostics.Can_only_convert_property_with_modifier.Localize(locale.FromContext(r.ctx))
	}
	return declaration, ""
}
func availableAccessors(r *request) []protocol.ApplicableRefactorInfo {
	if r.span.End() == 0 {
		return nil
	}
	declaration, reason := accessorInfo(r, r.options.Invoked)
	if declaration == nil && !r.preferences.ProvideRefactorNotApplicableReason.IsTrue() {
		return nil
	}
	description := diagnostics.Generate_get_and_set_accessors.Localize(locale.FromContext(r.ctx))
	return []protocol.ApplicableRefactorInfo{{Name: accessorName, Description: description, Actions: []protocol.RefactorActionInfo{{Name: accessorName, Description: description, Kind: accessorKind, NotApplicableReason: reason}}}}
}
func editAccessors(r *request, _ string, _ Arguments) (*EditInfo, error) {
	if r.span.End() == 0 {
		return nil, nil
	}
	declaration, reason := accessorInfo(r, true)
	if declaration == nil {
		return nil, &change.InvalidEdits{Reason: "accessor generation is not applicable: " + reason}
	}
	files, err := r.service.GetReferenceAccessorChanges(r.ctx, r.types(), r.file, r.span.Pos(), r.span.End())
	if err != nil || len(files) == 0 {
		return nil, err
	}
	result := &EditInfo{Changes: files}
	name := declaration.Name().Text()
	if strings.HasPrefix(name, "_") {
		name = uniqueName(strings.TrimPrefix(name, "_"), r.file)
	} else {
		name = uniqueName("_"+name, r.file)
	}
	if err := setRenameLocation(r, result, r.file.FileName(), name, ast.IsParameterDeclaration(declaration)); err != nil {
		return nil, err
	}
	if !ast.IsIdentifier(declaration.Name()) {
		*result.RenamePosition--
	}
	return result, r.ctx.Err()
}
