package refactor

import (
	"slices"
	"sort"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/checker"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/nodebuilder"
	"github.com/totoer/goya/internal/tsfrontend/upstream/printer"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"github.com/totoer/goya/lsp/protocol"
)

const destructuredParametersName = "Convert parameters to destructured object"
const destructuredParametersKind = "refactor.rewrite.parameters.toDestructured"

var destructuredParametersProvider = provider{name: destructuredParametersName, kinds: []string{destructuredParametersKind}, available: availableDestructuredParameters, edits: editDestructuredParameters}

func hasThisParameter(parameters []*ast.Node) bool {
	return len(parameters) > 0 && ast.IsThisIdentifier(parameters[0].Name())
}
func refactorableParameters(declaration *ast.Node) []*ast.Node {
	parameters := declaration.Parameters()
	if hasThisParameter(parameters) {
		return parameters[1:]
	}
	return parameters
}
func validConstFunctionVariable(node *ast.Node) bool {
	return node != nil && ast.IsVariableDeclaration(node) && ast.IsVarConst(node) && ast.IsIdentifier(node.Name()) && node.Type() == nil
}
func contextualMethodSymbol(r *request, node *ast.Node) *ast.Symbol {
	element := ls.ReferenceContainingObjectLiteralElement(node)
	if element == nil {
		return nil
	}
	typ := r.types().GetContextualTypeForObjectLiteralElement(element, checker.ContextFlagsNone)
	if typ != nil {
		symbol := typ.Symbol()
		if symbol != nil && symbol.CheckFlags&ast.CheckFlagsSynthetic == 0 {
			return symbol
		}
	}
	return nil
}
func singleImplementation(r *request, node *ast.Node) bool {
	return node.Body() != nil && !r.types().ReferenceIsImplementationOfOverload(node)
}
func namedOrDefault(node *ast.Node) bool {
	return node.Name() != nil || findModifier(node, ast.KindDefaultKeyword) != nil
}
func validDestructurableFunction(r *request, node *ast.Node) bool {
	parameters := node.Parameters()
	if len(refactorableParameters(node)) < 1 {
		return false
	}
	for _, parameter := range parameters {
		data := parameter.AsParameterDeclaration()
		if data.DotDotDotToken != nil {
			typ := r.types().GetTypeAtLocation(parameter)
			if !r.types().IsArrayType(typ) && !checker.IsTupleType(typ) {
				return false
			}
		}
		if parameter.Modifiers() != nil || !ast.IsIdentifier(parameter.Name()) {
			return false
		}
	}
	switch node.Kind {
	case ast.KindFunctionDeclaration:
		return namedOrDefault(node) && singleImplementation(r, node)
	case ast.KindMethodDeclaration:
		if ast.IsObjectLiteralExpression(node.Parent) {
			symbol := contextualMethodSymbol(r, node.Name())
			return symbol != nil && len(symbol.Declarations) == 1 && singleImplementation(r, node)
		}
		return singleImplementation(r, node)
	case ast.KindConstructor:
		if ast.IsClassDeclaration(node.Parent) {
			return namedOrDefault(node.Parent) && singleImplementation(r, node)
		}
		return validConstFunctionVariable(node.Parent.Parent) && singleImplementation(r, node)
	case ast.KindFunctionExpression, ast.KindArrowFunction:
		return validConstFunctionVariable(node.Parent)
	}
	return false
}
func destructurableFunction(r *request) *ast.Node {
	token := astnav.GetTouchingToken(r.file, r.span.Pos())
	fn := ancestor(token.Parent, ast.IsFunctionLikeDeclaration)
	if doc := ancestor(token, ast.IsJSDocNode); doc != nil {
		parent := ancestor(doc, func(node *ast.Node) bool { return !ast.IsJSDocNode(node) })
		if parent != nil && ast.IsFunctionLikeDeclaration(parent) {
			return nil
		}
	}
	if fn != nil && validDestructurableFunction(r, fn) && nodeContains(fn, token) && !nodeContains(fn.Body(), token) {
		return fn
	}
	return nil
}
func availableDestructuredParameters(r *request) []protocol.ApplicableRefactorInfo {
	if destructurableFunction(r) == nil {
		return nil
	}
	description := diagnostics.Convert_parameters_to_destructured_object.Localize(locale.FromContext(r.ctx))
	return []protocol.ApplicableRefactorInfo{{Name: destructuredParametersName, Description: description, Actions: []protocol.RefactorActionInfo{{Name: destructuredParametersName, Description: description, Kind: destructuredParametersKind}}}}
}
func destructuredFunctionNames(r *request, node *ast.Node) []*ast.Node {
	switch node.Kind {
	case ast.KindFunctionDeclaration:
		if node.Name() != nil {
			return []*ast.Node{node.Name()}
		}
		return []*ast.Node{findModifier(node, ast.KindDefaultKeyword)}
	case ast.KindMethodDeclaration:
		return []*ast.Node{node.Name()}
	case ast.KindConstructor:
		keyword := astnav.FindChildOfKind(node, ast.KindConstructorKeyword, r.file)
		if ast.IsClassExpression(node.Parent) {
			return []*ast.Node{node.Parent.Parent.Name(), keyword}
		}
		return []*ast.Node{keyword}
	case ast.KindArrowFunction:
		return []*ast.Node{node.Parent.Name()}
	case ast.KindFunctionExpression:
		if node.Name() != nil {
			return []*ast.Node{node.Name(), node.Parent.Name()}
		}
		return []*ast.Node{node.Parent.Name()}
	}
	return nil
}
func destructuredClassNames(node *ast.Node) []*ast.Node {
	class := node.Parent
	if ast.IsClassDeclaration(class) {
		if class.Name() != nil {
			return []*ast.Node{class.Name()}
		}
		return []*ast.Node{findModifier(class, ast.KindDefaultKeyword)}
	}
	if ast.IsClassExpression(class) {
		if class.Name() != nil {
			return []*ast.Node{class.Name(), class.Parent.Name()}
		}
		return []*ast.Node{class.Parent.Name()}
	}
	return nil
}
func symbolTarget(r *request, node *ast.Node) *ast.Symbol {
	return r.types().GetReferenceSymbolTarget(r.types().GetSymbolAtLocation(node))
}
func refImportExport(node *ast.Node) bool {
	parent := node.Parent
	return ast.IsImportSpecifier(parent) || ast.IsImportClause(parent) || ast.IsImportEqualsDeclaration(parent) || ast.IsNamespaceImport(parent) || ast.IsExportSpecifier(parent) || ast.IsExportAssignment(parent)
}
func referenceCall(node *ast.Node) *ast.Node {
	parent := node.Parent
	if ast.IsCallExpression(parent) || ast.IsNewExpression(parent) {
		if parent.Expression() == node {
			return parent
		}
		return nil
	}
	if ast.IsPropertyAccessExpression(parent) && parent.Name() == node || ast.IsElementAccessExpression(parent) && parent.AsElementAccessExpression().ArgumentExpression == node {
		call := parent.Parent
		if call != nil && (ast.IsCallExpression(call) || ast.IsNewExpression(call)) && call.Expression() == parent {
			return call
		}
	}
	return nil
}
func validContextualMethodSignature(node *ast.Node) bool {
	return ast.IsMethodSignatureDeclaration(node) && (ast.IsInterfaceDeclaration(node.Parent) || ast.IsTypeLiteralNode(node.Parent))
}

type parameterReferences struct {
	calls, declarations []*ast.Node
	signature           *ast.Node
	valid               bool
}

func groupedParameterReferences(r *request, fn *ast.Node) parameterReferences {
	functions := destructuredFunctionNames(r, fn)
	classes := []*ast.Node{}
	if ast.IsConstructorDeclaration(fn) {
		classes = destructuredClassNames(fn)
	}
	names := slices.Clone(functions)
	for _, name := range classes {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	entries := []*ls.ReferenceEntry{}
	for _, name := range names {
		if r.ctx.Err() != nil {
			return parameterReferences{}
		}
		entries = append(entries, r.service.GetReferenceRefactorEntries(r.ctx, name)...)
	}
	result := parameterReferences{valid: true}
	functionSymbols := []*ast.Symbol{}
	classSymbols := []*ast.Symbol{}
	contextualSymbols := []*ast.Symbol{}
	for _, name := range functions {
		functionSymbols = append(functionSymbols, symbolTarget(r, name))
		contextualSymbols = append(contextualSymbols, contextualMethodSymbol(r, name))
	}
	for _, name := range classes {
		classSymbols = append(classSymbols, symbolTarget(r, name))
	}
	constructor := ast.IsConstructorDeclaration(fn)
	for _, entry := range entries {
		if !entry.IsNodeEntry() {
			result.valid = false
			continue
		}
		node := entry.Node()
		target := symbolTarget(r, node)
		if slices.Contains(contextualSymbols, target) {
			if validContextualMethodSignature(node.Parent) {
				result.signature = node.Parent
				continue
			}
			if call := referenceCall(node); call != nil {
				result.calls = append(result.calls, call)
				continue
			}
		}
		if contextual := contextualMethodSymbol(r, node); contextual != nil && slices.Contains(contextualSymbols, contextual) {
			if referenceIsDeclaration(node.Parent) {
				result.declarations = append(result.declarations, node)
				continue
			}
		}
		if slices.Contains(functionSymbols, target) || ast.IsNewExpressionTarget(node, false, false) {
			if refImportExport(node) {
				continue
			}
			if referenceIsDeclaration(node.Parent) {
				result.declarations = append(result.declarations, node)
				continue
			}
			if call := referenceCall(node); call != nil {
				result.calls = append(result.calls, call)
				continue
			}
		}
		if constructor && slices.Contains(classSymbols, target) {
			if refImportExport(node) {
				continue
			}
			if referenceIsDeclaration(node.Parent) {
				result.declarations = append(result.declarations, node)
				continue
			}
			if (ast.IsPropertyAccessExpression(node.Parent) || ast.IsElementAccessExpression(node.Parent)) && node.Parent.Expression() == node {
				continue
			}
			if ast.IsClassDeclaration(fn.Parent) && (ls.ReferenceMeaningFromLocation(node) == ast.SemanticMeaningType || ast.IsExpressionWithTypeArgumentsInClassExtendsClause(node.Parent)) {
				continue
			}
		}
		result.valid = false
	}
	for _, declaration := range result.declarations {
		if !slices.Contains(names, declaration) {
			result.valid = false
		}
	}
	return result
}

type parameterAccessibility struct {
	checker    *checker.Checker
	accessible bool
}

func (a *parameterAccessibility) TrackSymbol(symbol *ast.Symbol, declaration *ast.Node, meaning ast.SymbolFlags) bool {
	a.accessible = a.accessible && a.checker.IsSymbolAccessible(symbol, declaration, meaning, false).Accessibility == printer.SymbolAccessibilityAccessible
	return !a.accessible
}
func (a *parameterAccessibility) ReportInaccessibleThisError()                         { a.accessible = false }
func (a *parameterAccessibility) ReportPrivateInBaseOfClassExpression(string)          { a.accessible = false }
func (a *parameterAccessibility) ReportInaccessibleUniqueSymbolError()                 { a.accessible = false }
func (a *parameterAccessibility) ReportCyclicStructureError()                          {}
func (a *parameterAccessibility) ReportLikelyUnsafeImportRequiredError(string, string) {}
func (a *parameterAccessibility) ReportTruncationError()                               {}
func (a *parameterAccessibility) ReportNonlocalAugmentation(*ast.SourceFile, *ast.Symbol, *ast.Symbol) {
}
func (a *parameterAccessibility) ReportNonSerializableProperty(string) {}
func (a *parameterAccessibility) ReportInferenceFallback(*ast.Node)    {}
func (a *parameterAccessibility) PushErrorFallbackNode(*ast.Node)      {}
func (a *parameterAccessibility) PopErrorFallbackNode()                {}
func accessibleParameterType(r *request, t *change.Tracker, node *ast.Node) *ast.Node {
	tracker := &parameterAccessibility{checker: r.types(), accessible: true}
	builder := checker.NewNodeBuilder(r.types(), t.EmitContext)
	typ := builder.TypeToTypeNode(r.types().GetTypeAtLocation(node), node, nodebuilder.FlagsNoTruncation, nodebuilder.InternalFlagsAllowUnresolvedNames, tracker)
	if tracker.accessible {
		return typ
	}
	return nil
}
func optionalDestructuredParameter(r *request, node *ast.Node) bool {
	if node.AsParameterDeclaration().DotDotDotToken != nil {
		return !checker.IsTupleType(r.types().GetTypeAtLocation(node))
	}
	return r.types().ReferenceIsOptionalParameter(node)
}
func newDestructuredParameters(r *request, t *change.Tracker, declaration *ast.Node) []*ast.Node {
	parameters := refactorableParameters(declaration)
	bindings := []*ast.Node{}
	members := []*ast.Node{}
	allOptional := true
	for _, node := range parameters {
		parameter := node.AsParameterDeclaration()
		name := node.Name().Text()
		optional := optionalDestructuredParameter(r, node)
		initializer := parameter.Initializer
		if parameter.DotDotDotToken != nil && optional {
			initializer = t.NewArrayLiteralExpression(t.NewNodeList(nil), false)
		}
		element := t.NewBindingElement(nil, nil, t.NewIdentifier(name), initializer)
		suppressBothTrivia(t, element)
		if parameter.Initializer != nil && initializer != nil {
			copyParameterComments(r, t, parameter.Initializer, initializer)
		}
		bindings = append(bindings, element)
		typ := parameter.Type
		if typ == nil && (parameter.Initializer != nil || parameter.DotDotDotToken != nil) {
			typ = accessibleParameterType(r, t, node)
		}
		question := parameter.QuestionToken
		if optional {
			question = t.NewToken(ast.KindQuestionToken)
		} else {
			allOptional = false
		}
		member := t.NewPropertySignatureDeclaration(nil, t.NewIdentifier(name), question, typ, nil)
		suppressBothTrivia(t, member)
		copyParameterComments(r, t, node.Name(), member.Name())
		if parameter.Type != nil && typ != nil {
			copyParameterComments(r, t, parameter.Type, typ)
		}
		members = append(members, member)
	}
	pattern := t.NewBindingPattern(ast.KindObjectBindingPattern, t.NewNodeList(bindings))
	typ := t.NewTypeLiteralNode(t.NewNodeList(members))
	t.AddEmitFlags(typ, printer.EFSingleLine)
	var initializer *ast.Node
	if allOptional {
		initializer = t.NewObjectLiteralExpression(t.NewNodeList(nil), false)
	}
	parameter := t.NewParameterDeclaration(nil, nil, pattern, nil, typ, initializer)
	result := []*ast.Node{parameter}
	if hasThisParameter(declaration.Parameters()) {
		original := declaration.Parameters()[0]
		data := original.AsParameterDeclaration()
		thisParameter := t.NewParameterDeclaration(nil, nil, original.Name(), nil, data.Type, nil)
		suppressBothTrivia(t, thisParameter.Name())
		copyParameterComments(r, t, original.Name(), thisParameter.Name())
		if data.Type != nil {
			suppressBothTrivia(t, data.Type)
			copyParameterComments(r, t, data.Type, thisParameter.Type())
		}
		result = []*ast.Node{thisParameter, parameter}
	}
	for index, node := range result {
		result[index] = cloneParameterTypes(r, t, node)
	}
	return result
}
func newDestructuredArgument(r *request, t *change.Tracker, fn *ast.Node, arguments []*ast.Node) (*ast.Node, error) {
	parameters := refactorableParameters(fn)
	hasRest := parameters[len(parameters)-1].AsParameterDeclaration().DotDotDotToken != nil
	nonRest := arguments
	if hasRest {
		nonRest = arguments[:min(len(arguments), len(parameters)-1)]
	}
	properties := []*ast.Node{}
	for index, argument := range nonRest {
		if index >= len(parameters) {
			return nil, &change.InvalidEdits{Reason: "destructured-parameter proposal has more arguments than parameters"}
		}
		name := parameters[index].Name().Text()
		var property *ast.Node
		if ast.IsIdentifier(argument) && argument.Text() == name {
			property = t.NewShorthandPropertyAssignment(nil, t.NewIdentifier(name), nil, nil, nil, nil)
		} else {
			property = t.NewPropertyAssignment(nil, t.NewIdentifier(name), nil, nil, argument)
		}
		suppressBothTrivia(t, property.Name())
		if ast.IsPropertyAssignment(property) {
			suppressBothTrivia(t, property.Initializer())
		}
		copyParameterComments(r, t, argument, property)
		properties = append(properties, property)
	}
	if hasRest && len(arguments) >= len(parameters) {
		rest := arguments[len(parameters)-1:]
		properties = append(properties, t.NewPropertyAssignment(nil, t.NewIdentifier(parameters[len(parameters)-1].Name().Text()), nil, nil, t.NewArrayLiteralExpression(t.NewNodeList(rest), false)))
	}
	return cloneWithTrivia(t, t.NewObjectLiteralExpression(t.NewNodeList(properties), false)), nil
}
func editDestructuredParameters(r *request, action string, _ Arguments) (*EditInfo, error) {
	if action != destructuredParametersName {
		return nil, &change.InvalidEdits{Reason: "invalid destructured-parameter action"}
	}
	fn := destructurableFunction(r)
	if fn == nil {
		return nil, nil
	}
	references := groupedParameterReferences(r, fn)
	if !references.valid {
		return &EditInfo{Changes: []ls.ReferenceFileCodeChanges{}}, r.ctx.Err()
	}
	t := r.tracker()
	// The reference anchors both declaration and contextual signature parameters in
	// the request file, including foreign-signature source-coordinate bugs.
	params := newDestructuredParameters(r, t, fn)
	if references.signature != nil {
		t.ReplaceReferenceParameters(r.file, references.signature, newDestructuredParameters(r, t, references.signature))
	}
	t.ReplaceReferenceParameters(r.file, fn, params)
	// Source intentionally deduplicates by position, not by filename or node id.
	sort.SliceStable(references.calls, func(i, j int) bool { return references.calls[i].Pos() < references.calls[j].Pos() })
	previous := -1
	for _, call := range references.calls {
		if call.Pos() == previous {
			continue
		}
		previous = call.Pos()
		list := call.ArgumentList()
		if list == nil || len(list.Nodes) == 0 {
			continue
		}
		argument, err := newDestructuredArgument(r, t, fn, list.Nodes)
		if err != nil {
			return nil, err
		}
		file := ast.GetSourceFileOfNode(call)
		span := t.GetAdjustedRange(file, list.Nodes[0], list.Nodes[len(list.Nodes)-1], change.LeadingTriviaOptionIncludeAll, change.TrailingTriviaOptionInclude)
		t.ReplaceRange(file, span, argument, change.NodeOptions{LeadingTriviaOption: change.LeadingTriviaOptionIncludeAll, TrailingTriviaOption: change.TrailingTriviaOptionInclude})
	}
	return finish(t), r.ctx.Err()
}

func suppressBothTrivia(t *change.Tracker, node *ast.Node) {
	suppressTrivia(t, node, true)
	suppressTrivia(t, node, false)
}

// TypeScript 6 declaration classification excludes Corsa's JS assignment/call
// declaration nodes. Refactor reference validation uses the pinned classification.
func referenceIsDeclaration(node *ast.Node) bool {
	if node.Kind == ast.KindTypeParameter {
		return node.Parent != nil && node.Parent.Kind != ast.KindJSDocTemplateTag || ast.IsInJSFile(node)
	}
	switch node.Kind {
	case ast.KindArrowFunction,
		ast.KindBindingElement,
		ast.KindClassDeclaration,
		ast.KindClassExpression,
		ast.KindClassStaticBlockDeclaration,
		ast.KindConstructor,
		ast.KindEnumDeclaration,
		ast.KindEnumMember,
		ast.KindExportSpecifier,
		ast.KindFunctionDeclaration,
		ast.KindFunctionExpression,
		ast.KindGetAccessor,
		ast.KindImportClause,
		ast.KindImportEqualsDeclaration,
		ast.KindImportSpecifier,
		ast.KindInterfaceDeclaration,
		ast.KindJsxAttribute,
		ast.KindMethodDeclaration,
		ast.KindMethodSignature,
		ast.KindModuleDeclaration,
		ast.KindNamespaceExportDeclaration,
		ast.KindNamespaceImport,
		ast.KindNamespaceExport,
		ast.KindParameter,
		ast.KindPropertyAssignment,
		ast.KindPropertyDeclaration,
		ast.KindPropertySignature,
		ast.KindSetAccessor,
		ast.KindShorthandPropertyAssignment,
		ast.KindTypeAliasDeclaration,
		ast.KindVariableDeclaration,
		ast.KindJSDocTypedefTag,
		ast.KindJSDocCallbackTag,
		ast.KindJSDocPropertyTag,
		ast.KindNamedTupleMember:
		return true
	}
	return false
}

func copyParameterComments(r *request, t *change.Tracker, from, to *ast.Node) {
	copyComments(r, t, from, to)
	t.AddEmitFlags(to, printer.EFReferenceSyntheticComments)
}
func cloneParameterTypes(r *request, t *change.Tracker, node *ast.Node) *ast.Node {
	var visit func(*ast.Node) bool
	visit = func(n *ast.Node) bool {
		if (ast.IsTupleTypeNode(n) || ast.IsTypeLiteralNode(n)) && n.Pos() >= 0 {
			file := ast.GetSourceFileOfNode(n)
			if file != nil && scanner.GetECMALineOfPosition(file, n.Pos()) == scanner.GetECMALineOfPosition(file, n.End()) {
				t.AddEmitFlags(n, printer.EFSingleLine)
			}
		}
		return n.ForEachChild(visit)
	}
	visit(node)
	return cloneWithTrivia(t, node)
}
