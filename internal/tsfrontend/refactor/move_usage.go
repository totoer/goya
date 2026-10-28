package refactor

import (
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"slices"
)

type moveSymbolEntry struct {
	symbol    *ast.Symbol
	typeOnly  bool
	reference *ast.Node
}
type moveSymbols struct {
	entries []moveSymbolEntry
	indices map[*ast.Symbol]int
}

func (s *moveSymbols) has(symbol *ast.Symbol) bool { _, ok := s.indices[symbol]; return ok }
func (s *moveSymbols) set(symbol *ast.Symbol, typeOnly bool, reference *ast.Node) {
	if s.indices == nil {
		s.indices = map[*ast.Symbol]int{}
	}
	if i, ok := s.indices[symbol]; ok {
		s.entries[i] = moveSymbolEntry{symbol, typeOnly, reference}
	} else {
		s.indices[symbol] = len(s.entries)
		s.entries = append(s.entries, moveSymbolEntry{symbol, typeOnly, reference})
	}
}
func (s *moveSymbols) remove(symbol *ast.Symbol) {
	if i, ok := s.indices[symbol]; ok {
		s.entries = append(s.entries[:i], s.entries[i+1:]...)
		delete(s.indices, symbol)
		for j := i; j < len(s.entries); j++ {
			s.indices[s.entries[j].symbol] = j
		}
	}
}

type moveUsage struct {
	moved                 moveSymbols
	targetImportsFromOld  moveSymbols
	oldImportsFromTarget  moveSymbols
	importsNeededByTarget moveSymbols
	unusedOldImports      moveSymbols
}

func moveNonVariableTopLevel(node *ast.Node) bool {
	if node == nil {
		return false
	}
	switch node.Kind {
	case ast.KindFunctionDeclaration, ast.KindClassDeclaration, ast.KindModuleDeclaration, ast.KindEnumDeclaration, ast.KindTypeAliasDeclaration, ast.KindInterfaceDeclaration, ast.KindImportEqualsDeclaration:
		return true
	}
	return false
}
func moveTopLevelDeclaration(node *ast.Node) bool {
	return node != nil && (moveNonVariableTopLevel(node) && ast.IsSourceFile(node.Parent) || ast.IsVariableDeclaration(node) && node.Parent != nil && node.Parent.Parent != nil && node.Parent.Parent.Parent != nil && ast.IsSourceFile(node.Parent.Parent.Parent))
}
func moveTopLevelStatement(node *ast.Node) *ast.Node {
	if ast.IsVariableDeclaration(node) {
		return node.Parent.Parent
	}
	if ast.IsBindingElement(node) {
		return moveTopLevelStatement(node.Parent.Parent)
	}
	return node
}
func moveForEachBinding(name *ast.Node, visit func(*ast.Node) bool) bool {
	if ast.IsIdentifier(name) {
		return visit(name.Parent)
	}
	if ast.IsArrayBindingPattern(name) || ast.IsObjectBindingPattern(name) {
		for _, element := range name.Elements() {
			if !ast.IsOmittedExpression(element) && moveForEachBinding(element.Name(), visit) {
				return true
			}
		}
	}
	return false
}
func moveExportsAssignment(statement *ast.Node) bool {
	return ast.IsExpressionStatement(statement) && ast.IsBinaryExpression(statement.Expression()) && ast.GetAssignmentDeclarationKind(statement.Expression()) == ast.JSDeclarationKindExportsProperty
}

func moveForEachTopLevel(statement *ast.Node, visit func(*ast.Node) bool) bool {
	if moveNonVariableTopLevel(statement) {
		return visit(statement)
	}
	if ast.IsVariableStatement(statement) {
		for _, decl := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
			if moveForEachBinding(decl.Name(), visit) {
				return true
			}
		}
	}
	if moveExportsAssignment(statement) {
		return visit(statement)
	}
	return false
}
func moveDeclarationName(node *ast.Node) *ast.Node {
	if ast.IsExpressionStatement(node) {
		return node.Expression().AsBinaryExpression().Left.Name()
	}
	return node.Name()
}
func moveImportedDeclaration(decl *ast.Node) bool {
	switch decl.Kind {
	case ast.KindImportEqualsDeclaration, ast.KindImportSpecifier, ast.KindImportClause, ast.KindNamespaceImport:
		return true
	case ast.KindVariableDeclaration:
		return decl.Parent != nil && decl.Parent.Parent != nil && decl.Parent.Parent.Parent != nil && ast.IsSourceFile(decl.Parent.Parent.Parent) && moveRequire(decl.Initializer())
	case ast.KindBindingElement:
		return decl.Parent != nil && decl.Parent.Parent != nil && ast.IsVariableDeclaration(decl.Parent.Parent) && moveImportedDeclaration(decl.Parent.Parent)
	}
	return false
}
func moveImportAliasDeclaration(decl *ast.Node) bool {
	switch decl.Kind {
	case ast.KindImportEqualsDeclaration, ast.KindImportSpecifier, ast.KindImportClause, ast.KindNamespaceImport, ast.KindBindingElement, ast.KindVariableDeclaration:
		return true
	}
	return false
}
func moveEachReference(r *request, node *ast.Node, visit func(*ast.Symbol, bool)) {
	var walk func(*ast.Node) bool
	walk = func(n *ast.Node) bool {
		if ast.IsIdentifier(n) && !ast.IsDeclarationName(n) {
			if symbol := r.types().GetSymbolAtLocation(n); symbol != nil {
				visit(symbol, ast.IsValidTypeOnlyAliasUseSite(n))
			}
		} else {
			n.ForEachChild(walk)
		}
		return false
	}
	node.ForEachChild(walk)
}
func moveExistingLocals(r *request, target *ast.SourceFile, statements []*ast.Node) map[*ast.Symbol]bool {
	result := map[*ast.Symbol]bool{}
	c := r.types()
	for _, statement := range target.Statements.Nodes {
		moveForEachImport(statement, func(node *ast.Node) {
			if ast.IsImportDeclaration(node) {
				clause := node.AsImportDeclaration().ImportClause
				if clause != nil {
					bindings := clause.AsImportClause().NamedBindings
					if bindings != nil && ast.IsNamedImports(bindings) {
						for _, element := range bindings.Elements() {
							name := element.AsImportSpecifier().PropertyName
							if name == nil {
								name = element.Name()
							}
							if symbol := c.GetSymbolAtLocation(name); symbol != nil {
								result[c.SkipAlias(symbol)] = true
							}
						}
					}
				}
			}
			if ast.IsVariableDeclaration(node) && moveRequire(node.Initializer()) && ast.IsObjectBindingPattern(node.Name()) {
				for _, element := range node.Name().Elements() {
					name := element.AsBindingElement().PropertyName
					if name == nil {
						name = element.Name()
					}
					if symbol := c.GetSymbolAtLocation(name); symbol != nil {
						result[c.SkipAlias(symbol)] = true
					}
				}
			}
		})
	}
	for _, statement := range statements {
		moveEachReference(r, statement, func(symbol *ast.Symbol, _ bool) {
			symbol = c.SkipAlias(symbol)
			if symbol != nil && symbol.ValueDeclaration != nil && ast.GetSourceFileOfNode(symbol.ValueDeclaration).Path() == target.Path() {
				result[symbol] = true
			}
		})
	}
	return result
}
func getMoveUsage(r *request, selection *moveSelection, existing map[*ast.Symbol]bool) (*moveUsage, error) {
	usage := &moveUsage{}
	c := r.types()
	var jsxSymbol *ast.Symbol
	for _, statement := range selection.all {
		if statement.SubtreeFacts()&ast.SubtreeContainsJsx != 0 {
			symbol := c.ResolveName(c.GetJsxNamespace(statement), statement, ast.SymbolFlagsNamespace, true)
			if symbol != nil {
				for _, decl := range symbol.Declarations {
					if moveImportedDeclaration(decl) {
						jsxSymbol = symbol
						var ref *ast.Node
						if len(symbol.Declarations) > 0 && moveImportAliasDeclaration(symbol.Declarations[0]) {
							ref = symbol.Declarations[0]
						}
						usage.importsNeededByTarget.set(symbol, false, ref)
						break
					}
				}
			}
			break
		}
	}
	for _, statement := range selection.all {
		var failure error
		moveForEachTopLevel(statement, func(decl *ast.Node) bool {
			symbol := decl.Symbol()
			if ast.IsExpressionStatement(decl) {
				symbol = c.GetSymbolAtLocation(decl.Expression().AsBinaryExpression().Left)
			}
			if symbol == nil {
				failure = &change.InvalidEdits{Reason: "moved declaration has no symbol"}
				return true
			}
			usage.moved.set(symbol, false, nil)
			return false
		})
		if failure != nil {
			return nil, failure
		}
	}
	for _, statement := range selection.all {
		moveEachReference(r, statement, func(symbol *ast.Symbol, typeOnly bool) {
			if len(symbol.Declarations) == 0 {
				return
			}
			if existing[c.SkipAlias(symbol)] {
				usage.unusedOldImports.set(symbol, false, nil)
				return
			}
			var imported *ast.Node
			for _, decl := range symbol.Declarations {
				if moveImportedDeclaration(decl) {
					imported = decl
					break
				}
			}
			if imported != nil {
				var ref *ast.Node
				if moveImportAliasDeclaration(imported) {
					ref = imported
				}
				// The pinned implementation tests the previous tuple's truthiness. A
				// repeated reference consequently retains its latest type-only use flag.
				usage.importsNeededByTarget.set(symbol, typeOnly, ref)
			} else if !usage.moved.has(symbol) {
				allOld := true
				for _, decl := range symbol.Declarations {
					if !moveTopLevelDeclaration(decl) || ast.GetSourceFileOfNode(decl) != r.file {
						allOld = false
						break
					}
				}
				if allOld {
					usage.targetImportsFromOld.set(symbol, typeOnly, nil)
				}
			}
		})
	}
	for _, entry := range usage.importsNeededByTarget.entries {
		usage.unusedOldImports.set(entry.symbol, false, nil)
	}
	for _, statement := range r.file.Statements.Nodes {
		if slices.Contains(selection.all, statement) {
			continue
		}
		if jsxSymbol != nil && statement.SubtreeFacts()&ast.SubtreeContainsJsx != 0 {
			usage.unusedOldImports.remove(jsxSymbol)
		}
		moveEachReference(r, statement, func(symbol *ast.Symbol, typeOnly bool) {
			if usage.moved.has(symbol) {
				usage.oldImportsFromTarget.set(symbol, typeOnly, nil)
			}
			usage.unusedOldImports.remove(symbol)
		})
	}
	return usage, nil
}
func moveForEachImport(statement *ast.Node, visit func(*ast.Node)) {
	if ast.IsImportDeclaration(statement) {
		if ast.IsStringLiteral(statement.AsImportDeclaration().ModuleSpecifier) {
			visit(statement)
		}
	} else if ast.IsImportEqualsDeclaration(statement) {
		ref := statement.AsImportEqualsDeclaration().ModuleReference
		if ast.IsExternalModuleReference(ref) && ast.IsStringLiteralLike(ref.AsExternalModuleReference().Expression) {
			visit(statement)
		}
	} else if ast.IsVariableStatement(statement) {
		for _, decl := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
			if moveRequire(decl.Initializer()) {
				visit(decl)
			}
		}
	}
}
func moveImportSpecifier(node *ast.Node) *ast.Node {
	if ast.IsImportDeclaration(node) {
		return node.AsImportDeclaration().ModuleSpecifier
	}
	if ast.IsImportEqualsDeclaration(node) {
		return node.AsImportEqualsDeclaration().ModuleReference.AsExternalModuleReference().Expression
	}
	return node.Initializer().Arguments()[0]
}
func moveForEachImportAlias(node *ast.Node, visit func(*ast.Node)) {
	if ast.IsImportDeclaration(node) {
		clause := node.AsImportDeclaration().ImportClause
		if clause == nil {
			return
		}
		if clause.Name() != nil {
			visit(clause)
		}
		bindings := clause.AsImportClause().NamedBindings
		if bindings == nil {
			return
		}
		if ast.IsNamespaceImport(bindings) {
			visit(bindings)
		} else if ast.IsNamedImports(bindings) {
			for _, element := range bindings.Elements() {
				visit(element)
			}
		}
	} else if ast.IsImportEqualsDeclaration(node) {
		visit(node)
	} else if ast.IsVariableDeclaration(node) {
		name := node.Name()
		if ast.IsIdentifier(name) {
			visit(node)
		} else if ast.IsObjectBindingPattern(name) {
			for _, element := range name.Elements() {
				if ast.IsIdentifier(element.Name()) {
					visit(element)
				}
			}
		}
	}
}
