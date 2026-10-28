package refactor

// The extraction range and control-flow rules follow TypeScript 6.0.3's
// services/refactors/extractSymbol.ts. Compiler nodes remain request-owned.
import (
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"slices"
)

type extractionFacts uint8

const (
	extractionReturn extractionFacts = 1 << iota
	extractionGenerator
	extractionAsync
	extractionThis
	extractionFunctionThis
	extractionStatic
)

type symbolRange struct {
	expression *ast.Node
	statements []*ast.Node
	facts      extractionFacts
	thisNode   *ast.Node
}

func (s *symbolRange) first() *ast.Node {
	if s.expression != nil {
		return s.expression
	}
	return s.statements[0]
}
func (s *symbolRange) last() *ast.Node {
	if s.expression != nil {
		return s.expression
	}
	return s.statements[len(s.statements)-1]
}
func (s *symbolRange) affected(r *request) core.TextRange {
	if s.expression != nil {
		return s.expression.Loc
	}
	return core.NewTextRange(tokenStart(r, s.first()), s.last().End())
}
func extractionJSXAttribute(node *ast.Node) bool {
	return ast.IsStringLiteral(node) && node.Parent != nil && ast.IsJsxAttribute(node.Parent)
}
func extractionJSXContent(node *ast.Node) bool {
	return extractionJSXAttribute(node) || ((ast.IsJsxElement(node) || ast.IsJsxSelfClosingElement(node) || ast.IsJsxFragment(node)) && node.Parent != nil && (ast.IsJsxElement(node.Parent) || ast.IsJsxFragment(node.Parent)))
}
func extractionExpression(node *ast.Node) bool {
	if node == nil || node.Parent == nil || ast.IsEnumMember(node.Parent) {
		return false
	}
	switch node.Kind {
	case ast.KindStringLiteral:
		return !ast.IsImportDeclaration(node.Parent) && !ast.IsImportSpecifier(node.Parent)
	case ast.KindSpreadElement, ast.KindObjectBindingPattern, ast.KindBindingElement:
		return false
	case ast.KindIdentifier:
		return !ast.IsBindingElement(node.Parent) && !ast.IsImportSpecifier(node.Parent) && !ast.IsExportSpecifier(node.Parent)
	}
	return true
}
func extractionScope(node *ast.Node) bool {
	if node == nil {
		return false
	}
	if ast.IsArrowFunction(node) {
		return ast.IsBlock(node.Body())
	}
	return ast.IsFunctionLikeDeclaration(node) || ast.IsSourceFile(node) || ast.IsModuleBlock(node) || ast.IsClassLike(node)
}
func extractionScopes(target *symbolRange) []*ast.Node {
	current := target.first()
	if target.facts&extractionThis != 0 && target.facts&extractionFunctionThis == 0 {
		class := ancestor(current, ast.IsClassLike)
		if class != nil {
			if fn := ancestor(current, ast.IsFunctionLikeDeclaration); fn != nil {
				return []*ast.Node{fn, class}
			}
			return []*ast.Node{class}
		}
	}
	scopes := []*ast.Node{}
	for current != nil {
		current = current.Parent
		if current != nil && ast.IsParameterDeclaration(current) {
			if fn := ancestor(current, ast.IsFunctionLikeDeclaration); fn != nil {
				current = fn.Parent
			}
		}
		if extractionScope(current) {
			scopes = append(scopes, current)
			if ast.IsSourceFile(current) {
				break
			}
		}
	}
	return scopes
}
func extractionParentInSpan(r *request, node *ast.Node, span core.TextRange) *ast.Node {
	for node != nil && !ast.IsSourceFile(node) && node.Parent != nil && containsText(r, span, node.Parent.Loc) {
		node = node.Parent
	}
	if node != nil && node.Parent != nil {
		return node
	}
	return nil
}
func getSymbolRange(r *request, invoked bool) (*symbolRange, string) {
	if r.span.Len() == 0 && !invoked {
		return nil, "Cannot extract empty range."
	}
	cursor := r.span.Len() == 0 && invoked
	startToken := astnav.GetTokenAtPosition(r.file, r.span.Pos())
	for startToken != nil && ast.IsJsxText(startToken) && startToken.AsJsxText().ContainsOnlyTriviaWhiteSpaces {
		startToken = astnav.FindNextToken(startToken, startToken.Parent, r.file)
	}
	endToken := extractionTokenOnLeft(r, r.span.End())
	adjusted := r.span
	if startToken != nil && endToken != nil && invoked {
		end := endToken.End()
		if end < len(r.file.Text()) && r.file.Text()[end] == ';' {
			end++
		}
		adjusted = core.NewTextRange(tokenStart(r, startToken), end)
	}
	var start, end *ast.Node
	if cursor {
		start = ancestor(startToken, func(n *ast.Node) bool {
			return n.Parent != nil && extractionExpression(n) && !ast.IsBinaryExpression(n.Parent)
		})
		end = start
	} else {
		start = extractionParentInSpan(r, startToken, adjusted)
		end = extractionParentInSpan(r, endToken, adjusted)
	}
	if start == nil || end == nil {
		return nil, "Cannot extract range."
	}
	if start.Flags&ast.NodeFlagsJSDoc != 0 {
		return nil, "Cannot extract JSDoc."
	}
	if start.Parent != end.Parent {
		return nil, "Cannot extract range."
	}
	result := &symbolRange{}
	check := func(node *ast.Node) string { return checkExtractionNode(r, result, node) }
	if start != end {
		if !extractionBlockLike(start.Parent) {
			return nil, "Cannot extract range."
		}
		for _, statement := range start.Parent.Statements() {
			if statement == start || len(result.statements) > 0 {
				if reason := check(statement); reason != "" {
					return nil, reason
				}
				result.statements = append(result.statements, statement)
			}
			if statement == end {
				break
			}
		}
		if len(result.statements) == 0 {
			return nil, "Cannot extract range."
		}
		return result, ""
	}
	if ast.IsReturnStatement(start) && start.Expression() == nil {
		return nil, "Cannot extract range."
	}
	node := start
	switch node.Kind {
	case ast.KindReturnStatement:
		if node.Expression() != nil {
			node = node.Expression()
		}
	case ast.KindVariableStatement, ast.KindVariableDeclarationList:
		list := node
		if ast.IsVariableStatement(node) {
			list = node.AsVariableStatement().DeclarationList
		}
		var init *ast.Node
		count := 0
		for _, decl := range list.AsVariableDeclarationList().Declarations.Nodes {
			if decl.Initializer() != nil {
				count++
				init = decl.Initializer()
			}
		}
		if count == 1 {
			node = init
		}
	case ast.KindVariableDeclaration:
		if node.Initializer() != nil {
			node = node.Initializer()
		}
	}
	root := node
	if ast.IsExpressionStatement(root) {
		root = root.Expression()
	}
	if ast.IsIdentifier(root) {
		return nil, "Select more than a single identifier."
	}
	if reason := check(node); reason != "" {
		return nil, reason
	}
	if ast.IsStatement(node) {
		result.statements = []*ast.Node{node}
	} else if ast.IsExpressionNode(node) || extractionJSXAttribute(node) {
		if ast.IsExpressionStatement(node.Parent) {
			result.statements = []*ast.Node{node.Parent}
		} else {
			result.expression = node
		}
	}
	return result, ""
}
func checkExtractionNode(r *request, target *symbolRange, root *ast.Node) string {
	if !ast.IsStatement(root) && !(ast.IsExpressionNode(root) && extractionExpression(root)) && !extractionJSXAttribute(root) {
		return "Statement or expression expected."
	}
	if root.Flags&ast.NodeFlagsAmbient != 0 {
		return "Cannot extract code from ambient contexts"
	}
	class := ancestor(root, ast.IsClassLike)
	if class != nil {
		for n := root; n != nil && n != class; n = n.Parent {
			if ast.IsPropertyDeclaration(n) {
				if ast.HasSyntacticModifier(n, ast.ModifierFlagsStatic) {
					target.facts |= extractionStatic
				}
				break
			}
			if ast.IsParameterDeclaration(n) {
				if fn := ancestor(n, ast.IsFunctionLikeDeclaration); fn != nil && ast.IsConstructorDeclaration(fn) {
					target.facts |= extractionStatic
				}
				break
			}
			if ast.IsMethodDeclaration(n) && ast.HasSyntacticModifier(n, ast.ModifierFlagsStatic) {
				target.facts |= extractionStatic
			}
		}
	}
	const (
		breakJump    = 1
		continueJump = 2
		returnJump   = 4
	)
	jumps := returnJump
	labels := []string{}
	reason := ""
	var visit func(*ast.Node) bool
	visit = func(node *ast.Node) bool {
		if reason != "" {
			return true
		}
		if referenceIsDeclaration(node) {
			decl := node
			if ast.IsVariableDeclaration(node) {
				decl = node.Parent.Parent
			}
			if ast.HasSyntacticModifier(decl, ast.ModifierFlagsExport) {
				reason = "Cannot extract exported declaration"
				return true
			}
		}
		switch node.Kind {
		case ast.KindImportDeclaration:
			reason = "Cannot extract import statement."
			return true
		case ast.KindExportAssignment:
			reason = "Cannot extract exported declaration"
			return true
		case ast.KindSuperKeyword:
			if ast.IsCallExpression(node.Parent) {
				class := ancestor(node, ast.IsClassLike)
				if class == nil || class.Pos() < r.span.Pos() || class.End() >= r.span.End() {
					reason = "Cannot extract super call."
					return true
				}
			} else {
				target.facts |= extractionThis
				target.thisNode = node
			}
		case ast.KindArrowFunction:
			var checkThis func(*ast.Node) bool
			checkThis = func(n *ast.Node) bool {
				if n.Kind == ast.KindThisKeyword || ast.IsThisIdentifier(n) && n.Parent != nil && ast.IsParameterDeclaration(n.Parent) {
					target.facts |= extractionThis
					target.thisNode = node
				} else if !ast.IsClassLike(n) && !(ast.IsFunctionLike(n) && !ast.IsArrowFunction(n)) {
					n.ForEachChild(checkThis)
				}
				return false
			}
			node.ForEachChild(checkThis)
			fallthrough
		case ast.KindClassDeclaration, ast.KindFunctionDeclaration:
			if ast.IsSourceFile(node.Parent) && node.Parent.AsSourceFile().ExternalModuleIndicator == nil {
				reason = "Function will not visible in the new scope."
			}
			return false
		case ast.KindClassExpression, ast.KindFunctionExpression, ast.KindMethodDeclaration, ast.KindConstructor, ast.KindGetAccessor, ast.KindSetAccessor:
			return false
		}
		saved := jumps
		switch node.Kind {
		case ast.KindIfStatement:
			jumps &^= returnJump
		case ast.KindTryStatement:
			jumps = 0
		case ast.KindBlock:
			if node.Parent != nil && ast.IsTryStatement(node.Parent) && node.Parent.AsTryStatement().FinallyBlock == node {
				jumps = returnJump
			}
		case ast.KindDefaultClause, ast.KindCaseClause:
			jumps |= breakJump
		default:
			if ast.IsIterationStatement(node, false) {
				jumps |= breakJump | continueJump
			}
		}
		switch node.Kind {
		case ast.KindThisType, ast.KindThisKeyword:
			target.facts |= extractionThis
			target.thisNode = node
		case ast.KindLabeledStatement:
			labels = append(labels, node.AsLabeledStatement().Label.Text())
			node.ForEachChild(visit)
			labels = labels[:len(labels)-1]
		case ast.KindBreakStatement, ast.KindContinueStatement:
			label := extractionJumpLabel(node)
			if label != nil {
				if !slices.Contains(labels, label.Text()) {
					reason = "Cannot extract range containing labeled break or continue with target outside of the range."
				}
			} else {
				jump := continueJump
				if ast.IsBreakStatement(node) {
					jump = breakJump
				}
				if jumps&jump == 0 {
					reason = "Cannot extract range containing conditional break or continue statements."
				}
			}
		case ast.KindAwaitExpression:
			target.facts |= extractionAsync
		case ast.KindYieldExpression:
			target.facts |= extractionGenerator
		case ast.KindReturnStatement:
			if jumps&returnJump != 0 {
				target.facts |= extractionReturn
			} else {
				reason = "Cannot extract range containing conditional return statement."
			}
		default:
			node.ForEachChild(visit)
		}
		jumps = saved
		return false
	}
	visit(root)
	if target.facts&extractionThis != 0 {
		container := ast.GetThisContainer(root, false, false)
		if container != nil && (ast.IsFunctionDeclaration(container) || ast.IsFunctionExpression(container) || (ast.IsMethodDeclaration(container) && ast.IsObjectLiteralExpression(container.Parent))) {
			target.facts |= extractionFunctionThis
		}
	}
	return reason
}

func extractionTokenOnLeft(r *request, position int) *ast.Node {
	node := astnav.GetTokenAtPosition(r.file, position)
	if ast.IsTokenKind(node.Kind) && position > tokenStart(r, node) && position < node.End() {
		return node
	}
	return astnav.FindPrecedingToken(r.file, position)
}

func extractionBlockLike(n *ast.Node) bool {
	return ast.IsSourceFile(n) || ast.IsBlock(n) || ast.IsModuleBlock(n) || ast.IsCaseClause(n)
}
func extractionJumpLabel(n *ast.Node) *ast.Node {
	if ast.IsBreakStatement(n) {
		return n.AsBreakStatement().Label
	}
	return n.AsContinueStatement().Label
}
func extractionUnaryWrite(n *ast.Node) *ast.Node {
	if ast.IsPrefixUnaryExpression(n) {
		u := n.AsPrefixUnaryExpression()
		if u.Operator == ast.KindPlusPlusToken || u.Operator == ast.KindMinusMinusToken {
			return u.Operand
		}
	}
	if ast.IsPostfixUnaryExpression(n) {
		u := n.AsPostfixUnaryExpression()
		if u.Operator == ast.KindPlusPlusToken || u.Operator == ast.KindMinusMinusToken {
			return u.Operand
		}
	}
	return nil
}
