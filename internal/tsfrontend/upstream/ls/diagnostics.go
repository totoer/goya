// Modified for Goya: internal import paths relocated by cmd/importupstream.

// Modified for Goya: embedded-language-service.

package ls

import (
	"context"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"slices"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/compiler"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsconv"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
)

// getAllDiagnostics collects all diagnostics for a file: syntactic, semantic,
// suggestion, and (when declarations are emitted) declaration diagnostics.
// referenceTargetGrammarDiagnostics restores TS 6 target-dependent class member
// grammar in the editor only; compiler Check/Compile retain native semantics.
func referenceTargetGrammarDiagnostics(ctx context.Context, program *compiler.Program, file *ast.SourceFile, items []*ast.Diagnostic) []*ast.Diagnostic {
	if program.Options().GetEmitScriptTarget() >= core.ScriptTargetES2015 || len(file.Diagnostics()) > 0 {
		return items
	}
	added := false
	var visit func(*ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ctx.Err() != nil {
			return true
		}
		if node.Parent != nil && ast.IsClassLike(node.Parent) {
			name := node.Name()
			var message *diagnostics.Message
			switch node.Kind {
			case ast.KindMethodDeclaration, ast.KindPropertyDeclaration:
				if name != nil && ast.IsPrivateIdentifier(name) {
					message = diagnostics.Private_identifiers_are_only_available_when_targeting_ECMAScript_2015_and_higher
				}
				if message == nil && ast.IsAutoAccessorPropertyDeclaration(node) && node.Flags&ast.NodeFlagsAmbient == 0 && !(name != nil && ast.IsStringLiteral(name) && name.Text() == "constructor") {
					message = diagnostics.Properties_with_the_accessor_modifier_are_only_available_when_targeting_ECMAScript_2015_and_higher
				}
			case ast.KindGetAccessor, ast.KindSetAccessor:
				if node.Flags&ast.NodeFlagsAmbient == 0 && name != nil && ast.IsPrivateIdentifier(name) {
					message = diagnostics.Private_identifiers_are_only_available_when_targeting_ECMAScript_2015_and_higher
				}
			}
			if message != nil && name != nil {
				candidate := ast.NewDiagnostic(file, core.NewTextRange(scanner.GetTokenPosOfNode(name, file, false), name.End()), message)
				duplicate := false
				for _, existing := range items {
					if existing.Code() == candidate.Code() && existing.Loc() == candidate.Loc() {
						duplicate = true
						break
					}
				}
				if !duplicate {
					items = append(items, candidate)
					added = true
				}
			}
		}
		return node.ForEachChild(visit)
	}
	visit(file.AsNode())
	if added {
		slices.SortStableFunc(items, ast.CompareDiagnostics)
	}
	return items
}

// TypeScript 6 reports unused ambient import aliases as suggestions, including
// when noUnusedLocals is enabled. Native compiler diagnostics remain unchanged.
func referenceAmbientImportDiagnostics(ctx context.Context, program *compiler.Program, file *ast.SourceFile, items []*ast.Diagnostic) []*ast.Diagnostic {
	ch, done := program.GetTypeCheckerForFile(ctx, file)
	defer done()
	appendDiagnostic := func(node *ast.Node, message *diagnostics.Message, args ...any) {
		span := core.NewTextRange(scanner.GetTokenPosOfNode(node, file, false), node.End())
		candidate := ast.NewDiagnostic(file, span, message, args...)
		candidate.SetCategory(diagnostics.CategorySuggestion)
		for i, old := range items {
			if old.Code() == candidate.Code() && old.Loc() == span {
				items[i] = candidate
				return
			}
		}
		for i, old := range items {
			if old.Category() == diagnostics.CategorySuggestion && old.Pos() > candidate.Pos() {
				items = slices.Insert(items, i, candidate)
				return
			}
		}
		items = append(items, candidate)
	}
	var hasParseError func(*ast.Node) bool
	hasParseError = func(node *ast.Node) bool {
		return node.Flags&(ast.NodeFlagsThisNodeHasError|ast.NodeFlagsThisNodeOrAnySubNodesHasError) != 0 || node.ForEachChild(hasParseError)
	}
	var visit func(*ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ctx.Err() != nil {
			return true
		}
		if ast.IsModuleDeclaration(node) && (node.Flags&ast.NodeFlagsAmbient != 0 || node.ModifierFlags()&ast.ModifierFlagsAmbient != 0) && !ast.IsAmbientModule(node) && !hasParseError(node) && ast.IsExternalOrCommonJSModule(file) {
			name := node.Name()
			symbol := node.Symbol()
			if ast.IsIdentifier(name) && symbol != nil && node.ModifierFlags()&ast.ModifierFlagsExport == 0 {
				used := false
				resolved := ch.GetSymbolAtLocation(name)
				if resolved == nil {
					used = true
				} else {
					for _, reference := range ch.GetReferencesToSymbolInFile(file, resolved) {
						if reference != name && reference.Flags&ast.NodeFlagsJSDoc == 0 {
							used = true
							break
						}
					}
				}
				if !used {
					appendDiagnostic(name, diagnostics.X_0_is_declared_but_its_value_is_never_read, name.Text())
				}
			}
		}
		if ast.IsImportDeclaration(node) && node.Flags&ast.NodeFlagsAmbient != 0 && !hasParseError(node) {
			declaration := node.AsImportDeclaration()
			if declaration.ImportClause == nil {
				return false
			}
			clause := declaration.ImportClause.AsImportClause()
			names := []*ast.Node{}
			if clause.Name() != nil {
				names = append(names, clause.Name())
			}
			if clause.NamedBindings != nil {
				if ast.IsNamespaceImport(clause.NamedBindings) {
					names = append(names, clause.NamedBindings.Name())
				} else {
					for _, specifier := range clause.NamedBindings.Elements() {
						names = append(names, specifier.Name())
					}
				}
			}
			unused := []*ast.Node{}
			for _, name := range names {
				if ast.IsIdentifier(name) && !strings.HasPrefix(name.Text(), "_") && !ch.IsDeclarationUsed(file, name.AsIdentifier(), false, false) {
					unused = append(unused, name)
				}
			}
			if len(unused) > 0 && len(unused) == len(names) {
				if len(unused) == 1 {
					appendDiagnostic(node, diagnostics.X_0_is_declared_but_its_value_is_never_read, unused[0].Text())
				} else {
					appendDiagnostic(node, diagnostics.All_imports_in_import_declaration_are_unused)
				}
			} else {
				for _, name := range unused {
					appendDiagnostic(name, diagnostics.X_0_is_declared_but_its_value_is_never_read, name.Text())
				}
			}
			return false
		}
		return node.ForEachChild(visit)
	}
	visit(file.AsNode())
	return items
}

// referenceUnusedDiagnostics restores the pinned editor diagnostic shapes without
// changing the native compiler's Check/Compile diagnostics.
// TypeScript 6 retains the assignability head for missing object properties
// in argument and satisfies diagnostics. This adaptation changes editor display
// and provider routing only; native Check/Compile diagnostics remain unchanged.
func referenceMissingObjectDiagnosticHeads(file *ast.SourceFile, items []*ast.Diagnostic) []*ast.Diagnostic {
	result := make([]*ast.Diagnostic, 0, len(items))
	for _, item := range items {
		code := item.Code()
		if code != diagnostics.Type_0_is_missing_the_following_properties_from_type_1_Colon_2.Code() && code != diagnostics.Type_0_is_missing_the_following_properties_from_type_1_Colon_2_and_3_more.Code() && code != diagnostics.Property_0_is_missing_in_type_1_but_required_in_type_2.Code() {
			result = append(result, item)
			continue
		}
		token := astnav.GetTokenAtPosition(file, item.Pos())
		var message *diagnostics.Message
		if token != nil && token.Parent != nil {
			parent := token.Parent
			if ast.IsSatisfiesExpression(parent) {
				message = diagnostics.Type_0_does_not_satisfy_the_expected_type_1
			}
			expression := referenceFixableErrorSpanExpression(file, item.Loc())
			if expression != nil && expression.Parent != nil && ast.IsCallExpression(expression.Parent) && slices.Contains(expression.Parent.Arguments(), expression) {
				message = diagnostics.Argument_of_type_0_is_not_assignable_to_parameter_of_type_1
			}
		}
		args := item.MessageArgs()
		if message != nil {
			var source, target string
			if code == diagnostics.Property_0_is_missing_in_type_1_but_required_in_type_2.Code() {
				if len(args) >= 3 {
					source = args[1]
					target = args[2]
				}
			} else {
				if len(args) >= 2 {
					source = args[0]
					target = args[1]
				}
			}
			if source != "" && target != "" {
				result = append(result, ast.NewDiagnosticChain(item, message, source, target))
				continue
			}
		}
		result = append(result, item)
	}
	return result
}
func referenceUnusedDiagnostics(file *ast.SourceFile, items []*ast.Diagnostic) []*ast.Diagnostic {
	result := make([]*ast.Diagnostic, 0, len(items))
	fresh := func(old *ast.Diagnostic, node *ast.Node, message *diagnostics.Message, args ...any) *ast.Diagnostic {
		diagnostic := ast.NewDiagnostic(file, core.NewTextRange(scanner.GetTokenPosOfNode(node, file, false), node.End()), message, args...)
		diagnostic.SetCategory(old.Category())
		return diagnostic
	}
	for _, item := range items {
		code := item.Code()
		if code != diagnostics.X_0_is_declared_but_its_value_is_never_read.Code() && code != diagnostics.X_0_is_declared_but_never_used.Code() && code != diagnostics.All_destructured_elements_are_unused.Code() && code != diagnostics.All_variables_are_unused.Code() {
			result = append(result, item)
			continue
		}
		token := astnav.GetTokenAtPosition(file, item.Pos())
		if token == nil || token.Parent == nil {
			result = append(result, item)
			continue
		}
		parent := token.Parent
		if (code == diagnostics.X_0_is_declared_but_its_value_is_never_read.Code() || code == diagnostics.X_0_is_declared_but_never_used.Code()) && ast.IsIdentifier(token) {
			declaration := ast.FindAncestor(token, ast.IsImportDeclaration)
			if declaration != nil && declaration.AsImportDeclaration().ImportClause != nil {
				clause := declaration.AsImportDeclaration().ImportClause.AsImportClause()
				count := 0
				if clause.Name() != nil {
					count++
				}
				if clause.NamedBindings != nil {
					if ast.IsNamespaceImport(clause.NamedBindings) {
						count++
					} else {
						count += len(clause.NamedBindings.Elements())
					}
				}
				if count == 1 {
					result = append(result, fresh(item, declaration, diagnostics.X_0_is_declared_but_its_value_is_never_read, token.Text()))
					continue
				}
			}
		}
		if code == diagnostics.X_0_is_declared_but_never_used.Code() && ast.IsTypeParameterDeclaration(parent) {
			node := parent
			if parent.Parent != nil && ast.IsInferTypeNode(parent.Parent) {
				node = parent.Parent
			}
			replacement := fresh(item, node, diagnostics.X_0_is_declared_but_its_value_is_never_read, parent.Name().Text())
			if node == parent && parent.Parent != nil {
				list := parent.Parent.TypeParameterList()
				if list != nil && len(list.Nodes) == 1 {
					end := min(len(file.Text()), scanner.SkipTriviaEx(file.Text(), list.End(), nil)+1)
					replacement.SetLocation(core.NewTextRange(list.Pos()-1, end))
				}
			}
			result = append(result, replacement)
			continue
		}
		if code == diagnostics.All_destructured_elements_are_unused.Code() && ast.IsArrayBindingPattern(parent) {
			root := ast.GetRootDeclaration(parent)
			if root != nil && !ast.IsParameterDeclaration(root) {
				for _, element := range parent.Elements() {
					if element.Name() != nil {
						result = append(result, fresh(item, element, diagnostics.X_0_is_declared_but_its_value_is_never_read, scanner.GetTextOfNode(element.Name())))
					}
				}
				continue
			}
		}
		if code == diagnostics.All_variables_are_unused.Code() && ast.IsVariableDeclarationList(parent) && parent.Parent != nil && ast.IsVariableStatement(parent.Parent) {
			replacement := item.Clone()
			replacement.SetLocation(core.NewTextRange(scanner.GetTokenPosOfNode(parent.Parent, file, false), parent.Parent.End()))
			result = append(result, replacement)
			continue
		}
		result = append(result, item)
	}
	return result
}

func getAllDiagnostics(ctx context.Context, program *compiler.Program, file *ast.SourceFile, referencePublication ...bool) []*ast.Diagnostic {
	var diags []*ast.Diagnostic
	diags = append(diags, program.GetSyntacticDiagnostics(ctx, file)...)
	diags = append(diags, referenceTargetGrammarDiagnostics(ctx, program, file, program.GetSemanticDiagnostics(ctx, file))...)
	referenceSemanticEnd := len(diags)
	suggestions := referenceAsyncConversionDiagnostics(ctx, program, file, referenceUnmatchedParamDiagnostics(ctx, program, file, referenceJSDocDiagnostics(file, referenceRequireImportDiagnostics(file, program.GetSuggestionDiagnostics(ctx, file)))))
	slices.SortStableFunc(suggestions, referenceInferenceSuggestionOrder)
	diags = append(diags, suggestions...)
	if program.Options().GetEmitDeclarations() {
		var declarationDiagnostics []*ast.Diagnostic
		if program.Options().IsolatedDeclarations.IsTrue() {
			declarationDiagnostics = referenceIsolatedDeclarationDiagnostics(ctx, program, file)
		} else {
			declarationDiagnostics = program.GetDeclarationDiagnostics(ctx, file)
		}
		if len(referencePublication) > 0 && referencePublication[0] {
			diags = slices.Concat(diags[:referenceSemanticEnd], declarationDiagnostics, diags[referenceSemanticEnd:])
		} else {
			diags = append(diags, declarationDiagnostics...)
		}
	}
	return referenceInferenceDiagnosticHeads(file, referenceClassPrototypeDiagnostics(ctx, program, file, referenceMissingObjectDiagnosticHeads(file, referenceUnusedDiagnostics(file, referenceAmbientImportDiagnostics(ctx, program, file, diags)))))
}

func (l *LanguageService) ProvideDiagnostics(ctx context.Context, uri lsproto.DocumentUri) (lsproto.DocumentDiagnosticResponse, error) {
	program, file := l.getProgramAndFile(uri)

	if l.UserPreferences().EnableValidation.IsFalse() {
		diagnostics := []*lsproto.Diagnostic{}
		return lsproto.RelatedFullDocumentDiagnosticReportOrUnchangedDocumentDiagnosticReport{
			FullDocumentDiagnosticReport: &lsproto.RelatedFullDocumentDiagnosticReport{
				Items: diagnostics,
			},
		}, nil
	}

	diagnostics := getAllDiagnostics(ctx, program, file, true)

	return lsproto.RelatedFullDocumentDiagnosticReportOrUnchangedDocumentDiagnosticReport{
		FullDocumentDiagnosticReport: &lsproto.RelatedFullDocumentDiagnosticReport{
			Items: l.toLSPDiagnostics(ctx, diagnostics),
		},
	}, nil
}

func (l *LanguageService) toLSPDiagnostics(ctx context.Context, diagnostics ...[]*ast.Diagnostic) []*lsproto.Diagnostic {
	size := 0
	for _, diagSlice := range diagnostics {
		size += len(diagSlice)
	}
	lspDiagnostics := make([]*lsproto.Diagnostic, 0, size)
	for _, diagSlice := range diagnostics {
		for _, diag := range diagSlice {
			lspDiagnostics = append(lspDiagnostics, lsconv.DiagnosticToLSPPull(ctx, l.converters, diag, l.UserPreferences().ReportStyleChecksAsWarnings.IsTrue()))
		}
	}
	return lspDiagnostics
}
