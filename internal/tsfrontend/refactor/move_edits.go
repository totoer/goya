package refactor

import (
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsutil"
	"github.com/totoer/goya/internal/tsfrontend/upstream/modulespecifiers"
	"path"
	"slices"
	"strings"
)

func moveAddESExport(t *change.Tracker, node *ast.Node) *ast.Node {
	modifiers := []*ast.Node{t.NewToken(ast.KindExportKeyword)}
	decorators := []*ast.Node{}
	if node.Modifiers() != nil {
		for _, modifier := range node.Modifiers().Nodes {
			if ast.IsDecorator(modifier) {
				decorators = append(decorators, modifier)
			} else {
				modifiers = append(modifiers, modifier)
			}
		}
	}
	if ast.IsClassDeclaration(node) {
		modifiers = append(decorators, modifiers...)
	}
	mods := t.NewModifierList(modifiers)
	switch node.Kind {
	case ast.KindFunctionDeclaration:
		d := node.AsFunctionDeclaration()
		return t.UpdateFunctionDeclaration(d, mods, d.AsteriskToken, d.Name(), d.TypeParameters, d.Parameters, d.Type, nil, d.Body)
	case ast.KindClassDeclaration:
		d := node.AsClassDeclaration()
		return t.UpdateClassDeclaration(d, mods, d.Name(), d.TypeParameters, d.HeritageClauses, d.Members)
	case ast.KindVariableStatement:
		return t.UpdateVariableStatement(node.AsVariableStatement(), mods, node.AsVariableStatement().DeclarationList)
	case ast.KindModuleDeclaration:
		d := node.AsModuleDeclaration()
		return t.UpdateModuleDeclaration(d, mods, d.Keyword, d.Name(), d.Body)
	case ast.KindEnumDeclaration:
		d := node.AsEnumDeclaration()
		return t.UpdateEnumDeclaration(d, mods, d.Name(), d.Members)
	case ast.KindTypeAliasDeclaration:
		d := node.AsTypeAliasDeclaration()
		return t.UpdateTypeAliasDeclaration(d, mods, d.Name(), d.TypeParameters, d.Type)
	case ast.KindInterfaceDeclaration:
		d := node.AsInterfaceDeclaration()
		return t.UpdateInterfaceDeclaration(d, mods, d.Name(), d.TypeParameters, d.HeritageClauses, d.Members)
	case ast.KindImportEqualsDeclaration:
		d := node.AsImportEqualsDeclaration()
		return t.UpdateImportEqualsDeclaration(d, mods, d.IsTypeOnly, d.Name(), d.ModuleReference)
	}
	return node
}
func moveExported(file *ast.SourceFile, node *ast.Node, name *ast.Node) bool {
	if !ast.IsExpressionStatement(node) && ast.HasSyntacticModifier(node, ast.ModifierFlagsExport) {
		return true
	}
	return name != nil && file.Symbol != nil && file.Symbol.Exports != nil && file.Symbol.Exports[name.Text()] != nil
}
func moveAddOldExports(r *request, t *change.Tracker, usage *moveUsage) {
	seen := map[*ast.Node]bool{}
	for _, entry := range usage.targetImportsFromOld.entries {
		for _, decl := range entry.symbol.Declarations {
			if !moveTopLevelDeclaration(decl) {
				continue
			}
			name := moveDeclarationName(decl)
			if name == nil || !ast.IsIdentifier(name) {
				continue
			}
			top := moveTopLevelStatement(decl)
			if seen[top] {
				continue
			}
			seen[top] = true
			if !moveExported(r.file, top, name) && !ast.IsExpressionStatement(top) {
				pos := r.converters.PositionToLineAndCharacter(r.file, core.TextPos(tokenStart(r, top)))
				t.InsertText(r.file, pos, "export ")
			}
		}
	}
}
func moveExportedBody(r *request, t *change.Tracker, selection *moveSelection, usage *moveUsage) []*ast.Node {
	result := []*ast.Node{}
	for _, statement := range selection.all {
		needsExport := false
		if (moveNonVariableTopLevel(statement) || ast.IsVariableStatement(statement)) && !moveExported(r.file, statement, nil) {
			moveForEachTopLevel(statement, func(decl *ast.Node) bool {
				needsExport = usage.oldImportsFromTarget.has(decl.Symbol())
				return needsExport
			})
		}
		node := cloneWithTrivia(t, statement)
		if needsExport {
			node = moveAddESExport(t, node)
		}
		extractionPreserveTypeLayout(t, node)
		result = append(result, node)
	}
	return result
}
func moveBindingName(t *change.Tracker, name *ast.Node, keep func(*ast.Node) bool) *ast.Node {
	if ast.IsIdentifier(name) {
		if keep(name) {
			return name
		}
		return nil
	}
	if ast.IsArrayBindingPattern(name) {
		return name
	}
	if ast.IsObjectBindingPattern(name) {
		nodes := []*ast.Node{}
		for _, element := range name.Elements() {
			if element.AsBindingElement().PropertyName != nil || !ast.IsIdentifier(element.Name()) || keep(element.Name()) {
				nodes = append(nodes, element)
			}
		}
		if len(nodes) > 0 {
			return t.NewBindingPattern(ast.KindObjectBindingPattern, t.NewNodeList(nodes))
		}
	}
	return nil
}
func moveFilterImport(t *change.Tracker, node, module *ast.Node, keep func(*ast.Node) bool) *ast.Node {
	if ast.IsImportDeclaration(node) {
		clause := node.AsImportDeclaration().ImportClause
		if clause == nil {
			return nil
		}
		c := clause.AsImportClause()
		var name, bindings *ast.Node
		if c.Name() != nil && keep(c.Name()) {
			name = c.Name()
		}
		if c.NamedBindings != nil {
			if ast.IsNamespaceImport(c.NamedBindings) {
				if keep(c.NamedBindings.Name()) {
					bindings = c.NamedBindings
				}
			} else {
				elements := []*ast.Node{}
				for _, element := range c.NamedBindings.Elements() {
					if keep(element.Name()) {
						elements = append(elements, element)
					}
				}
				if len(elements) > 0 {
					bindings = t.NewNamedImports(t.NewNodeList(elements))
				}
			}
		}
		if name == nil && bindings == nil {
			return nil
		}
		return t.NewImportDeclaration(nil, t.NewImportClause(c.PhaseModifier, name, bindings), cloneWithTrivia(t, module), nil)
	}
	if ast.IsImportEqualsDeclaration(node) {
		if keep(node.Name()) {
			return node
		}
		return nil
	}
	if ast.IsVariableDeclaration(node) {
		name := moveBindingName(t, node.Name(), keep)
		if name == nil {
			return nil
		}
		call := t.NewCallExpression(t.NewIdentifier("require"), nil, nil, t.NewNodeList([]*ast.Node{module}), ast.NodeFlagsNone)
		return t.NewVariableStatement(nil, t.NewVariableDeclarationList(t.NewNodeList([]*ast.Node{t.NewVariableDeclaration(name, nil, node.Type(), call)}), node.Parent.Flags))
	}
	return nil
}
func moveDeleteUnusedImport(t *change.Tracker, file *ast.SourceFile, node *ast.Node, unused func(*ast.Node) bool) {
	if ast.IsImportDeclaration(node) && node.AsImportDeclaration().ImportClause != nil {
		clause := node.AsImportDeclaration().ImportClause
		c := clause.AsImportClause()
		bindings := c.NamedBindings
		allNamed := bindings == nil
		if bindings != nil && ast.IsNamedImports(bindings) && len(bindings.Elements()) != 0 {
			allNamed = true
			for _, element := range bindings.Elements() {
				if !unused(element.Name()) {
					allNamed = false
					break
				}
			}
		}
		if (c.Name() == nil || unused(c.Name())) && allNamed {
			t.Delete(file, node)
			return
		}
	}
	moveForEachImportAlias(node, func(decl *ast.Node) {
		if decl.Name() != nil && ast.IsIdentifier(decl.Name()) && unused(decl.Name()) {
			t.Delete(file, decl)
		}
	})
}
func moveNamespaceIdentifier(node *ast.Node) *ast.Node {
	if ast.IsImportDeclaration(node) {
		clause := node.AsImportDeclaration().ImportClause
		if clause != nil {
			bindings := clause.AsImportClause().NamedBindings
			if bindings != nil && ast.IsNamespaceImport(bindings) {
				return bindings.Name()
			}
		}
		return nil
	}
	if ast.IsImportEqualsDeclaration(node) {
		return node.Name()
	}
	if ast.IsVariableDeclaration(node) && ast.IsIdentifier(node.Name()) {
		return node.Name()
	}
	return nil
}
func moveString(t *change.Tracker, value string, quote lsutil.QuotePreference) *ast.Node {
	flags := ast.TokenFlagsNone
	if quote == lsutil.QuotePreferenceSingle {
		flags = ast.TokenFlagsSingleQuote
	}
	return t.NewStringLiteral(value, flags)
}
func moveNamespaceImportNode(t *change.Tracker, node *ast.Node, name, module string, quote lsutil.QuotePreference) *ast.Node {
	id := t.NewIdentifier(name)
	spec := moveString(t, module, quote)
	if ast.IsImportDeclaration(node) {
		return t.NewImportDeclaration(nil, t.NewImportClause(ast.KindUnknown, nil, t.NewNamespaceImport(id)), spec, nil)
	}
	if ast.IsImportEqualsDeclaration(node) {
		return t.NewImportEqualsDeclaration(nil, false, id, t.NewExternalModuleReference(spec))
	}
	return t.NewVariableDeclaration(id, nil, nil, t.NewCallExpression(t.NewIdentifier("require"), nil, nil, t.NewNodeList([]*ast.Node{spec}), ast.NodeFlagsNone))
}
func moveUpdateNamespace(r *request, t *change.Tracker, file *ast.SourceFile, node, id *ast.Node, module string, usage *moveUsage, quote lsutil.QuotePreference) {
	preferred := moduleIdentifier(module)
	unique := false
	references := []*ast.Node{}
	symbol := r.types().GetSymbolAtLocation(id)
	if symbol == nil {
		return
	}
	for _, ref := range r.types().GetReferencesToSymbolInFile(file, symbol) {
		if !ast.IsPropertyAccessExpression(ref.Parent) {
			continue
		}
		unique = unique || r.types().ResolveName(preferred, ref, ast.SymbolFlagsAll, true) != nil
		if usage.moved.has(r.types().GetSymbolAtLocation(ref.Parent.Name())) {
			references = append(references, ref)
		}
	}
	if len(references) == 0 {
		return
	}
	name := preferred
	if unique {
		name = uniqueName(preferred, file)
	}
	for _, ref := range references {
		t.ReplaceNode(file, ref, t.NewIdentifier(name), nil)
	}
	// The pinned provider uses the preferred spelling for the declaration even
	// when its rewritten references required a unique local name.
	t.InsertNodeAfter(file, node, moveNamespaceImportNode(t, node, preferred, module, quote))
}
func moveUpdateConsumers(r *request, t *change.Tracker, usage *moveUsage, target string, quote lsutil.QuotePreference) {
	c := r.types()
	for _, file := range r.program.GetSourceFiles() {
		if file == r.file {
			continue
		}
		for _, statement := range file.Statements.Nodes {
			moveForEachImport(statement, func(node *ast.Node) {
				if c.GetSymbolAtLocation(moveImportSpecifier(node)) != r.file.Symbol {
					return
				}
				shouldMove := func(name *ast.Node) bool {
					var symbol *ast.Symbol
					if ast.IsBindingElement(name.Parent) {
						element := name.Parent.AsBindingElement()
						property := element.PropertyName
						if property == nil {
							property = element.Name()
						}
						if ast.IsIdentifier(property) || ast.IsStringLiteralLike(property) {
							symbol = c.GetPropertyOfType(c.GetTypeAtLocation(name.Parent.Parent), property.Text())
						}
					} else {
						symbol = c.GetSymbolAtLocation(name)
						if symbol != nil {
							symbol = c.SkipAlias(symbol)
						}
					}
					return symbol != nil && usage.moved.has(symbol)
				}
				moveDeleteUnusedImport(t, file, node, shouldMove)
				absolute := target
				if !path.IsAbs(absolute) {
					absolute = path.Join(path.Dir(r.file.FileName()), target)
				}
				absolute = path.Clean(absolute)
				if r.service.UseCaseSensitiveFileNames() {
					if absolute == file.FileName() {
						return
					}
				} else if strings.EqualFold(absolute, file.FileName()) {
					return
				}
				module := modulespecifiers.GetModuleSpecifier(r.program.Options(), r.program, file, file.FileName(), "", absolute, modulespecifiers.ModuleSpecifierOptions{})
				if newImport := moveFilterImport(t, node, moveString(t, module, quote), shouldMove); newImport != nil {
					t.InsertNodeAfter(file, statement, newImport)
				}
				if namespace := moveNamespaceIdentifier(node); namespace != nil {
					moveUpdateNamespace(r, t, file, node, namespace, module, usage, quote)
				}
			})
		}
	}
}
func moveToExistingFile(r *request, t *change.Tracker, target *ast.SourceFile, body []*ast.Node, selection *moveSelection) {
	removed := map[*ast.Node]bool{}
	c := r.types()
	if target.Symbol != nil && target.Symbol.Exports != nil {
		groups := map[*ast.Node][]*ast.Node{}
		order := []*ast.Node{}
		for _, node := range selection.all {
			if !(moveNonVariableTopLevel(node) || ast.IsVariableStatement(node)) || !ast.HasSyntacticModifier(node, ast.ModifierFlagsExport) {
				continue
			}
			moveForEachTopLevel(node, func(decl *ast.Node) bool {
				symbol := decl.Symbol()
				if symbol == nil {
					return false
				}
				existing := target.Symbol.Exports[symbol.Name]
				if existing == nil {
					return false
				}
				var exported *ast.Node
				for _, declaration := range existing.Declarations {
					if ast.IsExportDeclaration(declaration) {
						exported = declaration
					} else if ast.IsExportSpecifier(declaration) && ast.IsExportDeclaration(declaration.Parent.Parent) {
						exported = declaration.Parent.Parent
					}
					if exported != nil {
						break
					}
				}
				if exported != nil && exported.AsExportDeclaration().ModuleSpecifier != nil {
					if _, ok := groups[exported]; !ok {
						order = append(order, exported)
					}
					if !slices.Contains(groups[exported], decl) {
						groups[exported] = append(groups[exported], decl)
					}
				}
				return false
			})
		}
		for _, node := range order {
			declaration := node.AsExportDeclaration()
			clause := declaration.ExportClause
			if clause == nil || !ast.IsNamedExports(clause) || len(clause.Elements()) == 0 {
				continue
			}
			elements := []*ast.Node{}
			for _, element := range clause.Elements() {
				symbol := element.Symbol()
				if symbol != nil {
					symbol = c.SkipAlias(symbol)
				}
				matched := false
				if symbol != nil {
					for _, decl := range symbol.Declarations {
						if moveTopLevelDeclaration(decl) && slices.Contains(groups[node], decl) {
							matched = true
							break
						}
					}
				}
				if !matched {
					elements = append(elements, element)
				}
			}
			if len(elements) == 0 {
				t.DeleteNode(target, node, change.LeadingTriviaOptionNone, change.TrailingTriviaOptionNone)
				removed[node] = true
			} else if len(elements) < len(clause.Elements()) {
				list := t.NewNodeList(elements)
				list.Loc = clause.AsNamedExports().Elements.Loc
				replacement := t.UpdateNamedExports(clause.AsNamedExports(), list)
				t.ReplaceNode(target, node, t.UpdateExportDeclaration(declaration, node.Modifiers(), declaration.IsTypeOnly, replacement, declaration.ModuleSpecifier, declaration.Attributes), nil)
			}
		}
	}
	var last *ast.Node
	for _, statement := range target.Statements.Nodes {
		if ast.IsExportDeclaration(statement) && statement.AsExportDeclaration().ModuleSpecifier != nil && !removed[statement] {
			last = statement
		}
	}
	if last != nil {
		t.InsertReferenceNodesBefore(target, last, body, true)
	} else {
		t.InsertNodesAfter(target, target.Statements.Nodes[len(target.Statements.Nodes)-1], body)
	}
}
