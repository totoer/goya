package refactor

import (
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/checker"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/nodebuilder"
	"github.com/totoer/goya/lsp/protocol"
)

const inferReturnName = "Infer function return type"
const inferReturnKind = "refactor.rewrite.function.returnType"

var inferReturnProvider = provider{name: inferReturnName, kinds: []string{inferReturnKind}, available: availableInferReturn, edits: editInferReturn}

func convertibleReturnDeclaration(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindFunctionDeclaration, ast.KindFunctionExpression, ast.KindArrowFunction, ast.KindMethodDeclaration:
		return true
	}
	return false
}
func inferReturnInfo(r *request) (declaration, typeNode *ast.Node, reason string) {
	if !kindBeginsWith(inferReturnKind, r.options.Kind) {
		return nil, nil, ""
	}
	token := astnav.GetTouchingPropertyName(r.file, r.span.Pos())
	for node := token; node != nil; node = node.Parent {
		if ast.IsBlock(node) || node.Parent != nil && ast.IsArrowFunction(node.Parent) && (node.Kind == ast.KindEqualsGreaterThanToken || node.Parent.Body() == node) {
			break
		}
		if convertibleReturnDeclaration(node) {
			declaration = node
			break
		}
	}
	if declaration == nil || declaration.Body() == nil || declaration.Type() != nil {
		return nil, nil, diagnostics.Return_type_must_be_inferred_from_a_function.Localize(locale.FromContext(r.ctx))
	}
	c := r.types()
	var returnType *checker.Type
	if c.ReferenceIsImplementationOfOverload(declaration) {
		signatures := c.GetCallSignatures(c.GetTypeAtLocation(declaration))
		if len(signatures) > 1 {
			types := []*checker.Type{}
			for _, signature := range signatures {
				if t := c.GetReturnTypeOfSignature(signature); t != nil {
					types = append(types, t)
				}
			}
			returnType = c.GetUnionType(types)
		}
	}
	if returnType == nil {
		signature := c.GetSignatureFromDeclaration(declaration)
		if signature != nil {
			predicate := c.GetTypePredicateOfSignature(signature)
			if predicate != nil && predicate.Type() != nil {
				node := c.ReferencePredicateTypeNode(predicate, declaration)
				if node != nil {
					return declaration, node, ""
				}
			} else {
				returnType = c.GetReturnTypeOfSignature(signature)
			}
		}
	}
	if returnType == nil {
		return nil, nil, diagnostics.Could_not_determine_function_return_type.Localize(locale.FromContext(r.ctx))
	}
	node := c.TypeToTypeNodeEx(returnType, declaration, nodebuilder.FlagsNoTruncation, nodebuilder.InternalFlagsAllowUnresolvedNames, nil)
	return declaration, node, ""
}
func availableInferReturn(r *request) []protocol.ApplicableRefactorInfo {
	declaration, typeNode, reason := inferReturnInfo(r)
	if (declaration == nil || typeNode == nil) && (!r.preferences.ProvideRefactorNotApplicableReason.IsTrue() || reason == "") {
		return nil
	}
	description := diagnostics.Infer_function_return_type.Localize(locale.FromContext(r.ctx))
	return []protocol.ApplicableRefactorInfo{{Name: inferReturnName, Description: description, Actions: []protocol.RefactorActionInfo{{Name: inferReturnName, Description: description, Kind: inferReturnKind, NotApplicableReason: reason}}}}
}
func editInferReturn(r *request, _ string, _ Arguments) (*EditInfo, error) {
	declaration, typeNode, _ := inferReturnInfo(r)
	if declaration == nil || typeNode == nil {
		return nil, nil
	}
	t := r.tracker()
	end := astnav.FindChildOfKind(declaration, ast.KindCloseParenToken, r.file)
	needParens := ast.IsArrowFunction(declaration) && end == nil
	if needParens && len(declaration.Parameters()) > 0 {
		end = declaration.Parameters()[0]
	}
	if end != nil {
		if needParens {
			t.InsertNodeBefore(r.file, end, t.NewToken(ast.KindOpenParenToken), false, change.LeadingTriviaOptionNone)
			t.InsertNodeAfter(r.file, end, t.NewToken(ast.KindCloseParenToken))
		}
		t.InsertNodeAt(r.file, core.TextPos(end.End()), typeNode, change.NodeOptions{Prefix: ": "})
	}
	return finish(t), r.ctx.Err()
}
