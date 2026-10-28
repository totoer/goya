package refactor

import (
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/autoimport"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsutil"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"slices"
)

type moveImportState struct {
	clause                   *ast.Node
	defaultImport, namespace *autoimport.ReferenceImportBinding
	named                    []autoimport.ReferenceImportBinding
	indices                  map[string]int
	useRequire               bool
}

func (s *moveImportState) addNamed(binding autoimport.ReferenceImportBinding) {
	if s.indices == nil {
		s.indices = map[string]int{}
	}
	if i, ok := s.indices[binding.Name]; ok {
		if s.named[i].AddAsTypeOnly > binding.AddAsTypeOnly {
			binding.AddAsTypeOnly = s.named[i].AddAsTypeOnly
		}
		s.named[i] = binding
	} else {
		s.indices[binding.Name] = len(s.named)
		s.named = append(s.named, binding)
	}
}
func moveMergeDefault(previous *autoimport.ReferenceImportBinding, next autoimport.ReferenceImportBinding) *autoimport.ReferenceImportBinding {
	if previous != nil && previous.Name != next.Name {
		panic(&change.InvalidEdits{Reason: "conflicting default imports"})
	}
	if previous != nil && previous.AddAsTypeOnly > next.AddAsTypeOnly {
		next.AddAsTypeOnly = previous.AddAsTypeOnly
	}
	return &next
}

type moveImportAdder struct {
	r                      *request
	t                      *change.Tracker
	file                   *ast.SourceFile
	view                   *autoimport.View
	future                 bool
	existing               []*moveImportState
	existingByClause       map[*ast.Node]*moveImportState
	newImports             []*moveImportState
	newKeys                []string
	newByKey               map[string]*moveImportState
	remove, verbatim       []*ast.Node
	removeSet, verbatimSet map[*ast.Node]bool
}

func newMoveImportAdder(r *request, t *change.Tracker, file *ast.SourceFile, view *autoimport.View, future bool) *moveImportAdder {
	return &moveImportAdder{r: r, t: t, file: file, view: view, future: future, existingByClause: map[*ast.Node]*moveImportState{}, newByKey: map[string]*moveImportState{}, removeSet: map[*ast.Node]bool{}, verbatimSet: map[*ast.Node]bool{}}
}
func (a *moveImportAdder) hasFixes() bool {
	return len(a.existing) > 0 || len(a.newImports) > 0 || len(a.remove) > 0 || len(a.verbatim) > 0
}
func (a *moveImportAdder) removeImport(decl *ast.Node) {
	if !a.removeSet[decl] {
		a.removeSet[decl] = true
		a.remove = append(a.remove, decl)
	}
}
func (a *moveImportAdder) addVerbatim(decl *ast.Node) {
	if !a.verbatimSet[decl] {
		a.verbatimSet[decl] = true
		a.verbatim = append(a.verbatim, decl)
	}
}
func (a *moveImportAdder) addSymbol(symbol *ast.Symbol, typeOnly bool, reference *ast.Node) {
	fix := autoimport.ReferenceImportFixForSymbol(a.r.ctx, a.view, a.r.types(), symbol, typeOnly)
	if fix == nil {
		return
	}
	copy := *fix
	raw := *fix.AutoImportFix
	copy.AutoImportFix = &raw
	local := autoimport.ReferenceImportName(symbol)
	if reference != nil && reference.Name() != nil && ast.IsIdentifier(reference.Name()) {
		local = reference.Name().Text()
	}
	raw.Name = local
	if reference != nil && ast.IsTypeOnlyImportDeclaration(reference) && raw.AddAsTypeOnly == lsproto.AddAsTypeOnlyAllowed && (raw.Kind == lsproto.AutoImportFixKindAddNew || raw.Kind == lsproto.AutoImportFixKindAddToExisting) {
		raw.AddAsTypeOnly = lsproto.AddAsTypeOnlyRequired
	}
	property := ""
	if symbol.Name != local {
		property = symbol.Name
	}
	a.addFix(&copy, property)
}
func (a *moveImportAdder) addModule(symbol *ast.Symbol, reference *ast.Node) {
	if reference == nil {
		panic(&change.InvalidEdits{Reason: "missing module reference import"})
	}
	fix := autoimport.ReferenceModuleImportFix(a.r.ctx, a.view, a.r.types(), a.t.NewIdentifier(symbol.Name), reference, symbol)
	if fix != nil {
		a.addFix(fix, "")
	}
}
func (a *moveImportAdder) addMoved(symbol *ast.Symbol, typeOnly bool, file string) {
	fix := autoimport.ReferenceFutureImportFix(a.r.ctx, a.view, autoimport.ReferenceImportName(symbol), file, symbol.Name == "default" && symbol.Parent != nil, symbol.Flags, typeOnly)
	if fix != nil {
		a.addFix(fix, "")
	}
}
func (a *moveImportAdder) newEntry(fix *autoimport.Fix) *moveImportState {
	typeKey := "1|" + fix.ModuleSpecifier
	normalKey := "0|" + fix.ModuleSpecifier
	typeEntry, normalEntry := a.newByKey[typeKey], a.newByKey[normalKey]
	key := normalKey
	if fix.ImportKind == lsproto.ImportKindDefault && fix.AddAsTypeOnly == lsproto.AddAsTypeOnlyRequired {
		key = typeKey
		if typeEntry != nil {
			return typeEntry
		}
	} else if fix.AddAsTypeOnly == lsproto.AddAsTypeOnlyAllowed && (typeEntry != nil || normalEntry != nil) {
		if typeEntry != nil {
			return typeEntry
		}
		return normalEntry
	} else if normalEntry != nil {
		return normalEntry
	}
	entry := &moveImportState{useRequire: fix.UseRequire}
	a.newByKey[key] = entry
	a.newKeys = append(a.newKeys, key)
	a.newImports = append(a.newImports, entry)
	return entry
}
func (a *moveImportAdder) addFix(fix *autoimport.Fix, property string) {
	binding := autoimport.ReferenceImportBinding{Name: fix.Name, PropertyName: property, Kind: fix.ImportKind, AddAsTypeOnly: fix.AddAsTypeOnly}
	switch fix.Kind {
	case lsproto.AutoImportFixKindAddToExisting:
		clause := autoimport.ReferenceImportClauseForFix(a.file, fix)
		entry := a.existingByClause[clause]
		if entry == nil {
			entry = &moveImportState{clause: clause}
			a.existingByClause[clause] = entry
			a.existing = append(a.existing, entry)
		}
		if fix.ImportKind == lsproto.ImportKindNamed {
			entry.addNamed(binding)
		} else {
			entry.defaultImport = moveMergeDefault(entry.defaultImport, binding)
		}
	case lsproto.AutoImportFixKindAddNew:
		entry := a.newEntry(fix)
		if entry.useRequire != fix.UseRequire {
			panic(&change.InvalidEdits{Reason: "mixed import and require additions"})
		}
		switch fix.ImportKind {
		case lsproto.ImportKindDefault:
			entry.defaultImport = moveMergeDefault(entry.defaultImport, binding)
		case lsproto.ImportKindNamed:
			entry.addNamed(binding)
		case lsproto.ImportKindCommonJS:
			if a.r.program.Options().VerbatimModuleSyntax.IsTrue() {
				entry.addNamed(binding)
			} else {
				if entry.namespace != nil && entry.namespace.Name != binding.Name {
					panic(&change.InvalidEdits{Reason: "conflicting namespace import additions"})
				}
				entry.namespace = &binding
			}
		case lsproto.ImportKindNamespace:
			if entry.namespace != nil && entry.namespace.Name != binding.Name {
				panic(&change.InvalidEdits{Reason: "conflicting namespace import additions"})
			}
			entry.namespace = &binding
		}
	case lsproto.AutoImportFixKindPromoteTypeOnly:
	default:
		panic(&change.InvalidEdits{Reason: "unexpected import fix in move operation"})
	}
}
func moveAllRemoved(elements []*ast.Node, removed map[*ast.Node]bool) bool {
	for _, element := range elements {
		if !removed[element] {
			return false
		}
	}
	return true
}
func (a *moveImportAdder) writeRemovals() map[*ast.Node]bool {
	removedWhileAdding := map[*ast.Node]bool{}
	if len(a.remove) == 0 {
		return removedWhileAdding
	}
	imports, variables := []*ast.Node{}, []*ast.Node{}
	for _, decl := range a.remove {
		if node := ancestor(decl, ast.IsImportDeclaration); node != nil && !slices.Contains(imports, node) {
			imports = append(imports, node)
		}
		if node := ancestor(decl, func(n *ast.Node) bool { return ast.IsVariableDeclaration(n) && moveRequire(n.Initializer()) }); node != nil && !slices.Contains(variables, node) {
			variables = append(variables, node)
		}
	}
	emptyImports, emptyVariables, namedDeleted := map[*ast.Node]bool{}, map[*ast.Node]bool{}, map[*ast.Node]bool{}
	for _, node := range imports {
		clause := node.AsImportDeclaration().ImportClause
		var bindings *ast.Node
		if clause != nil {
			bindings = clause.AsImportClause().NamedBindings
		}
		noDefault := clause == nil || clause.Name() == nil || a.removeSet[clause]
		noNamespace := bindings == nil || !ast.IsNamespaceImport(bindings) || a.removeSet[bindings]
		noNamed := bindings == nil || !ast.IsNamedImports(bindings) || moveAllRemoved(bindings.Elements(), a.removeSet)
		if a.existingByClause[clause] == nil && noDefault && noNamespace && noNamed {
			emptyImports[node] = true
		}
	}
	for _, node := range variables {
		name := node.Name()
		binding := ast.IsObjectBindingPattern(name)
		if (!binding || a.existingByClause[name] == nil) && (!binding || moveAllRemoved(name.Elements(), a.removeSet)) {
			emptyVariables[node] = true
		}
	}
	for _, node := range imports {
		clause := node.AsImportDeclaration().ImportClause
		if clause == nil {
			continue
		}
		bindings := clause.AsImportClause().NamedBindings
		entry := a.existingByClause[clause]
		if bindings != nil && !emptyImports[node] && (entry == nil || len(entry.named) == 0) && (ast.IsNamespaceImport(bindings) || moveAllRemoved(bindings.Elements(), a.removeSet)) {
			namedDeleted[node] = true
		}
	}
	for _, node := range imports {
		if emptyImports[node] {
			a.t.Delete(a.file, node)
		}
	}
	for _, node := range variables {
		if emptyVariables[node] {
			a.t.Delete(a.file, node)
		}
	}
	for _, node := range imports {
		if namedDeleted[node] {
			clause := node.AsImportDeclaration().ImportClause
			c := clause.AsImportClause()
			a.t.ReplaceNode(a.file, clause, a.t.UpdateImportClause(c, c.PhaseModifier, c.Name(), nil), nil)
		}
	}
	for _, decl := range a.remove {
		imported := ancestor(decl, ast.IsImportDeclaration)
		if imported != nil && !emptyImports[imported] && !namedDeleted[imported] {
			if ast.IsImportClause(decl) {
				a.t.Delete(a.file, decl.Name())
			} else {
				if !ast.IsImportSpecifier(decl) {
					panic(&change.InvalidEdits{Reason: "namespace import removal was not handled"})
				}
				clause := imported.AsImportDeclaration().ImportClause
				entry := a.existingByClause[clause]
				if entry != nil && len(entry.named) > 0 {
					removedWhileAdding[decl] = true
				} else {
					a.t.Delete(a.file, decl)
				}
			}
		} else if ast.IsBindingElement(decl) {
			entry := a.existingByClause[decl.Parent]
			if entry != nil && len(entry.named) > 0 {
				removedWhileAdding[decl] = true
			} else {
				a.t.Delete(a.file, decl)
			}
		} else if ast.IsImportEqualsDeclaration(decl) {
			a.t.Delete(a.file, decl)
		}
	}
	return removedWhileAdding
}
func (a *moveImportAdder) combinedVerbatim() []*ast.Node {
	declarations, requires := []*ast.Node{}, []*ast.Node{}
	result := []*ast.Node{}
	for _, node := range a.verbatim {
		if ast.IsImportEqualsDeclaration(node) {
			result = append(result, cloneWithTrivia(a.t, node))
		}
		if d := ancestor(node, ast.IsImportDeclaration); d != nil && !slices.Contains(declarations, d) {
			declarations = append(declarations, d)
		}
		if d := ancestor(node, func(n *ast.Node) bool { return ast.IsVariableStatement(n) && movePureImport(n) }); d != nil && !slices.Contains(requires, d) {
			requires = append(requires, d)
		}
	}
	for _, decl := range declarations {
		if a.verbatimSet[decl] {
			result = append(result, cloneWithTrivia(a.t, decl))
			continue
		}
		declaration := decl.AsImportDeclaration()
		clause := declaration.ImportClause
		if clause != nil {
			c := clause.AsImportClause()
			var name, bindings *ast.Node
			if a.verbatimSet[clause] {
				name = c.Name()
			}
			if c.NamedBindings != nil {
				if ast.IsNamespaceImport(c.NamedBindings) && a.verbatimSet[c.NamedBindings] {
					bindings = c.NamedBindings
				} else if ast.IsNamedImports(c.NamedBindings) {
					elements := []*ast.Node{}
					for _, element := range c.NamedBindings.Elements() {
						if a.verbatimSet[element] {
							elements = append(elements, element)
						}
					}
					if len(elements) > 0 {
						bindings = a.t.UpdateNamedImports(c.NamedBindings.AsNamedImports(), a.t.NewNodeList(elements))
					}
				}
			}
			clause = a.t.UpdateImportClause(c, c.PhaseModifier, name, bindings)
		}
		node := a.t.UpdateImportDeclaration(declaration, decl.Modifiers(), clause, declaration.ModuleSpecifier, declaration.Attributes)
		result = append(result, cloneWithTrivia(a.t, node))
	}
	for _, statement := range requires {
		if a.verbatimSet[statement] {
			result = append(result, cloneWithTrivia(a.t, statement))
			continue
		}
		list := statement.AsVariableStatement().DeclarationList
		decls := []*ast.Node{}
		for _, decl := range list.AsVariableDeclarationList().Declarations.Nodes {
			if a.verbatimSet[decl] {
				decls = append(decls, decl)
				continue
			}
			name := decl.Name()
			if ast.IsObjectBindingPattern(name) {
				elements := []*ast.Node{}
				for _, element := range name.Elements() {
					if a.verbatimSet[element] {
						elements = append(elements, element)
					}
				}
				name = a.t.UpdateBindingPattern(name.AsBindingPattern(), a.t.NewNodeList(elements))
			}
			d := decl.AsVariableDeclaration()
			decls = append(decls, a.t.UpdateVariableDeclaration(d, name, d.ExclamationToken, d.Type, d.Initializer))
		}
		node := a.t.UpdateVariableStatement(statement.AsVariableStatement(), statement.Modifiers(), a.t.UpdateVariableDeclarationList(list.AsVariableDeclarationList(), a.t.NewNodeList(decls), list.Flags))
		result = append(result, cloneWithTrivia(a.t, node))
	}
	return result
}
func (a *moveImportAdder) write(oldQuote lsutil.QuotePreference) []*ast.Node {
	quote := lsutil.GetQuotePreference(a.file, a.r.preferences)
	if len(a.file.Imports()) == 0 {
		quote = oldQuote
	}
	removed := a.writeRemovals()
	for _, entry := range a.existing {
		autoimport.ReferenceWriteExistingImport(a.t, a.file, entry.clause, entry.defaultImport, entry.named, removed, a.r.preferences)
	}
	declarations := []*ast.Node{}
	for i, entry := range a.newImports {
		declarations = append(declarations, autoimport.ReferenceNewImportDeclarations(a.t, a.newKeys[i][2:], quote, entry.defaultImport, entry.named, entry.namespace, a.r.program.Options(), a.r.preferences, entry.useRequire)...)
	}
	declarations = append(declarations, a.combinedVerbatim()...)
	if !a.future && len(declarations) > 0 {
		autoimport.ReferenceInsertImports(a.t, a.file, declarations, a.r.preferences)
		return nil
	}
	return declarations
}
