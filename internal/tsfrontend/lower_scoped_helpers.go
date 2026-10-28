package tsfrontend

import (
	ir "github.com/totoer/goya/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/token"
	"github.com/totoer/goya/unistring"
)

func moduleHasAwait(n *ast.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == ast.KindAwaitExpression || n.Kind == ast.KindForOfStatement && n.AsForInOrOfStatement().AwaitModifier != nil {
		return true
	}
	if ast.IsFunctionLike(n) {
		return moduleHasAwait(n.Name())
	}
	return n.ForEachChild(func(child *ast.Node) bool { return moduleHasAwait(child) })
}

// Scoped super helpers are lexical closures with the method's home object.
// Construct operations directly, without printing or parsing helper text.
func (l *lowerer) addScopedHelpers(n *ast.Node, block *ir.BlockStatement) {
	for _, helper := range l.emit.GetEmitHelpers(n) {
		if !scopedHelper(helper.Name) {
			continue
		}
		advanced := helper.Name == "typescript:advanced-async-super"
		names := map[unistring.String]bool{}
		var collect func(*ast.Node)
		collect = func(node *ast.Node) {
			if node.Kind == ast.KindIdentifier && node.Text() == "_superIndex" && l.emit.HasAutoGenerateInfo(node) {
				id := l.identifier(node)
				if !names[id.Name] {
					names[id.Name] = true
					key := &ir.Identifier{Name: "\x00goya.superKey", Idx: l.idx(n)}
					value := &ir.Identifier{Name: "\x00goya.superValue", Idx: l.idx(n)}
					access := &ir.BracketExpression{Left: &ir.SuperExpression{Idx: l.idx(n)}, Member: key, LeftBracket: l.idx(n), RightBracket: l.end(n) - 1}
					getter := &ir.ArrowFunctionLiteral{Start: l.idx(n), End: l.end(n), ParameterList: &ir.ParameterList{List: []*ir.Binding{{Target: key}}}, Body: &ir.ExpressionBody{Expression: access}}
					var expr ir.Expression = getter
					if advanced {
						setter := &ir.ArrowFunctionLiteral{Start: l.idx(n), End: l.end(n), ParameterList: &ir.ParameterList{List: []*ir.Binding{{Target: key}, {Target: value}}}, Body: &ir.ExpressionBody{Expression: &ir.AssignExpression{Operator: token.ASSIGN, Left: access, Right: value}}}
						expr = &ir.CallExpression{Callee: &ir.Identifier{Name: HelperIdentifierPrefix + "__superIndex", Idx: l.idx(n)}, ArgumentList: []ir.Expression{getter, setter}, LeftParenthesis: l.idx(n), RightParenthesis: l.end(n) - 1}
					}
					block.List = append([]ir.Statement{&ir.LexicalDeclaration{Idx: l.idx(n), Token: token.CONST, List: []*ir.Binding{{Target: id, Initializer: expr}}}}, block.List...)
				}
			}
			node.ForEachChild(func(child *ast.Node) bool { collect(child); return false })
		}
		collect(n)
	}
}
