package refactor

import (
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsutil"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"github.com/totoer/goya/internal/tsfrontend/upstream/stringutil"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
	"github.com/totoer/goya/lsp/protocol"
)

const convertImportName = "Convert import"
const namespaceToNamedName = "Convert namespace import to named imports"
const namedToNamespaceName = "Convert named imports to namespace import"
const namedToDefaultImportName = "Convert named imports to default import"
const importNamed = 0
const importDefault = 1
const importNamespace = 2

var convertImportProvider = provider{name: convertImportName, kinds: []string{"refactor.rewrite.import.named", "refactor.rewrite.import.default", "refactor.rewrite.import.namespace"}, available: availableConvertImport, edits: editConvertImport}

type importInfo struct {
	node *ast.Node
	kind int
}

func importAction(r *request, kind int) protocol.RefactorActionInfo {
	switch kind {
	case importNamed:
		return protocol.RefactorActionInfo{Name: namespaceToNamedName, Description: diagnostics.Convert_namespace_import_to_named_imports.Localize(locale.FromContext(r.ctx)), Kind: "refactor.rewrite.import.named"}
	case importDefault:
		return protocol.RefactorActionInfo{Name: namedToDefaultImportName, Description: diagnostics.Convert_named_imports_to_default_import.Localize(locale.FromContext(r.ctx)), Kind: "refactor.rewrite.import.default"}
	default:
		return protocol.RefactorActionInfo{Name: namedToNamespaceName, Description: diagnostics.Convert_named_imports_to_namespace_import.Localize(locale.FromContext(r.ctx)), Kind: "refactor.rewrite.import.namespace"}
	}
}
func allowSyntheticDefaults(options *core.CompilerOptions) bool {
	if options.AllowSyntheticDefaultImports != core.TSUnknown {
		return options.AllowSyntheticDefaultImports.IsTrue()
	}
	module := options.Module
	interop := options.ESModuleInterop.IsTrue()
	if options.ESModuleInterop == core.TSUnknown {
		interop = module == core.ModuleKindNode16 || module == core.ModuleKindNode18 || module == core.ModuleKindNode20 || module == core.ModuleKindNodeNext || module == core.ModuleKindPreserve
	}
	resolution := options.ModuleResolution
	if resolution == core.ModuleResolutionKindUnknown {
		switch module {
		case core.ModuleKindNode16, core.ModuleKindNode18, core.ModuleKindNode20:
			resolution = core.ModuleResolutionKindNode16
		case core.ModuleKindNodeNext:
			resolution = core.ModuleResolutionKindNodeNext
		case core.ModuleKindPreserve:
			resolution = core.ModuleResolutionKindBundler
		case core.ModuleKindCommonJS:
			resolution = core.ModuleResolutionKindNode10
		default:
			resolution = core.ModuleResolutionKindClassic
		}
	}
	return interop || module == core.ModuleKindSystem || resolution == core.ModuleResolutionKindBundler
}
func importModuleSpecifier(node *ast.Node) *ast.Node {
	if node.Kind == ast.KindJSDocImportTag {
		return node.AsJSDocImportTag().ModuleSpecifier
	}
	return node.ModuleSpecifier()
}
func importClause(node *ast.Node) *ast.Node {
	if node.Kind == ast.KindJSDocImportTag {
		return node.AsJSDocImportTag().ImportClause
	}
	return node.ImportClause()
}
func importConversionInfo(r *request, partial bool) (*importInfo, string) {
	token := astnav.GetTokenAtPosition(r.file, r.span.Pos())
	node := parentInSpan(r, token)
	if partial {
		node = ancestor(token, func(n *ast.Node) bool { return ast.IsImportDeclaration(n) || n.Kind == ast.KindJSDocImportTag })
	}
	if node == nil || !ast.IsImportDeclaration(node) && node.Kind != ast.KindJSDocImportTag {
		return nil, "Selection is not an import declaration."
	}
	if next := astnav.FindNextToken(node, node.Parent, r.file); next != nil && r.span.End() > tokenStart(r, next) {
		return nil, ""
	}
	clause := importClause(node)
	if clause == nil {
		return nil, diagnostics.Could_not_find_import_clause.Localize(locale.FromContext(r.ctx))
	}
	bindings := clause.AsImportClause().NamedBindings
	if bindings == nil {
		return nil, diagnostics.Could_not_find_namespace_import_or_named_imports.Localize(locale.FromContext(r.ctx))
	}
	if ast.IsNamespaceImport(bindings) {
		return &importInfo{node: bindings, kind: importNamed}, ""
	}
	kind := importNamespace
	if allowSyntheticDefaults(r.program.Options()) {
		external := r.types().ResolveExternalModuleName(importModuleSpecifier(node))
		if external != nil && r.types().ResolveExternalModuleSymbol(external) != external {
			kind = importDefault
		}
	}
	return &importInfo{node: bindings, kind: kind}, ""
}
func availableConvertImport(r *request) []protocol.ApplicableRefactorInfo {
	info, reason := importConversionInfo(r, r.options.Invoked)
	if info != nil {
		action := importAction(r, info.kind)
		return []protocol.ApplicableRefactorInfo{{Name: convertImportName, Description: action.Description, Actions: []protocol.RefactorActionInfo{action}}}
	}
	if reason == "" || !r.preferences.ProvideRefactorNotApplicableReason.IsTrue() {
		return nil
	}
	result := []protocol.ApplicableRefactorInfo{}
	for _, kind := range []int{importNamed, importDefault, importNamespace} {
		a := importAction(r, kind)
		a.NotApplicableReason = reason
		result = append(result, protocol.ApplicableRefactorInfo{Name: convertImportName, Description: a.Description, Actions: []protocol.RefactorActionInfo{a}})
	}
	return result
}
func symbolReferences(r *request, name *ast.Node) []*ast.Node {
	symbol := r.types().GetSymbolAtLocation(name)
	if symbol == nil {
		return nil
	}
	refs := r.types().GetReferencesToSymbolInFile(r.file, symbol)
	result := []*ast.Node{}
	for _, ref := range refs {
		if ref != name {
			result = append(result, ref)
		}
	}
	return result
}
func newImportClause(t *change.Tracker, name *ast.Node, elements []*ast.Node) *ast.Node {
	var bindings *ast.Node
	if len(elements) > 0 {
		bindings = t.NewNamedImports(t.NewNodeList(elements))
	}
	return t.NewImportClause(ast.KindUnknown, name, bindings)
}
func newImport(t *change.Tracker, original, name *ast.Node, elements []*ast.Node) *ast.Node {
	return t.NewImportDeclaration(nil, newImportClause(t, name, elements), importModuleSpecifier(original), nil)
}
func propertyOrQualified(node *ast.Node) bool {
	return ast.IsPropertyAccessExpression(node) || ast.IsQualifiedName(node)
}
func leftPropertyOrQualified(node *ast.Node) *ast.Node {
	if ast.IsQualifiedName(node) {
		return node.AsQualifiedName().Left
	}
	return node.Expression()
}
func rightPropertyOrQualified(node *ast.Node) *ast.Node {
	if ast.IsQualifiedName(node) {
		return node.AsQualifiedName().Right
	}
	return node.Name()
}
func namespaceToNamed(r *request, t *change.Tracker, node *ast.Node) error {
	namespaceUsed := false
	replace := []*ast.Node{}
	conflicts := map[string]bool{}
	for _, ref := range symbolReferences(r, node.Name()) {
		parent := ref.Parent
		if !propertyOrQualified(parent) {
			namespaceUsed = true
			continue
		}
		name := rightPropertyOrQualified(parent).Text()
		if r.types().ResolveName(name, ref, ast.SymbolFlagsAll, true) != nil {
			conflicts[name] = true
		}
		if leftPropertyOrQualified(parent) != ref {
			return &change.InvalidEdits{Reason: "namespace reference is not the left side of an access"}
		}
		replace = append(replace, parent)
	}
	names := map[string]string{}
	order := []string{}
	for _, access := range replace {
		exported := rightPropertyOrQualified(access).Text()
		local, ok := names[exported]
		if !ok {
			local = exported
			if conflicts[exported] {
				local = uniqueName(exported, r.file)
			}
			names[exported] = local
			order = append(order, exported)
		}
		t.ReplaceNode(r.file, access, t.NewIdentifier(local), nil)
	}
	specifiers := []*ast.Node{}
	for _, exported := range order {
		specifiers = append(specifiers, importSpecifier(t, exported, names[exported]))
	}
	declaration := node.Parent.Parent
	if namespaceUsed && !allowSyntheticDefaults(r.program.Options()) && ast.IsImportDeclaration(declaration) {
		t.InsertNodeAfter(r.file, declaration, newImport(t, declaration, nil, specifiers))
	} else {
		var defaultName *ast.Node
		if namespaceUsed {
			defaultName = t.NewIdentifier(node.Name().Text())
		}
		t.ReplaceNode(r.file, node.Parent, newImportClause(t, defaultName, specifiers), nil)
	}
	return nil
}
func moduleIdentifier(specifier string) string {
	base := tspath.GetBaseFileName(strings.TrimSuffix(tspath.RemoveFileExtension(specifier), "/index"))
	var b strings.Builder
	lastValid := true
	for index, value := range base {
		valid := value <= 0xffff && scanner.IsIdentifierPart(value)
		if index == 0 {
			valid = value <= 0xffff && scanner.IsIdentifierStart(value)
		}
		if valid {
			text := string(value)
			if index != 0 && !lastValid {
				text = stringutil.ToUpperJS(text)
			}
			b.WriteString(text)
		}
		lastValid = valid
	}
	result := b.String()
	if result == "" || lsutil.IsNonContextualKeyword(scanner.StringToToken(result)) {
		return "_" + result
	}
	return result
}
func namedToNamespace(r *request, t *change.Tracker, node *ast.Node, useDefault bool) {
	elements := node.AsNamedImports().Elements.Nodes
	declaration := node.Parent.Parent
	symbols := map[*ast.Symbol]bool{}
	for _, element := range elements {
		if symbol := r.types().GetSymbolAtLocation(element.Name()); symbol != nil {
			symbols[symbol] = true
		}
	}
	preferred := "module"
	if module := importModuleSpecifier(declaration); module != nil && ast.IsStringLiteral(module) {
		preferred = moduleIdentifier(module.Text())
	}
	conflict := false
	for _, element := range elements {
		for _, ref := range symbolReferences(r, element.Name()) {
			symbol := r.types().ResolveName(preferred, ref, ast.SymbolFlagsAll, true)
			if symbol != nil && (!symbols[symbol] || ast.IsExportSpecifier(ref.Parent)) {
				conflict = true
				break
			}
		}
		if conflict {
			break
		}
	}
	namespace := preferred
	if conflict {
		namespace = uniqueName(preferred, r.file)
	}
	needed := []*ast.Node{}
	seen := map[*ast.Node]bool{}
	for _, element := range elements {
		specifier := element.AsImportSpecifier()
		property := specifier.PropertyName
		if property == nil {
			property = element.Name()
		}
		for _, ref := range symbolReferences(r, element.Name()) {
			var access *ast.Node
			if ast.IsStringLiteral(property) {
				access = t.NewElementAccessExpression(t.NewIdentifier(namespace), nil, t.NewStringLiteral(property.Text(), ast.TokenFlagsNone), ast.NodeFlagsNone)
			} else {
				access = t.NewPropertyAccessExpression(t.NewIdentifier(namespace), nil, t.NewIdentifier(property.Text()), ast.NodeFlagsNone)
			}
			if ast.IsShorthandPropertyAssignment(ref.Parent) {
				t.ReplaceNode(r.file, ref.Parent, t.NewPropertyAssignment(nil, t.NewIdentifier(ref.Text()), nil, nil, access), nil)
			} else if ast.IsExportSpecifier(ref.Parent) {
				if !seen[element] {
					seen[element] = true
					needed = append(needed, element)
				}
			} else {
				t.ReplaceNode(r.file, ref, access, nil)
			}
		}
	}
	var replacement *ast.Node
	if useDefault {
		replacement = t.NewIdentifier(namespace)
	} else {
		replacement = t.NewNamespaceImport(t.NewIdentifier(namespace))
	}
	t.ReplaceNode(r.file, node, replacement, nil)
	if len(needed) > 0 && ast.IsImportDeclaration(declaration) {
		imports := []*ast.Node{}
		for _, element := range needed {
			specifier := element.AsImportSpecifier()
			var property *ast.Node
			if specifier.PropertyName != nil {
				if ast.IsStringLiteral(specifier.PropertyName) {
					property = t.NewStringLiteral(specifier.PropertyName.Text(), ast.TokenFlagsNone)
				} else {
					property = t.NewIdentifier(specifier.PropertyName.Text())
				}
			}
			imports = append(imports, t.NewImportSpecifier(specifier.IsTypeOnly, property, t.NewIdentifier(element.Name().Text())))
		}
		t.InsertNodeAfter(r.file, declaration, newImport(t, declaration, nil, imports))
	}
}
func editConvertImport(r *request, action string, _ Arguments) (*EditInfo, error) {
	if action != namespaceToNamedName && action != namedToNamespaceName && action != namedToDefaultImportName {
		return nil, &change.InvalidEdits{Reason: "invalid import-conversion action"}
	}
	info, reason := importConversionInfo(r, true)
	if info == nil {
		return nil, &change.InvalidEdits{Reason: "import conversion is not applicable: " + reason}
	}
	t := r.tracker()
	if info.kind == importNamed {
		if err := namespaceToNamed(r, t, info.node); err != nil {
			return nil, err
		}
	} else {
		namedToNamespace(r, t, info.node, info.kind == importDefault)
	}
	return finish(t), r.ctx.Err()
}
