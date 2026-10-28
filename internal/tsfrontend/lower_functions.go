package tsfrontend

import (
	ir "github.com/totoer/goya/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/token"
)

func (l *lowerer) parameters(n *ast.Node) *ir.ParameterList {
	parameters := n.ParameterList()
	result := &ir.ParameterList{Opening: l.idx(n), Closing: l.idx(n.Body())}
	if parameters == nil {
		return result
	}
	for _, parameter := range parameters.Nodes {
		p := parameter.AsParameterDeclaration()
		// The special TypeScript this parameter has no runtime argument slot.
		if p.Name().Kind == ast.KindIdentifier && p.Name().Text() == "this" {
			continue
		}
		if p.DotDotDotToken != nil {
			result.Rest = l.bindingTarget(p.Name())
			continue
		}
		result.List = append(result.List, &ir.Binding{Target: l.bindingTarget(p.Name()), Initializer: l.expression(p.Initializer)})
	}
	return result
}

func (l *lowerer) function(n *ast.Node) *ir.FunctionLiteral {
	parentDeclarations := l.declarations
	l.declarations = nil
	defer func() { l.declarations = parentDeclarations }()
	result := &ir.FunctionLiteral{Function: l.idx(n), Source: l.text(n), Async: n.ModifierFlags()&ast.ModifierFlagsAsync != 0, Generator: n.BodyData().AsteriskToken != nil}
	original := l.sourceNode(n)
	if ast.IsFunctionLike(original) && original.BodyData().AsteriskToken != nil && original.ModifierFlags()&ast.ModifierFlagsAsync != 0 && !result.Async && !result.Generator {
		result.NormalizedAsyncGenerator = true
	}
	if ast.IsFunctionLike(original) && original.BodyData().AsteriskToken == nil && original.ModifierFlags()&ast.ModifierFlagsAsync != 0 && !result.Async && !result.Generator {
		result.NormalizedAsync = true
	}
	if n.Kind == ast.KindFunctionDeclaration || n.Kind == ast.KindFunctionExpression {
		result.Name = l.identifier(n.Name())
	}
	result.ParameterList = l.parameters(n)
	result.Body = l.block(n.Body())
	result.DeclarationList = l.declarations
	l.record(n, result)
	return result
}
func (l *lowerer) arrow(n *ast.Node) *ir.ArrowFunctionLiteral {
	parentDeclarations := l.declarations
	l.declarations = nil
	defer func() { l.declarations = parentDeclarations }()
	result := &ir.ArrowFunctionLiteral{Start: l.idx(n), End: l.end(n), Source: l.text(n), Async: n.ModifierFlags()&ast.ModifierFlagsAsync != 0}
	if original := l.sourceNode(n); original.ModifierFlags()&ast.ModifierFlagsAsync != 0 && !result.Async {
		result.NormalizedAsync = true
	}
	result.ParameterList = l.parameters(n)
	if n.Body().Kind == ast.KindBlock {
		result.Body = l.block(n.Body())
	} else {
		result.Body = &ir.ExpressionBody{Expression: l.expression(n.Body())}
	}
	result.DeclarationList = l.declarations
	l.record(n, result)
	return result
}

func (l *lowerer) bindingTarget(n *ast.Node) ir.BindingTarget {
	result := l.assignmentTarget(n)
	if binding, ok := result.(ir.BindingTarget); ok {
		return binding
	}
	l.unsupported(n)
	return nil
}
func (l *lowerer) assignmentTarget(n *ast.Node) ir.Expression {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case ast.KindArrayBindingPattern:
		result := &ir.ArrayPattern{LeftBracket: l.idx(n), RightBracket: l.end(n) - 1}
		for _, element := range n.AsBindingPattern().Elements.Nodes {
			if element.Kind == ast.KindOmittedExpression {
				result.Elements = append(result.Elements, nil)
				continue
			}
			e := element.AsBindingElement()
			if e.Name() == nil {
				result.Elements = append(result.Elements, nil)
				continue
			}
			if e.DotDotDotToken != nil {
				result.Rest = l.bindingTarget(e.Name())
			} else {
				var target ir.Expression = l.bindingTarget(e.Name())
				if e.Initializer != nil {
					target = &ir.AssignExpression{Operator: token.ASSIGN, Left: target, Right: l.expression(e.Initializer)}
				}
				result.Elements = append(result.Elements, target)
			}
		}
		l.record(n, result)
		return result
	case ast.KindObjectBindingPattern:
		result := &ir.ObjectPattern{LeftBrace: l.idx(n), RightBrace: l.end(n) - 1}
		for _, element := range n.AsBindingPattern().Elements.Nodes {
			e := element.AsBindingElement()
			if e.DotDotDotToken != nil {
				result.Rest = l.bindingTarget(e.Name())
				continue
			}
			if e.PropertyName == nil && e.Name().Kind == ast.KindIdentifier {
				result.Properties = append(result.Properties, &ir.PropertyShort{Name: *l.identifier(e.Name()), Initializer: l.expression(e.Initializer)})
				continue
			}
			name := e.PropertyName
			if name == nil {
				name = e.Name()
			}
			key, computed := l.propertyName(name)
			var target ir.Expression = l.bindingTarget(e.Name())
			if e.Initializer != nil {
				target = &ir.AssignExpression{Operator: token.ASSIGN, Left: target, Right: l.expression(e.Initializer)}
			}
			result.Properties = append(result.Properties, &ir.PropertyKeyed{Key: key, Computed: computed, Kind: ir.PropertyKindValue, Value: target})
		}
		l.record(n, result)
		return result
	case ast.KindArrayLiteralExpression:
		result := &ir.ArrayPattern{LeftBracket: l.idx(n), RightBracket: l.end(n) - 1}
		for _, element := range n.AsArrayLiteralExpression().Elements.Nodes {
			if element.Kind == ast.KindSpreadElement {
				result.Rest = l.assignmentTarget(element.Expression())
			} else {
				result.Elements = append(result.Elements, l.assignmentTarget(element))
			}
		}
		l.record(n, result)
		return result
	case ast.KindObjectLiteralExpression:
		result := &ir.ObjectPattern{LeftBrace: l.idx(n), RightBrace: l.end(n) - 1}
		for _, property := range n.AsObjectLiteralExpression().Properties.Nodes {
			if property.Kind == ast.KindSpreadAssignment {
				result.Rest = l.assignmentTarget(property.Expression())
				continue
			}
			if property.Kind == ast.KindPropertyAssignment {
				p := property.AsPropertyAssignment()
				key, computed := l.propertyName(p.Name())
				result.Properties = append(result.Properties, &ir.PropertyKeyed{Key: key, Computed: computed, Kind: ir.PropertyKindValue, Value: l.assignmentTarget(p.Initializer)})
			} else {
				result.Properties = append(result.Properties, l.property(property))
			}
		}
		l.record(n, result)
		return result
	case ast.KindBinaryExpression:
		x := n.AsBinaryExpression()
		if x.OperatorToken.Kind == ast.KindEqualsToken {
			return &ir.AssignExpression{Operator: token.ASSIGN, Left: l.assignmentTarget(x.Left), Right: l.expression(x.Right)}
		}
	}
	return l.expression(n)
}
