package tsfrontend

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"

	ir "github.com/totoer/goya/ast"
	"github.com/totoer/goya/file"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/bundled"
	"github.com/totoer/goya/internal/tsfrontend/upstream/checker"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/printer"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"github.com/totoer/goya/internal/tsfrontend/upstream/transformers"
	"github.com/totoer/goya/internal/tsfrontend/upstream/transformers/estransforms"
	"github.com/totoer/goya/internal/tsfrontend/upstream/transformers/inliners"
	"github.com/totoer/goya/internal/tsfrontend/upstream/transformers/jsxtransforms"
	"github.com/totoer/goya/internal/tsfrontend/upstream/transformers/moduletransforms"
	"github.com/totoer/goya/internal/tsfrontend/upstream/transformers/tstransforms"
	"github.com/totoer/goya/token"
	"github.com/totoer/goya/unistring"
)

type loweringFailure struct{ diagnostic Diagnostic }
type lowerer struct {
	ctx              context.Context
	file             *ast.SourceFile
	emit             *printer.EmitContext
	module           *IRModule
	declarations     []*ir.VariableDeclaration
	checker          *checker.Checker
	globals          map[string]bool
	standardGlobals  map[string]bool
	names            *printer.NameGenerator
	analysis         *Analysis
	imports          map[*ast.Symbol]moduleImport
	syntheticImports map[unistring.String]moduleImport
}

func lowerAnalysis(ctx context.Context, a *Analysis) (result *ExecutionIR, diagnostics []Diagnostic, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			switch x := failure.(type) {
			case loweringFailure:
				result = nil
				diagnostics = []Diagnostic{x.diagnostic}
			case contextFailure:
				result = nil
				err = x.err
			default:
				panic(failure)
			}
		}
	}()
	checker, release := a.program.GetTypeChecker(ctx)
	defer release()
	result = &ExecutionIR{key: a.key, profile: a.profile}
	for _, source := range a.program.GetSourceFiles() {
		digest := sha256.Sum256([]byte(source.Text()))
		result.sources = append(result.sources, SourceIdentity{source.FileName(), fmt.Sprintf("%x", digest)})
	}
	slices.SortFunc(result.sources, func(a, b SourceIdentity) int { return strings.Compare(a.ID, b.ID) })
	type preparedModule struct {
		lowerer     *lowerer
		source      *ast.SourceFile
		transformed *ast.SourceFile
		helpers     []string
	}
	var prepared []preparedModule
	byID := make(map[string]*IRModule)
	for _, source := range a.program.GetSourceFiles() {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if source.IsDeclarationFile {
			continue
		}
		emit := printer.NewEmitContext()
		options := a.program.Options()
		if redirect := a.program.GetRedirectForResolution(source); redirect != nil {
			options = redirect.CompilerOptions()
		}
		transformOptions := &transformers.TransformOptions{Context: emit, CompilerOptions: options, Resolver: checker.GetEmitResolver(), EmitResolver: checker.GetEmitResolver(), GetEmitModuleFormatOfFile: a.program.GetEmitModuleFormatOfFile}
		transformed := source
		if options.EmitDecoratorMetadata.IsTrue() {
			transformed = tstransforms.NewMetadataTransformer(transformOptions).TransformSourceFile(transformed)
		}
		transformed = tstransforms.NewTypeEraserTransformer(transformOptions).TransformSourceFile(transformed)
		if !options.VerbatimModuleSyntax.IsTrue() {
			transformed = tstransforms.NewImportElisionTransformer(transformOptions).TransformSourceFile(transformed)
		}
		transformed = tstransforms.NewRuntimeSyntaxTransformer(transformOptions).TransformSourceFile(transformed)
		if options.ExperimentalDecorators.IsTrue() {
			transformed = tstransforms.NewLegacyDecoratorsTransformer(transformOptions).TransformSourceFile(transformed)
		}
		// ESNext preserves proposal syntax for a JavaScript printer. Our VM needs
		// those proposals normalized even when the caller asks to preserve them
		// in textual output; checking still uses the original captured options.
		executionOptions := *transformOptions
		executionOptions.CompilerOptions = options.Clone()
		// Textual helper-emission choices never introduce an external helper
		// runtime. Checking still uses the caller's original options.
		executionOptions.CompilerOptions.ImportHelpers = core.TSFalse
		executionOptions.CompilerOptions.NoEmitHelpers = core.TSFalse
		if options.GetEmitScriptTarget() >= core.ScriptTargetESNext {
			executionOptions.CompilerOptions.Target = core.ScriptTargetES2025
		}
		// The retained VM has native async functions and generators, but async
		// generators normalize to a generator plus native promise helpers.
		if source.AsNode().SubtreeFacts()&ast.SubtreeContainsForAwaitOrAsyncGenerator != 0 && executionOptions.CompilerOptions.GetEmitScriptTarget() > core.ScriptTargetES2017 {
			executionOptions.CompilerOptions.UseDefineForClassFields = core.BoolToTristate(options.GetUseDefineForClassFields())
			executionOptions.CompilerOptions.Target = core.ScriptTargetES2017
		}
		if source.LanguageVariant == core.LanguageVariantJSX {
			if options.Jsx == core.JsxEmitPreserve || options.Jsx == core.JsxEmitReactNative {
				executionOptions.CompilerOptions.Jsx = core.JsxEmitReact
			}
			if executionOptions.CompilerOptions.GetJSXTransformEnabled() {
				transformed = jsxtransforms.NewJSXTransformer(&executionOptions).TransformSourceFile(transformed)
			}
		}
		if transform := estransforms.GetESTransformer(&executionOptions); transform != nil {
			transformed = transform.TransformSourceFile(transformed)
		}
		moduleRequests := transformed.Statements.Nodes
		commonJS := ast.IsExternalModule(source) && a.program.GetEmitModuleFormatOfFile(source) == core.ModuleKindCommonJS
		if commonJS {
			transformed = moduletransforms.NewCommonJSModuleTransformer(&executionOptions).TransformSourceFile(transformed)
		}
		helperNames := make(map[string]bool)
		var collectHelpers func(*ast.Node)
		collectHelpers = func(n *ast.Node) {
			for _, helper := range emit.GetEmitHelpers(n) {
				helperNames[helper.Name] = true
			}
			n.ForEachChild(func(child *ast.Node) bool { collectHelpers(child); return false })
		}
		collectHelpers(transformed.AsNode())
		for _, helper := range emit.ReadEmitHelpers() {
			helperNames[helper.Name] = true
		}
		var helpers []string
		for name := range helperNames {
			helpers = append(helpers, name)
		}
		slices.Sort(helpers)
		if !options.GetIsolatedModules() {
			transformed = inliners.NewConstEnumInliningTransformer(transformOptions).TransformSourceFile(transformed)
		}
		module := &IRModule{id: source.FileName(), ranges: make(map[ir.Node]Range), external: ast.IsExternalModule(source)}
		module.async = moduleHasAwait(transformed.AsNode())
		module.commonJS = commonJS || hasExportEquals(moduleRequests)
		module.json = source.ScriptKind == core.ScriptKindJSON
		if module.json {
			module.external = true
		}
		module.profile = a.profile
		module.strict = module.external || options.GetStrictOptionValue(options.AlwaysStrict)
		l := &lowerer{ctx: ctx, file: source, emit: emit, module: module, checker: checker, globals: make(map[string]bool), standardGlobals: make(map[string]bool), analysis: a, imports: make(map[*ast.Symbol]moduleImport), syntheticImports: make(map[unistring.String]moduleImport)}
		l.names = &printer.NameGenerator{Context: emit, GetTextOfNode: func(n *ast.Node) string { return n.Text() }, IsFileLevelUniqueNameInCurrentFile: func(name string, _ bool) bool { _, exists := source.Identifiers[name]; return !exists }}
		module.tree = &ir.Program{File: file.NewFile(source.FileName(), source.Text(), 1)}
		l.collectModuleRequests(moduleRequests)
		l.collectDynamicImports(source.AsNode())
		if commonJS {
			for i := range module.dependencies {
				module.dependencies[i].Phase = "require"
				module.dependencies[i].Namespace = true
			}
		}
		byID[module.id] = module
		prepared = append(prepared, preparedModule{l, source, transformed, helpers})
	}
	reachable := make(map[string]bool)
	var visit func(string)
	visit = func(id string) {
		if reachable[id] {
			return
		}
		module := byID[id]
		if module == nil {
			return
		}
		reachable[id] = true
		for _, dependency := range module.dependencies {
			if !dependency.Host {
				visit(dependency.ID)
			}
		}
		for _, dependency := range module.dynamicDependencies {
			if dependency.Host {
				result.hostRoots = append(result.hostRoots, dependency.Specifier)
			} else {
				visit(dependency.ID)
			}
		}
	}
	for _, id := range a.roots {
		if byID[id] != nil {
			result.roots = append(result.roots, id)
			visit(id)
		}
	}
	for _, id := range a.moduleRoots {
		if strings.HasPrefix(id, "/") {
			visit(id)
		} else {
			result.hostRoots = append(result.hostRoots, id)
		}
	}
	for _, item := range prepared {
		l, source, transformed := item.lowerer, item.source, item.transformed
		module := l.module
		if !reachable[module.id] {
			continue
		}
		for _, dependency := range module.dependencies {
			target := byID[dependency.ID]
			json := target != nil && target.json
			if message := ValidateImportAttributes(dependency.Attributes, json, dependency.Phase == "require"); message != "" {
				return nil, []Diagnostic{{Namespace: "goya", Code: 1111, Severity: "error", Message: message, File: module.id, Range: &dependency.Range}}, nil
			}
		}
		if len(item.helpers) > 0 {
			var unknown []string
			for _, name := range item.helpers {
				if _, ok := nativeHelpers[name]; !ok && !scopedHelper(name) {
					unknown = append(unknown, name)
				}
			}
			if len(unknown) > 0 {
				return nil, []Diagnostic{{Namespace: "goya", Code: 1103, Severity: "error", Message: "runtime transform helpers are not implemented yet: " + strings.Join(unknown, ", "), File: source.FileName()}}, nil
			}
		}
		if module.json {
			module.tree.Body = l.jsonModuleStatements(source)
		} else if module.commonJS {
			l.collectCommonJSExports()
		} else {
			l.collectImports(transformed.Statements.Nodes)
			l.collectExports(transformed.Statements.Nodes)
		}
		if !module.json {
			module.tree.Body = l.statements(transformed.Statements.Nodes)
		}
		module.tree.DeclarationList = l.declarations
		for name := range l.globals {
			module.globalNames = append(module.globalNames, name)
		}
		slices.Sort(module.globalNames)
		for name := range l.standardGlobals {
			module.standardGlobalNames = append(module.standardGlobalNames, name)
		}
		slices.Sort(module.standardGlobalNames)
		result.modules = append(result.modules, module)
	}
	if err := a.prepareImportCatalog(ctx, result); err != nil {
		return nil, nil, err
	}
	return result, nil, nil
}

type contextFailure struct{ err error }

func (l *lowerer) requireGlobal(n *ast.Node) {
	original := l.sourceNode(n)
	if original.Kind != ast.KindIdentifier || original.Flags&ast.NodeFlagsSynthesized != 0 {
		return
	}
	symbol := l.checker.GetSymbolAtLocation(original)
	if symbol == nil && original.Pos() < 0 {
		l.globals[n.Text()] = true
		return
	}
	if symbol == nil {
		// An explicitly suppressed missing name still needs an implementation.
		// typeof and with have their own standard dynamic lookup semantics.
		if original.Text() == "arguments" || original.Text() == "globalThis" {
			return
		}
		for parent := original.Parent; parent != nil; parent = parent.Parent {
			if parent.Kind == ast.KindWithStatement || parent.Kind == ast.KindTypeOfExpression {
				return
			}
		}
		l.globals[n.Text()] = true
		return
	}
	if symbol.ValueDeclaration == nil {
		return
	}
	l.requireGlobalSymbol(symbol, n.Text())
}

func (l *lowerer) requireAmbientMember(n *ast.Node) {
	original := l.sourceNode(n)
	if original.Pos() < 0 || original.Flags&ast.NodeFlagsSynthesized != 0 {
		return
	}
	symbol := l.checker.GetSymbolAtLocation(original)
	if symbol == nil && original.Kind == ast.KindElementAccessExpression {
		access := original.AsElementAccessExpression()
		receiver := l.checker.GetTypeAtLocation(access.Expression)
		key := l.checker.GetTypeAtLocation(access.ArgumentExpression)
		for _, alternative := range key.Distributed() {
			if alternative.IsStringLiteral() {
				l.requireAmbientSymbol(l.checker.GetPropertyOfType(receiver, alternative.AsLiteralType().Value().(string)))
			}
		}
		return
	}
	l.requireAmbientSymbol(symbol)
}

func (l *lowerer) requireAmbientSymbol(symbol *ast.Symbol) {
	if symbol == nil || symbol.ValueDeclaration == nil {
		return
	}
	declaration := symbol.ValueDeclaration
	switch declaration.Kind {
	case ast.KindVariableDeclaration, ast.KindFunctionDeclaration, ast.KindModuleDeclaration:
	default:
		return
	}
	for parent := declaration.Parent; parent != nil; parent = parent.Parent {
		if parent.Kind == ast.KindModuleDeclaration && !ast.IsGlobalScopeAugmentation(parent) {
			return
		}
	}
	// A global declaration reached through globalThis (including typed aliases)
	// keeps the same symbol. Ordinary object fields have member declarations.
	name := ast.GetDeclarationName(declaration)
	if name != "" {
		l.requireGlobalSymbol(symbol, name)
	}
}

func (l *lowerer) requireGlobalSymbol(symbol *ast.Symbol, name string) {
	declaration := symbol.ValueDeclaration
	source := ast.GetSourceFileOfNode(declaration)
	if source == nil {
		return
	}
	if strings.HasPrefix(source.FileName(), bundled.LibPath()+"/") {
		name := strings.TrimPrefix(source.FileName(), bundled.LibPath()+"/")
		if !strings.HasPrefix(name, "lib.dom") && !strings.HasPrefix(name, "lib.webworker") && name != "lib.scripthost.d.ts" {
			l.standardGlobals[ast.GetDeclarationName(declaration)] = true
			return
		}
	}
	if source.IsDeclarationFile || ast.GetCombinedModifierFlags(declaration)&ast.ModifierFlagsAmbient != 0 {
		l.globals[name] = true
	}
}

func (l *lowerer) sourceNode(n *ast.Node) *ast.Node { return l.emit.MostOriginal(n) }
func (l *lowerer) start(n *ast.Node) int {
	if n == nil {
		return 0
	}
	n = l.sourceNode(n)
	if n.Pos() < 0 || n.End() <= n.Pos() {
		return max(0, min(len(l.file.Text()), n.Pos()))
	}
	return max(0, min(len(l.file.Text()), min(n.End(), scanner.SkipTrivia(l.file.Text(), n.Pos()))))
}
func (l *lowerer) idx(n *ast.Node) file.Idx { return file.Idx(l.start(n) + 1) }
func (l *lowerer) end(n *ast.Node) file.Idx {
	n = l.sourceNode(n)
	return file.Idx(max(l.start(n), min(len(l.file.Text()), n.End())) + 1)
}
func (l *lowerer) text(n *ast.Node) string { return l.file.Text()[l.start(n) : int(l.end(n))-1] }
func (l *lowerer) record(n *ast.Node, result ir.Node) {
	if result == nil {
		return
	}
	if err := l.ctx.Err(); err != nil {
		panic(contextFailure{err})
	}
	l.module.ranges[result] = Range{Start: sourcePosition(l.file.Text(), l.start(n)), End: sourcePosition(l.file.Text(), int(l.end(n))-1)}
}
func (l *lowerer) unsupported(n *ast.Node) {
	r := Range{Start: sourcePosition(l.file.Text(), l.start(n)), End: sourcePosition(l.file.Text(), int(l.end(n))-1)}
	panic(loweringFailure{Diagnostic{Namespace: "goya", Code: 1101, Severity: "error", Message: fmt.Sprintf("execution lowering is not implemented for %s", n.Kind.String()), File: l.file.FileName(), Range: &r}})
}
func (l *lowerer) identifier(n *ast.Node) *ir.Identifier {
	if n == nil {
		return nil
	}
	if n.Kind != ast.KindIdentifier {
		l.unsupported(n)
	}
	name := n.Text()
	if l.emit.EmitFlags(n)&printer.EFHelperName != 0 {
		name = HelperIdentifierPrefix + name
	}
	if l.emit.HasAutoGenerateInfo(n) {
		name = l.names.GenerateName(n)
	}
	result := &ir.Identifier{Name: unistring.NewFromString(name), Idx: l.idx(n)}
	l.record(n, result)
	return result
}
func (l *lowerer) statements(nodes []*ast.Node) []ir.Statement {
	var statements []ir.Statement
	for _, n := range nodes {
		if statement := l.statement(n); statement != nil {
			statements = append(statements, statement)
		}
	}
	return statements
}
func (l *lowerer) block(n *ast.Node) *ir.BlockStatement {
	if n == nil {
		return nil
	}
	result := &ir.BlockStatement{LeftBrace: l.idx(n), RightBrace: l.end(n) - 1, List: l.statements(n.Statements())}
	l.addScopedHelpers(n, result)
	l.record(n, result)
	return result
}

func (l *lowerer) bindings(n *ast.Node) []*ir.Binding {
	var bindings []*ir.Binding
	for _, declaration := range n.AsVariableDeclarationList().Declarations.Nodes {
		binding := &ir.Binding{Target: l.bindingTarget(declaration.Name()), Initializer: l.expression(declaration.Initializer())}
		l.record(declaration, binding)
		bindings = append(bindings, binding)
	}
	if n.Flags&ast.NodeFlagsBlockScoped == 0 {
		l.declarations = append(l.declarations, &ir.VariableDeclaration{Var: l.idx(n), List: bindings})
	}
	if n.Flags&ast.NodeFlagsUsing != 0 {
		l.unsupported(n)
	}
	return bindings
}
func declarationToken(n *ast.Node) token.Token {
	if n.Flags&ast.NodeFlagsConst != 0 {
		return token.CONST
	}
	return token.LET
}

func (l *lowerer) statement(n *ast.Node) (result ir.Statement) {
	if n == nil {
		return nil
	}
	defer func() { l.record(n, result) }()
	switch n.Kind {
	case ast.KindNotEmittedStatement:
		return nil
	case ast.KindEmptyStatement:
		return &ir.EmptyStatement{Semicolon: l.idx(n)}
	case ast.KindBlock:
		return l.block(n)
	case ast.KindExpressionStatement:
		return &ir.ExpressionStatement{Expression: l.expression(n.Expression())}
	case ast.KindVariableStatement:
		list := n.AsVariableStatement().DeclarationList
		bindings := l.bindings(list)
		if list.Flags&ast.NodeFlagsBlockScoped != 0 {
			return &ir.LexicalDeclaration{Idx: l.idx(n), Token: declarationToken(list), List: bindings}
		}
		return &ir.VariableStatement{Var: l.idx(n), List: bindings}
	case ast.KindIfStatement:
		x := n.AsIfStatement()
		return &ir.IfStatement{If: l.idx(n), Test: l.expression(x.Expression), Consequent: l.statement(x.ThenStatement), Alternate: l.statement(x.ElseStatement)}
	case ast.KindWhileStatement:
		x := n.AsWhileStatement()
		return &ir.WhileStatement{While: l.idx(n), Test: l.expression(x.Expression), Body: l.statement(x.Statement)}
	case ast.KindDoStatement:
		x := n.AsDoStatement()
		return &ir.DoWhileStatement{Do: l.idx(n), Test: l.expression(x.Expression), Body: l.statement(x.Statement), RightParenthesis: l.end(n) - 1}
	case ast.KindForStatement:
		x := n.AsForStatement()
		return &ir.ForStatement{For: l.idx(n), Initializer: l.forInitializer(x.Initializer), Test: l.expression(x.Condition), Update: l.expression(x.Incrementor), Body: l.statement(x.Statement)}
	case ast.KindForInStatement, ast.KindForOfStatement:
		x := n.AsForInOrOfStatement()
		into := l.forInto(x.Initializer)
		if n.Kind == ast.KindForInStatement {
			return &ir.ForInStatement{For: l.idx(n), Into: into, Source: l.expression(x.Expression), Body: l.statement(x.Statement)}
		}
		return &ir.ForOfStatement{For: l.idx(n), Into: into, Source: l.expression(x.Expression), Body: l.statement(x.Statement), Await: x.AwaitModifier != nil}
	case ast.KindBreakStatement:
		return &ir.BranchStatement{Idx: l.idx(n), Token: token.BREAK, Label: l.identifier(n.AsBreakStatement().Label)}
	case ast.KindContinueStatement:
		return &ir.BranchStatement{Idx: l.idx(n), Token: token.CONTINUE, Label: l.identifier(n.AsContinueStatement().Label)}
	case ast.KindLabeledStatement:
		x := n.AsLabeledStatement()
		return &ir.LabelledStatement{Label: l.identifier(x.Label), Colon: l.idx(x.Statement) - 1, Statement: l.statement(x.Statement)}
	case ast.KindReturnStatement:
		return &ir.ReturnStatement{Return: l.idx(n), Argument: l.expression(n.Expression())}
	case ast.KindWithStatement:
		x := n.AsWithStatement()
		return &ir.WithStatement{With: l.idx(n), Object: l.expression(x.Expression), Body: l.statement(x.Statement)}
	case ast.KindThrowStatement:
		return &ir.ThrowStatement{Throw: l.idx(n), Argument: l.expression(n.Expression())}
	case ast.KindTryStatement:
		x := n.AsTryStatement()
		result := &ir.TryStatement{Try: l.idx(n), Body: l.block(x.TryBlock), Finally: l.block(x.FinallyBlock)}
		if x.CatchClause != nil {
			catch := x.CatchClause.AsCatchClause()
			result.Catch = &ir.CatchStatement{Catch: l.idx(x.CatchClause), Body: l.block(catch.Block)}
			if catch.VariableDeclaration != nil {
				result.Catch.Parameter = l.bindingTarget(catch.VariableDeclaration.Name())
			}
		}
		return result
	case ast.KindSwitchStatement:
		x := n.AsSwitchStatement()
		result := &ir.SwitchStatement{Switch: l.idx(n), Discriminant: l.expression(x.Expression), Default: -1, RightBrace: l.end(n) - 1}
		for i, clause := range x.CaseBlock.AsCaseBlock().Clauses.Nodes {
			if clause.Kind == ast.KindDefaultClause {
				result.Default = i
			}
			result.Body = append(result.Body, &ir.CaseStatement{Case: l.idx(clause), Test: l.expression(clause.AsCaseOrDefaultClause().Expression), Consequent: l.statements(clause.Statements())})
		}
		return result
	case ast.KindDebuggerStatement:
		return &ir.DebuggerStatement{Debugger: l.idx(n)}
	case ast.KindFunctionDeclaration:
		function := l.function(n)
		if n.ModifierFlags()&ast.ModifierFlagsDefault != 0 && n.Name() == nil {
			function.Name = l.defaultIdentifier(n)
			function.NameHint = "default"
		}
		return &ir.FunctionDeclaration{Function: function}
	case ast.KindClassDeclaration:
		if n.ModifierFlags()&ast.ModifierFlagsDefault != 0 && n.Name() == nil {
			return l.defaultStatement(n, l.class(n))
		}
		return &ir.ClassDeclaration{Class: l.class(n)}
	case ast.KindExportAssignment:
		if n.AsExportAssignment().IsExportEquals {
			return &ir.ExpressionStatement{Expression: &ir.AssignExpression{Operator: token.ASSIGN, Left: &ir.DotExpression{Left: &ir.Identifier{Name: ModuleObjectName, Idx: l.idx(n)}, Identifier: ir.Identifier{Name: "exports", Idx: l.idx(n)}}, Right: l.expression(n.AsExportAssignment().Expression)}}
		}
		return l.defaultStatement(n, l.expression(n.AsExportAssignment().Expression))
	case ast.KindImportDeclaration, ast.KindExportDeclaration:
		return nil // Runtime relationships are retained in the IR module record.
	case ast.KindImportEqualsDeclaration:
		x := n.AsImportEqualsDeclaration()
		if x.IsTypeOnly {
			return nil
		}
		if x.ModuleReference.Kind != ast.KindExternalModuleReference {
			l.unsupported(n)
		}
		return &ir.LexicalDeclaration{Idx: l.idx(n), Token: token.CONST, List: []*ir.Binding{{Target: l.identifier(x.Name()), Initializer: &ir.CallExpression{Callee: &ir.Identifier{Name: ModuleRequireName, Idx: l.idx(n)}, ArgumentList: []ir.Expression{l.expression(x.ModuleReference.Expression())}, LeftParenthesis: l.idx(n), RightParenthesis: l.end(n) - 1}}}}
	default:
		l.unsupported(n)
	}
	return nil
}

func (l *lowerer) forInitializer(n *ast.Node) ir.ForLoopInitializer {
	if n == nil {
		return nil
	}
	if n.Kind != ast.KindVariableDeclarationList {
		return &ir.ForLoopInitializerExpression{Expression: l.expression(n)}
	}
	bindings := l.bindings(n)
	if n.Flags&ast.NodeFlagsBlockScoped == 0 {
		return &ir.ForLoopInitializerVarDeclList{Var: l.idx(n), List: bindings}
	}
	return &ir.ForLoopInitializerLexicalDecl{LexicalDeclaration: ir.LexicalDeclaration{Idx: l.idx(n), Token: declarationToken(n), List: bindings}}
}
func (l *lowerer) forInto(n *ast.Node) ir.ForInto {
	if n.Kind != ast.KindVariableDeclarationList {
		return &ir.ForIntoExpression{Expression: l.assignmentTarget(n)}
	}
	bindings := l.bindings(n)
	if len(bindings) != 1 {
		l.unsupported(n)
	}
	if n.Flags&ast.NodeFlagsBlockScoped == 0 {
		return &ir.ForIntoVar{Binding: bindings[0]}
	}
	return &ir.ForDeclaration{Idx: l.idx(n), IsConst: n.Flags&ast.NodeFlagsConst != 0, Target: bindings[0].Target}
}
