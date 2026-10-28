package tsfrontend

import (
	"strings"

	ir "github.com/totoer/goya/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/unistring"
)

func (l *lowerer) class(n *ast.Node) *ir.ClassLiteral {
	if decorators := n.Decorators(); len(decorators) > 0 {
		l.unsupported(decorators[0])
	}
	x := n.ClassLikeData()
	result := &ir.ClassLiteral{Class: l.idx(n), RightBrace: l.end(n) - 1, Name: l.identifier(n.Name()), Source: l.text(n)}
	if x.HeritageClauses != nil {
		for _, heritage := range x.HeritageClauses.Nodes {
			h := heritage.AsHeritageClause()
			if h.Token == ast.KindExtendsKeyword && len(h.Types.Nodes) > 0 {
				result.SuperClass = l.expression(h.Types.Nodes[0])
			}
		}
	}
	for _, member := range x.Members.Nodes {
		if decorators := member.Decorators(); len(decorators) > 0 {
			l.unsupported(decorators[0])
		}
		if member.ModifierFlags()&ast.ModifierFlagsAccessor != 0 {
			l.unsupported(member)
		}
		static := member.ModifierFlags()&ast.ModifierFlagsStatic != 0
		var key ir.Expression
		computed := false
		if name := member.Name(); name != nil {
			if name.Kind == ast.KindPrivateIdentifier {
				key = &ir.PrivateIdentifier{Identifier: ir.Identifier{Name: unistring.NewFromString(strings.TrimPrefix(name.Text(), "#")), Idx: l.idx(name)}}
			} else {
				key, computed = l.propertyName(name)
			}
		}
		switch member.Kind {
		case ast.KindSemicolonClassElement:
		case ast.KindPropertyDeclaration:
			result.Body = append(result.Body, &ir.FieldDefinition{Idx: l.idx(member), Key: key, Computed: computed, Static: static, Initializer: l.expression(member.Initializer())})
		case ast.KindConstructor:
			result.Body = append(result.Body, &ir.MethodDefinition{Idx: l.idx(member), Key: &ir.StringLiteral{Idx: l.idx(member), Value: "constructor", Literal: "constructor"}, Kind: ir.PropertyKindMethod, Body: l.function(member)})
		case ast.KindMethodDeclaration, ast.KindGetAccessor, ast.KindSetAccessor:
			kind := ir.PropertyKindMethod
			if member.Kind == ast.KindGetAccessor {
				kind = ir.PropertyKindGet
			}
			if member.Kind == ast.KindSetAccessor {
				kind = ir.PropertyKindSet
			}
			result.Body = append(result.Body, &ir.MethodDefinition{Idx: l.idx(member), Key: key, Computed: computed, Static: static, Kind: kind, Body: l.function(member)})
		case ast.KindClassStaticBlockDeclaration:
			parentDeclarations := l.declarations
			l.declarations = nil
			body := member.AsClassStaticBlockDeclaration().Body
			block := l.block(body)
			result.Body = append(result.Body, &ir.ClassStaticBlock{Static: l.idx(member), Block: block, Source: l.text(body), DeclarationList: l.declarations})
			l.declarations = parentDeclarations
		default:
			l.unsupported(member)
		}
	}
	l.record(n, result)
	return result
}
