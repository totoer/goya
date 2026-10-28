package refactor

import (
	"fmt"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"github.com/totoer/goya/lsp/protocol"
	"strconv"
	"strings"
)

const extractSymbolName = "Extract Symbol"
const extractFunctionKind = "refactor.extract.function"
const extractConstantKind = "refactor.extract.constant"

var extractSymbolProvider = provider{name: extractSymbolName, kinds: []string{extractConstantKind, extractFunctionKind}, available: availableExtractSymbol, edits: editExtractSymbol}

func extractionDescription(r *request, scope *ast.Node, constant bool, index int) string {
	what := "function"
	if ast.IsFunctionLikeDeclaration(scope) {
		what = "inner function"
	} else if ast.IsClassLike(scope) {
		what = "method"
	}
	if constant {
		what = "constant"
		if ast.IsClassLike(scope) {
			what = "readonly field"
		}
		if index == 0 && !ast.IsClassLike(scope) {
			return "Extract to " + what + " in enclosing scope"
		}
	}
	where := ""
	text := func(node *ast.Node) string { return r.file.Text()[tokenStart(r, node):node.End()] }
	switch scope.Kind {
	case ast.KindConstructor:
		where = "constructor"
	case ast.KindFunctionDeclaration, ast.KindFunctionExpression:
		where = "anonymous function"
		if scope.Name() != nil {
			where = "function '" + scope.Name().Text() + "'"
		}
	case ast.KindArrowFunction:
		where = "arrow function"
	case ast.KindMethodDeclaration:
		where = "method '" + text(scope.Name()) + "'"
	case ast.KindGetAccessor:
		where = "'get " + text(scope.Name()) + "'"
	case ast.KindSetAccessor:
		where = "'set " + text(scope.Name()) + "'"
	case ast.KindClassDeclaration:
		where = "anonymous class declaration"
		if scope.Name() != nil {
			where = "class '" + scope.Name().Text() + "'"
		}
	case ast.KindClassExpression:
		where = "anonymous class expression"
		if scope.Name() != nil {
			where = "class expression '" + scope.Name().Text() + "'"
		}
	case ast.KindModuleBlock:
		where = "namespace '" + text(scope.Parent.Name()) + "'"
	case ast.KindSourceFile:
		where = "global scope"
		if scope.AsSourceFile().ExternalModuleIndicator != nil {
			where = "module scope"
		}
	}
	return "Extract to " + what + " in " + where
}
func availableExtractSymbol(r *request) []protocol.ApplicableRefactorInfo {
	target, reason := getSymbolRange(r, r.options.Invoked)
	infos := []protocol.ApplicableRefactorInfo{}
	kinds := []string{extractFunctionKind, extractConstantKind}
	descriptions := []string{"Extract function", "Extract constant"}
	names := []string{"Extract Function", "Extract Constant"}
	if target == nil {
		if !r.preferences.ProvideRefactorNotApplicableReason.IsTrue() {
			return nil
		}
		for i, kind := range kinds {
			if kindBeginsWith(kind, r.options.Kind) {
				infos = append(infos, protocol.ApplicableRefactorInfo{Name: extractSymbolName, Description: descriptions[i], Actions: []protocol.RefactorActionInfo{{Name: names[i], Description: descriptions[i], Kind: protocol.CodeActionKind(kind), NotApplicableReason: reason}}})
			}
		}
		return infos
	}
	analysis := analyzeSymbolExtraction(r, target)
	span := r.converters.ToLSPRange(r.file, target.affected(r))
	affected := protocol.Range{Start: protocol.Position(span.Start), End: protocol.Position(span.End)}
	for i, kind := range kinds {
		if !kindBeginsWith(kind, r.options.Kind) {
			continue
		}
		actions := []protocol.RefactorActionInfo{}
		seen := map[string]bool{}
		var firstError *protocol.RefactorActionInfo
		for index, scope := range analysis.scopes {
			description := extractionDescription(r, scope.scope, i == 1, index)
			errors := scope.functionErrors
			prefix := "function"
			if i == 1 {
				errors = scope.constantErrors
				prefix = "constant"
			}
			action := protocol.RefactorActionInfo{Name: fmt.Sprintf("%s_scope_%d", prefix, index), Description: description, Kind: protocol.CodeActionKind(kind)}
			if len(errors) == 0 {
				if !seen[description] {
					seen[description] = true
					action.Range = &affected
					actions = append(actions, action)
				}
			} else if firstError == nil {
				action.NotApplicableReason = errors[0]
				firstError = &action
			}
		}
		if len(actions) == 0 && r.preferences.ProvideRefactorNotApplicableReason.IsTrue() && firstError != nil {
			actions = append(actions, *firstError)
		}
		if len(actions) > 0 {
			infos = append(infos, protocol.ApplicableRefactorInfo{Name: extractSymbolName, Description: descriptions[i], Actions: actions})
		}
	}
	return infos
}
func editExtractSymbol(r *request, action string, _ Arguments) (*EditInfo, error) {
	target, reason := getSymbolRange(r, true)
	if target == nil {
		return nil, &change.InvalidEdits{Reason: "symbol extraction is not applicable: " + reason}
	}
	constant := strings.HasPrefix(action, "constant_scope_")
	prefix := "function_scope_"
	if constant {
		prefix = "constant_scope_"
	}
	index, err := strconv.Atoi(strings.TrimPrefix(action, prefix))
	if err != nil || !strings.HasPrefix(action, prefix) || index < 0 {
		return nil, &change.InvalidEdits{Reason: "invalid extraction action"}
	}
	analysis := analyzeSymbolExtraction(r, target)
	if index >= len(analysis.scopes) {
		return nil, &change.InvalidEdits{Reason: "invalid extraction scope"}
	}
	scope := analysis.scopes[index]
	errors := scope.functionErrors
	if constant {
		errors = scope.constantErrors
	}
	if len(errors) > 0 {
		return nil, &change.InvalidEdits{Reason: errors[0]}
	}
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	if constant {
		return extractConstant(r, analysis, scope)
	}
	return extractFunction(r, analysis, scope)
}
func extractionStatements(scope *ast.Node) []*ast.Node {
	if ast.IsFunctionLikeDeclaration(scope) {
		if scope.Body() != nil && ast.IsBlock(scope.Body()) {
			return scope.Body().Statements()
		}
		return nil
	}
	if ast.IsClassLike(scope) {
		return scope.Members()
	}
	return scope.Statements()
}
func extractionInsertFunctionBefore(min int, scope *ast.Node) *ast.Node {
	for _, child := range extractionStatements(scope) {
		if child.Pos() >= min && ast.IsFunctionLikeDeclaration(child) && !ast.IsConstructorDeclaration(child) {
			return child
		}
	}
	return nil
}
func extractionInsertPropertyBefore(max int, scope *ast.Node) *ast.Node {
	members := scope.Members()
	var prev *ast.Node
	allProperties := true
	for _, member := range members {
		if member.Pos() > max {
			if prev != nil {
				return prev
			}
			return members[0]
		}
		if allProperties && !ast.IsPropertyDeclaration(member) {
			if prev != nil {
				return member
			}
			allProperties = false
		}
		prev = member
	}
	return prev
}
func extractionInsertConstantBefore(node, scope *ast.Node) *ast.Node {
	var previous *ast.Node
	for curr := node; curr != nil && curr != scope; curr = curr.Parent {
		if extractionScope(curr) {
			previous = curr
		}
	}
	if previous == nil {
		previous = node
	}
	for curr := previous.Parent; curr != nil; curr = curr.Parent {
		if extractionBlockLike(curr) {
			var prev *ast.Node
			for _, statement := range curr.Statements() {
				if statement.Pos() > node.Pos() {
					break
				}
				prev = statement
			}
			if prev == nil && ast.IsCaseClause(curr) {
				return curr.Parent.Parent
			}
			return prev
		}
		if curr == scope {
			break
		}
	}
	return nil
}
func extractionNodeName(r *request, node, scope *ast.Node) string {
	if ast.IsPropertyAccessExpression(node) && !ast.IsClassLike(scope) {
		name := node.Name()
		if !ast.IsPrivateIdentifier(name) && scanner.IdentifierToKeywordKind(name.AsIdentifier()) == ast.KindUnknown && r.types().ResolveName(name.Text(), node, ast.SymbolFlagsValue, false) == nil {
			return name.Text()
		}
	}
	base := "newLocal"
	if ast.IsClassLike(scope) {
		base = "newProperty"
	}
	return uniqueName(base, r.file)
}
