package tsfrontend

import (
	"fmt"
	"slices"
	"strings"

	ir "github.com/totoer/goya/ast"
	"github.com/totoer/goya/file"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/module"
	"github.com/totoer/goya/token"
	"github.com/totoer/goya/unistring"
)

// IDs are canonical virtual source paths, or host:<specifier> for declarations
// that require an application implementation. Resolution is captured upstream;
// no backend path probes the operating system or invokes a second resolver.
type IRDependency struct {
	ID              string
	Specifier       string
	Host            bool
	Phase           string
	Attributes      map[string]string
	RequiredExports []string
	Namespace       bool
	Range           Range
}
type IRExport struct {
	Name      string
	From      int // -1 for a local lexical binding
	Imported  string
	Namespace bool
	Star      bool
	Local     ir.Expression
}
type moduleImport struct {
	dependency int
	imported   string
	namespace  bool
}

func (l *lowerer) dependency(specifier, attributes *ast.Node, phase string) int {
	dependency := l.resolveDependency(specifier, attributes, phase)
	for index, existing := range l.module.dependencies {
		if existing.ID == dependency.ID && existing.Phase == phase && equalStringMap(existing.Attributes, dependency.Attributes) {
			return index
		}
	}
	l.module.dependencies = append(l.module.dependencies, dependency)
	return len(l.module.dependencies) - 1
}

func (l *lowerer) resolveDependency(specifier, attributes *ast.Node, phase string) IRDependency {
	name := specifier.Text()
	id := "host:" + name
	host := true
	original := l.sourceNode(specifier)
	var resolution *module.ResolvedModule
	if original.Pos() < 0 || original.Parent == nil {
		resolution = l.analysis.program.GetResolvedModule(l.file, name, l.analysis.program.GetDefaultResolutionModeForFile(l.file))
	} else {
		resolution = l.analysis.program.GetResolvedModuleFromModuleSpecifier(l.file, original)
	}
	if resolution != nil && resolution.ResolvedFileName != "" {
		resolved := resolution.ResolvedFileName
		if source := l.analysis.program.GetSourceFileForResolvedModule(resolved); source != nil {
			resolved = source.FileName()
		}
		if !(strings.HasSuffix(resolved, ".d.ts") || strings.HasSuffix(resolved, ".d.mts") || strings.HasSuffix(resolved, ".d.cts")) {
			id = resolved
			host = false
		}
	}
	values := make(map[string]string)
	if attributes != nil {
		for _, attribute := range attributes.AsImportAttributes().Attributes.Nodes {
			item := attribute.AsImportAttribute()
			values[item.Name().Text()] = item.Value.Text()
		}
	}
	return IRDependency{ID: id, Specifier: name, Host: host, Phase: phase, Attributes: values, Namespace: phase == "require" || phase == "dynamic", Range: Range{Start: sourcePosition(l.file.Text(), l.start(specifier)), End: sourcePosition(l.file.Text(), int(l.end(specifier))-1)}}
}

func (l *lowerer) collectDynamicImports(n *ast.Node) {
	if n.Kind == ast.KindCallExpression {
		x := n.AsCallExpression()
		callee := x.Expression
		if callee.Kind == ast.KindImportKeyword || (callee.Kind == ast.KindMetaProperty && callee.AsMetaProperty().KeywordToken == ast.KindImportKeyword && callee.Text() == "defer") {
			if x.Arguments != nil && len(x.Arguments.Nodes) > 0 {
				specifier := x.Arguments.Nodes[0]
				if ast.IsStringLiteralLike(specifier) {
					l.module.dynamicDependencies = append(l.module.dynamicDependencies, l.resolveDependency(specifier, nil, "dynamic"))
				} else {
					l.module.computedImport = true
				}
			}
		}
	}
	n.ForEachChild(func(child *ast.Node) bool { l.collectDynamicImports(child); return false })
}
func equalStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if value, ok := b[k]; !ok || value != v {
			return false
		}
	}
	return true
}
func moduleImportName(index int) unistring.String {
	return unistring.String(fmt.Sprintf("\x00goya.import.%d", index))
}

func (l *lowerer) addImport(name *ast.Node, reference moduleImport) {
	original := l.sourceNode(name)
	if original.Flags&ast.NodeFlagsSynthesized != 0 || original.Parent == nil {
		l.syntheticImports[l.identifier(name).Name] = reference
	} else {
		symbol := l.checker.GetSymbolAtLocation(original)
		if symbol == nil {
			l.unsupported(name)
		}
		l.imports[symbol] = reference
	}
	dependency := &l.module.dependencies[reference.dependency]
	if reference.namespace {
		dependency.Namespace = true
	} else if !slices.Contains(dependency.RequiredExports, reference.imported) {
		dependency.RequiredExports = append(dependency.RequiredExports, reference.imported)
	}
}
func (l *lowerer) importedReference(n *ast.Node) (moduleImport, bool) {
	original := l.sourceNode(n)
	if original.Kind != ast.KindIdentifier || original.Flags&ast.NodeFlagsSynthesized != 0 {
		if n.Kind == ast.KindIdentifier {
			reference, ok := l.syntheticImports[l.identifier(n).Name]
			return reference, ok
		}
		return moduleImport{}, false
	}
	var symbol *ast.Symbol
	if original.Parent != nil && original.Parent.Kind == ast.KindShorthandPropertyAssignment && original.Parent.Name() == original {
		// A shorthand name has a property symbol; resolve its value binding instead.
		symbol = l.checker.GetShorthandAssignmentValueSymbol(original.Parent)
	} else {
		symbol = l.checker.GetSymbolAtLocation(original)
	}
	reference, ok := l.imports[symbol]
	return reference, ok
}
func (l *lowerer) importExpression(n *ast.Node, reference moduleImport) ir.Expression {
	base := &ir.Identifier{Name: moduleImportName(reference.dependency), Idx: l.idx(n)}
	if reference.namespace {
		return base
	}
	return &ir.BracketExpression{Left: base, Member: &ir.StringLiteral{Idx: l.idx(n), Value: executionString(reference.imported)}, LeftBracket: l.idx(n), RightBracket: l.end(n)}
}

func (l *lowerer) callCallee(n *ast.Node) ir.Expression {
	for n.Kind == ast.KindParenthesizedExpression || n.Kind == ast.KindNonNullExpression || n.Kind == ast.KindAsExpression || n.Kind == ast.KindTypeAssertionExpression {
		n = n.Expression()
	}
	if n.Kind == ast.KindIdentifier {
		if reference, ok := l.importedReference(n); ok && !reference.namespace {
			return &ir.SequenceExpression{Sequence: []ir.Expression{&ir.NumberLiteral{Idx: l.idx(n), Literal: "0", Value: float64(0)}, l.importExpression(n, reference)}}
		}
	}
	return l.chainExpression(n)
}

const ModuleRegisterName unistring.String = "\x00goya.module.register"
const ModuleMetaName unistring.String = "\x00goya.module.meta"
const ModuleRequireName unistring.String = "\x00goya.module.require"
const ModuleExportsName unistring.String = "\x00goya.module.exports"
const ModuleObjectName unistring.String = "\x00goya.module.object"

// FactoryTree builds a private generator. The first explicit yield separates
// lexical instantiation (including hoisted functions and TDZ cells) from user
// evaluation. No user initializer executes while export getters are installed.
func (m *IRModule) FactoryTree() *ir.Program {
	parameters := &ir.ParameterList{Opening: 1, Closing: 1, List: []*ir.Binding{{Target: &ir.Identifier{Name: ModuleRegisterName, Idx: 1}}, {Target: &ir.Identifier{Name: ModuleMetaName, Idx: 1}}}}
	for _, name := range []unistring.String{ModuleRequireName, ModuleExportsName, ModuleObjectName} {
		parameters.List = append(parameters.List, &ir.Binding{Target: &ir.Identifier{Name: name, Idx: 1}})
	}
	for i := range m.dependencies {
		parameters.List = append(parameters.List, &ir.Binding{Target: &ir.Identifier{Name: moduleImportName(i), Idx: 1}})
	}
	var body []ir.Statement
	reflectGetter := &ir.ArrowFunctionLiteral{Start: 1, End: 1, ParameterList: &ir.ParameterList{Opening: 1, Closing: 1}, Body: &ir.ExpressionBody{Expression: &ir.Identifier{Name: "Reflect", Idx: 1}}}
	body = append(body, &ir.ExpressionStatement{Expression: &ir.CallExpression{Callee: &ir.Identifier{Name: ModuleRegisterName, Idx: 1}, LeftParenthesis: 1, RightParenthesis: 1, ArgumentList: []ir.Expression{&ir.UnaryExpression{Operator: token.VOID, Idx: 1, Operand: &ir.NumberLiteral{Idx: 1, Literal: "0", Value: float64(0)}}, reflectGetter}}})
	for _, export := range m.exports {
		if m.commonJS {
			continue
		}
		if export.From >= 0 || export.Star {
			continue
		}
		getter := &ir.ArrowFunctionLiteral{Start: 1, End: 1, ParameterList: &ir.ParameterList{Opening: 1, Closing: 1}, Body: &ir.ExpressionBody{Expression: export.Local}}
		body = append(body, &ir.ExpressionStatement{Expression: &ir.CallExpression{Callee: &ir.Identifier{Name: ModuleRegisterName, Idx: 1}, LeftParenthesis: 1, RightParenthesis: 1, ArgumentList: []ir.Expression{&ir.StringLiteral{Idx: 1, Value: executionString(export.Name)}, getter}}})
	}
	body = append(body, &ir.ExpressionStatement{Expression: &ir.YieldExpression{Yield: 1}})
	body = append(body, m.tree.Body...)
	function := &ir.FunctionLiteral{Function: 1, Module: true, Generator: true, ParameterList: parameters, Body: &ir.BlockStatement{LeftBrace: 1, RightBrace: file.Idx(len(m.tree.File.Source()) + 1), List: body}, DeclarationList: slices.Clone(m.tree.DeclarationList), Source: m.tree.File.Source()}
	return &ir.Program{File: m.tree.File, Body: []ir.Statement{&ir.ExpressionStatement{Expression: function}}}
}
func (l *lowerer) collectImports(statements []*ast.Node) {
	for _, n := range statements {
		if n.Kind != ast.KindImportDeclaration {
			continue
		}
		x := n.AsImportDeclaration()
		phase := "evaluation"
		if x.ImportClause != nil && x.ImportClause.AsImportClause().PhaseModifier == ast.KindTypeKeyword {
			continue
		}
		if x.ImportClause != nil && x.ImportClause.AsImportClause().PhaseModifier == ast.KindDeferKeyword {
			phase = "defer"
		}
		index := l.dependency(x.ModuleSpecifier, x.Attributes, phase)
		if x.ImportClause == nil {
			continue
		}
		clause := x.ImportClause.AsImportClause()
		if clause.Name() != nil {
			l.addImport(clause.Name(), moduleImport{dependency: index, imported: "default"})
		}
		if clause.NamedBindings == nil {
			continue
		}
		if clause.NamedBindings.Kind == ast.KindNamespaceImport {
			l.addImport(clause.NamedBindings.Name(), moduleImport{dependency: index, namespace: true})
			continue
		}
		for _, element := range clause.NamedBindings.AsNamedImports().Elements.Nodes {
			specifier := element.AsImportSpecifier()
			if specifier.IsTypeOnly {
				continue
			}
			name := specifier.Name().Text()
			if specifier.PropertyName != nil {
				name = specifier.PropertyName.Text()
			}
			l.addImport(specifier.Name(), moduleImport{dependency: index, imported: name})
		}
	}
}

func (l *lowerer) collectModuleRequests(statements []*ast.Node) {
	for _, n := range statements {
		switch n.Kind {
		case ast.KindImportDeclaration:
			x := n.AsImportDeclaration()
			phase := "evaluation"
			if x.ImportClause != nil {
				modifier := x.ImportClause.AsImportClause().PhaseModifier
				if modifier == ast.KindTypeKeyword {
					continue
				}
				if modifier == ast.KindDeferKeyword {
					phase = "defer"
				}
			}
			l.dependency(x.ModuleSpecifier, x.Attributes, phase)
		case ast.KindExportDeclaration:
			x := n.AsExportDeclaration()
			if !x.IsTypeOnly && x.ModuleSpecifier != nil {
				l.dependency(x.ModuleSpecifier, x.Attributes, "evaluation")
			}
		case ast.KindImportEqualsDeclaration:
			x := n.AsImportEqualsDeclaration()
			if !x.IsTypeOnly && x.ModuleReference.Kind == ast.KindExternalModuleReference {
				l.dependency(x.ModuleReference.Expression(), nil, "require")
			}
		}
	}
}
func (l *lowerer) localExport(exported string, name *ast.Node) {
	if reference, ok := l.imports[l.file.Locals[name.Text()]]; ok {
		l.module.exports = append(l.module.exports, IRExport{Name: exported, From: reference.dependency, Imported: reference.imported, Namespace: reference.namespace})
		return
	}
	if reference, ok := l.importedReference(name); ok {
		l.module.exports = append(l.module.exports, IRExport{Name: exported, From: reference.dependency, Imported: reference.imported, Namespace: reference.namespace})
		return
	}
	l.module.exports = append(l.module.exports, IRExport{Name: exported, From: -1, Local: l.identifier(name)})
}
func (l *lowerer) exportBinding(name *ast.Node) {
	if name.Kind == ast.KindIdentifier {
		l.localExport(name.Text(), name)
		return
	}
	for _, element := range name.AsBindingPattern().Elements.Nodes {
		if n := element.Name(); n != nil {
			l.exportBinding(n)
		}
	}
}
func (l *lowerer) collectExports(statements []*ast.Node) {
	for _, n := range statements {
		if n.Kind == ast.KindExportAssignment && !n.AsExportAssignment().IsExportEquals {
			l.module.exports = append(l.module.exports, IRExport{Name: "default", From: -1, Local: l.defaultIdentifier(n)})
			continue
		}
		if n.ModifierFlags()&ast.ModifierFlagsExport != 0 {
			if n.Kind == ast.KindVariableStatement {
				for _, d := range n.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
					l.exportBinding(d.Name())
				}
			} else if n.Name() != nil {
				name := n.Name().Text()
				if n.ModifierFlags()&ast.ModifierFlagsDefault != 0 {
					name = "default"
				}
				l.localExport(name, n.Name())
			} else if n.ModifierFlags()&ast.ModifierFlagsDefault != 0 {
				l.module.exports = append(l.module.exports, IRExport{Name: "default", From: -1, Local: l.defaultIdentifier(n)})
			}
		}
		if n.Kind != ast.KindExportDeclaration {
			continue
		}
		x := n.AsExportDeclaration()
		if x.IsTypeOnly {
			continue
		}
		from := -1
		if x.ModuleSpecifier != nil {
			from = l.dependency(x.ModuleSpecifier, x.Attributes, "evaluation")
		}
		if x.ExportClause == nil {
			if from >= 0 {
				l.module.dependencies[from].Namespace = true
			}
			l.module.exports = append(l.module.exports, IRExport{From: from, Star: true})
			continue
		}
		if x.ExportClause.Kind == ast.KindNamespaceExport {
			if from >= 0 {
				l.module.dependencies[from].Namespace = true
			}
			l.module.exports = append(l.module.exports, IRExport{Name: x.ExportClause.Name().Text(), From: from, Namespace: true})
			continue
		}
		for _, element := range x.ExportClause.AsNamedExports().Elements.Nodes {
			specifier := element.AsExportSpecifier()
			if specifier.IsTypeOnly {
				continue
			}
			local := specifier.Name()
			if specifier.PropertyName != nil {
				local = specifier.PropertyName
			}
			if from < 0 {
				l.localExport(specifier.Name().Text(), local)
			} else {
				if !slices.Contains(l.module.dependencies[from].RequiredExports, local.Text()) {
					l.module.dependencies[from].RequiredExports = append(l.module.dependencies[from].RequiredExports, local.Text())
				}
				l.module.exports = append(l.module.exports, IRExport{Name: specifier.Name().Text(), From: from, Imported: local.Text()})
			}
		}
	}
}

func (l *lowerer) defaultIdentifier(n *ast.Node) *ir.Identifier {
	return &ir.Identifier{Name: unistring.String(fmt.Sprintf("\x00goya.default.%d", l.start(n))), Idx: l.idx(n)}
}
func defaultNameHint(expression ir.Expression) {
	switch x := expression.(type) {
	case *ir.FunctionLiteral:
		if x.Name == nil {
			x.NameHint = "default"
		}
	case *ir.ArrowFunctionLiteral:
		x.NameHint = "default"
	case *ir.ClassLiteral:
		if x.Name == nil {
			x.NameHint = "default"
		}
	}
}
func (l *lowerer) defaultStatement(n *ast.Node, expression ir.Expression) ir.Statement {
	defaultNameHint(expression)
	return &ir.LexicalDeclaration{Idx: l.idx(n), Token: token.CONST, List: []*ir.Binding{{Target: l.defaultIdentifier(n), Initializer: expression}}}
}
