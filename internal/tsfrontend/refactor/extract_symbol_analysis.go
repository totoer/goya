package refactor

import (
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/checker"
	"slices"
)

type extractionUsage struct {
	write  bool
	symbol *ast.Symbol
	node   *ast.Node
}
type extractionScopeInfo struct {
	scope               *ast.Node
	names               []string
	usages              map[string]extractionUsage
	types               []*checker.Type
	substitutions       map[*ast.Node]*ast.Node
	symbolSubstitutions map[*ast.Symbol]*ast.Node
	functionErrors      []string
	constantErrors      []string
}
type symbolExtraction struct {
	target  *symbolRange
	scopes  []*extractionScopeInfo
	exposed []*ast.Node
}

func extractionTypeParameters(node *ast.Node) []*ast.Node {
	if node.FunctionLikeData() == nil && !ast.IsClassLike(node) && !ast.IsInterfaceDeclaration(node) && !ast.IsTypeAliasDeclaration(node) && node.Kind != ast.KindJSDocTemplateTag {
		return nil
	}
	if list := node.TypeParameterList(); list != nil {
		return list.Nodes
	}
	return nil
}
func analyzeSymbolExtraction(r *request, target *symbolRange) *symbolExtraction {
	result := &symbolExtraction{target: target}
	c := r.types()
	expression := target.expression
	if expression == nil && len(target.statements) == 1 && ast.IsExpressionStatement(target.statements[0]) {
		expression = target.statements[0].Expression()
	}
	diagnostic := ""
	if expression == nil {
		diagnostic = "expression expected."
	} else if c.GetTypeAtLocation(expression).Flags()&(checker.TypeFlagsVoid|checker.TypeFlagsNever) != 0 {
		diagnostic = "No reason to extract constant of type."
	}
	for _, scope := range extractionScopes(target) {
		info := &extractionScopeInfo{scope: scope, usages: map[string]extractionUsage{}, substitutions: map[*ast.Node]*ast.Node{}, symbolSubstitutions: map[*ast.Symbol]*ast.Node{}}
		if diagnostic != "" {
			info.constantErrors = append(info.constantErrors, diagnostic)
		}
		if ast.IsArrowFunction(scope) && !ast.IsBlock(scope.Body()) {
			info.constantErrors = append(info.constantErrors, "Cannot extract constant to an arrow function without a block")
		}
		result.scopes = append(result.scopes, info)
	}
	generic := ancestor(target.first(), func(n *ast.Node) bool { return len(extractionTypeParameters(n)) != 0 }) != nil
	allTypes := map[*checker.Type]bool{}
	recordTypes := func(t *checker.Type) {
		for _, p := range c.ReferenceWalkTypeParameters(t, func() bool { return r.ctx.Err() != nil }) {
			allTypes[p] = true
		}
	}
	visible := []*ast.Node{}
	seen := map[*ast.Symbol]int{}
	enclosing := target.affected(r)
	symbolOf := func(node *ast.Node) *ast.Symbol {
		if ast.IsIdentifier(node) && node.Parent != nil && ast.IsShorthandPropertyAssignment(node.Parent) && node.Parent.Name() == node {
			return c.GetShorthandAssignmentValueSymbol(node.Parent)
		}
		return c.GetSymbolAtLocation(node)
	}
	factory := ast.NewNodeFactory(ast.NodeFactoryHooks{})
	var qualify func(*ast.Symbol, *ast.Node, bool) *ast.Node
	qualify = func(symbol *ast.Symbol, scope *ast.Node, isType bool) *ast.Node {
		if symbol == nil {
			return nil
		}
		for _, decl := range symbol.Declarations {
			if decl.Parent == scope {
				return factory.NewIdentifier(symbol.Name)
			}
		}
		prefix := qualify(symbol.Parent, scope, isType)
		if prefix == nil {
			return nil
		}
		if isType {
			return factory.NewQualifiedName(prefix, factory.NewIdentifier(symbol.Name))
		}
		return factory.NewPropertyAccessExpression(prefix, nil, factory.NewIdentifier(symbol.Name), ast.NodeFlagsNone)
	}
	var record func(*ast.Node, bool, bool)
	record = func(node *ast.Node, write, isType bool) {
		symbol := symbolOf(node)
		if symbol == nil {
			return
		}
		use := 1
		if write {
			use = 2
		}
		previous := seen[symbol]
		if previous < use {
			seen[symbol] = use
			if previous != 0 {
				for _, scope := range result.scopes {
					if _, ok := scope.usages[node.Text()]; ok {
						scope.usages[node.Text()] = extractionUsage{write, symbol, node}
					}
				}
			} else {
				var decl *ast.Node
				for _, d := range symbol.Declarations {
					if ast.GetSourceFileOfNode(d) == r.file {
						decl = d
						break
					}
				}
				if decl == nil || enclosing.Pos() <= tokenStart(r, decl) && decl.End() <= enclosing.End() {
					return
				}
				if target.facts&extractionGenerator != 0 && write {
					reason := "Cannot extract range containing writes to references located outside of the target range in generators."
					for _, scope := range result.scopes {
						scope.functionErrors = append(scope.functionErrors, reason)
						scope.constantErrors = append(scope.constantErrors, reason)
					}
				}
				for _, scope := range result.scopes {
					if c.ResolveName(symbol.Name, scope.scope, symbol.Flags, false) == symbol {
						continue
					}
					if _, ok := scope.symbolSubstitutions[symbol]; ok {
						continue
					}
					exported := symbol
					if symbol.ExportSymbol != nil {
						exported = symbol.ExportSymbol
					}
					if subst := qualify(exported, scope.scope, isType); subst != nil {
						scope.symbolSubstitutions[symbol] = subst
					} else if isType {
						if symbol.Flags&ast.SymbolFlagsTypeParameter == 0 {
							reason := "Type will not visible in the new scope."
							scope.functionErrors = append(scope.functionErrors, reason)
							scope.constantErrors = append(scope.constantErrors, reason)
						}
					} else {
						if _, ok := scope.usages[node.Text()]; !ok {
							scope.names = append(scope.names, node.Text())
						}
						scope.usages[node.Text()] = extractionUsage{write, symbol, node}
					}
				}
			}
		}
		for _, scope := range result.scopes {
			if subst := scope.symbolSubstitutions[symbol]; subst != nil {
				scope.substitutions[node] = subst
			}
		}
	}
	var collect func(*ast.Node, bool)
	collect = func(node *ast.Node, write bool) {
		if r.ctx.Err() != nil {
			return
		}
		if generic {
			recordTypes(c.GetTypeAtLocation(node))
		}
		if referenceIsDeclaration(node) && node.Symbol() != nil {
			visible = append(visible, node)
		}
		if ast.IsAssignmentExpression(node, false) {
			b := node.AsBinaryExpression()
			collect(b.Left, true)
			collect(b.Right, false)
		} else if operand := extractionUnaryWrite(node); operand != nil {
			collect(operand, true)
		} else if ast.IsPropertyAccessExpression(node) || ast.IsElementAccessExpression(node) {
			node.ForEachChild(func(n *ast.Node) bool { collect(n, false); return false })
		} else if ast.IsIdentifier(node) {
			if node.Parent == nil {
				return
			}
			if ast.IsQualifiedName(node.Parent) && node != node.Parent.AsQualifiedName().Left {
				return
			}
			if ast.IsPropertyAccessExpression(node.Parent) && node != node.Parent.Expression() {
				return
			}
			record(node, write, ast.IsPartOfTypeNode(node))
		} else {
			node.ForEachChild(func(n *ast.Node) bool { collect(n, false); return false })
		}
	}
	if target.expression != nil {
		collect(target.expression, false)
	} else {
		for _, statement := range target.statements {
			collect(statement, false)
		}
	}
	if generic && target.expression != nil && !ast.IsJsxAttribute(target.expression) {
		recordTypes(c.GetContextualType(target.expression, checker.ContextFlagsNone))
	}
	if len(allTypes) > 0 {
		seenTypes := []*checker.Type{}
		index := 0
		for curr := target.first(); curr != nil && index < len(result.scopes); curr = curr.Parent {
			if curr == result.scopes[index].scope {
				result.scopes[index].types = slices.Clone(seenTypes)
				index++
			}
			for _, decl := range extractionTypeParameters(curr) {
				typ := c.GetTypeAtLocation(decl)
				if allTypes[typ] && !slices.Contains(seenTypes, typ) {
					seenTypes = append(seenTypes, typ)
				}
			}
		}
	}
	var firstNonVariable *ast.Node
	if len(visible) > 0 && len(result.scopes) > 0 {
		scope := result.scopes[0].scope
		if !ast.IsBlockScope(scope, scope.Parent) {
			scope = ast.GetEnclosingBlockScopeContainer(scope)
		}
		seenExposed := map[*ast.Symbol]bool{}
		var visit func(*ast.Node) bool
		visit = func(node *ast.Node) bool {
			if node == target.expression || slices.Contains(target.statements, node) {
				return false
			}
			if sym := symbolOf(node); sym != nil {
				for _, decl := range visible {
					if decl.Symbol() == sym {
						if ast.IsVariableDeclaration(decl) {
							if !seenExposed[sym] {
								result.exposed = append(result.exposed, decl)
								seenExposed[sym] = true
							}
						} else if firstNonVariable == nil {
							firstNonVariable = decl
						}
						break
					}
				}
			}
			node.ForEachChild(visit)
			return false
		}
		if scope != nil {
			scope.ForEachChild(visit)
		}
	}
	for i, scope := range result.scopes {
		if i > 0 && (len(scope.usages) > 0 || len(scope.types) > 0) {
			scope.constantErrors = append(scope.constantErrors, "Cannot access variables from nested scopes")
		}
		if target.facts&extractionFunctionThis != 0 && ast.IsClassLike(scope.scope) {
			scope.functionErrors = append(scope.functionErrors, "Cannot extract functions containing this to method")
		}
		hasWrite := false
		readonlyWrite := false
		for _, name := range scope.names {
			usage := scope.usages[name]
			if usage.write {
				hasWrite = true
				if usage.symbol.Flags&ast.SymbolFlagsClassMember != 0 && usage.symbol.ValueDeclaration != nil && ast.GetCombinedModifierFlags(usage.symbol.ValueDeclaration)&ast.ModifierFlagsReadonly != 0 {
					readonlyWrite = true
				}
			}
		}
		reason := ""
		if hasWrite && target.expression != nil {
			reason = "Cannot write back side-effects when extracting an expression"
		} else if readonlyWrite && i > 0 {
			reason = "Cannot move initialization of read-only class property outside of the constructor"
		} else if firstNonVariable != nil {
			reason = "Cannot extract exported declaration"
		}
		if reason != "" {
			scope.functionErrors = append(scope.functionErrors, reason)
			scope.constantErrors = append(scope.constantErrors, reason)
		}
	}
	return result
}
