package refactor

import (
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"github.com/totoer/goya/lsp/protocol"
)

const convertExportName = "Convert export"
const defaultToNamedName = "Convert default export to named export"
const namedToDefaultName = "Convert named export to default export"

var convertExportProvider = provider{name: convertExportName, kinds: []string{"refactor.rewrite.export.named", "refactor.rewrite.export.default"}, available: availableConvertExport, edits: editConvertExport}

type exportInfo struct {
	node, name *ast.Node
	module     *ast.Symbol
	wasDefault bool
}

func parentInSpan(r *request, node *ast.Node) *ast.Node {
	for node != nil && node.Parent != nil {
		parent := node.Parent
		if ast.IsSourceFile(parent) || !(r.span.Pos() <= tokenStart(r, parent) && tokenStart(r, parent) <= r.span.End() && parent.End() <= r.span.End()) {
			return node
		}
		node = parent
	}
	return nil
}
func convertExportInfo(r *request, partial bool) (*exportInfo, string) {
	token := astnav.GetTokenAtPosition(r.file, r.span.Pos())
	node := parentInSpan(r, token)
	if token.Parent != nil && token.Parent.ModifierFlags()&ast.ModifierFlagsExport != 0 && partial {
		node = token.Parent
	}
	if node == nil || node.Parent == nil || !(ast.IsSourceFile(node.Parent) || ast.IsModuleBlock(node.Parent) && node.Parent.Parent != nil && ast.IsAmbientModule(node.Parent.Parent)) {
		return nil, diagnostics.Could_not_find_export_statement.Localize(locale.FromContext(r.ctx))
	}
	var module *ast.Symbol
	if ast.IsSourceFile(node.Parent) {
		module = node.Parent.Symbol()
	} else {
		module = node.Parent.Parent.Symbol()
		if module != nil && module.ValueDeclaration != nil && ast.IsExternalModuleAugmentation(module.ValueDeclaration) {
			module = r.types().GetMergedSymbol(module)
		}
	}
	flags := node.ModifierFlags()
	if flags == 0 && ast.IsExportAssignment(node) && !node.AsExportAssignment().IsExportEquals {
		flags = ast.ModifierFlagsExport | ast.ModifierFlagsDefault
	}
	wasDefault := flags&ast.ModifierFlagsDefault != 0
	if flags&ast.ModifierFlagsExport == 0 || !wasDefault && module != nil && module.Exports["default"] != nil {
		return nil, diagnostics.This_file_already_has_a_default_export.Localize(locale.FromContext(r.ctx))
	}
	var name *ast.Node
	switch node.Kind {
	case ast.KindFunctionDeclaration, ast.KindClassDeclaration, ast.KindInterfaceDeclaration, ast.KindEnumDeclaration, ast.KindTypeAliasDeclaration, ast.KindModuleDeclaration:
		name = node.Name()
		if name == nil {
			return nil, ""
		}
	case ast.KindVariableStatement:
		list := node.AsVariableStatement().DeclarationList
		declarations := list.AsVariableDeclarationList().Declarations.Nodes
		if list.Flags&ast.NodeFlagsConst == 0 || len(declarations) != 1 || declarations[0].Initializer() == nil {
			return nil, ""
		}
		name = declarations[0].Name()
	case ast.KindExportAssignment:
		if node.AsExportAssignment().IsExportEquals {
			return nil, ""
		}
		name = node.Expression()
	default:
		return nil, ""
	}
	if !ast.IsIdentifier(name) || r.types().GetSymbolAtLocation(name) == nil {
		return nil, diagnostics.Can_only_convert_named_export.Localize(locale.FromContext(r.ctx))
	}
	return &exportInfo{node: node, name: name, module: module, wasDefault: wasDefault}, ""
}
func exportAction(r *request, named bool) protocol.RefactorActionInfo {
	if named {
		return protocol.RefactorActionInfo{Name: defaultToNamedName, Description: diagnostics.Convert_default_export_to_named_export.Localize(locale.FromContext(r.ctx)), Kind: "refactor.rewrite.export.named"}
	}
	return protocol.RefactorActionInfo{Name: namedToDefaultName, Description: diagnostics.Convert_named_export_to_default_export.Localize(locale.FromContext(r.ctx)), Kind: "refactor.rewrite.export.default"}
}
func availableConvertExport(r *request) []protocol.ApplicableRefactorInfo {
	info, reason := convertExportInfo(r, r.options.Invoked)
	if info == nil && (reason == "" || !r.preferences.ProvideRefactorNotApplicableReason.IsTrue()) {
		return nil
	}
	group := protocol.ApplicableRefactorInfo{Name: convertExportName}
	if info != nil {
		action := exportAction(r, info.wasDefault)
		group.Description = action.Description
		group.Actions = []protocol.RefactorActionInfo{action}
	} else {
		group.Description = diagnostics.Convert_default_export_to_named_export.Localize(locale.FromContext(r.ctx))
		for _, named := range []bool{true, false} {
			action := exportAction(r, named)
			action.NotApplicableReason = reason
			group.Actions = append(group.Actions, action)
		}
	}
	return []protocol.ApplicableRefactorInfo{group}
}
func findModifier(node *ast.Node, kind ast.Kind) *ast.Node {
	if node.Modifiers() != nil {
		for _, m := range node.Modifiers().Nodes {
			if m.Kind == kind {
				return m
			}
		}
	}
	return nil
}
func deleteExportModifier(t *change.Tracker, file *ast.SourceFile, node *ast.Node) {
	if node == nil {
		panic(&change.InvalidEdits{Reason: "export refactor has no modifier"})
	}
	start := scanner.GetTokenPosOfNode(node, file, false)
	end := scanner.SkipTriviaEx(file.Text(), node.End(), &scanner.SkipTriviaOptions{StopAfterLineBreak: true})
	t.DeleteRange(file, core.NewTextRange(start, end))
}
func importSpecifier(t *change.Tracker, property, name string) *ast.Node {
	var original *ast.Node
	if property != name {
		original = t.NewIdentifier(property)
	}
	return t.NewImportSpecifier(false, original, t.NewIdentifier(name))
}
func exportSpecifier(t *change.Tracker, property, name string) *ast.Node {
	var original *ast.Node
	if property != name {
		original = t.NewIdentifier(property)
	}
	return t.NewExportSpecifier(false, original, t.NewIdentifier(name))
}
func changeExport(r *request, t *change.Tracker, info *exportInfo) {
	if info.wasDefault {
		if ast.IsExportAssignment(info.node) && !info.node.AsExportAssignment().IsExportEquals {
			spec := exportSpecifier(t, info.name.Text(), info.name.Text())
			t.ReplaceNode(r.file, info.node, t.NewExportDeclaration(nil, false, t.NewNamedExports(t.NewNodeList([]*ast.Node{spec})), nil, nil), nil)
		} else {
			modifier := findModifier(info.node, ast.KindDefaultKeyword)
			if modifier == nil {
				panic(&change.InvalidEdits{Reason: "default export has no default modifier"})
			}
			t.ReplaceRangeWithText(r.file, t.GetAdjustedRange(r.file, modifier, modifier, change.LeadingTriviaOptionIncludeAll, change.TrailingTriviaOptionNone), "")
		}
		return
	}
	export := findModifier(info.node, ast.KindExportKeyword)
	if export == nil {
		panic(&change.InvalidEdits{Reason: "export refactor has no export keyword"})
	}
	switch info.node.Kind {
	case ast.KindFunctionDeclaration, ast.KindClassDeclaration, ast.KindInterfaceDeclaration:
		t.InsertNodeAfter(r.file, export, t.NewToken(ast.KindDefaultKeyword))
		return
	case ast.KindVariableStatement:
		declaration := info.node.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes[0]
		symbol := r.types().GetSymbolAtLocation(info.name)
		if !r.types().IsSymbolReferencedInFile(r.file, info.name.AsIdentifier(), symbol) && declaration.Type() == nil {
			t.ReplaceNode(r.file, info.node, t.NewExportAssignment(nil, false, nil, declaration.Initializer()), nil)
			return
		}
	}
	deleteExportModifier(t, r.file, export)
	t.InsertNodeAfter(r.file, info.node, t.NewExportAssignment(nil, false, nil, t.NewIdentifier(info.name.Text())))
}
func changeDefaultToNamedImport(r *request, t *change.Tracker, file *ast.SourceFile, ref *ast.Node, exportName string) error {
	parent := ref.Parent
	switch parent.Kind {
	case ast.KindPropertyAccessExpression:
		t.ReplaceNode(file, ref, t.NewIdentifier(exportName), nil)
	case ast.KindImportSpecifier, ast.KindExportSpecifier:
		t.ReplaceNode(file, parent, importSpecifier(t, exportName, parent.Name().Text()), nil)
	case ast.KindImportClause:
		clause := parent.AsImportClause()
		spec := importSpecifier(t, exportName, ref.Text())
		bindings := clause.NamedBindings
		if bindings == nil {
			t.ReplaceNode(file, ref, t.NewNamedImports(t.NewNodeList([]*ast.Node{spec})), nil)
		} else if ast.IsNamespaceImport(bindings) {
			t.DeleteRange(file, core.NewTextRange(scanner.GetTokenPosOfNode(ref, file, false), scanner.GetTokenPosOfNode(bindings, file, false)))
			module := clause.Parent.ModuleSpecifier()
			flags := ast.TokenFlagsNone
			if ast.IsStringLiteral(module) && len(file.Text()) > scanner.GetTokenPosOfNode(module, file, false) && file.Text()[scanner.GetTokenPosOfNode(module, file, false)] == '\'' {
				flags = ast.TokenFlagsSingleQuote
			}
			source := t.NewStringLiteral(module.Text(), flags)
			imported := t.NewImportDeclaration(nil, t.NewImportClause(ast.KindUnknown, nil, t.NewNamedImports(t.NewNodeList([]*ast.Node{spec}))), source, nil)
			t.InsertNodeAfter(file, clause.Parent, imported)
		} else {
			t.Delete(file, ref)
			elements := bindings.AsNamedImports().Elements
			t.InsertNodeAt(file, core.TextPos(elements.End()), spec, change.NodeOptions{Prefix: ", "})
		}
	case ast.KindImportType:
		node := parent.AsImportTypeNode()
		t.ReplaceNode(file, parent, t.NewImportTypeNode(node.IsTypeOf, node.Argument, node.Attributes, t.NewIdentifier(exportName), node.TypeArguments), nil)
	default:
		return &change.InvalidEdits{Reason: "unexpected default export reference"}
	}
	return nil
}
func changeNamedToDefaultImport(t *change.Tracker, file *ast.SourceFile, ref *ast.Node) error {
	parent := ref.Parent
	switch parent.Kind {
	case ast.KindPropertyAccessExpression:
		t.ReplaceNode(file, ref, t.NewIdentifier("default"), nil)
	case ast.KindImportSpecifier:
		name := t.NewIdentifier(parent.Name().Text())
		list := parent.Parent
		if len(list.AsNamedImports().Elements.Nodes) == 1 {
			t.ReplaceNode(file, list, name, nil)
		} else {
			t.Delete(file, parent)
			t.InsertNodeBefore(file, list, name, false, change.LeadingTriviaOptionNone)
		}
	case ast.KindExportSpecifier:
		t.ReplaceNode(file, parent, exportSpecifier(t, "default", parent.Name().Text()), nil)
	default:
		return &change.InvalidEdits{Reason: "unexpected named export reference"}
	}
	return nil
}
func editConvertExport(r *request, action string, _ Arguments) (*EditInfo, error) {
	if action != defaultToNamedName && action != namedToDefaultName {
		return nil, &change.InvalidEdits{Reason: "invalid export-conversion action"}
	}
	info, reason := convertExportInfo(r, true)
	if info == nil {
		return nil, &change.InvalidEdits{Reason: "export conversion is not applicable: " + reason}
	}
	t := r.tracker()
	changeExport(r, t, info)
	refs, err := r.service.GetReferenceExportReferences(r.ctx, r.types(), r.types().GetSymbolAtLocation(info.name), info.module, info.name.Text(), info.wasDefault)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if ref == info.name {
			continue
		}
		file := ast.GetSourceFileOfNode(ref)
		if info.wasDefault {
			err = changeDefaultToNamedImport(r, t, file, ref, info.name.Text())
		} else {
			err = changeNamedToDefaultImport(t, file, ref)
		}
		if err != nil {
			return nil, err
		}
	}
	return finish(t), r.ctx.Err()
}
