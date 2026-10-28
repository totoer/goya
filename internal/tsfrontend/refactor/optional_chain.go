package refactor

import (
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/lsp/protocol"
)

const optionalChainName = "Convert to optional chain expression"
const optionalChainKind = "refactor.rewrite.expression.optionalChain"

var optionalChainProvider = provider{name: optionalChainName, kinds: []string{optionalChainKind}, available: availableOptionalChain, edits: editOptionalChain}

type optionalChainInfo struct {
	final, expression *ast.Node
	occurrences       []*ast.Node
}

func validChainExpression(node *ast.Node) bool {
	return ast.IsBinaryExpression(node) || ast.IsConditionalExpression(node)
}
func validChainExpressionStatement(node *ast.Node) bool {
	return validChainExpression(node) || ast.IsExpressionStatement(node) || ast.IsReturnStatement(node) || ast.IsVariableStatement(node)
}
func chainStatementExpression(node *ast.Node) *ast.Node {
	if validChainExpression(node) {
		return node
	}
	if ast.IsVariableStatement(node) {
		declarations := node.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
		if len(declarations) == 1 {
			if expression := declarations[0].Initializer(); expression != nil && validChainExpression(expression) {
				return expression
			}
		}
		return nil
	}
	if expression := node.Expression(); expression != nil && validChainExpression(expression) {
		return expression
	}
	return nil
}
func chainFinalExpression(node *ast.Node) *ast.Node {
	node = ast.SkipParentheses(node)
	if ast.IsBinaryExpression(node) {
		return chainFinalExpression(node.AsBinaryExpression().Left)
	}
	if (ast.IsPropertyAccessExpression(node) || ast.IsElementAccessExpression(node) || ast.IsCallExpression(node)) && !ast.IsOptionalChain(node) {
		return node
	}
	return nil
}
func chainText(r *request, node *ast.Node) string {
	return r.file.Text()[tokenStart(r, node):node.End()]
}
func chainNodeText(r *request, node *ast.Node) string {
	if ast.IsIdentifier(node) || ast.IsStringOrNumericLiteralLike(node) {
		return chainText(r, node)
	}
	if ast.IsPropertyAccessExpression(node) {
		return chainNodeText(r, node.Name())
	}
	if ast.IsElementAccessExpression(node) {
		return chainNodeText(r, node.AsElementAccessExpression().ArgumentExpression)
	}
	return "" // The reference's undefined text for unsupported chain components.
}
func chainStartsWith(r *request, chain, subchain *ast.Node) bool {
	for ast.IsCallExpression(chain) || ast.IsPropertyAccessExpression(chain) || ast.IsElementAccessExpression(chain) {
		if chainNodeText(r, chain) == chainNodeText(r, subchain) {
			break
		}
		chain = chain.Expression()
	}
	for ast.IsPropertyAccessExpression(chain) && ast.IsPropertyAccessExpression(subchain) || ast.IsElementAccessExpression(chain) && ast.IsElementAccessExpression(subchain) {
		if chainNodeText(r, chain) != chainNodeText(r, subchain) {
			return false
		}
		chain = chain.Expression()
		subchain = subchain.Expression()
	}
	return ast.IsIdentifier(chain) && ast.IsIdentifier(subchain) && chainText(r, chain) == chainText(r, subchain)
}
func matchingChainStart(r *request, chain, subchain *ast.Node) *ast.Node {
	if !(ast.IsIdentifier(subchain) || ast.IsPropertyAccessExpression(subchain) || ast.IsElementAccessExpression(subchain)) {
		return nil
	}
	if chainStartsWith(r, chain, subchain) {
		return subchain
	}
	return nil
}
func chainOccurrences(r *request, matchTo, expression *ast.Node) []*ast.Node {
	result := []*ast.Node{}
	for ast.IsBinaryExpression(expression) && expression.AsBinaryExpression().OperatorToken.Kind == ast.KindAmpersandAmpersandToken {
		match := matchingChainStart(r, ast.SkipParentheses(matchTo), ast.SkipParentheses(expression.AsBinaryExpression().Right))
		if match == nil {
			break
		}
		result = append(result, match)
		matchTo = match
		expression = expression.AsBinaryExpression().Left
	}
	if match := matchingChainStart(r, matchTo, expression); match != nil {
		result = append(result, match)
	}
	return result
}
func optionalChainRange(r *request, considerEmpty bool) (*optionalChainInfo, string) {
	empty := r.span.Len() == 0
	if empty && !considerEmpty {
		return nil, ""
	}
	start := astnav.GetTokenAtPosition(r.file, r.span.Pos())
	end := astnav.GetTokenAtPosition(r.file, r.span.End())
	if !(ast.IsTokenKind(end.Kind) && r.span.End() > tokenStart(r, end) && r.span.End() < end.End()) {
		end = astnav.FindPrecedingToken(r.file, r.span.End())
	}
	adjustedEnd := start.End()
	if end != nil && end.End() >= start.Pos() {
		adjustedEnd = end.End()
	}
	adjusted := core.NewTextRange(start.Pos(), adjustedEnd)
	var parent *ast.Node
	for node := start; node.Parent != nil; node = node.Parent {
		if validChainExpressionStatement(node) && (empty && !validChainExpressionStatement(node.Parent) || !empty && adjusted.Len() != 0 && node.End() >= adjusted.End()) {
			parent = node
			break
		}
	}
	reason := diagnostics.Could_not_find_convertible_access_expression.Localize(locale.FromContext(r.ctx))
	if parent == nil {
		return nil, reason
	}
	expression := chainStatementExpression(parent)
	if expression == nil {
		return nil, reason
	}
	info := &optionalChainInfo{expression: expression}
	if ast.IsConditionalExpression(expression) {
		conditional := expression.AsConditionalExpression()
		info.final = chainFinalExpression(conditional.WhenTrue)
		if info.final == nil || r.types().IsNullableType(r.types().GetTypeAtLocation(info.final)) {
			return nil, reason
		}
		condition := conditional.Condition
		if (ast.IsPropertyAccessExpression(condition) || ast.IsIdentifier(condition)) && matchingChainStart(r, condition, info.final.Expression()) != nil {
			info.occurrences = []*ast.Node{condition}
		} else if ast.IsBinaryExpression(condition) {
			info.occurrences = chainOccurrences(r, info.final.Expression(), condition)
			if len(info.occurrences) == 0 {
				return nil, diagnostics.Could_not_find_matching_access_expressions.Localize(locale.FromContext(r.ctx))
			}
		} else {
			return nil, ""
		}
	} else {
		binary := expression.AsBinaryExpression()
		if binary.OperatorToken.Kind != ast.KindAmpersandAmpersandToken {
			return nil, diagnostics.Can_only_convert_logical_AND_access_chains.Localize(locale.FromContext(r.ctx))
		}
		info.final = chainFinalExpression(binary.Right)
		if info.final == nil {
			return nil, reason
		}
		info.occurrences = chainOccurrences(r, info.final.Expression(), binary.Left)
		if len(info.occurrences) == 0 {
			return nil, diagnostics.Could_not_find_matching_access_expressions.Localize(locale.FromContext(r.ctx))
		}
	}
	if len(info.occurrences) == 0 {
		return nil, ""
	}
	return info, ""
}
func availableOptionalChain(r *request) []protocol.ApplicableRefactorInfo {
	info, reason := optionalChainRange(r, r.options.Invoked)
	if info == nil && (reason == "" || !r.preferences.ProvideRefactorNotApplicableReason.IsTrue()) {
		return nil
	}
	description := diagnostics.Convert_to_optional_chain_expression.Localize(locale.FromContext(r.ctx))
	return []protocol.ApplicableRefactorInfo{{Name: optionalChainName, Description: description, Actions: []protocol.RefactorActionInfo{{Name: optionalChainName, Description: description, Kind: optionalChainKind, NotApplicableReason: reason}}}}
}
func convertChainOccurrences(r *request, t *change.Tracker, node *ast.Node, occurrences *[]*ast.Node) *ast.Node {
	if !(ast.IsPropertyAccessExpression(node) || ast.IsElementAccessExpression(node) || ast.IsCallExpression(node)) {
		return node
	}
	head := convertChainOccurrences(r, t, node.Expression(), occurrences)
	found := len(*occurrences) > 0 && chainText(r, (*occurrences)[len(*occurrences)-1]) == chainText(r, node.Expression())
	if found {
		*occurrences = (*occurrences)[:len(*occurrences)-1]
	}
	var question *ast.Node
	switch node.Kind {
	case ast.KindCallExpression:
		call := node.AsCallExpression()
		question = call.QuestionDotToken
		if found {
			question = t.NewToken(ast.KindQuestionDotToken)
		}
		return t.NewCallExpression(head, question, call.TypeArguments, call.Arguments, ast.NodeFlagsOptionalChain)
	case ast.KindPropertyAccessExpression:
		access := node.AsPropertyAccessExpression()
		question = access.QuestionDotToken
		if found {
			question = t.NewToken(ast.KindQuestionDotToken)
		}
		return t.NewPropertyAccessExpression(head, question, access.Name(), ast.NodeFlagsOptionalChain)
	default:
		access := node.AsElementAccessExpression()
		question = access.QuestionDotToken
		if found {
			question = t.NewToken(ast.KindQuestionDotToken)
		}
		return t.NewElementAccessExpression(head, question, access.ArgumentExpression, ast.NodeFlagsOptionalChain)
	}
}
func editOptionalChain(r *request, _ string, _ Arguments) (*EditInfo, error) {
	info, reason := optionalChainRange(r, true)
	if info == nil {
		return nil, &change.InvalidEdits{Reason: "optional-chain conversion is not applicable: " + reason}
	}
	t := r.tracker()
	first := info.occurrences[len(info.occurrences)-1]
	converted := convertChainOccurrences(r, t, info.final, &info.occurrences)
	if ast.IsBinaryExpression(info.expression) {
		span := t.GetAdjustedRange(r.file, first, info.final, change.LeadingTriviaOptionExclude, change.TrailingTriviaOptionExclude)
		t.ReplaceRange(r.file, span, converted, change.NodeOptions{LeadingTriviaOption: change.LeadingTriviaOptionExclude, TrailingTriviaOption: change.TrailingTriviaOptionExclude})
	} else {
		changed := t.NewBinaryExpression(nil, converted, nil, t.NewToken(ast.KindQuestionQuestionToken), info.expression.AsConditionalExpression().WhenFalse)
		t.ReplaceNode(r.file, info.expression, changed, nil)
	}
	return finish(t), r.ctx.Err()
}
