package refactor

import (
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/printer"
	"github.com/totoer/goya/lsp/protocol"
)

const overloadName = "Convert overload list to single signature"
const overloadKind = "refactor.rewrite.function.overloadList"

var overloadProvider = provider{name: overloadName, kinds: []string{overloadKind}, available: availableOverload, edits: editOverload}

func convertibleSignature(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindMethodSignature, ast.KindMethodDeclaration, ast.KindCallSignature, ast.KindConstructor, ast.KindConstructSignature, ast.KindFunctionDeclaration:
		return true
	}
	return false
}
func overloadInfo(r *request) []*ast.Node {
	token := astnav.GetTokenAtPosition(r.file, r.span.Pos())
	declaration := ancestor(token, convertibleSignature)
	if declaration == nil {
		return nil
	}
	if body := declaration.Body(); body != nil && body.Pos() <= r.span.Pos() && r.span.Pos() < body.End() {
		return nil
	}
	symbol := declaration.Symbol()
	if symbol == nil || len(symbol.Declarations) <= 1 {
		return nil
	}
	declarations := symbol.Declarations
	kind := declarations[0].Kind
	if !convertibleSignature(declarations[0]) {
		return nil
	}
	c := r.types()
	var firstReturnSet bool
	var firstReturn = c.GetUnknownType()
	for _, decl := range declarations {
		if ast.GetSourceFileOfNode(decl) != r.file || decl.Kind != kind || decl.TypeParameterList() != nil {
			return nil
		}
		for _, parameter := range decl.Parameters() {
			if parameter.Modifiers() != nil || !ast.IsIdentifier(parameter.Name()) {
				return nil
			}
		}
		signature := c.GetSignatureFromDeclaration(decl)
		if signature == nil {
			return nil
		}
		returned := c.GetReturnTypeOfSignature(signature)
		if !firstReturnSet {
			firstReturn = returned
			firstReturnSet = true
		} else if returned != firstReturn {
			return nil
		}
	}
	return declarations
}
func availableOverload(r *request) []protocol.ApplicableRefactorInfo {
	if overloadInfo(r) == nil {
		return nil
	}
	description := diagnostics.Convert_overload_list_to_single_signature.Localize(locale.FromContext(r.ctx))
	return []protocol.ApplicableRefactorInfo{{Name: overloadName, Description: description, Actions: []protocol.RefactorActionInfo{{Name: overloadName, Description: description, Kind: overloadKind}}}}
}
func overloadParameters(r *request, t *change.Tracker, declarations []*ast.Node) *ast.NodeList {
	last := declarations[len(declarations)-1]
	if ast.IsFunctionLikeDeclaration(last) && last.Body() != nil {
		declarations = declarations[:len(declarations)-1]
	}
	tuples := []*ast.Node{}
	for _, declaration := range declarations {
		members := []*ast.Node{}
		comments := false
		for _, node := range declaration.Parameters() {
			parameter := node.AsParameterDeclaration()
			typ := parameter.Type
			if typ == nil {
				typ = t.NewKeywordTypeNode(ast.KindAnyKeyword)
			}
			member := t.NewNamedTupleMember(parameter.DotDotDotToken, parameter.Name(), parameter.QuestionToken, typ)
			member.Loc = node.Loc
			if doc := r.service.GetSymbolDocumentationComment(r.types(), node.Symbol()); doc != "" {
				text := "*\n * " + strings.ReplaceAll(doc, "\n", "\n * ") + "\n "
				t.SetSyntheticLeadingComments(member, []printer.SynthesizedComment{{Kind: ast.KindMultiLineCommentTrivia, Loc: core.NewTextRange(-1, -1), Text: text, HasTrailingNewLine: true, HasLeadingNewLine: true}})
				comments = true
			}
			members = append(members, member)
		}
		tuple := t.NewTupleTypeNode(t.NewNodeList(members))
		if !comments {
			t.AddEmitFlags(tuple, printer.EFSingleLine)
		}
		tuples = append(tuples, tuple)
	}
	rest := t.NewParameterDeclaration(nil, t.NewToken(ast.KindDotDotDotToken), t.NewIdentifier("args"), nil, t.NewUnionTypeNode(t.NewNodeList(tuples)), nil)
	return t.NewNodeList([]*ast.Node{rest})
}
func editOverload(r *request, _ string, _ Arguments) (*EditInfo, error) {
	declarations := overloadInfo(r)
	if len(declarations) == 0 {
		return nil, nil
	}
	last := declarations[len(declarations)-1]
	t := r.tracker()
	parameters := overloadParameters(r, t, declarations)
	var updated *ast.Node
	switch last.Kind {
	case ast.KindMethodSignature:
		node := last.AsMethodSignatureDeclaration()
		updated = t.UpdateMethodSignatureDeclaration(node, node.Modifiers(), node.Name(), node.PostfixToken, node.TypeParameters, parameters, node.Type)
	case ast.KindMethodDeclaration:
		node := last.AsMethodDeclaration()
		updated = t.UpdateMethodDeclaration(node, node.Modifiers(), node.AsteriskToken, node.Name(), node.PostfixToken, node.TypeParameters, parameters, node.Type, node.FullSignature, node.Body)
	case ast.KindCallSignature:
		node := last.AsCallSignatureDeclaration()
		updated = t.UpdateCallSignatureDeclaration(node, node.TypeParameters, parameters, node.Type)
	case ast.KindConstructor:
		node := last.AsConstructorDeclaration()
		updated = t.UpdateConstructorDeclaration(node, node.Modifiers(), node.TypeParameters, parameters, node.Type, node.FullSignature, node.Body)
	case ast.KindConstructSignature:
		node := last.AsConstructSignatureDeclaration()
		updated = t.UpdateConstructSignatureDeclaration(node, node.TypeParameters, parameters, node.Type)
	case ast.KindFunctionDeclaration:
		node := last.AsFunctionDeclaration()
		updated = t.UpdateFunctionDeclaration(node, node.Modifiers(), node.AsteriskToken, node.Name(), node.TypeParameters, parameters, node.Type, node.FullSignature, node.Body)
	}
	if updated == nil || updated == last {
		return nil, nil
	}
	span := t.GetAdjustedRange(r.file, declarations[0], last, change.LeadingTriviaOptionExclude, change.TrailingTriviaOptionExclude)
	t.ReplaceRange(r.file, span, updated, change.NodeOptions{LeadingTriviaOption: change.LeadingTriviaOptionExclude, TrailingTriviaOption: change.TrailingTriviaOptionExclude})
	return finish(t), r.ctx.Err()
}
