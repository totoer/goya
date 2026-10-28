package refactor

import (
	"strings"
	"unicode/utf8"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"github.com/totoer/goya/lsp/protocol"
)

const stringTemplateName = "Convert to template string"
const stringTemplateKind = "refactor.rewrite.string"

var stringTemplateProvider = provider{name: stringTemplateName, kinds: []string{stringTemplateKind}, available: availableStringTemplate, edits: editStringTemplate}

func nonEqualsBinary(node *ast.Node) bool {
	return ast.IsBinaryExpression(node) && node.AsBinaryExpression().OperatorToken.Kind != ast.KindEqualsToken && node.AsBinaryExpression().OperatorToken.Kind != ast.KindPlusEqualsToken
}
func parentBinary(node *ast.Node) *ast.Node {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		switch parent.Kind {
		case ast.KindPropertyAccessExpression, ast.KindElementAccessExpression:
			continue
		case ast.KindTemplateExpression, ast.KindBinaryExpression:
			if parent.Parent == nil || !nonEqualsBinary(parent.Parent) {
				return parent
			}
		default:
			return node
		}
	}
	return node
}

type concatenation struct {
	nodes, operators []*ast.Node
	hasString, valid bool
}

func stringTree(node *ast.Node) concatenation {
	if !ast.IsBinaryExpression(node) {
		return concatenation{nodes: []*ast.Node{node}, hasString: ast.IsStringLiteral(node) || ast.IsNoSubstitutionTemplateLiteral(node), valid: true}
	}
	binary := node.AsBinaryExpression()
	left := stringTree(binary.Left)
	if !(left.hasString || ast.IsStringLiteral(binary.Right) || ast.IsTemplateExpression(binary.Right)) {
		return concatenation{nodes: []*ast.Node{node}, valid: true}
	}
	left.nodes = append(left.nodes, binary.Right)
	left.operators = append(left.operators, binary.OperatorToken)
	left.hasString = true
	left.valid = left.valid && binary.OperatorToken.Kind == ast.KindPlusToken
	return left
}
func stringTemplateNode(r *request) *ast.Node {
	node := astnav.GetTokenAtPosition(r.file, r.span.Pos())
	nested := parentBinary(node)
	parts := stringTree(nested)
	if !(parts.hasString && parts.valid) && nested.Parent != nil && ast.IsParenthesizedExpression(nested.Parent) && nested.Parent.Parent != nil && ast.IsBinaryExpression(nested.Parent.Parent) {
		return nested.Parent.Parent
	}
	return node
}
func availableStringTemplate(r *request) []protocol.ApplicableRefactorInfo {
	node := parentBinary(stringTemplateNode(r))
	literal := ast.IsStringLiteral(node)
	if literal && !r.options.Invoked {
		return nil
	}
	parts := stringTree(node)
	reason := ""
	if !(ast.IsExpressionNode(node) && (literal || ast.IsBinaryExpression(node) && parts.hasString && parts.valid)) {
		if !r.preferences.ProvideRefactorNotApplicableReason.IsTrue() {
			return nil
		}
		reason = diagnostics.Can_only_convert_string_concatenations_and_string_literals.Localize(locale.FromContext(r.ctx))
	}
	description := diagnostics.Convert_to_template_string.Localize(locale.FromContext(r.ctx))
	return []protocol.ApplicableRefactorInfo{{Name: stringTemplateName, Description: description, Actions: []protocol.RefactorActionInfo{{Name: stringTemplateName, Description: description, Kind: stringTemplateKind, NotApplicableReason: reason}}}}
}
func escapeRawTemplate(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		c, size := utf8.DecodeRuneInString(text[i:])
		if c == '\\' && i+size < len(text) {
			next, n := utf8.DecodeRuneInString(text[i+size:])
			if next != '\r' && next != '\n' && next != '\u2028' && next != '\u2029' {
				b.WriteString(text[i : i+size+n])
				i += size + n
				continue
			}
		}
		if c == '$' || c == '`' {
			b.WriteByte('\\')
		}
		b.WriteString(text[i : i+size])
		i += size
	}
	return b.String()
}
func rawTemplatePart(r *request, node *ast.Node) string {
	raw := r.file.Text()[tokenStart(r, node):node.End()]
	end := len(raw) - 1
	if node.Kind == ast.KindTemplateHead || node.Kind == ast.KindTemplateMiddle {
		end--
	}
	if end < 1 {
		return ""
	}
	return raw[1:end]
}
func concatStrings(r *request, index int, nodes []*ast.Node) (next int, text, raw string, indexes []int) {
	for index < len(nodes) {
		node := nodes[index]
		if ast.IsStringLiteralLike(node) {
			text += node.Text()
			source := r.file.Text()[tokenStart(r, node):node.End()]
			if len(source) >= 2 {
				raw += escapeRawTemplate(source[1 : len(source)-1])
			}
			indexes = append(indexes, index)
			index++
		} else if ast.IsTemplateExpression(node) {
			head := node.AsTemplateExpression().Head
			text += head.Text()
			raw += rawTemplatePart(r, head)
			break
		} else {
			break
		}
	}
	return index, text, raw, indexes
}
func copyExpressionComments(r *request, t *change.Tracker, node *ast.Node) {
	expression := node.Expression()
	line := false
	copyTrailing(r, t, node, expression, ast.KindMultiLineCommentTrivia, &line)
	copyTrailingAsLeading(r, t, expression, expression, ast.KindMultiLineCommentTrivia, &line)
}
func nodesToTemplate(r *request, t *change.Tracker, parts concatenation) *ast.Node {
	line := false
	operatorComments := func(index int, target *ast.Node) {
		if index < len(parts.operators) {
			copyTrailing(r, t, parts.operators[index], target, ast.KindMultiLineCommentTrivia, &line)
		}
	}
	literalComments := func(indexes []int, target *ast.Node) {
		for _, index := range indexes {
			copyTrailing(r, t, parts.nodes[index], target, ast.KindMultiLineCommentTrivia, &line)
			operatorComments(index, target)
		}
	}
	begin, text, raw, indexes := concatStrings(r, 0, parts.nodes)
	if begin == len(parts.nodes) {
		node := t.NewNoSubstitutionTemplateLiteral(text, ast.TokenFlagsNone)
		node.TemplateLiteralLikeData().RawText = raw
		literalComments(indexes, node)
		return node
	}
	head := t.NewTemplateHead(text, raw, ast.TokenFlagsNone)
	literalComments(indexes, head)
	spans := []*ast.Node{}
	for i := begin; i < len(parts.nodes); i++ {
		current := parts.nodes[i]
		if ast.IsParenthesizedExpression(current) {
			copyExpressionComments(r, t, current)
			current = current.Expression()
		}
		operatorComments(i, current)
		next, text, raw, indexes := concatStrings(r, i+1, parts.nodes)
		i = next - 1
		last := i == len(parts.nodes)-1
		if ast.IsTemplateExpression(current) {
			original := current.AsTemplateExpression().TemplateSpans.Nodes
			for index, node := range original {
				copyExpressionComments(r, t, node)
				span := node.AsTemplateSpan()
				lastSpan := index == len(original)-1
				value, rawValue := span.Literal.Text(), rawTemplatePart(r, span.Literal)
				if lastSpan {
					value += text
					rawValue += raw
				}
				var literal *ast.Node
				if last && lastSpan {
					literal = t.NewTemplateTail(value, rawValue, ast.TokenFlagsNone)
				} else {
					literal = t.NewTemplateMiddle(value, rawValue, ast.TokenFlagsNone)
				}
				spans = append(spans, t.NewTemplateSpan(span.Expression, literal))
			}
		} else {
			var literal *ast.Node
			if last {
				literal = t.NewTemplateTail(text, raw, ast.TokenFlagsNone)
			} else {
				literal = t.NewTemplateMiddle(text, raw, ast.TokenFlagsNone)
			}
			literalComments(indexes, literal)
			spans = append(spans, t.NewTemplateSpan(current, literal))
		}
	}
	return t.NewTemplateExpression(head, t.NewNodeList(spans))
}
func editStringTemplate(r *request, action string, _ Arguments) (*EditInfo, error) {
	if action != diagnostics.Convert_to_template_string.Localize(locale.FromContext(r.ctx)) {
		return nil, &change.InvalidEdits{Reason: "invalid template-string action"}
	}
	node := parentBinary(stringTemplateNode(r))
	t := r.tracker()
	template := nodesToTemplate(r, t, stringTree(node))
	comments := scanner.GetTrailingCommentRanges(t.NodeFactory, r.file.Text(), node.End())
	first, last := -1, -1
	for comment := range comments {
		if first < 0 {
			first = comment.Pos()
		}
		last = comment.End()
	}
	if first >= 0 {
		t.DeleteRange(r.file, core.NewTextRange(first, last))
	}
	t.ReplaceNode(r.file, node, template, nil)
	return finish(t), r.ctx.Err()
}
