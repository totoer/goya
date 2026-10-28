package refactor

import (
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/lsp/protocol"
)

const bracesName = "Add or remove braces in an arrow function"
const addBracesName = "Add braces to arrow function"
const removeBracesName = "Remove braces from arrow function"
const addBracesKind = "refactor.rewrite.arrow.braces.add"
const removeBracesKind = "refactor.rewrite.arrow.braces.remove"

var bracesProvider = provider{name: bracesName, kinds: []string{removeBracesKind}, available: availableBraces, edits: editBraces}

type bracesInfo struct {
	function, expression, statement *ast.Node
	add                             bool
}

func nodeContains(outer, inner *ast.Node) bool {
	return outer != nil && inner != nil && outer.Pos() <= inner.Pos() && inner.End() <= outer.End()
}
func convertibleBraces(r *request, considerBodies bool) (*bracesInfo, string) {
	token := astnav.GetTokenAtPosition(r.file, r.span.Pos())
	fn := ast.GetContainingFunction(token)
	if fn == nil {
		return nil, diagnostics.Could_not_find_a_containing_arrow_function.Localize(locale.FromContext(r.ctx))
	}
	if !ast.IsArrowFunction(fn) {
		return nil, diagnostics.Containing_function_is_not_an_arrow_function.Localize(locale.FromContext(r.ctx))
	}
	if !nodeContains(fn, token) || (nodeContains(fn.Body(), token) && !considerBodies) {
		return nil, ""
	}
	body := fn.Body()
	if ast.IsExpression(body) {
		return &bracesInfo{function: fn, expression: body, add: true}, ""
	}
	if ast.IsBlock(body) && len(body.Statements()) == 1 {
		statement := body.Statements()[0]
		if ast.IsReturnStatement(statement) {
			return &bracesInfo{function: fn, expression: statement.Expression(), statement: statement}, ""
		}
	}
	return nil, ""
}
func bracesAction(r *request, add bool) protocol.RefactorActionInfo {
	if add {
		return protocol.RefactorActionInfo{Name: addBracesName, Description: diagnostics.Add_braces_to_arrow_function.Localize(locale.FromContext(r.ctx)), Kind: addBracesKind}
	}
	return protocol.RefactorActionInfo{Name: removeBracesName, Description: diagnostics.Remove_braces_from_arrow_function.Localize(locale.FromContext(r.ctx)), Kind: removeBracesKind}
}
func availableBraces(r *request) []protocol.ApplicableRefactorInfo {
	info, reason := convertibleBraces(r, r.options.Invoked)
	if info == nil && (!r.preferences.ProvideRefactorNotApplicableReason.IsTrue() || reason == "") {
		return nil
	}
	group := protocol.ApplicableRefactorInfo{Name: bracesName, Description: diagnostics.Add_or_remove_braces_in_an_arrow_function.Localize(locale.FromContext(r.ctx)), Actions: []protocol.RefactorActionInfo{}}
	if info != nil {
		group.Actions = append(group.Actions, bracesAction(r, info.add))
	} else {
		for _, add := range []bool{true, false} {
			action := bracesAction(r, add)
			action.NotApplicableReason = reason
			group.Actions = append(group.Actions, action)
		}
	}
	return []protocol.ApplicableRefactorInfo{group}
}
func needsParentheses(node *ast.Node) bool {
	return ast.IsBinaryExpression(node) && node.AsBinaryExpression().OperatorToken.Kind == ast.KindCommaToken || ast.IsObjectLiteralExpression(node) || (ast.IsAsExpression(node) || ast.IsSatisfiesExpression(node)) && ast.IsObjectLiteralExpression(node.Expression())
}
func editBraces(r *request, action string, _ Arguments) (*EditInfo, error) {
	info, reason := convertibleBraces(r, true)
	if info == nil {
		return nil, &change.InvalidEdits{Reason: "arrow braces refactor is not applicable: " + reason}
	}
	t := r.tracker()
	var body *ast.Node
	switch action {
	case addBracesName:
		statement := t.NewReturnStatement(info.expression)
		body = t.NewBlock(t.NewNodeList([]*ast.Node{statement}), true)
		line := true
		copyLeading(r, t, info.expression, statement, ast.KindMultiLineCommentTrivia, &line)
	case removeBracesName:
		if info.statement == nil {
			return nil, &change.InvalidEdits{Reason: "arrow braces action has no return statement"}
		}
		body = info.expression
		if body == nil {
			body = t.NewVoidExpression(t.NewNumericLiteral("0", ast.TokenFlagsNone))
		}
		if body != nil && ast.IsObjectLiteralExpression(ast.GetLeftmostExpression(body, false)) {
			body = t.NewParenthesizedExpression(body)
		}
		if needsParentheses(body) {
			body = t.NewParenthesizedExpression(body)
		}
		line := false
		copyTrailingAsLeading(r, t, info.statement, body, ast.KindMultiLineCommentTrivia, &line)
		copyLeading(r, t, info.statement, body, ast.KindMultiLineCommentTrivia, &line)
		copyTrailing(r, t, info.statement, body, ast.KindMultiLineCommentTrivia, &line)
	default:
		return nil, &change.InvalidEdits{Reason: "invalid arrow braces action"}
	}
	t.ReplaceNode(r.file, info.function.Body(), body, nil)
	return finish(t), r.ctx.Err()
}
