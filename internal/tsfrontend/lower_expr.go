package tsfrontend

import (
	"math/big"
	"strconv"
	"strings"

	ir "github.com/totoer/goya/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/stringutil"
	"github.com/totoer/goya/token"
	"github.com/totoer/goya/unistring"
)

var executionOperators = map[ast.Kind]token.Token{
	ast.KindPlusToken: token.PLUS, ast.KindMinusToken: token.MINUS, ast.KindAsteriskToken: token.MULTIPLY, ast.KindAsteriskAsteriskToken: token.EXPONENT, ast.KindSlashToken: token.SLASH, ast.KindPercentToken: token.REMAINDER,
	ast.KindAmpersandToken: token.AND, ast.KindBarToken: token.OR, ast.KindCaretToken: token.EXCLUSIVE_OR, ast.KindLessThanLessThanToken: token.SHIFT_LEFT, ast.KindGreaterThanGreaterThanToken: token.SHIFT_RIGHT, ast.KindGreaterThanGreaterThanGreaterThanToken: token.UNSIGNED_SHIFT_RIGHT,
	ast.KindAmpersandAmpersandToken: token.LOGICAL_AND, ast.KindBarBarToken: token.LOGICAL_OR, ast.KindQuestionQuestionToken: token.COALESCE,
	ast.KindEqualsEqualsToken: token.EQUAL, ast.KindEqualsEqualsEqualsToken: token.STRICT_EQUAL, ast.KindExclamationEqualsToken: token.NOT_EQUAL, ast.KindExclamationEqualsEqualsToken: token.STRICT_NOT_EQUAL,
	ast.KindLessThanToken: token.LESS, ast.KindGreaterThanToken: token.GREATER, ast.KindLessThanEqualsToken: token.LESS_OR_EQUAL, ast.KindGreaterThanEqualsToken: token.GREATER_OR_EQUAL,
	ast.KindInKeyword: token.IN, ast.KindInstanceOfKeyword: token.INSTANCEOF,
	ast.KindEqualsToken: token.ASSIGN, ast.KindExclamationToken: token.NOT, ast.KindTildeToken: token.BITWISE_NOT, ast.KindPlusPlusToken: token.INCREMENT, ast.KindMinusMinusToken: token.DECREMENT,
	ast.KindTypeOfKeyword: token.TYPEOF, ast.KindVoidKeyword: token.VOID, ast.KindDeleteKeyword: token.DELETE,
	ast.KindPlusEqualsToken: token.PLUS, ast.KindMinusEqualsToken: token.MINUS, ast.KindAsteriskEqualsToken: token.MULTIPLY, ast.KindAsteriskAsteriskEqualsToken: token.EXPONENT, ast.KindSlashEqualsToken: token.SLASH, ast.KindPercentEqualsToken: token.REMAINDER,
	ast.KindAmpersandEqualsToken: token.AND, ast.KindBarEqualsToken: token.OR, ast.KindCaretEqualsToken: token.EXCLUSIVE_OR, ast.KindLessThanLessThanEqualsToken: token.SHIFT_LEFT, ast.KindGreaterThanGreaterThanEqualsToken: token.SHIFT_RIGHT, ast.KindGreaterThanGreaterThanGreaterThanEqualsToken: token.UNSIGNED_SHIFT_RIGHT,
	ast.KindAmpersandAmpersandEqualsToken: token.LOGICAL_AND, ast.KindBarBarEqualsToken: token.LOGICAL_OR, ast.KindQuestionQuestionEqualsToken: token.COALESCE,
}

func (l *lowerer) operator(kind ast.Kind, n *ast.Node) token.Token {
	if op, ok := executionOperators[kind]; ok {
		return op
	}
	l.unsupported(n)
	return token.ILLEGAL
}

func executionString(text string) unistring.String {
	var runes []rune
	for text != "" {
		r, size := stringutil.DecodeJSStringRune(text)
		runes = append(runes, r)
		text = text[size:]
	}
	return unistring.NewFromRunes(runes)
}

func (l *lowerer) expression(n *ast.Node) (result ir.Expression) {
	if n == nil {
		return nil
	}
	defer func() { l.record(n, result) }()
	if n.Flags&ast.NodeFlagsOptionalChain != 0 {
		return &ir.OptionalChain{Expression: l.chainExpression(n)}
	}
	return l.plainExpression(n)
}
func (l *lowerer) chainExpression(n *ast.Node) ir.Expression {
	if n != nil && n.Flags&ast.NodeFlagsOptionalChain != 0 {
		result := l.plainExpression(n)
		l.record(n, result)
		return result
	}
	return l.expression(n)
}
func (l *lowerer) chainBase(n *ast.Node, optional bool) ir.Expression {
	base := l.chainExpression(n)
	if optional {
		base = &ir.Optional{Expression: base}
	}
	return base
}

func (l *lowerer) plainExpression(n *ast.Node) ir.Expression {
	switch n.Kind {
	case ast.KindIdentifier:
		if l.module.commonJS && n.Pos() < 0 && n.Parent == nil {
			var name unistring.String
			switch n.Text() {
			case "require":
				name = ModuleRequireName
			case "exports":
				name = ModuleExportsName
			case "module":
				name = ModuleObjectName
			}
			if name != "" {
				return &ir.Identifier{Name: name, Idx: l.idx(n)}
			}
		}
		if reference, ok := l.importedReference(n); ok {
			return l.importExpression(n, reference)
		}
		l.requireGlobal(n)
		return l.identifier(n)
	case ast.KindImportKeyword:
		return &ir.Identifier{Name: "\x00goya.dynamicImport", Idx: l.idx(n)}
	case ast.KindPrivateIdentifier:
		return &ir.PrivateIdentifier{Identifier: ir.Identifier{Name: unistring.NewFromString(strings.TrimPrefix(n.Text(), "#")), Idx: l.idx(n)}}
	case ast.KindThisKeyword:
		return &ir.ThisExpression{Idx: l.idx(n)}
	case ast.KindSuperKeyword:
		return &ir.SuperExpression{Idx: l.idx(n)}
	case ast.KindMetaProperty:
		if n.AsMetaProperty().KeywordToken == ast.KindImportKeyword {
			if n.Text() == "defer" {
				return &ir.Identifier{Name: "\x00goya.dynamicImport.defer", Idx: l.idx(n)}
			}
			return &ir.Identifier{Name: ModuleMetaName, Idx: l.idx(n)}
		}
		return &ir.MetaProperty{Meta: &ir.Identifier{Name: "new", Idx: l.idx(n)}, Property: &ir.Identifier{Name: "target", Idx: l.idx(n)}, Idx: l.idx(n)}
	case ast.KindTrueKeyword, ast.KindFalseKeyword:
		return &ir.BooleanLiteral{Idx: l.idx(n), Literal: n.Kind.String(), Value: n.Kind == ast.KindTrueKeyword}
	case ast.KindNullKeyword:
		return &ir.NullLiteral{Idx: l.idx(n), Literal: "null"}
	case ast.KindNumericLiteral, ast.KindBigIntLiteral:
		text := strings.ReplaceAll(n.Text(), "_", "")
		literal := l.text(n)
		if n.Kind == ast.KindBigIntLiteral {
			integer, ok := new(big.Int).SetString(strings.TrimSuffix(text, "n"), 0)
			if !ok {
				l.unsupported(n)
			}
			return &ir.NumberLiteral{Idx: l.idx(n), Literal: literal, Value: integer}
		}
		var number float64
		if strings.HasPrefix(strings.ToLower(text), "0x") || strings.HasPrefix(strings.ToLower(text), "0o") || strings.HasPrefix(strings.ToLower(text), "0b") {
			integer, ok := new(big.Int).SetString(text, 0)
			if !ok {
				l.unsupported(n)
			}
			number, _ = new(big.Float).SetInt(integer).Float64()
		} else {
			var err error
			number, err = strconv.ParseFloat(text, 64)
			if err != nil && number == 0 {
				l.unsupported(n)
			}
		}
		return &ir.NumberLiteral{Idx: l.idx(n), Literal: literal, Value: number}
	case ast.KindStringLiteral:
		return &ir.StringLiteral{Idx: l.idx(n), Literal: l.text(n), Value: executionString(n.Text())}
	case ast.KindRegularExpressionLiteral:
		text := l.text(n)
		last := strings.LastIndexByte(text, '/')
		if last < 1 {
			l.unsupported(n)
		}
		return &ir.RegExpLiteral{Idx: l.idx(n), Literal: text, Pattern: text[1:last], Flags: text[last+1:]}
	case ast.KindParenthesizedExpression, ast.KindPartiallyEmittedExpression, ast.KindAsExpression, ast.KindSatisfiesExpression, ast.KindTypeAssertionExpression, ast.KindNonNullExpression:
		return l.expression(n.Expression())
	case ast.KindBinaryExpression:
		x := n.AsBinaryExpression()
		kind := x.OperatorToken.Kind
		if kind == ast.KindCommaToken {
			return &ir.SequenceExpression{Sequence: []ir.Expression{l.expression(x.Left), l.expression(x.Right)}}
		}
		if ast.IsAssignmentOperator(kind) {
			return &ir.AssignExpression{Operator: l.operator(kind, n), Left: l.assignmentTarget(x.Left), Right: l.expression(x.Right)}
		}
		op := l.operator(kind, n)
		comparison := op == token.EQUAL || op == token.STRICT_EQUAL || op == token.NOT_EQUAL || op == token.STRICT_NOT_EQUAL || op == token.LESS || op == token.LESS_OR_EQUAL || op == token.GREATER || op == token.GREATER_OR_EQUAL || op == token.IN || op == token.INSTANCEOF
		return &ir.BinaryExpression{Operator: op, Left: l.expression(x.Left), Right: l.expression(x.Right), Comparison: comparison}
	case ast.KindPrefixUnaryExpression:
		x := n.AsPrefixUnaryExpression()
		return &ir.UnaryExpression{Operator: l.operator(x.Operator, n), Idx: l.idx(n), Operand: l.expression(x.Operand)}
	case ast.KindPostfixUnaryExpression:
		x := n.AsPostfixUnaryExpression()
		return &ir.UnaryExpression{Operator: l.operator(x.Operator, n), Idx: l.end(n) - 2, Operand: l.expression(x.Operand), Postfix: true}
	case ast.KindTypeOfExpression, ast.KindVoidExpression, ast.KindDeleteExpression:
		kind := ast.KindTypeOfKeyword
		if n.Kind == ast.KindVoidExpression {
			kind = ast.KindVoidKeyword
		}
		if n.Kind == ast.KindDeleteExpression {
			kind = ast.KindDeleteKeyword
		}
		return &ir.UnaryExpression{Operator: l.operator(kind, n), Idx: l.idx(n), Operand: l.expression(n.Expression())}
	case ast.KindConditionalExpression:
		x := n.AsConditionalExpression()
		return &ir.ConditionalExpression{Test: l.expression(x.Condition), Consequent: l.expression(x.WhenTrue), Alternate: l.expression(x.WhenFalse)}
	case ast.KindPropertyAccessExpression:
		l.requireAmbientMember(n)
		x := n.AsPropertyAccessExpression()
		left := l.chainBase(x.Expression, x.QuestionDotToken != nil)
		if x.Name().Kind == ast.KindPrivateIdentifier {
			return &ir.PrivateDotExpression{Left: left, Identifier: ir.PrivateIdentifier{Identifier: ir.Identifier{Name: unistring.NewFromString(strings.TrimPrefix(x.Name().Text(), "#")), Idx: l.idx(x.Name())}}}
		}
		return &ir.DotExpression{Left: left, Identifier: *l.identifier(x.Name())}
	case ast.KindElementAccessExpression:
		l.requireAmbientMember(n)
		x := n.AsElementAccessExpression()
		return &ir.BracketExpression{Left: l.chainBase(x.Expression, x.QuestionDotToken != nil), Member: l.expression(x.ArgumentExpression), LeftBracket: l.idx(x.ArgumentExpression) - 1, RightBracket: l.end(n) - 1}
	case ast.KindCallExpression:
		x := n.AsCallExpression()
		callee := l.callCallee(x.Expression)
		if x.QuestionDotToken != nil {
			callee = &ir.Optional{Expression: callee}
		}
		args := l.expressions(x.Arguments)
		if id, ok := callee.(*ir.Identifier); ok && id.Name == ModuleRequireName && len(args) == 1 && len(x.Arguments.Nodes) == 1 {
			if original := l.sourceNode(x.Arguments.Nodes[0]); original.Kind == ast.KindStringLiteral {
				args[0] = &ir.StringLiteral{Idx: l.idx(original), Value: executionString(original.Text())}
			}
		}
		if id, ok := callee.(*ir.Identifier); ok && id.Name == HelperIdentifierPrefix+"__asyncGenerator" {
			args = append(args, &ir.Identifier{Name: "\x00goya.currentCallee", Idx: l.idx(n)})
		}
		return &ir.CallExpression{Callee: callee, ArgumentList: args, LeftParenthesis: l.end(x.Expression), RightParenthesis: l.end(n) - 1}
	case ast.KindNewExpression:
		x := n.AsNewExpression()
		return &ir.NewExpression{New: l.idx(n), Callee: l.expression(x.Expression), ArgumentList: l.expressions(x.Arguments), LeftParenthesis: l.end(x.Expression), RightParenthesis: l.end(n) - 1}
	case ast.KindArrayLiteralExpression:
		return &ir.ArrayLiteral{LeftBracket: l.idx(n), RightBracket: l.end(n) - 1, Value: l.expressions(n.AsArrayLiteralExpression().Elements)}
	case ast.KindOmittedExpression:
		return nil
	case ast.KindSpreadElement, ast.KindSpreadAssignment:
		return &ir.SpreadElement{Expression: l.expression(n.Expression())}
	case ast.KindObjectLiteralExpression:
		result := &ir.ObjectLiteral{LeftBrace: l.idx(n), RightBrace: l.end(n) - 1}
		for _, p := range n.AsObjectLiteralExpression().Properties.Nodes {
			property := l.property(p)
			if l.module.json {
				property.(*ir.PropertyKeyed).Computed = true
			}
			result.Value = append(result.Value, property)
		}
		return result
	case ast.KindFunctionExpression:
		return l.function(n)
	case ast.KindClassExpression:
		return l.class(n)
	case ast.KindExpressionWithTypeArguments:
		return l.expression(n.Expression())
	case ast.KindArrowFunction:
		return l.arrow(n)
	case ast.KindAwaitExpression:
		return &ir.AwaitExpression{Await: l.idx(n), Argument: l.expression(n.Expression())}
	case ast.KindYieldExpression:
		x := n.AsYieldExpression()
		return &ir.YieldExpression{Yield: l.idx(n), Argument: l.expression(x.Expression), Delegate: x.AsteriskToken != nil}
	case ast.KindNoSubstitutionTemplateLiteral, ast.KindTemplateExpression:
		return l.template(n, nil)
	case ast.KindTaggedTemplateExpression:
		x := n.AsTaggedTemplateExpression()
		return l.template(x.Template, l.callCallee(x.Tag))
	}
	l.unsupported(n)
	return nil
}

func (l *lowerer) expressions(list *ast.NodeList) []ir.Expression {
	if list == nil {
		return nil
	}
	result := make([]ir.Expression, len(list.Nodes))
	for i, n := range list.Nodes {
		result[i] = l.expression(n)
	}
	return result
}
func (l *lowerer) propertyName(n *ast.Node) (ir.Expression, bool) {
	if n.Kind == ast.KindComputedPropertyName {
		return l.expression(n.Expression()), true
	}
	if n.Kind == ast.KindIdentifier {
		return &ir.StringLiteral{Idx: l.idx(n), Literal: n.Text(), Value: unistring.NewFromString(n.Text())}, false
	}
	return l.expression(n), false
}
func (l *lowerer) property(n *ast.Node) ir.Property {
	switch n.Kind {
	case ast.KindPropertyAssignment:
		x := n.AsPropertyAssignment()
		key, computed := l.propertyName(x.Name())
		return &ir.PropertyKeyed{Key: key, Computed: computed, Kind: ir.PropertyKindValue, Value: l.expression(x.Initializer)}
	case ast.KindShorthandPropertyAssignment:
		x := n.AsShorthandPropertyAssignment()
		if reference, ok := l.importedReference(x.Name()); ok {
			return &ir.PropertyKeyed{Key: &ir.StringLiteral{Idx: l.idx(x.Name()), Value: executionString(x.Name().Text())}, Kind: ir.PropertyKindValue, Value: l.importExpression(x.Name(), reference)}
		}
		l.requireGlobal(x.Name())
		return &ir.PropertyShort{Name: *l.identifier(x.Name()), Initializer: l.expression(x.ObjectAssignmentInitializer)}
	case ast.KindSpreadAssignment:
		return &ir.SpreadElement{Expression: l.expression(n.Expression())}
	case ast.KindMethodDeclaration, ast.KindGetAccessor, ast.KindSetAccessor:
		key, computed := l.propertyName(n.Name())
		kind := ir.PropertyKindMethod
		if n.Kind == ast.KindGetAccessor {
			kind = ir.PropertyKindGet
		}
		if n.Kind == ast.KindSetAccessor {
			kind = ir.PropertyKindSet
		}
		return &ir.PropertyKeyed{Key: key, Computed: computed, Kind: kind, Value: l.function(n)}
	}
	l.unsupported(n)
	return nil
}
func (l *lowerer) template(n *ast.Node, tag ir.Expression) *ir.TemplateLiteral {
	result := &ir.TemplateLiteral{OpenQuote: l.idx(n), CloseQuote: l.end(n) - 1, Tag: tag}
	element := func(literal *ast.Node) *ir.TemplateElement {
		text := l.text(literal)
		raw := text
		if len(raw) >= 2 {
			raw = raw[1 : len(raw)-1]
		}
		if strings.HasSuffix(raw, "$") {
			raw = strings.TrimSuffix(raw, "$")
		}
		raw = strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
		return &ir.TemplateElement{Idx: l.idx(literal), Literal: raw, Parsed: executionString(literal.Text()), Valid: literal.TemplateLiteralLikeData().TemplateFlags&ast.TokenFlagsContainsInvalidEscape == 0}
	}
	if n.Kind == ast.KindNoSubstitutionTemplateLiteral {
		result.Elements = []*ir.TemplateElement{element(n)}
		return result
	}
	x := n.AsTemplateExpression()
	result.Elements = append(result.Elements, element(x.Head))
	for _, span := range x.TemplateSpans.Nodes {
		s := span.AsTemplateSpan()
		result.Expressions = append(result.Expressions, l.expression(s.Expression))
		result.Elements = append(result.Elements, element(s.Literal))
	}
	return result
}
