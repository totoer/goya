package refactor

import (
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/lsp/protocol"
)

const functionName = "Convert arrow function or function expression"
const anonymousName = "Convert to anonymous function"
const namedName = "Convert to named function"
const arrowName = "Convert to arrow function"
const anonymousKind = "refactor.rewrite.function.anonymous"
const namedKind = "refactor.rewrite.function.named"
const arrowKind = "refactor.rewrite.function.arrow"

var functionProvider = provider{name: functionName, kinds: []string{anonymousKind, namedKind, arrowKind}, available: availableFunction, edits: editFunction}

type functionInfo struct {
	node             *ast.Node
	selectedVariable bool
}

func containingThis(node *ast.Node) bool {
	if node == nil {
		return false
	}
	found := false
	var visit func(*ast.Node) bool
	visit = func(child *ast.Node) bool {
		if child.Kind == ast.KindThisKeyword || ast.IsThisIdentifier(child) {
			found = true
			return true
		}
		if !ast.IsClassLike(child) && !ast.IsFunctionDeclaration(child) && !ast.IsFunctionExpression(child) {
			return child.ForEachChild(visit)
		}
		return false
	}
	node.ForEachChild(visit)
	return found
}
func functionReferenced(r *request, node *ast.Node) bool {
	name := node.Name()
	if name == nil {
		return false
	}
	ch := r.types()
	symbol := ch.GetSymbolAtLocation(name)
	return symbol != nil && ch.IsSymbolReferencedInFile(r.file, name.AsIdentifier(), symbol)
}
func functionFromVariable(r *request, parent *ast.Node) *ast.Node {
	if parent == nil {
		return nil
	}
	var declaration *ast.Node
	if ast.IsVariableDeclaration(parent) {
		declaration = parent
	} else if ast.IsVariableDeclarationList(parent) && len(parent.AsVariableDeclarationList().Declarations.Nodes) == 1 {
		declaration = parent.AsVariableDeclarationList().Declarations.Nodes[0]
	}
	if declaration == nil {
		return nil
	}
	initializer := declaration.Initializer()
	if initializer != nil && (ast.IsArrowFunction(initializer) || ast.IsFunctionExpression(initializer) && !functionReferenced(r, initializer)) {
		return initializer
	}
	return nil
}
func getFunctionInfo(r *request) *functionInfo {
	token := astnav.GetTokenAtPosition(r.file, r.span.Pos())
	if token == nil {
		return nil
	}
	ch := r.types()
	if fn := functionFromVariable(r, token.Parent); fn != nil && !containingThis(fn.Body()) && !ch.ReferenceContainsArguments(fn) {
		return &functionInfo{node: fn, selectedVariable: true}
	}
	fn := ast.GetContainingFunction(token)
	if fn != nil && (ast.IsFunctionExpression(fn) || ast.IsArrowFunction(fn)) && !nodeContains(fn.Body(), token) && !containingThis(fn.Body()) && !ch.ReferenceContainsArguments(fn) {
		if ast.IsFunctionExpression(fn) && functionReferenced(r, fn) {
			return nil
		}
		return &functionInfo{node: fn}
	}
	return nil
}
func functionAction(r *request, name string) protocol.RefactorActionInfo {
	kind := ""
	var message *diagnostics.Message
	switch name {
	case namedName:
		kind = namedKind
		message = diagnostics.Convert_to_named_function
	case anonymousName:
		kind = anonymousKind
		message = diagnostics.Convert_to_anonymous_function
	case arrowName:
		kind = arrowKind
		message = diagnostics.Convert_to_arrow_function
	}
	return protocol.RefactorActionInfo{Name: name, Description: message.Localize(locale.FromContext(r.ctx)), Kind: protocol.CodeActionKind(kind)}
}
func availableFunction(r *request) []protocol.ApplicableRefactorInfo {
	info := getFunctionInfo(r)
	if info == nil {
		return nil
	}
	possible, errors := []protocol.RefactorActionInfo{}, []protocol.RefactorActionInfo{}
	add := func(name, kind string, applicable bool, reason *diagnostics.Message) {
		if !kindBeginsWith(kind, r.options.Kind) {
			return
		}
		action := functionAction(r, name)
		if applicable {
			possible = append(possible, action)
		} else {
			action.NotApplicableReason = reason.Localize(locale.FromContext(r.ctx))
			errors = append(errors, action)
		}
	}
	add(namedName, namedKind, info.selectedVariable || ast.IsArrowFunction(info.node) && ast.IsVariableDeclaration(info.node.Parent), diagnostics.Could_not_convert_to_named_function)
	add(anonymousName, anonymousKind, !info.selectedVariable && ast.IsArrowFunction(info.node), diagnostics.Could_not_convert_to_anonymous_function)
	add(arrowName, arrowKind, ast.IsFunctionExpression(info.node), diagnostics.Could_not_convert_to_arrow_function)
	if len(possible) == 0 && r.preferences.ProvideRefactorNotApplicableReason.IsTrue() {
		possible = errors
	}
	return []protocol.ApplicableRefactorInfo{{Name: functionName, Description: diagnostics.Convert_arrow_function_or_function_expression.Localize(locale.FromContext(r.ctx)), Actions: possible}}
}
func functionBlock(r *request, t *change.Tracker, body *ast.Node) *ast.Node {
	if !ast.IsExpression(body) {
		return body
	}
	statement := t.NewReturnStatement(body)
	statement.Loc = body.Loc
	suppressTrivia(t, statement, true)
	suppressTrivia(t, statement, false)
	line := true
	copyTrailingAsLeading(r, t, body, statement, ast.KindUnknown, &line)
	return t.NewBlock(t.NewNodeList([]*ast.Node{statement}), true)
}
func variableInfo(fn *ast.Node) (declaration, list, statement, name *ast.Node) {
	declaration = fn.Parent
	if declaration == nil || !ast.IsVariableDeclaration(declaration) || !ast.IsVariableDeclarationList(declaration.Parent) || declaration.Parent.Parent == nil || !ast.IsVariableStatement(declaration.Parent.Parent) || !ast.IsIdentifier(declaration.Name()) {
		return nil, nil, nil, nil
	}
	return declaration, declaration.Parent, declaration.Parent.Parent, declaration.Name()
}
func editFunction(r *request, action string, _ Arguments) (*EditInfo, error) {
	info := getFunctionInfo(r)
	if info == nil {
		return nil, nil
	}
	fn := info.node
	t := r.tracker()
	var asterisk *ast.Node
	if ast.IsFunctionExpression(fn) {
		asterisk = fn.AsFunctionExpression().AsteriskToken
	}
	switch action {
	case anonymousName:
		body := functionBlock(r, t, fn.Body())
		node := t.NewFunctionExpression(fn.Modifiers(), asterisk, nil, fn.TypeParameterList(), fn.ParameterList(), fn.Type(), fn.FunctionLikeData().FullSignature, body)
		t.ReplaceNode(r.file, fn, node, nil)
	case namedName:
		declaration, list, statement, name := variableInfo(fn)
		if declaration == nil {
			return nil, nil
		}
		body := functionBlock(r, t, fn.Body())
		suppressTrivia(t, statement, true)
		flags := ast.GetCombinedModifierFlags(declaration)&ast.ModifierFlagsExport | fn.ModifierFlags()
		modifiers := ast.CreateModifiersFromModifierFlags(flags, t.NewModifier)
		var modifierList *ast.ModifierList
		if len(modifiers) > 0 {
			modifierList = t.NewModifierList(modifiers)
		}
		node := t.NewFunctionDeclaration(modifierList, asterisk, name, fn.TypeParameterList(), fn.ParameterList(), fn.Type(), fn.FunctionLikeData().FullSignature, body)
		if len(list.AsVariableDeclarationList().Declarations.Nodes) == 1 {
			t.ReplaceNode(r.file, statement, node, nil)
		} else {
			t.Delete(r.file, declaration)
			t.InsertNodeAfter(r.file, statement, node)
		}
	case arrowName:
		if !ast.IsFunctionExpression(fn) {
			return nil, nil
		}
		body := fn.Body()
		statements := body.Statements()
		if len(statements) == 1 && ast.IsReturnStatement(statements[0]) && statements[0].Expression() != nil {
			head := statements[0]
			body = head.Expression()
			suppressTrivia(t, body, true)
			suppressTrivia(t, body, false)
			copyComments(r, t, head, body)
		}
		node := t.NewArrowFunction(fn.Modifiers(), fn.TypeParameterList(), fn.ParameterList(), fn.Type(), fn.FunctionLikeData().FullSignature, t.NewToken(ast.KindEqualsGreaterThanToken), body)
		t.ReplaceNode(r.file, fn, node, nil)
	default:
		return nil, &change.InvalidEdits{Reason: "invalid function refactor action"}
	}
	return finish(t), r.ctx.Err()
}
