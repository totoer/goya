package refactor

import (
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/checker"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/nodebuilder"
	"github.com/totoer/goya/internal/tsfrontend/upstream/printer"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
)

func extractionTypeNode(r *request, t *change.Tracker, typ *checker.Type, scope *ast.Node) *ast.Node {
	if typ == nil {
		return nil
	}
	return checker.NewNodeBuilder(r.types(), t.EmitContext).TypeToTypeNode(typ, scope, nodebuilder.FlagsNoTruncation, nodebuilder.InternalFlagsAllowUnresolvedNames, nil)
}
func extractionSubstitute(t *change.Tracker, node *ast.Node, substitutions map[*ast.Node]*ast.Node) *ast.Node {
	if len(substitutions) == 0 {
		return node
	}
	var visitor *ast.NodeVisitor
	visitor = ast.NewNodeVisitor(func(n *ast.Node) *ast.Node {
		if replacement := substitutions[n]; replacement != nil {
			return cloneWithTrivia(t, replacement)
		}
		return n.VisitEachChild(visitor)
	}, t.NodeFactory, ast.NodeVisitorHooks{})
	return visitor.VisitNode(node)
}
func extractConstant(r *request, analysis *symbolExtraction, info *extractionScopeInfo) (*EditInfo, error) {
	target := analysis.target
	node := target.expression
	if node == nil {
		node = target.statements[0].Expression()
	}
	scope := info.scope
	c := r.types()
	t := r.tracker()
	name := extractionNodeName(r, node, scope)
	var typ *ast.Node
	if c.IsContextSensitive(node) {
		typ = extractionTypeNode(r, t, c.GetContextualType(node, checker.ContextFlagsNone), scope)
	}
	initializer := extractionSubstitute(t, ast.SkipParentheses(node), info.substitutions)
	typ, initializer = extractionFunctionInitializer(r, t, node, scope, typ, initializer)
	suppressBothTrivia(t, initializer)
	reference := t.NewIdentifier(name)
	if ast.IsClassLike(scope) {
		modifiers := []*ast.Node{t.NewToken(ast.KindPrivateKeyword)}
		var lhs *ast.Node = t.NewKeywordExpression(ast.KindThisKeyword)
		if target.facts&extractionStatic != 0 {
			modifiers = append(modifiers, t.NewToken(ast.KindStaticKeyword))
			if scope.Name() == nil {
				return nil, &change.InvalidEdits{Reason: "static extraction requires a class name"}
			}
			lhs = t.NewIdentifier(scope.Name().Text())
		}
		modifiers = append(modifiers, t.NewToken(ast.KindReadonlyKeyword))
		declaration := t.NewPropertyDeclaration(t.NewModifierList(modifiers), t.NewIdentifier(name), nil, typ, initializer)
		t.AddEmitFlags(declaration, printer.EFReferenceIgnoreSourceNewlines)
		reference = t.NewPropertyAccessExpression(lhs, nil, reference, ast.NodeFlagsNone)
		if extractionJSXContent(node) {
			reference = t.NewJsxExpression(nil, reference)
		}
		before := extractionInsertPropertyBefore(node.Pos(), scope)
		if before == nil {
			return nil, &change.InvalidEdits{Reason: "missing class insertion point"}
		}
		t.InsertNodeBefore(r.file, before, declaration, true, change.LeadingTriviaOptionNone)
		t.ReplaceNode(r.file, node, reference, nil)
	} else {
		declaration := t.NewVariableDeclaration(t.NewIdentifier(name), nil, typ, initializer)
		var old *ast.Node
		var previous *ast.Node
		for curr := node; curr != nil && curr != scope; curr = curr.Parent {
			if ast.IsVariableDeclaration(curr) && curr.Initializer() == previous && ast.IsVariableDeclarationList(curr.Parent) && len(curr.Parent.AsVariableDeclarationList().Declarations.Nodes) > 1 {
				old = curr
				break
			}
			previous = curr
		}
		if old != nil {
			t.AddEmitFlags(declaration, printer.EFReferenceIgnoreSourceNewlines)
			t.InsertNodeBefore(r.file, old, declaration, false, change.LeadingTriviaOptionNone)
			t.ReplaceNode(r.file, node, reference, nil)
		} else {
			statement := t.NewVariableStatement(nil, t.NewVariableDeclarationList(t.NewNodeList([]*ast.Node{declaration}), ast.NodeFlagsConst))
			t.AddEmitFlags(statement, printer.EFReferenceIgnoreSourceNewlines)
			if ast.IsExpressionStatement(node.Parent) && ancestor(node, extractionScope) == scope {
				t.ReplaceNode(r.file, node.Parent, statement, nil)
			} else {
				before := extractionInsertConstantBefore(node, scope)
				if before == nil {
					return nil, &change.InvalidEdits{Reason: "missing constant insertion point"}
				}
				if before.Pos() == 0 {
					t.InsertAtTopOfFile(r.file, []*ast.Node{statement}, false)
				} else {
					t.InsertNodeBefore(r.file, before, statement, false, change.LeadingTriviaOptionNone)
				}
				if ast.IsExpressionStatement(node.Parent) {
					t.Delete(r.file, node.Parent)
				} else {
					if extractionJSXContent(node) {
						reference = t.NewJsxExpression(nil, reference)
					}
					t.ReplaceNode(r.file, node, reference, nil)
				}
			}
		}
	}
	result := finish(t)
	if err := setRenameLocation(r, result, r.file.FileName(), name, true); err != nil {
		return nil, err
	}
	return result, nil
}
func extractionFunctionInitializer(r *request, t *change.Tracker, original, scope, typ, initializer *ast.Node) (*ast.Node, *ast.Node) {
	if typ == nil || (!ast.IsFunctionExpression(initializer) && !ast.IsArrowFunction(initializer)) || initializer.TypeParameterList() != nil {
		return typ, initializer
	}
	c := r.types()
	signatures := c.GetSignaturesOfType(c.GetTypeAtLocation(original), checker.SignatureKindCall)
	if len(signatures) != 1 || len(signatures[0].TypeParameters()) != 0 {
		return typ, initializer
	}
	signature := signatures[0]
	parameters := []*ast.Node{}
	for _, parameter := range initializer.Parameters() {
		if parameter.Type() != nil {
			parameters = append(parameters, parameter)
			continue
		}
		paramType := c.GetTypeAtLocation(parameter)
		if paramType == c.GetAnyType() {
			return typ, initializer
		}
		p := parameter.AsParameterDeclaration()
		parameters = append(parameters, t.UpdateParameterDeclaration(p, parameter.Modifiers(), p.DotDotDotToken, parameter.Name(), p.QuestionToken, extractionTypeNode(r, t, paramType, scope), p.Initializer))
	}
	returnType := initializer.Type()
	if returnType == nil {
		returnType = extractionTypeNode(r, t, c.GetReturnTypeOfSignature(signature), scope)
	}
	// The pinned implementation takes modifiers from the original selection.
	modifiers := original.Modifiers()
	if ast.IsArrowFunction(initializer) {
		a := initializer.AsArrowFunction()
		return nil, t.UpdateArrowFunction(a, modifiers, a.TypeParameters, t.NewNodeList(parameters), returnType, nil, a.EqualsGreaterThanToken, a.Body)
	}
	if this := signature.ThisParameter(); this != nil && (len(parameters) == 0 || (ast.IsIdentifier(parameters[0].Name()) && parameters[0].Name().Text() != "this")) {
		p := t.NewParameterDeclaration(nil, nil, t.NewIdentifier("this"), nil, extractionTypeNode(r, t, c.GetTypeOfSymbolAtLocation(this, original), scope), nil)
		parameters = append([]*ast.Node{p}, parameters...)
	}
	f := initializer.AsFunctionExpression()
	return nil, t.UpdateFunctionExpression(f, modifiers, f.AsteriskToken, f.Name(), f.TypeParameters, t.NewNodeList(parameters), returnType, nil, f.Body)
}

func extractionPreserveTypeLayout(t *change.Tracker, node *ast.Node) {
	var visit func(*ast.Node) bool
	visit = func(n *ast.Node) bool {
		if (ast.IsTupleTypeNode(n) || ast.IsTypeLiteralNode(n) || ast.IsBlock(n)) && n.Pos() >= 0 {
			if f := ast.GetSourceFileOfNode(n); f != nil && scanner.GetECMALineOfPosition(f, n.Pos()) == scanner.GetECMALineOfPosition(f, n.End()) {
				t.AddEmitFlags(n, printer.EFSingleLine)
			}
		}
		return n.ForEachChild(visit)
	}
	visit(node)
}
