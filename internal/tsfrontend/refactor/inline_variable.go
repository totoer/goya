package refactor

import (
	"slices"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"github.com/totoer/goya/lsp/protocol"
)

const inlineName = "Inline variable"
const inlineKind = "refactor.inline.variable"

var inlineProvider = provider{name: inlineName, kinds: []string{inlineKind}, available: availableInline, edits: editInline}

type inlineInfo struct {
	declaration, replacement *ast.Node
	references               []*ast.Node
}

func initializedStatementVariable(node *ast.Node) bool {
	return ast.IsVariableDeclaration(node) && node.Initializer() != nil && node.Parent != nil && ast.IsVariableDeclarationList(node.Parent) && node.Parent.Parent != nil && ast.IsVariableStatement(node.Parent.Parent) && ast.IsIdentifier(node.Name())
}
func inliningInfo(r *request, fromReference bool) (*inlineInfo, string) {
	token := astnav.GetTouchingPropertyName(r.file, r.span.Pos())
	if !ast.IsIdentifier(token) {
		return nil, ""
	}
	c := r.types()
	var declaration *ast.Node
	multiple := func() (*inlineInfo, string) {
		return nil, diagnostics.Variables_with_multiple_declarations_cannot_be_inlined.Localize(locale.FromContext(r.ctx))
	}
	if initializedStatementVariable(token.Parent) {
		symbol := c.GetMergedSymbol(token.Parent.Symbol())
		if symbol == nil || len(symbol.Declarations) != 1 {
			return multiple()
		}
		declaration = token.Parent
	} else if fromReference {
		symbol := c.GetMergedSymbol(c.ResolveName(token.Text(), token, ast.SymbolFlagsValue, false))
		if symbol == nil || len(symbol.Declarations) != 1 {
			return multiple()
		}
		declaration = symbol.Declarations[0]
		if !initializedStatementVariable(declaration) {
			return nil, ""
		}
	} else {
		return nil, diagnostics.Could_not_find_variable_to_inline.Localize(locale.FromContext(r.ctx))
	}
	if declaration.Parent.Parent.ModifierFlags()&ast.ModifierFlagsExport != 0 {
		return nil, ""
	}
	symbol := c.GetMergedSymbol(declaration.Symbol())
	references := []*ast.Node{}
	for _, node := range c.GetReferencesToSymbolInFile(r.file, symbol) {
		if node == declaration.Name() {
			continue
		}
		parent := node.Parent
		if ast.IsWriteAccessForReference(node) && !ast.IsShorthandPropertyAssignment(parent) || ast.IsExportSpecifier(parent) || ast.IsExportAssignment(parent) || ast.IsTypeQueryNode(parent) || declaration.Pos() <= node.Pos() && node.Pos() <= declaration.End() {
			return nil, ""
		}
		references = append(references, node)
	}
	if len(references) == 0 {
		return nil, ""
	}
	return &inlineInfo{declaration: declaration, replacement: declaration.Initializer(), references: references}, ""
}
func availableInline(r *request) []protocol.ApplicableRefactorInfo {
	info, reason := inliningInfo(r, r.options.Invoked)
	if info == nil && (reason == "" || !r.preferences.ProvideRefactorNotApplicableReason.IsTrue()) {
		return nil
	}
	description := diagnostics.Inline_variable.Localize(locale.FromContext(r.ctx))
	return []protocol.ApplicableRefactorInfo{{Name: inlineName, Description: description, Actions: []protocol.RefactorActionInfo{{Name: inlineName, Description: description, Kind: inlineKind, NotApplicableReason: reason}}}}
}
func inlineReplacement(t *change.Tracker, reference, replacement *ast.Node) *ast.Node {
	replacement = cloneWithTrivia(t, replacement)
	parent := reference.Parent
	if ast.IsExpression(parent) && (ast.GetExpressionPrecedence(replacement) < ast.GetExpressionPrecedence(parent) || needsParentheses(parent)) {
		return t.NewParenthesizedExpression(replacement)
	}
	if ast.IsFunctionLike(replacement) && (ast.IsCallLikeExpression(parent) || ast.IsPropertyAccessExpression(parent)) {
		return t.NewParenthesizedExpression(replacement)
	}
	if ast.IsPropertyAccessExpression(parent) && (ast.IsNumericLiteral(replacement) || ast.IsObjectLiteralExpression(replacement)) {
		return t.NewParenthesizedExpression(replacement)
	}
	if ast.IsIdentifier(reference) && ast.IsShorthandPropertyAssignment(parent) {
		return t.NewPropertyAssignment(nil, reference, nil, nil, replacement)
	}
	return replacement
}
func editInline(r *request, action string, _ Arguments) (*EditInfo, error) {
	if action != inlineName {
		return nil, &change.InvalidEdits{Reason: "invalid inline-variable action"}
	}
	info, _ := inliningInfo(r, true)
	if info == nil {
		return nil, nil
	}
	t := r.tracker()
	for _, node := range info.references {
		var parent *ast.Node
		if ast.IsStringLiteral(info.replacement) && ast.IsIdentifier(node) {
			parent = ast.WalkUpParenthesizedExpressions(node.Parent)
		}
		if parent != nil && ast.IsTemplateSpan(parent) && !ast.IsTaggedTemplateExpression(parent.Parent.Parent) {
			expression := parent.Parent.AsTemplateExpression()
			index := slices.Index(expression.TemplateSpans.Nodes, parent)
			previous := expression.Head
			if index > 0 {
				previous = expression.TemplateSpans.Nodes[index-1]
			}
			text := strings.ReplaceAll(strings.ReplaceAll(info.replacement.Text(), "\\", "\\\\"), "`", "\\`")
			span := core.NewTextRange(previous.End()-2, scanner.GetTokenPosOfNode(parent.AsTemplateSpan().Literal, r.file, false)+1)
			t.ReplaceRangeWithText(r.file, r.converters.ToLSPRange(r.file, span), text)
		} else {
			t.ReplaceNode(r.file, node, inlineReplacement(t, node, info.replacement), nil)
		}
	}
	t.Delete(r.file, info.declaration)
	return finish(t), r.ctx.Err()
}

// Match getSynthesizedDeepClone: clone every leaf, preserve source trivia ranges,
// and assign parents only on the owned tree.
func cloneWithTrivia(t *change.Tracker, node *ast.Node) *ast.Node {
	if node == nil {
		return nil
	}
	var visitor *ast.NodeVisitor
	visitor = ast.NewNodeVisitor(func(node *ast.Node) *ast.Node {
		if node == nil {
			return nil
		}
		changed := node.VisitEachChild(visitor)
		if changed == node {
			changed = node.Clone(t)
		}
		// Native EmitContext.copyFrom omits synthetic comments. The pinned
		// synthesized deep clone carries these request-owned comments forward.
		if comments := t.GetSyntheticLeadingComments(node); len(comments) > 0 {
			t.SetSyntheticLeadingComments(changed, slices.Clone(comments))
		}
		if comments := t.GetSyntheticTrailingComments(node); len(comments) > 0 {
			t.SetSyntheticTrailingComments(changed, slices.Clone(comments))
		}
		return changed
	}, t.NodeFactory, ast.NodeVisitorHooks{})
	result := visitor.VisitNode(node)
	ast.SetParentInChildren(result)
	return result
}
