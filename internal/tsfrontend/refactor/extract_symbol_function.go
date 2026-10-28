package refactor

import (
	"cmp"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/checker"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/autoimport"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/nodebuilder"
	"github.com/totoer/goya/internal/tsfrontend/upstream/printer"
	"slices"
)

func extractionAssignments(t *change.Tracker, exposed []*ast.Node, writes []extractionUsage) []*ast.Node {
	result := []*ast.Node{}
	appendName := func(name string) {
		result = append(result, t.NewShorthandPropertyAssignment(nil, t.NewIdentifier(name), nil, nil, nil, nil))
	}
	for _, decl := range exposed {
		appendName(decl.Symbol().Name)
	}
	for _, write := range writes {
		appendName(write.symbol.Name)
	}
	return result
}
func extractionFunctionBody(t *change.Tracker, target *symbolRange, exposed []*ast.Node, writes []extractionUsage, substitutions map[*ast.Node]*ast.Node) (*ast.Node, string) {
	hasWrites := writes != nil || len(exposed) > 0
	if target.expression == nil && !hasWrites && len(substitutions) == 0 {
		return t.NewBlock(t.NewNodeList(target.statements), true), ""
	}
	statements := slices.Clone(target.statements)
	if target.expression != nil {
		statements = []*ast.Node{t.NewReturnStatement(ast.SkipParentheses(target.expression))}
	}
	returnName := ""
	if hasWrites || len(substitutions) > 0 {
		ignoreReturns := false
		var visitor *ast.NodeVisitor
		visitor = ast.NewNodeVisitor(func(node *ast.Node) *ast.Node {
			if !ignoreReturns && ast.IsReturnStatement(node) && hasWrites {
				assignments := extractionAssignments(t, exposed, writes)
				if node.Expression() != nil {
					if returnName == "" {
						returnName = "__return"
					}
					assignments = append([]*ast.Node{t.NewPropertyAssignment(nil, t.NewIdentifier(returnName), nil, nil, visitor.VisitNode(node.Expression()))}, assignments...)
				}
				if len(assignments) == 1 {
					return t.NewReturnStatement(assignments[0].Name())
				}
				return t.NewReturnStatement(t.NewObjectLiteralExpression(t.NewNodeList(assignments), false))
			}
			old := ignoreReturns
			ignoreReturns = ignoreReturns || ast.IsFunctionLikeDeclaration(node) || ast.IsClassLike(node)
			var result *ast.Node
			if replacement := substitutions[node]; replacement != nil {
				result = cloneWithTrivia(t, replacement)
			} else {
				result = node.VisitEachChild(visitor)
			}
			ignoreReturns = old
			return result
		}, t.NodeFactory, ast.NodeVisitorHooks{})
		for i, node := range statements {
			statements[i] = visitor.VisitNode(node)
		}
		if hasWrites && target.facts&extractionReturn == 0 && target.expression == nil {
			assignments := extractionAssignments(t, exposed, writes)
			var returned *ast.Node
			if len(assignments) == 1 {
				returned = assignments[0].Name()
			} else {
				returned = t.NewObjectLiteralExpression(t.NewNodeList(assignments), false)
			}
			statements = append(statements, t.NewReturnStatement(returned))
		}
	}
	return t.NewBlock(t.NewNodeList(statements), true), returnName
}
func extractionOptionalType(t *change.Tracker, node *ast.Node) *ast.Node {
	if node == nil {
		return nil
	}
	clone := cloneWithTrivia(t, node)
	without := clone
	for ast.IsParenthesizedTypeNode(without) {
		without = without.Type()
	}
	if ast.IsUnionTypeNode(without) {
		for _, part := range without.AsUnionTypeNode().Types.Nodes {
			if part.Kind == ast.KindUndefinedKeyword {
				return clone
			}
		}
	}
	return t.NewUnionTypeNode(t.NewNodeList([]*ast.Node{clone, t.NewKeywordTypeNode(ast.KindUndefinedKeyword)}))
}
func extractFunction(r *request, analysis *symbolExtraction, info *extractionScopeInfo) (*EditInfo, error) {
	target := analysis.target
	scope := info.scope
	t := r.tracker()
	c := r.types()
	base := "newFunction"
	if ast.IsClassLike(scope) {
		base = "newMethod"
	}
	name := uniqueName(base, r.file)
	view, err := r.service.ReferenceAutoImportView(r.file)
	if err != nil {
		return nil, err
	}
	adder := autoimport.NewImportAdder(r.ctx, r.program, c, r.file, view, r.format, r.converters, r.preferences)
	symbols := map[*ast.Node]*ast.Symbol{}
	builder := checker.NewNodeBuilderEx(c, t.EmitContext, symbols)
	autoType := func(typ *checker.Type) *ast.Node {
		node := builder.TypeToTypeNode(typ, scope, nodebuilder.FlagsNoTruncation, nodebuilder.InternalFlagsAllowUnresolvedNames, nil)
		return autoimport.TypeNodeToAutoImportableTypeNode(node, adder, symbols)
	}
	parameters := []*ast.Node{}
	arguments := []*ast.Node{}
	var writes []extractionUsage
	for _, key := range info.names {
		usage := info.usages[key]
		typ := autoType(c.GetBaseTypeOfLiteralType(c.GetTypeOfSymbolAtLocation(usage.symbol, usage.node)))
		parameters = append(parameters, t.NewParameterDeclaration(nil, nil, t.NewIdentifier(key), nil, typ, nil))
		arguments = append(arguments, t.NewIdentifier(key))
		if usage.write {
			writes = append(writes, usage)
		}
	}
	type typeParameter struct {
		typ  *checker.Type
		decl *ast.Node
	}
	types := []typeParameter{}
	for _, typ := range info.types {
		var first *ast.Node
		if symbol := typ.Symbol(); symbol != nil {
			for _, decl := range symbol.Declarations {
				if decl.Pos() < r.span.Pos() && (first == nil || decl.Pos() < first.Pos()) {
					first = decl
				}
			}
		}
		types = append(types, typeParameter{typ, first})
	}
	slices.SortStableFunc(types, func(a, b typeParameter) int {
		pos := func(n *ast.Node) int {
			if n == nil {
				return -1
			}
			return n.Pos()
		}
		if order := cmp.Compare(pos(a.decl), pos(b.decl)); order != 0 {
			return order
		}
		symbolName := func(t *checker.Type) string {
			if s := t.Symbol(); s != nil {
				return ast.SymbolName(s)
			}
			return ""
		}
		if order := cmp.Compare(symbolName(a.typ), symbolName(b.typ)); order != 0 {
			return order
		}
		return cmp.Compare(a.typ.Id(), b.typ.Id())
	})
	typeParameters := []*ast.Node{}
	typeArguments := []*ast.Node{}
	for _, parameter := range types {
		if parameter.decl != nil {
			typeParameters = append(typeParameters, parameter.decl)
			typeArguments = append(typeArguments, t.NewTypeReferenceNode(parameter.decl.Name(), nil))
		}
	}
	var returnType *ast.Node
	if target.expression != nil {
		returnType = extractionTypeNode(r, t, c.GetContextualType(target.expression, checker.ContextFlagsNone), scope)
	}
	body, returnName := extractionFunctionBody(t, target, analysis.exposed, writes, info.substitutions)
	suppressBothTrivia(t, body)
	modifiers := []*ast.Node{}
	var star *ast.Node
	if ast.IsClassLike(scope) {
		modifiers = append(modifiers, t.NewToken(ast.KindPrivateKeyword))
		if target.facts&extractionStatic != 0 {
			modifiers = append(modifiers, t.NewToken(ast.KindStaticKeyword))
		}
	}
	if target.facts&extractionAsync != 0 {
		modifiers = append(modifiers, t.NewToken(ast.KindAsyncKeyword))
	}
	if target.facts&extractionGenerator != 0 {
		star = t.NewToken(ast.KindAsteriskToken)
	}
	callThis := target.facts&extractionFunctionThis != 0
	var declaration *ast.Node
	if ast.IsClassLike(scope) {
		declaration = t.NewMethodDeclaration(t.NewModifierList(modifiers), star, t.NewIdentifier(name), nil, t.NewNodeList(typeParameters), t.NewNodeList(parameters), returnType, nil, body)
	} else {
		if callThis {
			parameters = append([]*ast.Node{t.NewParameterDeclaration(nil, nil, t.NewIdentifier("this"), nil, extractionTypeNode(r, t, c.GetTypeAtLocation(target.thisNode), scope), nil)}, parameters...)
		}
		declaration = t.NewFunctionDeclaration(t.NewModifierList(modifiers), star, t.NewIdentifier(name), t.NewNodeList(typeParameters), t.NewNodeList(parameters), returnType, nil, body)
	}
	extractionPreserveTypeLayout(t, declaration)
	if before := extractionInsertFunctionBefore(target.last().End(), scope); before != nil {
		t.InsertNodeBefore(r.file, before, declaration, true, change.LeadingTriviaOptionNone)
	} else {
		t.InsertReferenceNodeAtEndOfScope(r.file, scope, declaration)
	}
	for _, edit := range adder.Edits() {
		t.ReplaceRangeWithText(r.file, edit.Range, edit.NewText)
	}
	called := t.NewIdentifier(name)
	if ast.IsClassLike(scope) {
		var lhs *ast.Node = t.NewKeywordExpression(ast.KindThisKeyword)
		if target.facts&extractionStatic != 0 {
			if scope.Name() == nil {
				return nil, &change.InvalidEdits{Reason: "static extraction requires a class name"}
			}
			lhs = t.NewIdentifier(scope.Name().Text())
		}
		called = t.NewPropertyAccessExpression(lhs, nil, called, ast.NodeFlagsNone)
	}
	if callThis {
		arguments = append([]*ast.Node{t.NewIdentifier("this")}, arguments...)
		called = t.NewPropertyAccessExpression(called, nil, t.NewIdentifier("call"), ast.NodeFlagsNone)
	}
	call := t.NewCallExpression(called, nil, t.NewNodeList(typeArguments), t.NewNodeList(arguments), ast.NodeFlagsNone)
	if target.facts&extractionGenerator != 0 {
		call = t.NewYieldExpression(t.NewToken(ast.KindAsteriskToken), call)
	}
	if target.facts&extractionAsync != 0 {
		call = t.NewAwaitExpression(call)
	}
	if extractionJSXContent(target.first()) {
		call = t.NewJsxExpression(nil, call)
	}
	newNodes := []*ast.Node{}
	variable := func(name, typ, initializer *ast.Node, flags ast.NodeFlags) *ast.Node {
		return t.NewVariableStatement(nil, t.NewVariableDeclarationList(t.NewNodeList([]*ast.Node{t.NewVariableDeclaration(name, nil, typ, initializer)}), flags))
	}
	assignment := func(left, right *ast.Node) *ast.Node {
		return t.NewBinaryExpression(nil, left, nil, t.NewToken(ast.KindEqualsToken), right)
	}
	if len(analysis.exposed) > 0 && writes == nil {
		if len(analysis.exposed) == 1 {
			v := analysis.exposed[0]
			newNodes = append(newNodes, variable(cloneWithTrivia(t, v.Name()), cloneWithTrivia(t, v.Type()), call, v.Parent.Flags))
		} else {
			bindings := []*ast.Node{}
			elements := []*ast.Node{}
			flags := analysis.exposed[0].Parent.Flags
			explicit := false
			for _, v := range analysis.exposed {
				bindings = append(bindings, t.NewBindingElement(nil, nil, cloneWithTrivia(t, v.Name()), nil))
				typ := extractionTypeNode(r, t, c.GetBaseTypeOfLiteralType(c.GetTypeAtLocation(v)), scope)
				elements = append(elements, t.NewPropertySignatureDeclaration(nil, t.NewIdentifier(v.Symbol().Name), nil, typ, nil))
				explicit = explicit || v.Type() != nil
				flags &= v.Parent.Flags
			}
			var typ *ast.Node
			if explicit {
				typ = t.NewTypeLiteralNode(t.NewNodeList(elements))
				t.AddEmitFlags(typ, printer.EFSingleLine)
			}
			newNodes = append(newNodes, variable(t.NewBindingPattern(ast.KindObjectBindingPattern, t.NewNodeList(bindings)), typ, call, flags))
		}
	} else if len(analysis.exposed) > 0 || writes != nil {
		for _, v := range analysis.exposed {
			flags := v.Parent.Flags
			if flags&ast.NodeFlagsConst != 0 {
				flags = (flags &^ ast.NodeFlagsConst) | ast.NodeFlagsLet
			}
			newNodes = append(newNodes, variable(t.NewIdentifier(v.Symbol().Name), extractionOptionalType(t, v.Type()), nil, flags))
		}
		if returnName != "" {
			newNodes = append(newNodes, variable(t.NewIdentifier(returnName), extractionOptionalType(t, returnType), nil, ast.NodeFlagsLet))
		}
		assignments := extractionAssignments(t, analysis.exposed, writes)
		if returnName != "" {
			assignments = append([]*ast.Node{t.NewShorthandPropertyAssignment(nil, t.NewIdentifier(returnName), nil, nil, nil, nil)}, assignments...)
		}
		if len(assignments) == 1 {
			newNodes = append(newNodes, t.NewExpressionStatement(assignment(assignments[0].Name(), call)))
			if target.facts&extractionReturn != 0 {
				newNodes = append(newNodes, t.NewReturnStatement(nil))
			}
		} else {
			newNodes = append(newNodes, t.NewExpressionStatement(assignment(t.NewObjectLiteralExpression(t.NewNodeList(assignments), false), call)))
			if returnName != "" {
				newNodes = append(newNodes, t.NewReturnStatement(t.NewIdentifier(returnName)))
			}
		}
	} else if target.facts&extractionReturn != 0 {
		newNodes = append(newNodes, t.NewReturnStatement(call))
	} else if target.expression == nil {
		newNodes = append(newNodes, t.NewExpressionStatement(call))
	} else {
		newNodes = append(newNodes, call)
	}
	if target.expression == nil {
		options := change.NodeOptions{LeadingTriviaOption: change.LeadingTriviaOptionExclude, TrailingTriviaOption: change.TrailingTriviaOptionExclude}
		span := t.GetAdjustedRange(r.file, target.first(), target.last(), options.LeadingTriviaOption, options.TrailingTriviaOption)
		t.ReplaceRangeWithNodes(r.file, span, newNodes, options)
	} else {
		t.ReplaceNodeWithNodes(r.file, target.expression, newNodes, nil)
	}
	result := finish(t)
	if err := setRenameLocation(r, result, r.file.FileName(), name, false); err != nil {
		return nil, err
	}
	return result, nil
}
