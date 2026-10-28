// Modified for Goya: internal import paths relocated by cmd/importupstream.

// Modified for Goya: embedded-language-service.

package ls

import (
	"context"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/internal/tsfrontend/upstream/transformers/declarations"
	"slices"
	"strconv"
	"unicode/utf16"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/checker"
	"github.com/totoer/goya/internal/tsfrontend/upstream/compiler"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/autoimport"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/nodebuilder"
	"github.com/totoer/goya/internal/tsfrontend/upstream/printer"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
)

var isolatedDeclarationsFixErrorCodes = []int32{
	diagnostics.Function_must_have_an_explicit_return_type_annotation_with_isolatedDeclarations.Code(),
	diagnostics.Method_must_have_an_explicit_return_type_annotation_with_isolatedDeclarations.Code(),
	diagnostics.At_least_one_accessor_must_have_an_explicit_type_annotation_with_isolatedDeclarations.Code(),
	diagnostics.Variable_must_have_an_explicit_type_annotation_with_isolatedDeclarations.Code(),
	diagnostics.Parameter_must_have_an_explicit_type_annotation_with_isolatedDeclarations.Code(),
	diagnostics.Property_must_have_an_explicit_type_annotation_with_isolatedDeclarations.Code(),
	diagnostics.Expression_type_can_t_be_inferred_with_isolatedDeclarations.Code(),
	diagnostics.Binding_elements_can_t_be_exported_directly_with_isolatedDeclarations.Code(),
	diagnostics.Computed_property_names_on_class_or_object_literals_cannot_be_inferred_with_isolatedDeclarations.Code(),
	diagnostics.Computed_properties_must_be_number_or_string_literals_variables_or_dotted_expressions_with_isolatedDeclarations.Code(),
	diagnostics.Enum_member_initializers_must_be_computable_without_references_to_external_symbols_with_isolatedDeclarations.Code(),
	diagnostics.Extends_clause_can_t_contain_an_expression_with_isolatedDeclarations.Code(),
	diagnostics.Objects_that_contain_shorthand_properties_can_t_be_inferred_with_isolatedDeclarations.Code(),
	diagnostics.Objects_that_contain_spread_assignments_can_t_be_inferred_with_isolatedDeclarations.Code(),
	diagnostics.Arrays_with_spread_elements_can_t_inferred_with_isolatedDeclarations.Code(),
	diagnostics.Default_exports_can_t_be_inferred_with_isolatedDeclarations.Code(),
	diagnostics.Only_const_arrays_can_be_inferred_with_isolatedDeclarations.Code(),
	diagnostics.Assigning_properties_to_functions_without_declaring_them_is_not_supported_with_isolatedDeclarations_Add_an_explicit_declaration_for_the_properties_assigned_to_this_function.Code(),
	diagnostics.Declaration_emit_for_this_parameter_requires_implicitly_adding_undefined_to_its_type_This_is_not_supported_with_isolatedDeclarations.Code(),
	diagnostics.Type_containing_private_name_0_can_t_be_used_with_isolatedDeclarations.Code(),
	diagnostics.Add_satisfies_and_a_type_assertion_to_this_expression_satisfies_T_as_T_to_make_the_type_explicit.Code(),
}

const fixMissingTypeAnnotationOnExportsFixID = "fixMissingTypeAnnotationOnExports"

// IsolatedDeclarationsFixProvider is the CodeFixProvider for isolatedDeclarations-related type annotation fixes.
var IsolatedDeclarationsFixProvider = &CodeFixProvider{
	Name:              "fixMissingTypeAnnotationOnExports",
	ErrorCodes:        isolatedDeclarationsFixErrorCodes,
	GetCodeActions:    getIsolatedDeclarationsCodeActions,
	FixIds:            []string{fixMissingTypeAnnotationOnExportsFixID},
	GetAllCodeActions: getAllIsolatedDeclarationsCodeActions,
}

// canHaveTypeAnnotationKinds are the node kinds that can have type annotations added.
var canHaveTypeAnnotationKinds = map[ast.Kind]bool{
	ast.KindGetAccessor:          true,
	ast.KindMethodDeclaration:    true,
	ast.KindPropertyDeclaration:  true,
	ast.KindFunctionDeclaration:  true,
	ast.KindFunctionExpression:   true,
	ast.KindArrowFunction:        true,
	ast.KindVariableDeclaration:  true,
	ast.KindParameter:            true,
	ast.KindExportAssignment:     true,
	ast.KindClassDeclaration:     true,
	ast.KindObjectBindingPattern: true,
	ast.KindArrayBindingPattern:  true,
}

// declarationEmitNodeBuilderFlags are the node builder flags used for declaration emit.
var declarationEmitNodeBuilderFlags = nodebuilder.FlagsMultilineObjectLiterals |
	nodebuilder.FlagsWriteClassExpressionAsTypeLiteral |
	nodebuilder.FlagsUseTypeOfFunction |
	nodebuilder.FlagsUseStructuralFallback |
	nodebuilder.FlagsAllowEmptyTuple |
	nodebuilder.FlagsGenerateNamesForShadowedTypeParams |
	nodebuilder.FlagsNoTruncation

type typePrintMode int

const (
	typePrintModeFull     typePrintMode = iota
	typePrintModeRelative               // typeof X
	typePrintModeWidened                // widened literal type
)

func getIsolatedDeclarationsCodeActions(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
	ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
	defer done()
	view, err := request.LS.getPreparedAutoImportView(request.SourceFile)
	if err != nil {
		return nil, err
	}
	fixes := []*CodeAction{}
	add := func(mode typePrintMode, name string, fn func(*isolatedDeclarationsFixer) string) {
		action := tryCodeAction(ctx, request, ch, view, func(f *isolatedDeclarationsFixer) string { f.typePrintMode = mode; return fn(f) })
		if action != nil {
			action.ReferenceFixName = name
			fixes = append(fixes, action)
		}
	}
	for _, mode := range []typePrintMode{typePrintModeFull, typePrintModeRelative, typePrintModeWidened} {
		add(mode, "add-annotation", func(f *isolatedDeclarationsFixer) string { return f.addTypeAnnotation(request.Span) })
	}
	for _, mode := range []typePrintMode{typePrintModeFull, typePrintModeRelative, typePrintModeWidened} {
		add(mode, "add-type-assertion", func(f *isolatedDeclarationsFixer) string { return f.addInlineAssertion(request.Span) })
	}
	add(typePrintModeFull, "extract-expression", func(f *isolatedDeclarationsFixer) string { return f.extractAsVariable(request.Span) })
	return fixes, ctx.Err()
}
func getAllIsolatedDeclarationsCodeActions(ctx context.Context, request *CodeFixContext) (*CombinedCodeActions, error) {
	allDiagnostics := getAllDiagnostics(ctx, request.Program, request.SourceFile)
	ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
	defer done()
	imports, err := createImportAdder(ctx, request, ch)
	if err != nil {
		return nil, err
	}
	tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
	f := &isolatedDeclarationsFixer{ctx: ctx, ls: request.LS, sourceFile: request.SourceFile, program: request.Program, checker: ch, changeTracker: tracker, importAdder: imports, locale: locale.FromContext(ctx), fixedNodes: map[*ast.Node]bool{}, expandoPropertiesAdded: map[*ast.Node]bool{}, typePrintMode: typePrintModeFull}
	for _, diag := range allDiagnostics {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if containsErrorCode(isolatedDeclarationsFixErrorCodes, diag.Code()) {
			f.addTypeAnnotation(core.NewTextRange(diag.Pos(), diag.End()))
		}
	}
	if imports != nil {
		for _, edit := range imports.Edits() {
			tracker.ReplaceRangeWithText(request.SourceFile, edit.Range, edit.NewText)
		}
	}
	return &CombinedCodeActions{Description: diagnostics.Add_all_missing_type_annotations.Localize(locale.FromContext(ctx)), ReferenceChanges: referenceTrackerFileChanges(tracker)}, ctx.Err()
}
func tryCodeAction(ctx context.Context, request *CodeFixContext, ch *checker.Checker, view *autoimport.View, fn func(*isolatedDeclarationsFixer) string) *CodeAction {
	tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
	var imports autoimport.ImportAdder
	if view != nil {
		imports = autoimport.NewImportAdder(ctx, request.Program, ch, request.SourceFile, view, request.LS.FormatOptions(), request.LS.converters, request.LS.UserPreferences())
	}
	f := &isolatedDeclarationsFixer{ctx: ctx, ls: request.LS, sourceFile: request.SourceFile, program: request.Program, checker: ch, changeTracker: tracker, importAdder: imports, locale: locale.FromContext(ctx), fixedNodes: map[*ast.Node]bool{}, expandoPropertiesAdded: map[*ast.Node]bool{}}
	description := fn(f)
	if description == "" || ctx.Err() != nil {
		return nil
	}
	if imports != nil {
		for _, edit := range imports.Edits() {
			tracker.ReplaceRangeWithText(request.SourceFile, edit.Range, edit.NewText)
		}
	}
	changes := referenceTrackerFileChanges(tracker)
	if len(changes) == 0 {
		return nil
	}
	return &CodeAction{Description: description, ReferenceChanges: changes, FixID: fixMissingTypeAnnotationOnExportsFixID, FixAllDescription: diagnostics.Add_all_missing_type_annotations.Localize(locale.FromContext(ctx))}
}

type isolatedDeclarationsFixer struct {
	ctx                    context.Context
	ls                     *LanguageService
	expandoPropertiesAdded map[*ast.Node]bool
	sourceFile             *ast.SourceFile
	program                *compiler.Program
	checker                *checker.Checker
	changeTracker          *change.Tracker
	importAdder            autoimport.ImportAdder
	locale                 locale.Locale
	fixedNodes             map[*ast.Node]bool
	typePrintMode          typePrintMode
	mutatedTarget          bool // set by inferType/relativeType when the target was mutated (e.g., spread decomposition)
}

func (f *isolatedDeclarationsFixer) addTypeAnnotation(span core.TextRange) string {
	if f.ctx.Err() != nil {
		return ""
	}
	nodeWithDiag := astnav.GetTokenAtPosition(f.sourceFile, span.Pos())

	expandoFunction := findExpandoFunction(f.checker, nodeWithDiag)
	if expandoFunction != nil {
		if ast.IsFunctionDeclaration(expandoFunction) {
			return f.createNamespaceForExpandoProperties(expandoFunction)
		}
		return f.fixIsolatedDeclarationError(expandoFunction)
	}

	nodeMissingType := findAncestorWithMissingType(nodeWithDiag)
	if nodeMissingType != nil {
		return f.fixIsolatedDeclarationError(nodeMissingType)
	}
	return ""
}

func (f *isolatedDeclarationsFixer) createNamespaceForExpandoProperties(expandoFunc *ast.Node) string {
	if f.expandoPropertiesAdded[expandoFunc] {
		return ""
	}
	f.expandoPropertiesAdded[expandoFunc] = true
	funcDecl := expandoFunc.AsFunctionDeclaration()
	if funcDecl.Name() == nil {
		return ""
	}

	t := f.checker.GetTypeAtLocation(expandoFunc)
	elements := f.checker.GetPropertiesOfType(t)
	if len(elements) == 0 {
		return ""
	}

	factory := f.changeTracker.NodeFactory

	var newProperties []*ast.Node
	for _, symbol := range elements {
		if !scanner.IsIdentifierText(symbol.Name, core.LanguageVariantStandard) {
			continue
		}
		// skip symbols that already have a variable declaration
		if symbol.ValueDeclaration != nil && ast.IsVariableDeclaration(symbol.ValueDeclaration) {
			continue
		}

		symType := f.checker.GetTypeOfSymbol(symbol)
		typeNode := f.typeToMinimizedReferenceType(symType, expandoFunc, declarationEmitNodeBuilderFlags)
		if typeNode == nil {
			continue
		}

		varDecl := factory.NewVariableDeclaration(factory.NewIdentifier(symbol.Name), nil, typeNode, nil)
		exportToken := factory.NewToken(ast.KindExportKeyword)
		varDeclList := factory.NewVariableDeclarationList(factory.NewNodeList([]*ast.Node{varDecl}), ast.NodeFlagsNone)
		varStmt := factory.NewVariableStatement(factory.NewModifierList([]*ast.Node{exportToken}), varDeclList)
		newProperties = append(newProperties, varStmt)
	}

	if len(newProperties) == 0 {
		return ""
	}

	var modifiers []*ast.Node
	if ast.HasSyntacticModifier(expandoFunc, ast.ModifierFlagsExport) {
		modifiers = append(modifiers, factory.NewToken(ast.KindExportKeyword))
	}
	modifiers = append(modifiers, factory.NewToken(ast.KindDeclareKeyword))

	namespace := factory.NewModuleDeclaration(
		factory.NewModifierList(modifiers),
		ast.KindNamespaceKeyword,
		factory.NewIdentifier(funcDecl.Name().Text()),
		factory.NewModuleBlock(factory.NewNodeList(newProperties)),
	)
	// Set the flags for namespace
	namespace.Flags = ast.NodeFlagsAmbient | ast.NodeFlagsExportContext | ast.NodeFlagsContextFlags

	f.changeTracker.InsertNodeAfter(f.sourceFile, expandoFunc, namespace)
	return diagnostics.Annotate_types_of_properties_expando_function_in_a_namespace.Localize(f.locale)
}

// needsParenthesizedExpressionForAssertion checks if an expression needs parentheses for an assertion.
func needsParenthesizedExpressionForAssertion(node *ast.Node) bool {
	return !ast.IsEntityNameExpression(node) && !ast.IsCallExpression(node) && !ast.IsObjectLiteralExpression(node) && !ast.IsArrayLiteralExpression(node)
}

// createAsExpression creates an `expr as Type` expression, parenthesizing if needed.
func createAsExpression(factory *ast.NodeFactory, node *ast.Node, typeNode *ast.Node) *ast.Node {
	if needsParenthesizedExpressionForAssertion(node) {
		node = factory.NewParenthesizedExpression(node)
	}
	return factory.NewAsExpression(node, typeNode)
}

func (f *isolatedDeclarationsFixer) addInlineAssertion(span core.TextRange) string {
	if f.ctx.Err() != nil {
		return ""
	}
	nodeWithDiag := astnav.GetTokenAtPosition(f.sourceFile, span.Pos())

	// No inline assertions for expando members
	expandoFunction := findExpandoFunction(f.checker, nodeWithDiag)
	if expandoFunction != nil {
		return ""
	}

	targetNode := findBestFittingNode(nodeWithDiag, span)
	if targetNode == nil || isValueSignatureDeclaration(targetNode) || isValueSignatureDeclaration(targetNode.Parent) {
		return ""
	}

	isExpressionTarget := ast.IsExpression(targetNode)
	isShorthandPropertyAssignmentTarget := ast.IsShorthandPropertyAssignment(targetNode)

	// Go's IsDeclaration is broader than TS's isDeclaration (e.g. CallExpression has DeclarationData
	// in Go but is not a declaration kind in TS). Use isNamedDeclarationKind to match TS behavior.
	if !isShorthandPropertyAssignmentTarget && isNamedDeclarationKind(targetNode) {
		return ""
	}
	// No inline assertions on binding patterns
	if ast.FindAncestor(targetNode, ast.IsBindingPattern) != nil {
		return ""
	}
	// No inline assertions on enum members
	if ast.FindAncestor(targetNode, ast.IsEnumMember) != nil {
		return ""
	}
	// No support for typeof in extends clauses
	if isExpressionTarget && (ast.FindAncestorKind(targetNode, ast.KindHeritageClause) != nil || ast.FindAncestor(targetNode, ast.IsTypeNode) != nil) {
		return ""
	}
	// Can't inline type spread elements
	if ast.IsSpreadElement(targetNode) {
		return ""
	}

	variableDeclaration := ast.FindAncestorKind(targetNode, ast.KindVariableDeclaration)
	var variableType *checker.Type
	if variableDeclaration != nil {
		variableType = f.checker.GetTypeAtLocation(variableDeclaration)
	}
	// Can't use typeof on unique symbols
	if variableType != nil && variableType.Flags()&checker.TypeFlagsUniqueESSymbol != 0 {
		return ""
	}

	if !isExpressionTarget && !isShorthandPropertyAssignmentTarget {
		return ""
	}

	typeNode := f.inferType(targetNode, variableType)
	if typeNode == nil || f.mutatedTarget {
		return ""
	}

	factory := f.changeTracker.NodeFactory

	if isShorthandPropertyAssignmentTarget {
		// Insert `: expr as Type` after the shorthand property name
		clonedName := factory.DeepCloneNode(targetNode.AsShorthandPropertyAssignment().Name())
		asExpr := createAsExpression(factory, clonedName, typeNode)
		f.changeTracker.InsertNodeAt(f.sourceFile, core.TextPos(targetNode.End()), asExpr, change.NodeOptions{Prefix: ": "})
	} else if isExpressionTarget {
		// Replace expression with `(expression) satisfies Type as Type` or `expression satisfies Type as Type`
		clonedTarget := factory.DeepCloneNode(targetNode)
		if needsParenthesizedExpressionForAssertion(targetNode) {
			clonedTarget = factory.NewParenthesizedExpression(clonedTarget)
		}
		clonedType := factory.DeepCloneNode(typeNode)
		satisfiesAsExpr := factory.NewAsExpression(
			factory.NewSatisfiesExpression(clonedTarget, clonedType),
			typeNode,
		)
		f.changeTracker.ReplaceNode(f.sourceFile, targetNode, satisfiesAsExpr, nil)
	} else {
		return ""
	}

	return diagnostics.Add_satisfies_and_an_inline_type_assertion_with_0.Localize(f.locale, typeToStringForDiag(typeNode, f.sourceFile, f.changeTracker))
}

func (f *isolatedDeclarationsFixer) extractAsVariable(span core.TextRange) string {
	if f.ctx.Err() != nil {
		return ""
	}
	nodeWithDiag := astnav.GetTokenAtPosition(f.sourceFile, span.Pos())
	targetNode := findBestFittingNode(nodeWithDiag, span)
	if targetNode == nil || isValueSignatureDeclaration(targetNode) || isValueSignatureDeclaration(targetNode.Parent) {
		return ""
	}

	if !ast.IsExpression(targetNode) {
		return ""
	}

	factory := f.changeTracker.NodeFactory

	// Array literals should be marked as const
	if ast.IsArrayLiteralExpression(targetNode) {
		constRef := factory.NewTypeReferenceNode(factory.NewIdentifier("const"), nil)
		cloned := factory.DeepCloneNode(targetNode)
		f.changeTracker.ReplaceNode(f.sourceFile, targetNode, createAsExpression(factory, cloned, constRef), nil)
		return diagnostics.Mark_array_literal_as_const.Localize(f.locale)
	}

	parentPropertyAssignment := ast.FindAncestorKind(targetNode, ast.KindPropertyAssignment)
	if parentPropertyAssignment != nil {
		// Identifiers or entity names can already be typeof-ed
		if parentPropertyAssignment == targetNode.Parent && ast.IsEntityNameExpression(targetNode) {
			return ""
		}

		tempName := f.changeTracker.EmitContext.Factory.NewUniqueNameEx(f.referenceIdentifierForNode(targetNode), printer.AutoGenerateOptions{Flags: printer.GeneratedIdentifierFlagsOptimistic})

		replacementTarget := targetNode
		initializationNode := targetNode

		// Handle spread elements: walk up to the spread's parent and handle const assertions
		if ast.IsSpreadElement(replacementTarget) {
			replacementTarget = ast.WalkUpParenthesizedExpressions(replacementTarget.Parent)
			if isConstAssertion(replacementTarget.Parent) {
				replacementTarget = replacementTarget.Parent
				initializationNode = replacementTarget
			} else {
				constRef := factory.NewTypeReferenceNode(factory.NewIdentifier("const"), nil)
				initializationNode = createAsExpression(factory, factory.DeepCloneNode(replacementTarget), constRef)
			}
		}

		if ast.IsEntityNameExpression(replacementTarget) {
			return ""
		}

		clonedInit := factory.DeepCloneNode(initializationNode)
		varDecl := factory.NewVariableDeclaration(tempName.AsNode(), nil, nil, clonedInit)
		varDeclList := factory.NewVariableDeclarationList(factory.NewNodeList([]*ast.Node{varDecl}), ast.NodeFlagsConst)
		varStmt := factory.NewVariableStatement(nil, varDeclList)

		statement := ast.FindAncestor(targetNode, ast.IsStatement)
		if statement == nil {
			return ""
		}
		f.changeTracker.InsertNodeBefore(f.sourceFile, statement, varStmt, false, change.LeadingTriviaOptionNone)

		typeQuery := factory.NewTypeQueryNode(tempName.AsNode(), nil)
		asExpr := factory.NewAsExpression(tempName.AsNode(), typeQuery)
		f.changeTracker.ReplaceNode(f.sourceFile, replacementTarget, asExpr, nil)

		idText := typeToStringForDiag(tempName.AsNode(), f.sourceFile, f.changeTracker)
		return diagnostics.Extract_to_variable_and_replace_with_0_as_typeof_0.Localize(f.locale, idText)
	}

	return ""
}

// findExpandoFunction finds the function declaration that has expando properties assigned to it.
// isExpandoPropertyDeclarationForFix matches TS's isExpandoPropertyDeclaration which includes
// PropertyAccessExpression, ElementAccessExpression, and BinaryExpression. The shared
// ast.IsExpandoPropertyDeclaration was narrowed to BinaryExpression only for checker purposes.
func isExpandoPropertyDeclarationForFix(node *ast.Node) bool {
	return node != nil && (ast.IsPropertyAccessExpression(node) || ast.IsElementAccessExpression(node) || ast.IsBinaryExpression(node))
}

func findExpandoFunction(ch *checker.Checker, node *ast.Node) *ast.Node {
	expandoDeclaration := ast.FindAncestorOrQuit(node, func(n *ast.Node) ast.FindAncestorResult {
		if ast.IsStatement(n) {
			return ast.FindAncestorQuit
		}
		if isExpandoPropertyDeclarationForFix(n) {
			return ast.FindAncestorTrue
		}
		return ast.FindAncestorFalse
	})

	if expandoDeclaration == nil || !isExpandoPropertyDeclarationForFix(expandoDeclaration) {
		return nil
	}

	assignmentTarget := expandoDeclaration
	// Some late bound expando members use the whole expression as the declaration.
	if ast.IsBinaryExpression(assignmentTarget) {
		assignmentTarget = assignmentTarget.AsBinaryExpression().Left
		if !isExpandoPropertyDeclarationForFix(assignmentTarget) {
			return nil
		}
	}

	var expression *ast.Node
	if ast.IsPropertyAccessExpression(assignmentTarget) {
		expression = assignmentTarget.AsPropertyAccessExpression().Expression
	} else if ast.IsElementAccessExpression(assignmentTarget) {
		expression = assignmentTarget.AsElementAccessExpression().Expression
	} else {
		return nil
	}

	targetType := ch.GetTypeAtLocation(expression)
	if targetType == nil {
		return nil
	}

	properties := ch.GetPropertiesOfType(targetType)
	found := false
	for _, p := range properties {
		if p.ValueDeclaration == expandoDeclaration || p.ValueDeclaration == expandoDeclaration.Parent {
			found = true
			break
		}
	}
	if !found {
		return nil
	}

	symbol := targetType.Symbol()
	if symbol == nil || symbol.ValueDeclaration == nil {
		return nil
	}

	fn := symbol.ValueDeclaration
	if (ast.IsFunctionExpression(fn) || ast.IsArrowFunction(fn)) && ast.IsVariableDeclaration(fn.Parent) {
		return fn.Parent
	}
	if ast.IsFunctionDeclaration(fn) {
		return fn
	}

	return nil
}

func (f *isolatedDeclarationsFixer) fixIsolatedDeclarationError(node *ast.Node) string {
	// Avoid creating duplicate fixes for the same node
	if f.fixedNodes[node] {
		return ""
	}
	f.fixedNodes[node] = true

	switch node.Kind {
	case ast.KindParameter, ast.KindPropertyDeclaration, ast.KindVariableDeclaration:
		return f.addTypeToVariableLike(node)
	case ast.KindArrowFunction, ast.KindFunctionExpression, ast.KindFunctionDeclaration,
		ast.KindMethodDeclaration, ast.KindGetAccessor:
		return f.addTypeToSignatureDeclaration(node)
	case ast.KindExportAssignment:
		return f.transformExportAssignment(node)
	case ast.KindClassDeclaration:
		return f.transformExtendsClauseWithExpression(node)
	case ast.KindObjectBindingPattern, ast.KindArrayBindingPattern:
		return f.transformDestructuringPatterns(node)
	default:
		return ""
	}
}

func (f *isolatedDeclarationsFixer) addTypeToSignatureDeclaration(funcNode *ast.Node) string {
	if funcNode.Type() != nil {
		return ""
	}
	typeNode := f.inferType(funcNode, nil)
	if typeNode == nil {
		return ""
	}
	f.changeTracker.TryInsertTypeAnnotation(f.sourceFile, funcNode, typeNode)
	return diagnostics.Add_return_type_0.Localize(f.locale, typeToStringForDiag(typeNode, f.sourceFile, f.changeTracker))
}

func (f *isolatedDeclarationsFixer) transformExportAssignment(defaultExport *ast.Node) string {
	exportAssignment := defaultExport.AsExportAssignment()
	if exportAssignment.IsExportEquals {
		return ""
	}

	expression := exportAssignment.Expression
	typeNode := f.inferType(expression, nil)
	if typeNode == nil {
		return ""
	}

	factory := f.changeTracker.NodeFactory

	defaultIdentifier := f.changeTracker.EmitContext.Factory.NewUniqueName("_default")

	// Deep clone the expression so synthesized nodes don't reference original source positions
	clonedExpression := factory.DeepCloneNode(expression)

	varDecl := factory.NewVariableDeclaration(defaultIdentifier.AsNode(), nil, typeNode, clonedExpression)
	varDeclList := factory.NewVariableDeclarationList(factory.NewNodeList([]*ast.Node{varDecl}), ast.NodeFlagsConst)
	varStmt := factory.NewVariableStatement(nil, varDeclList)

	newExport := factory.UpdateExportAssignment(defaultExport.AsExportAssignment(), defaultExport.Modifiers(), false, nil, defaultIdentifier.AsNode())

	f.changeTracker.ReplaceNodeWithNodes(f.sourceFile, defaultExport, []*ast.Node{varStmt, newExport}, nil)
	return diagnostics.Extract_default_export_to_variable.Localize(f.locale)
}

func (f *isolatedDeclarationsFixer) transformExtendsClauseWithExpression(classDecl *ast.Node) string {
	cd := classDecl.AsClassDeclaration()
	var extendsClause *ast.Node
	if cd.HeritageClauses != nil {
		for _, clause := range cd.HeritageClauses.Nodes {
			if clause.AsHeritageClause().Token == ast.KindExtendsKeyword {
				extendsClause = clause
				break
			}
		}
	}
	if extendsClause == nil {
		return ""
	}

	heritageTypes := extendsClause.AsHeritageClause().Types
	if heritageTypes == nil || len(heritageTypes.Nodes) == 0 {
		return ""
	}
	heritageExpression := heritageTypes.Nodes[0]
	expression := heritageExpression.AsExpressionWithTypeArguments().Expression

	heritageTypeNode := f.inferType(expression, nil)
	if heritageTypeNode == nil {
		return ""
	}

	factory := f.changeTracker.NodeFactory

	baseName := "Anonymous"
	if cd.Name() != nil {
		baseName = cd.Name().Text() + "Base"
	}
	baseClassName := f.changeTracker.EmitContext.Factory.NewUniqueNameEx(baseName, printer.AutoGenerateOptions{Flags: printer.GeneratedIdentifierFlagsOptimistic})

	// Create: const <BaseName>: <type> = <expression>;
	clonedExpression := factory.DeepCloneNode(expression)
	varDecl := factory.NewVariableDeclaration(baseClassName.AsNode(), nil, heritageTypeNode, clonedExpression)
	varDeclList := factory.NewVariableDeclarationList(factory.NewNodeList([]*ast.Node{varDecl}), ast.NodeFlagsConst)
	varStmt := factory.NewVariableStatement(nil, varDeclList)

	f.changeTracker.InsertNodeBefore(f.sourceFile, classDecl, varStmt, false, change.LeadingTriviaOptionNone)

	// Replace the heritage expression with the base class name
	end := heritageExpression.End()
	for comment := range scanner.GetTrailingCommentRanges(factory, f.sourceFile.Text(), end) {
		end = comment.End()
	}
	span := lsproto.Range{Start: f.ls.converters.PositionToLineAndCharacter(f.sourceFile, core.TextPos(heritageExpression.Pos())), End: f.ls.converters.PositionToLineAndCharacter(f.sourceFile, core.TextPos(end))}
	f.changeTracker.ReplaceRange(f.sourceFile, span, baseClassName, change.NodeOptions{Prefix: " "})

	return diagnostics.Extract_base_class_to_variable.Localize(f.locale)
}

type referenceIsolatedBinding struct {
	element, parentNode  *ast.Node
	parent               *referenceIsolatedBinding
	text                 string
	computed, identifier *ast.Node
	index                int
	kind                 int
}

func (f *isolatedDeclarationsFixer) transformDestructuringPatterns(pattern *ast.Node) string {
	declaration := pattern.Parent
	statement := declaration.Parent.Parent
	initializer := declaration.Initializer()
	if initializer == nil {
		return ""
	}
	factory := f.changeTracker.NodeFactory
	nodes := []*ast.Node{}
	variable := func(modifiers *ast.ModifierList, name, typ, value *ast.Node) *ast.Node {
		return factory.NewVariableStatement(modifiers, factory.NewVariableDeclarationList(factory.NewNodeList([]*ast.Node{factory.NewVariableDeclaration(name, nil, typ, value)}), ast.NodeFlagsConst))
	}
	base := initializer
	if !ast.IsIdentifier(initializer) {
		base = f.changeTracker.Factory.NewUniqueNameEx("dest", printer.AutoGenerateOptions{Flags: printer.GeneratedIdentifierFlagsOptimistic})
		nodes = append(nodes, variable(nil, base, nil, initializer))
	}
	root := &referenceIsolatedBinding{kind: 3, identifier: base}
	queue := []*referenceIsolatedBinding{}
	var appendPattern func(*ast.Node, *referenceIsolatedBinding)
	appendPattern = func(pattern *ast.Node, parent *referenceIsolatedBinding) {
		for index, element := range pattern.AsBindingPattern().Elements.Nodes {
			if ast.IsOmittedExpression(element) {
				continue
			}
			binding := element.AsBindingElement()
			item := &referenceIsolatedBinding{element: element, parent: parent}
			if ast.IsArrayBindingPattern(pattern) {
				item.kind = 2
				item.index = index
			} else if property := binding.PropertyName; property != nil {
				if ast.IsComputedPropertyName(property) {
					item.kind = 1
					item.computed = property.Expression()
				} else {
					item.text = property.Text()
				}
			} else {
				item.text = binding.Name().Text()
			}
			queue = append(queue, item)
		}
	}
	appendPattern(pattern, root)
	computed := map[*ast.Node]*ast.Node{}
	for current := 0; current < len(queue); current++ {
		item := queue[current]
		element := item.element.AsBindingElement()
		if property := element.PropertyName; property != nil && ast.IsComputedPropertyName(property) {
			expression := property.Expression()
			id := f.changeTracker.Factory.NewGeneratedNameForNode(expression)
			nodes = append(nodes, variable(nil, id, nil, expression))
			computed[expression] = id
		}
		name := element.Name()
		if ast.IsBindingPattern(name) {
			appendPattern(name, item)
			continue
		}
		typ := f.inferType(name, nil)
		chain := []*referenceIsolatedBinding{item}
		for cursor := item; cursor.parent != nil; {
			cursor = cursor.parent
			chain = append(chain, cursor)
		}
		value := chain[len(chain)-1].identifier
		for index := len(chain) - 2; index >= 0; index-- {
			next := chain[index]
			switch next.kind {
			case 0:
				tokens := scanner.NewScanner()
				tokens.SetText("." + next.text)
				if tokens.Scan() != ast.KindDotToken {
					panic(&change.InvalidEdits{Reason: "isolated destructuring property cannot be formatted"})
				}
				tokens.Scan()
				if tokens.TokenStart() != 1 || tokens.TokenEnd() != len(next.text)+1 {
					panic(&change.InvalidEdits{Reason: "isolated destructuring property cannot be formatted"})
				}
				value = factory.NewPropertyAccessExpression(value, nil, factory.NewIdentifier(next.text), ast.NodeFlagsOptionalChain)
			case 1:
				value = factory.NewElementAccessExpression(value, nil, computed[next.computed], ast.NodeFlagsNone)
			case 2:
				value = factory.NewElementAccessExpression(value, nil, factory.NewNumericLiteral(strconv.Itoa(next.index), ast.TokenFlagsNone), ast.NodeFlagsNone)
			}
		}
		if element.Initializer != nil {
			baseName := "temp"
			if element.PropertyName != nil && ast.IsIdentifier(element.PropertyName) {
				baseName = element.PropertyName.Text()
			}
			temporary := f.changeTracker.Factory.NewUniqueNameEx(baseName, printer.AutoGenerateOptions{Flags: printer.GeneratedIdentifierFlagsOptimistic})
			nodes = append(nodes, variable(nil, temporary, nil, value))
			value = factory.NewConditionalExpression(factory.NewBinaryExpression(nil, temporary, nil, factory.NewToken(ast.KindEqualsEqualsEqualsToken), factory.NewIdentifier("undefined")), factory.NewToken(ast.KindQuestionToken), element.Initializer, factory.NewToken(ast.KindColonToken), value)
		}
		nodes = append(nodes, variable(f.getExportModifier(statement), name, typ, value))
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList()
	if len(declarations.Declarations.Nodes) > 1 {
		remaining := []*ast.Node{}
		for _, other := range declarations.Declarations.Nodes {
			if other != declaration {
				remaining = append(remaining, other)
			}
		}
		nodes = append(nodes, factory.UpdateVariableStatement(statement.AsVariableStatement(), statement.Modifiers(), factory.UpdateVariableDeclarationList(declarations, factory.NewNodeList(remaining), declarations.Flags)))
	}
	f.changeTracker.ReplaceNodeWithNodes(f.sourceFile, statement, nodes, nil)
	return diagnostics.Extract_binding_expressions_to_variable.Localize(f.locale)
}

func (f *isolatedDeclarationsFixer) getExportModifier(enclosingVarStmt *ast.Node) *ast.ModifierList {
	if ast.HasSyntacticModifier(enclosingVarStmt, ast.ModifierFlagsExport) {
		exportToken := f.changeTracker.NodeFactory.NewToken(ast.KindExportKeyword)
		return f.changeTracker.NodeFactory.NewModifierList([]*ast.Node{exportToken})
	}
	return nil
}

func (f *isolatedDeclarationsFixer) inferType(node *ast.Node, variableType *checker.Type) *ast.Node {
	f.mutatedTarget = false
	if f.typePrintMode == typePrintModeRelative {
		return f.relativeType(node)
	}
	scope := ast.FindAncestor(node, isNamedDeclarationKind)
	if scope == nil {
		scope = f.sourceFile.AsNode()
	}
	var typ *checker.Type
	if isValueSignatureDeclaration(node) {
		if signature := f.checker.GetSignatureFromDeclaration(node); signature != nil {
			if predicate := f.checker.GetTypePredicateOfSignature(signature); predicate != nil {
				if predicate.Type() == nil {
					return nil
				}
				ids := map[*ast.Node]*ast.Symbol{}
				result, truncated := f.checker.ReferenceIsolatedTypeNode(nil, predicate, scope, declarationEmitNodeBuilderFlags|f.getExtraFlags(node, predicate.Type()), ids, f.changeTracker.EmitContext)
				if result == nil {
					return nil
				}
				result = f.referenceAutoImportType(result, ids, true)
				if truncated {
					return f.changeTracker.NewKeywordTypeNode(ast.KindAnyKeyword)
				}
				return result
			}
			typ = f.checker.GetReturnTypeOfSignature(signature)
		}
	} else {
		typ = f.checker.GetTypeAtLocation(node)
	}
	if typ == nil {
		return nil
	}
	if f.typePrintMode == typePrintModeWidened {
		if variableType != nil {
			typ = variableType
		}
		widened := f.checker.GetWidenedLiteralType(typ)
		if f.checker.IsTypeAssignableTo(widened, typ) {
			return nil
		}
		typ = widened
	}
	flags := declarationEmitNodeBuilderFlags | f.getExtraFlags(node, typ)
	if ast.IsParameterDeclaration(node) && f.checker.RequiresAddingImplicitUndefined(node) {
		typ = f.checker.GetUnionTypeEx([]*checker.Type{f.checker.GetUndefinedType(), typ}, checker.UnionReductionNone)
	}
	return f.typeToMinimizedReferenceType(typ, scope, flags)
}
func (f *isolatedDeclarationsFixer) getExtraFlags(node *ast.Node, t *checker.Type) nodebuilder.Flags {
	if (ast.IsVariableDeclaration(node) ||
		(ast.IsPropertyDeclaration(node) && ast.HasSyntacticModifier(node, ast.ModifierFlagsStatic|ast.ModifierFlagsReadonly))) &&
		t.Flags()&checker.TypeFlagsUniqueESSymbol != 0 {
		return nodebuilder.FlagsAllowUniqueESSymbolType
	}
	return nodebuilder.FlagsNone
}

// createTypeOfFromEntityNameExpression creates a `typeof X` type query node.
func (f *isolatedDeclarationsFixer) createTypeOfFromEntityNameExpression(node *ast.Node) *ast.TypeNode {
	return f.changeTracker.NodeFactory.NewTypeQueryNode(
		f.changeTracker.NodeFactory.DeepCloneNode(node), nil,
	)
}

// typeFromArraySpreadElements decomposes an array literal with spread elements into
// separate variables, returning a tuple type of typeof references.
func (f *isolatedDeclarationsFixer) typeFromArraySpreadElements(node *ast.ArrayLiteralExpression, name string) *ast.TypeNode {
	isInConstContext := ast.FindAncestor(node.AsNode(), isConstAssertion) != nil
	if !isInConstContext {
		return nil
	}
	if name == "" {
		name = "temp"
	}
	factory := f.changeTracker.NodeFactory
	return f.typeFromSpreads(
		node.AsNode(),
		name,
		isInConstContext,
		func(n *ast.Node) []*ast.Node {
			return n.AsArrayLiteralExpression().Elements.Nodes
		},
		ast.IsSpreadElement,
		func(expr *ast.Node) *ast.Node {
			return factory.NewSpreadElement(expr)
		},
		func(elements []*ast.Node) *ast.Node {
			return factory.NewArrayLiteralExpression(factory.NewNodeList(elements), true)
		},
		func(types []*ast.TypeNode) *ast.TypeNode {
			restTypes := make([]*ast.TypeNode, len(types))
			for i, t := range types {
				restTypes[i] = factory.NewRestTypeNode(t)
			}
			return factory.NewTupleTypeNode(factory.NewNodeList(restTypes))
		},
	)
}

// typeFromObjectSpreadAssignment decomposes an object literal with spread assignments into
// separate variables, returning an intersection type of typeof references.
func (f *isolatedDeclarationsFixer) typeFromObjectSpreadAssignment(node *ast.ObjectLiteralExpression, name string) *ast.TypeNode {
	isInConstContext := ast.FindAncestor(node.AsNode(), isConstAssertion) != nil
	if name == "" {
		name = "temp"
	}
	factory := f.changeTracker.NodeFactory
	return f.typeFromSpreads(
		node.AsNode(),
		name,
		isInConstContext,
		func(n *ast.Node) []*ast.Node {
			if n.AsObjectLiteralExpression().Properties != nil {
				return n.AsObjectLiteralExpression().Properties.Nodes
			}
			return nil
		},
		ast.IsSpreadAssignment,
		func(expr *ast.Node) *ast.Node {
			return factory.NewSpreadAssignment(expr)
		},
		func(elements []*ast.Node) *ast.Node {
			return factory.NewObjectLiteralExpression(factory.NewNodeList(elements), true)
		},
		func(types []*ast.TypeNode) *ast.TypeNode {
			return factory.NewIntersectionTypeNode(factory.NewNodeList(types))
		},
	)
}

// typeFromSpreads is the generic spread decomposition function, ported from TS's typeFromSpreads.
// It splits a literal with spread elements into separate const variables and returns a composed type.
func (f *isolatedDeclarationsFixer) typeFromSpreads(
	node *ast.Node,
	name string,
	isInConstContext bool,
	getChildren func(*ast.Node) []*ast.Node,
	isSpread func(*ast.Node) bool,
	createSpread func(*ast.Node) *ast.Node,
	makeNodeOfKind func([]*ast.Node) *ast.Node,
	finalType func([]*ast.TypeNode) *ast.TypeNode,
) *ast.TypeNode {
	factory := f.changeTracker.NodeFactory
	var intersectionTypes []*ast.TypeNode
	var newSpreads []*ast.Node
	var currentVariableProperties []*ast.Node

	statement := ast.FindAncestor(node, ast.IsStatement)

	children := getChildren(node)
	for _, prop := range children {
		if isSpread(prop) {
			f.finalizesVariablePart(factory, name, isInConstContext, statement, makeNodeOfKind, createSpread, &currentVariableProperties, &intersectionTypes, &newSpreads)
			if ast.IsEntityNameExpression(prop.Expression()) {
				intersectionTypes = append(intersectionTypes, f.createTypeOfFromEntityNameExpression(prop.Expression()))
				newSpreads = append(newSpreads, prop)
			} else {
				f.makeSpreadVariable(factory, name, isInConstContext, statement, createSpread, prop.Expression(), &intersectionTypes, &newSpreads)
			}
		} else {
			currentVariableProperties = append(currentVariableProperties, prop)
		}
	}

	if len(newSpreads) == 0 {
		return nil
	}

	f.finalizesVariablePart(factory, name, isInConstContext, statement, makeNodeOfKind, createSpread, &currentVariableProperties, &intersectionTypes, &newSpreads)

	f.changeTracker.ReplaceNode(f.sourceFile, node, makeNodeOfKind(newSpreads), nil)
	f.mutatedTarget = true

	return finalType(intersectionTypes)
}

// makeSpreadVariable creates a const variable for a spread expression and adds it to the decomposition.
func (f *isolatedDeclarationsFixer) makeSpreadVariable(
	factory *ast.NodeFactory,
	name string,
	isInConstContext bool,
	statement *ast.Node,
	createSpread func(*ast.Node) *ast.Node,
	expression *ast.Node,
	intersectionTypes *[]*ast.TypeNode,
	newSpreads *[]*ast.Node,
) {
	tempName := f.changeTracker.EmitContext.Factory.NewUniqueNameEx(
		name+"_Part"+strconv.Itoa(len(*newSpreads)+1),
		printer.AutoGenerateOptions{Flags: printer.GeneratedIdentifierFlagsOptimistic},
	).AsNode()

	var initializer *ast.Node
	if !isInConstContext {
		initializer = factory.DeepCloneNode(expression)
	} else {
		constRef := factory.NewTypeReferenceNode(factory.NewIdentifier("const"), nil)
		initializer = factory.NewAsExpression(factory.DeepCloneNode(expression), constRef)
	}

	varDecl := factory.NewVariableDeclaration(tempName, nil, nil, initializer)
	varDeclList := factory.NewVariableDeclarationList(factory.NewNodeList([]*ast.Node{varDecl}), ast.NodeFlagsConst)
	varStmt := factory.NewVariableStatement(nil, varDeclList)

	if statement != nil {
		f.changeTracker.InsertNodeBefore(f.sourceFile, statement, varStmt, false, change.LeadingTriviaOptionNone)
	}

	*intersectionTypes = append(*intersectionTypes, f.createTypeOfFromEntityNameExpression(tempName))
	*newSpreads = append(*newSpreads, createSpread(tempName))
}

// finalizesVariablePart finalizes accumulated non-spread properties into a variable.
func (f *isolatedDeclarationsFixer) finalizesVariablePart(
	factory *ast.NodeFactory,
	name string,
	isInConstContext bool,
	statement *ast.Node,
	makeNodeOfKind func([]*ast.Node) *ast.Node,
	createSpread func(*ast.Node) *ast.Node,
	currentVariableProperties *[]*ast.Node,
	intersectionTypes *[]*ast.TypeNode,
	newSpreads *[]*ast.Node,
) {
	if len(*currentVariableProperties) > 0 {
		f.makeSpreadVariable(factory, name, isInConstContext, statement, createSpread, makeNodeOfKind(*currentVariableProperties), intersectionTypes, newSpreads)
		*currentVariableProperties = nil
	}
}

// isConstAssertion checks if a node is an `as const` or `<const>` assertion.
func isConstAssertion(node *ast.Node) bool {
	if ast.IsAssertionExpression(node) {
		typeNode := node.Type()
		return ast.IsConstTypeReference(typeNode)
	}
	return false
}

// relativeType creates a typeof expression for a node, used in TypePrintMode.Relative.
// Instead of spelling out the full type, returns `typeof X` for identifiers.
// For object/array literals with spreads, decomposes into separate variables.
func (f *isolatedDeclarationsFixer) relativeType(node *ast.Node) *ast.TypeNode {
	if ast.IsParameterDeclaration(node) {
		return nil
	}
	if ast.IsShorthandPropertyAssignment(node) {
		return f.createTypeOfFromEntityNameExpression(node.AsShorthandPropertyAssignment().Name())
	}
	if ast.IsEntityNameExpression(node) {
		return f.createTypeOfFromEntityNameExpression(node)
	}
	if isConstAssertion(node) {
		return f.relativeType(node.Expression())
	}
	if ast.IsArrayLiteralExpression(node) {
		varDecl := ast.FindAncestorKind(node, ast.KindVariableDeclaration)
		partName := ""
		if varDecl != nil && ast.IsIdentifier(varDecl.Name()) {
			partName = varDecl.Name().AsIdentifier().Text
		}
		return f.typeFromArraySpreadElements(node.AsArrayLiteralExpression(), partName)
	}
	if ast.IsObjectLiteralExpression(node) {
		varDecl := ast.FindAncestorKind(node, ast.KindVariableDeclaration)
		partName := ""
		if varDecl != nil && ast.IsIdentifier(varDecl.Name()) {
			partName = varDecl.Name().AsIdentifier().Text
		}
		return f.typeFromObjectSpreadAssignment(node.AsObjectLiteralExpression(), partName)
	}
	if ast.IsVariableDeclaration(node) && node.Initializer() != nil {
		return f.relativeType(node.Initializer())
	}
	if ast.IsConditionalExpression(node) {
		cond := node.AsConditionalExpression()
		trueType := f.relativeType(cond.WhenTrue)
		if trueType == nil {
			return nil
		}
		trueMutated := f.mutatedTarget
		falseType := f.relativeType(cond.WhenFalse)
		if falseType == nil {
			return nil
		}
		f.mutatedTarget = trueMutated || f.mutatedTarget
		factory := f.changeTracker.NodeFactory
		return factory.NewUnionTypeNode(factory.NewNodeList([]*ast.Node{trueType, falseType}))
	}
	return nil
}

// typeToMinimizedReferenceType converts a type to a type node, then trims trailing
// type arguments that match their defaults. Ported from TS's
// services/codefixes/helpers.ts typeToMinimizedReferenceType.
func (f *isolatedDeclarationsFixer) typeToMinimizedReferenceType(typ *checker.Type, scope *ast.Node, flags nodebuilder.Flags) *ast.Node {
	typ = f.referenceSpreadTypeOrder(typ, scope, map[*checker.Type]bool{})
	ids := map[*ast.Node]*ast.Symbol{}
	node, truncated := f.checker.ReferenceIsolatedTypeNode(typ, nil, scope, flags, ids, f.changeTracker.EmitContext)
	if node == nil {
		return nil
	}
	if ast.IsTypeReferenceNode(node) && typ.ObjectFlags()&checker.ObjectFlagsReference != 0 {
		args := node.TypeArguments()
		if len(f.checker.GetTypeArguments(typ)) > 0 && len(args) > 0 {
			cutoff := endOfRequiredTypeParameters(f.checker, typ)
			if cutoff < len(args) {
				node = f.changeTracker.UpdateTypeReferenceNode(node.AsTypeReferenceNode(), node.AsTypeReferenceNode().TypeName, f.changeTracker.NewNodeList(args[:cutoff]))
			}
		}
	}
	node = f.referenceAutoImportType(node, ids, false)
	if truncated {
		return f.changeTracker.NewKeywordTypeNode(ast.KindAnyKeyword)
	}
	return node
}
func (f *isolatedDeclarationsFixer) referenceAutoImportType(node *ast.Node, ids map[*ast.Node]*ast.Symbol, predicate bool) *ast.Node {
	importType := func(node *ast.Node) *ast.Node {
		replacement, symbols := autoimport.TryGetAutoImportableReferenceFromTypeNode(node, ids)
		if replacement == nil {
			return node
		}
		for _, symbol := range symbols {
			if f.importAdder != nil {
				f.importAdder.AddImportFromExportedSymbol(symbol, true)
			}
		}
		return replacement
	}
	if predicate {
		if data := node.AsTypePredicateNode(); data.Type != nil && ast.IsImportTypeNode(data.Type) {
			node = f.changeTracker.UpdateTypePredicateNode(data, data.AssertsModifier, data.ParameterName, importType(data.Type))
		}
	} else {
		node = importType(node)
	}
	return f.changeTracker.DeepCloneNode(node)
}
func endOfRequiredTypeParameters(ch *checker.Checker, t *checker.Type) int {
	args := ch.GetTypeArguments(t)
	if len(args) == 0 {
		return 0
	}
	target := t.Target()
	if target == nil || target.AsInterfaceType() == nil {
		return len(args)
	}
	local, parameters := target.AsInterfaceType().LocalTypeParameters(), target.AsInterfaceType().TypeParameters()
	for cutoff := range args {
		if local == nil {
			continue
		}
		if cutoff >= len(local) {
			panic(&change.InvalidEdits{Reason: "isolated declaration generic parameter index is outside its target"})
		}
		if !ch.HasReferenceIsolatedConstraintCache(local[cutoff]) {
			continue
		}
		filled := ch.FillMissingTypeArguments(args[:cutoff], parameters, cutoff, false)
		all := true
		for index, typ := range filled {
			if index >= len(args) || typ != args[index] {
				all = false
				break
			}
		}
		if all {
			return cutoff
		}
	}
	return len(args)
}
func (f *isolatedDeclarationsFixer) addTypeToVariableLike(decl *ast.Node) string {
	typeNode := f.inferType(decl, nil)
	if typeNode == nil {
		return ""
	}
	if decl.Type() != nil {
		f.changeTracker.ReplaceNode(f.sourceFile, decl.Type(), typeNode, nil)
	} else {
		f.changeTracker.TryInsertTypeAnnotation(f.sourceFile, decl, typeNode)
		// Parenthesize paren-less arrow function parameters (`x => ...`) so the inserted `: T`
		// produces `(x: T) => ...` instead of the invalid `x: T => ...`. Queued after the type
		// annotation so that the `)` edit at param.End() sorts after the annotation insertion.
		if ast.IsParameterDeclaration(decl) && decl.Parent != nil && ast.IsArrowFunction(decl.Parent) {
			f.changeTracker.ParenthesizeArrowParameters(f.sourceFile, decl.Parent)
		}
	}
	return diagnostics.Add_annotation_of_type_0.Localize(f.locale, typeToStringForDiag(typeNode, f.sourceFile, f.changeTracker))
}

// typeToStringForDiag converts a type node to a string for use in diagnostic descriptions.
// It reuses the change tracker's EmitContext so that generated identifier names are resolved
// consistently with the actual code edits, and passes the source file so that the printer's
// name generator can check for conflicts with existing file-level identifiers.
func typeToStringForDiag(typeNode *ast.Node, sourceFile *ast.SourceFile, ct *change.Tracker) string {
	ct.EmitContext.SetEmitFlags(typeNode, printer.EFSingleLine)
	p := printer.NewPrinter(
		printer.PrinterOptions{
			NewLine: core.NewLineKindLF,
		},
		printer.PrintHandlers{},
		ct.EmitContext,
	)
	writer, release := printer.GetSingleLineStringWriter()
	defer release()
	p.Write(typeNode, sourceFile, writer, nil)
	result := writer.String()
	units := utf16.Encode([]rune(result))
	if len(units) > 160 {
		return string(utf16.Decode(units[:157])) + "..."
	}
	ct.EmitContext.SetEmitFlags(typeNode, printer.EFNone)
	return result
}

// findAncestorWithMissingType walks up the ancestor chain to find a node that
// can have a type annotation and is missing one.
func findAncestorWithMissingType(node *ast.Node) *ast.Node {
	return ast.FindAncestor(node, func(n *ast.Node) bool {
		if !canHaveTypeAnnotationKinds[n.Kind] {
			return false
		}
		if ast.IsObjectBindingPattern(n) || ast.IsArrayBindingPattern(n) {
			return ast.IsVariableDeclaration(n.Parent)
		}
		return true
	})
}

// findBestFittingNode walks up from the token to find the node that best fits the diagnostic span.
func findBestFittingNode(node *ast.Node, span core.TextRange) *ast.Node {
	if node == nil {
		return nil
	}
	for node != nil && node.End() < span.Pos()+span.Len() {
		node = node.Parent
	}
	for node.Parent != nil && node.Parent.Pos() == node.Pos() && node.Parent.End() == node.End() {
		node = node.Parent
	}
	if ast.IsIdentifier(node) && ast.HasInitializer(node.Parent) && node.Parent.Initializer() != nil {
		return node.Parent.Initializer()
	}
	if ast.IsIdentifier(node) && ast.IsShorthandPropertyAssignment(node.Parent) {
		return node.Parent
	}
	return node
}

// isNamedDeclarationKind matches TS's isDeclarationKind, which is narrower than Go's IsDeclaration.
// Go's IsDeclaration returns true for any node with DeclarationData (including CallExpression),
// while TS's isDeclaration only returns true for specific named declaration kinds.
func isNamedDeclarationKind(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindArrowFunction, ast.KindBindingElement, ast.KindClassDeclaration, ast.KindClassExpression,
		ast.KindClassStaticBlockDeclaration, ast.KindConstructor, ast.KindEnumDeclaration, ast.KindEnumMember,
		ast.KindExportSpecifier, ast.KindFunctionDeclaration, ast.KindFunctionExpression,
		ast.KindGetAccessor, ast.KindImportClause, ast.KindImportEqualsDeclaration,
		ast.KindImportSpecifier, ast.KindInterfaceDeclaration, ast.KindJsxAttribute,
		ast.KindMethodDeclaration, ast.KindMethodSignature, ast.KindModuleDeclaration,
		ast.KindNamespaceExportDeclaration, ast.KindNamespaceImport, ast.KindNamespaceExport,
		ast.KindParameter, ast.KindPropertyAssignment, ast.KindPropertyDeclaration,
		ast.KindPropertySignature, ast.KindSetAccessor, ast.KindShorthandPropertyAssignment,
		ast.KindTypeAliasDeclaration, ast.KindTypeParameter, ast.KindVariableDeclaration,
		ast.KindJSDocTypedefTag, ast.KindJSDocCallbackTag, ast.KindJSDocPropertyTag,
		ast.KindNamedTupleMember:
		return true
	}
	return false
}

// isValueSignatureDeclaration checks if a node is a function-like declaration that produces a value.
func isValueSignatureDeclaration(node *ast.Node) bool {
	return ast.IsFunctionExpression(node) || ast.IsArrowFunction(node) || ast.IsMethodDeclaration(node) ||
		ast.IsAccessor(node) || ast.IsFunctionDeclaration(node) || ast.IsConstructorDeclaration(node)
}

// getIdentifierNameForNode derives a meaningful variable name from a node expression.
// For property access expressions like `obj.foo`, returns "foo". Otherwise returns "newLocal".
// Ported from TS's getIdentifierForNode in services/refactors/helpers.ts.
func (f *isolatedDeclarationsFixer) referenceIdentifierForNode(node *ast.Node) string {
	if ast.IsPropertyAccessExpression(node) && !ast.IsPrivateIdentifier(node.Name()) && scanner.IdentifierToKeywordKind(node.Name().AsIdentifier()) == ast.KindUnknown && f.checker.ResolveName(node.Name().Text(), node, ast.SymbolFlagsValue, false) == nil {
		return node.Name().Text()
	}
	name := "newLocal"
	for index := 1; ; index++ {
		if _, exists := f.sourceFile.Identifiers[name]; !exists {
			return name
		}
		name = "newLocal_" + strconv.Itoa(index)
	}
}

// Return inference in TS 6 reports the signature boundary when local syntax is
// insufficient. The declaration observer supplies the actual emitted surface,
// including late-visible and nested signatures, without guessing export scope.
type referenceIsolatedReturnPlan struct {
	node, candidate, suppress, preservedType, array *ast.Node
	failed, expressionFallback                      bool
}

func referenceIsolatedContains(outer, inner *ast.Node) bool {
	return outer != nil && inner != nil && outer.Pos() <= inner.Pos() && inner.End() <= outer.End()
}
func referenceIsolatedContextual(node *ast.Node) bool {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if ast.IsCallExpression(parent) || (!ast.IsFunctionLikeDeclaration(parent) && parent.Type() != nil) || ast.IsJsxElement(parent) || ast.IsJsxExpression(parent) {
			return true
		}
	}
	return false
}
func referenceIsolatedReturnPlanFor(fn *ast.Node, ch *checker.Checker) *referenceIsolatedReturnPlan {
	plan := &referenceIsolatedReturnPlan{node: fn}
	if ast.GetFunctionFlags(fn)&ast.FunctionFlagsAsyncGenerator != 0 {
		plan.failed = true
		return plan
	}
	body := fn.Body()
	var expression *ast.Node
	if body != nil {
		if ast.IsBlock(body) {
			ast.ForEachReturnStatement(body, func(statement *ast.Node) bool {
				if statement.Parent != body {
					expression = nil
					return true
				}
				if expression == nil {
					expression = statement.Expression()
				} else {
					expression = nil
					return true
				}
				return false
			})
		} else {
			expression = body
		}
	}
	if expression == nil {
		plan.failed = true
		return plan
	}
	plan.candidate = expression
	if referenceIsolatedContextual(expression) {
		if (ast.IsAsExpression(expression) || (expression.Kind == ast.KindTypeAssertionExpression)) && !isConstAssertion(expression) {
			plan.suppress = expression.Expression()
			plan.preservedType = expression.Type()
			return plan
		}
		plan.failed = true
		return plan
	}
	constant := false
	for {
		switch expression.Kind {
		case ast.KindParenthesizedExpression:
			expression = expression.Expression()
			continue
		case ast.KindAsExpression, ast.KindTypeAssertionExpression:
			if !isConstAssertion(expression) {
				plan.suppress = expression.Expression()
				plan.preservedType = expression.Type()
				return plan
			}
			constant = true
			expression = expression.Expression()
			continue
		case ast.KindArrayLiteralExpression:
			if !constant {
				plan.array = expression
				plan.expressionFallback = true
			} else {
				for _, element := range expression.AsArrayLiteralExpression().Elements.Nodes {
					if ast.IsSpreadElement(element) {
						plan.expressionFallback = true
						break
					}
				}
			}
			return plan
		case ast.KindObjectLiteralExpression:
			for _, prop := range expression.AsObjectLiteralExpression().Properties.Nodes {
				if ast.IsSpreadAssignment(prop) || ast.IsShorthandPropertyAssignment(prop) {
					plan.expressionFallback = true
					break
				}
				if name := prop.Name(); name != nil && ast.IsComputedPropertyName(name) && !ast.IsPrimitiveLiteralValue(name.Expression(), false) {
					plan.expressionFallback = true
					break
				}
			}
			return plan
		case ast.KindClassExpression:
			plan.expressionFallback = true
			return plan
		case ast.KindArrowFunction, ast.KindFunctionExpression:
			return plan
		case ast.KindTemplateExpression:
			if !constant {
				plan.suppress = expression
				return plan
			}
		case ast.KindNullKeyword:
			plan.suppress = expression
			return plan
		case ast.KindIdentifier:
			if ch.GetSymbolAtLocation(expression) == ch.GetUndefinedSymbol() {
				plan.suppress = expression
				return plan
			}
		default:
			if ast.IsPrimitiveLiteralValue(expression, true) {
				plan.suppress = expression
				return plan
			}
		}
		plan.failed = true
		return plan
	}
}
func referenceIsolatedDeclarationDiagnostics(ctx context.Context, program *compiler.Program, file *ast.SourceFile) []*ast.Diagnostic {
	items, signatures := program.GetReferenceDeclarationDiagnostics(ctx, file)
	if ctx.Err() != nil || file.IsDeclarationFile {
		return items
	}
	ch, done := program.GetTypeCheckerForFile(ctx, file)
	defer done()
	create := declarations.ReferenceGetIsolatedDeclarationErrors(ch.GetEmitResolver())
	plans := []*referenceIsolatedReturnPlan{}
	seen := map[*ast.Node]bool{}
	byNode := map[*ast.Node]*referenceIsolatedReturnPlan{}
	for _, signature := range signatures {
		node := signature.Node
		parent := byNode[signature.Parent]
		// TS 6 uses expression serialization for non-constant arrays and object
		// literals that require inference. Native signature fallback suppresses
		// nested signatures on those paths; the reference expression path does not.
		if signature.Suppressed && (parent == nil || !parent.expressionFallback) {
			// Declaration inference in TS 6 keeps the expression traversal active.
			// Native inference suppresses it after a pseudo-type fallback instead.
			owner := signature.Parent
			if parent != nil || owner == nil || owner.Type() != nil {
				continue
			}
			if !ast.IsVariableDeclaration(owner) && !ast.IsPropertyDeclaration(owner) && !ast.IsParameterDeclaration(owner) && !ast.IsPropertyAssignment(owner) {
				continue
			}
			initializer := owner.Initializer()
			if initializer == nil {
				continue
			}
			for ast.IsParenthesizedExpression(initializer) || ast.IsAsExpression(initializer) || initializer.Kind == ast.KindTypeAssertionExpression {
				if !ast.IsParenthesizedExpression(initializer) && !isConstAssertion(initializer) {
					initializer = nil
					break
				}
				initializer = initializer.Expression()
			}
			if initializer == nil || !(ast.IsArrayLiteralExpression(initializer) || ast.IsObjectLiteralExpression(initializer)) {
				continue
			}
		}
		if parent != nil && parent.failed {
			continue
		}
		if seen[node] || node.Type() != nil {
			continue
		}
		seen[node] = true
		switch node.Kind {
		case ast.KindVariableDeclaration, ast.KindPropertyDeclaration, ast.KindPropertyAssignment, ast.KindParameter:
			// Inferred declaration types preserve the surrounding expression path;
			// unlike inferred signature returns, they do not suppress child reports.
			byNode[node] = &referenceIsolatedReturnPlan{node: node, expressionFallback: true}
		case ast.KindFunctionDeclaration, ast.KindFunctionExpression, ast.KindArrowFunction, ast.KindMethodDeclaration, ast.KindConstructSignature:
			plan := referenceIsolatedReturnPlanFor(node, ch)
			plans = append(plans, plan)
			byNode[node] = plan
		}
	}
	active := []*referenceIsolatedReturnPlan{}
	for _, plan := range plans {
		hidden := false
		for _, outer := range plans {
			if outer != plan && ((outer.failed && referenceIsolatedContains(outer.node.Body(), plan.node)) || referenceIsolatedContains(outer.suppress, plan.node)) {
				hidden = true
				break
			}
		}
		if !hidden {
			active = append(active, plan)
		}
	}
	fresh := func(node *ast.Node) *ast.Diagnostic {
		item := create(node)
		if ast.IsFunctionExpression(node) && node.Name() == nil {
			old := scanner.GetErrorRangeForNode(file, node)
			token := astnav.GetTokenAtPosition(file, scanner.GetTokenPosOfNode(node, file, false))
			if token != nil {
				span := core.NewTextRange(scanner.GetTokenPosOfNode(token, file, false), token.End())
				item = item.Clone()
				item.SetLocation(span)
				related := slices.Clone(item.RelatedInformation())
				for index, info := range related {
					if info.Code() == diagnostics.Add_a_return_type_to_the_function_expression.Code() && info.Loc() == old {
						info = info.Clone()
						info.SetLocation(span)
						related[index] = info
					}
				}
				item.SetRelatedInfo(related)
			}
		}
		return item
	}
	result := []*ast.Diagnostic{}
	for _, item := range items {
		if !slices.Contains(isolatedDeclarationsFixErrorCodes, item.Code()) {
			result = append(result, item)
			continue
		}
		token := astnav.GetTokenAtPosition(file, item.Pos())
		drop := false
		for _, plan := range active {
			if (item.Code() == 9007 || item.Code() == 9008) && item.Loc() == scanner.GetErrorRangeForNode(file, plan.node) {
				drop = true
				break
			}
			if plan.failed && referenceIsolatedContains(plan.node.Body(), token) {
				drop = true
				break
			}
			if referenceIsolatedContains(plan.suppress, token) && !referenceIsolatedContains(plan.preservedType, token) {
				drop = true
				break
			}
			if plan.array != nil && (item.Code() == 9013 || item.Code() == 9039) && referenceIsolatedContains(plan.array, token) && ast.GetContainingFunction(token) == plan.node {
				drop = true
				break
			}
		}
		if drop {
			continue
		}
		if item.Code() == 9039 && token != nil {
			node := token
			for node.Parent != nil && node.End() < item.End() {
				node = node.Parent
			}
			if !(ast.IsPartOfTypeNode(node) || node.Parent != nil && ast.IsTypeQueryNode(node.Parent)) && (ast.IsEntityName(node) || ast.IsEntityNameExpression(node)) {
				item = fresh(node)
			}
		}
		result = append(result, item)
	}
	for _, plan := range active {
		if ctx.Err() != nil {
			return nil
		}
		if plan.failed {
			result = append(result, fresh(plan.node))
		} else if plan.array != nil {
			result = append(result, fresh(plan.array))
		}
	}
	return compiler.SortAndDeduplicateDiagnostics(result)
}

// Spread types in the reference encounter properties from right-hand groups
// first. Rebuild only synthetic edit types; source checker types are untouched.
func (f *isolatedDeclarationsFixer) referenceSpreadTypeOrder(typ *checker.Type, scope *ast.Node, seen map[*checker.Type]bool) *checker.Type {
	if typ == nil || seen[typ] {
		return typ
	}
	seen[typ] = true
	defer delete(seen, typ)
	if typ.Flags()&checker.TypeFlagsUnion != 0 {
		types := typ.Types()
		ordered := make([]*checker.Type, 0, len(types))
		changed := false
		for _, t := range types {
			next := f.referenceSpreadTypeOrder(t, scope, seen)
			ordered = append(ordered, next)
			changed = changed || next != t
		}
		if changed {
			return f.checker.GetUnionType(ordered)
		}
		return typ
	}
	if typ.ObjectFlags()&checker.ObjectFlagsAnonymous == 0 {
		return typ
	}
	var literal *ast.Node
	if symbol := typ.Symbol(); symbol != nil && symbol.ValueDeclaration != nil && ast.IsObjectLiteralExpression(symbol.ValueDeclaration) {
		literal = symbol.ValueDeclaration
	}
	if literal == nil && scope != nil && scope.Type() == nil && (ast.IsVariableDeclaration(scope) || ast.IsPropertyDeclaration(scope)) {
		literal = scope.Initializer()
		for literal != nil && (ast.IsParenthesizedExpression(literal) || ast.IsAsExpression(literal) || (literal.Kind == ast.KindTypeAssertionExpression)) {
			literal = literal.Expression()
		}
	}
	if literal == nil || !ast.IsObjectLiteralExpression(literal) {
		return typ
	}
	groups := [][]string{}
	current := []string{}
	hasSpread := false
	for _, property := range literal.Properties() {
		if ast.IsSpreadAssignment(property) {
			hasSpread = true
			if len(current) > 0 {
				groups = append(groups, current)
				current = nil
			}
			spread := f.referenceSpreadTypeOrder(f.checker.GetTypeAtLocation(property.Expression()), nil, seen)
			names := []string{}
			for _, symbol := range f.checker.GetPropertiesOfType(spread) {
				names = append(names, symbol.Name)
			}
			groups = append(groups, names)
		} else if property.Name() != nil {
			if symbol := f.checker.GetSymbolAtLocation(property.Name()); symbol != nil {
				current = append(current, symbol.Name)
			}
		}
	}
	if !hasSpread {
		return typ
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	members := []*ast.Symbol{}
	used := map[string]bool{}
	for index := len(groups) - 1; index >= 0; index-- {
		for _, name := range groups[index] {
			if !used[name] {
				if symbol := f.checker.GetPropertyOfType(typ, name); symbol != nil {
					members = append(members, symbol)
					used[name] = true
				}
			}
		}
	}
	for _, symbol := range f.checker.GetPropertiesOfType(typ) {
		if !used[symbol.Name] {
			members = append(members, symbol)
			used[symbol.Name] = true
		}
	}
	return f.checker.NewReferenceInferenceObject(typ.Symbol(), members, f.checker.GetSignaturesOfType(typ, checker.SignatureKindCall), f.checker.GetSignaturesOfType(typ, checker.SignatureKindConstruct), f.checker.GetIndexInfosOfType(typ))
}
