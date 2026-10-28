// Modified for Goya: internal import paths relocated by cmd/importupstream.

// Modified for Goya: embedded-language-service.

package ls

import (
	"cmp"
	"context"
	"fmt"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/checker"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/autoimport"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsutil"
	"github.com/totoer/goya/internal/tsfrontend/upstream/nodebuilder"
	"github.com/totoer/goya/internal/tsfrontend/upstream/printer"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"github.com/totoer/goya/internal/tsfrontend/upstream/stringutil"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/collections"
	"github.com/totoer/goya/internal/tsfrontend/upstream/compiler"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsconv"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
)

// Reference quick-fix operations expose detached edit metadata to the facade.
// Providers retain the native AST/checker only for the duration of this call.
type ReferenceFileCodeChanges struct {
	FileName    string
	TextChanges []*lsproto.TextEdit
	IsNewFile   bool
}
type ReferenceCodeFixAction struct {
	FixName           string
	Description       string
	Changes           []ReferenceFileCodeChanges
	FixID             string
	FixAllDescription string
}

func referenceCodeFixName(provider *CodeFixProvider) string {
	if provider.Name != "" {
		return provider.Name
	}
	switch provider {
	case ImportFixProvider:
		return "import"
	case IsolatedDeclarationsFixProvider:
		return "fixMissingTypeAnnotationOnExports"
	case FixClassIncorrectlyImplementsInterfaceProvider:
		return "fixClassIncorrectlyImplementsInterface"
	}
	return ""
}
func (l *LanguageService) ProvideReferenceCodeFixes(ctx context.Context, uri lsproto.DocumentUri, diagnostic *lsproto.Diagnostic) ([]ReferenceCodeFixAction, error) {
	copy := *l
	copy.activeConfig.IncludeCompletionsForModuleExports = core.TSTrue
	l = &copy
	program, file := l.getProgramAndFile(uri)
	span := l.converters.FromLSPRange(file, diagnostic.Range)
	code := *diagnostic.Code.Integer
	result := []ReferenceCodeFixAction{}
	for _, provider := range codeFixProviders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !containsErrorCode(provider.ErrorCodes, code) {
			continue
		}
		request := &CodeFixContext{SourceFile: file, Span: span, ErrorCode: code, Program: program, LS: l, Diagnostic: diagnostic}
		actions, err := provider.GetCodeActions(ctx, request)
		if err != nil {
			return nil, err
		}
		availabilityCodes := provider.ErrorCodes
		if provider.ReferenceFixAllErrorCodes != nil {
			availabilityCodes = provider.ReferenceFixAllErrorCodes
		}
		available := hasMultipleFixableDiagnostics(ctx, program, file, availabilityCodes)
		for _, action := range actions {
			owned := *action
			if !available {
				owned.FixID = ""
				owned.FixAllDescription = ""
			}
			action = &owned
			fixName := action.ReferenceFixName
			if fixName == "" {
				fixName = referenceCodeFixName(provider)
			}
			result = append(result, ReferenceCodeFixAction{FixName: fixName, Description: action.Description, Changes: referenceActionChanges(file, action), FixID: action.FixID, FixAllDescription: action.FixAllDescription})
		}
	}
	return result, ctx.Err()
}
func (l *LanguageService) ProvideReferenceCombinedCodeFix(ctx context.Context, uri lsproto.DocumentUri, fixID string) ([]ReferenceFileCodeChanges, error) {
	copy := *l
	copy.activeConfig.IncludeCompletionsForModuleExports = core.TSTrue
	l = &copy
	program, file := l.getProgramAndFile(uri)
	for _, provider := range codeFixProviders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !slices.Contains(provider.FixIds, fixID) || provider.GetAllCodeActions == nil {
			continue
		}
		action, err := provider.GetAllCodeActions(ctx, &CodeFixContext{SourceFile: file, Program: program, LS: l, FixID: fixID})
		if err != nil {
			return nil, err
		}
		if action == nil {
			return []ReferenceFileCodeChanges{}, nil
		}
		if action.ReferenceChanges != nil {
			return action.ReferenceChanges, ctx.Err()
		}
		if len(action.Changes) == 0 {
			return []ReferenceFileCodeChanges{}, nil
		}
		return []ReferenceFileCodeChanges{{FileName: file.FileName(), TextChanges: action.Changes}}, ctx.Err()
	}
	return []ReferenceFileCodeChanges{}, nil
}
func ReferenceSupportedCodeFixes() []int32 {
	codes := map[int32]bool{}
	for _, provider := range codeFixProviders {
		for _, code := range provider.ErrorCodes {
			codes[code] = true
		}
	}
	result := make([]int32, 0, len(codes))
	for code := range codes {
		result = append(result, code)
	}
	slices.Sort(result)
	return result
}

func referenceActionChanges(file *ast.SourceFile, action *CodeAction) []ReferenceFileCodeChanges {
	if action.ReferenceChanges != nil {
		return action.ReferenceChanges
	}
	return []ReferenceFileCodeChanges{{FileName: file.FileName(), TextChanges: action.Changes}}
}

// Reference syntax fixes port the pinned TypeScript 6 providers. AST/checker
// state remains request-local; only detached text changes leave this layer.
func referenceTrackerFileChanges(tracker *change.Tracker) []ReferenceFileCodeChanges {
	result := []ReferenceFileCodeChanges{}
	for _, file := range tracker.GetReferenceOrderedChanges() {
		result = append(result, ReferenceFileCodeChanges{FileName: file.FileName, TextChanges: file.TextChanges})
	}
	return result
}

type referenceSyntaxFix func(context.Context, *CodeFixContext, *change.Tracker, map[*ast.Node]bool) (string, error)

func newReferenceSyntaxFixProvider(name string, codes []int32, allDescription *diagnostics.Message, apply referenceSyntaxFix, allArgs ...any) *CodeFixProvider {
	provider := &CodeFixProvider{Name: name, ErrorCodes: codes, FixIds: []string{name}}
	provider.GetCodeActions = func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		title, err := apply(ctx, request, tracker, map[*ast.Node]bool{})
		if err != nil {
			return nil, err
		}
		files := referenceTrackerFileChanges(tracker)
		if len(files) == 0 {
			return nil, nil
		}
		action := &CodeAction{Description: title, ReferenceChanges: files}
		if allDescription != nil {
			action.FixID = name
			action.FixAllDescription = allDescription.Localize(locale.FromContext(ctx), allArgs...)
		}
		return []*CodeAction{action}, ctx.Err()
	}
	provider.GetAllCodeActions = func(ctx context.Context, request *CodeFixContext) (*CombinedCodeActions, error) {
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		seen := map[*ast.Node]bool{}
		for _, diagnostic := range getAllDiagnostics(ctx, request.Program, request.SourceFile) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !slices.Contains(codes, diagnostic.Code()) {
				continue
			}
			individual := *request
			individual.Span = core.NewTextRange(diagnostic.Pos(), diagnostic.End())
			individual.ErrorCode = diagnostic.Code()
			if _, err := apply(ctx, &individual, tracker, seen); err != nil {
				return nil, err
			}
		}
		return &CombinedCodeActions{Description: allDescription.Localize(locale.FromContext(ctx), allArgs...), ReferenceChanges: referenceTrackerFileChanges(tracker)}, ctx.Err()
	}
	if allDescription == nil {
		provider.FixIds = nil
		provider.GetAllCodeActions = nil
	}
	return provider
}

// TS 6 creates all immediate children with the caller's text before searching
// for a const token. Preserve that traversal for foreign declaration lists,
// including no-result, rejected trivia and caller-file edit behavior.
func referenceConstKeywordInCaller(declaration *ast.Node, file *ast.SourceFile) *ast.Node {
	owner := ast.GetSourceFileOfNode(declaration)
	if owner == file {
		return astnav.FindChildOfKind(declaration, ast.KindConstKeyword, file)
	}
	ownerUnits := func(offset int) int { return len(utf16.Encode([]rune(owner.Text()[:offset]))) }
	callerUnits := utf16.Encode([]rune(file.Text()))
	byteAt := func(units int) int {
		count := 0
		for offset, r := range file.Text() {
			if count == units {
				return offset
			}
			count++
			if r > 0xffff {
				count++
			}
			if count > units {
				panic(&change.InvalidEdits{Reason: "const declaration source tokenization split a surrogate"})
			}
		}
		if units == count {
			return len(file.Text())
		}
		panic(&change.InvalidEdits{Reason: "const declaration source tokenization exceeded source"})
	}
	var keyword *ast.Node
	scanGap := func(start, end int, direct bool) {
		startUnits, endUnits := ownerUnits(start), ownerUnits(end)
		if startUnits >= endUnits {
			return
		}
		suffix := ""
		if startUnits < len(callerUnits) {
			suffix = string(utf16.Decode(callerUnits[startUnits:]))
		}
		scan := scanner.NewScanner()
		scan.SetText(suffix)
		pos := startUnits
		for pos < endUnits {
			kind := scan.Scan()
			endPos := startUnits + len(utf16.Encode([]rune(suffix[:scan.TokenEnd()])))
			if endPos <= endUnits {
				if kind == ast.KindIdentifier {
					panic(&change.InvalidEdits{Reason: "const declaration source tokenization found identifier trivia"})
				}
				if direct && kind == ast.KindConstKeyword && keyword == nil {
					fullStart := startUnits + len(utf16.Encode([]rune(suffix[:scan.TokenFullStart()])))
					keyword = file.GetOrCreateToken(kind, byteAt(fullStart), byteAt(endPos), declaration, scan.TokenFlags())
				}
			}
			pos = endPos
			if kind == ast.KindEndOfFile {
				break
			}
		}
	}
	list := declaration.AsVariableDeclarationList().Declarations
	scanGap(declaration.Pos(), list.Pos(), true)
	pos := list.Pos()
	for _, child := range list.Nodes {
		scanGap(pos, child.Pos(), false)
		pos = child.End()
	}
	scanGap(pos, list.End(), false)
	scanGap(list.End(), declaration.End(), true)
	return keyword
}

var ReferenceConvertConstToLetProvider = newReferenceSyntaxFixProvider("fixConvertConstToLet",
	[]int32{diagnostics.Cannot_assign_to_0_because_it_is_a_constant.Code()},
	diagnostics.Convert_all_const_to_let, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		checker, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		symbol := checker.GetSymbolAtLocation(token)
		if symbol == nil || symbol.ValueDeclaration == nil {
			return "", nil
		}
		declaration := symbol.ValueDeclaration.Parent
		if declaration == nil || !ast.IsVariableDeclarationList(declaration) {
			return "", nil
		}
		if seen[symbol.ValueDeclaration] {
			return "", nil
		}
		seen[symbol.ValueDeclaration] = true
		file := request.SourceFile
		keyword := referenceConstKeywordInCaller(declaration, file)
		if keyword == nil {
			return "", nil
		}
		tracker.ReplaceNode(file, keyword, tracker.NewToken(ast.KindLetKeyword), nil)
		return diagnostics.Convert_const_to_let.Localize(locale.FromContext(ctx)), ctx.Err()
	})

var ReferenceUseBigintLiteralProvider = newReferenceSyntaxFixProvider("useBigintLiteral",
	[]int32{diagnostics.Numeric_literals_with_absolute_values_equal_to_2_53_or_greater_are_too_large_to_be_represented_accurately_as_integers.Code()},
	diagnostics.Convert_all_to_bigint_numeric_literals, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil || !ast.IsNumericLiteral(token) || seen[token] {
			return "", nil
		}
		seen[token] = true
		text := scanner.GetTextOfNode(token) + "n"
		tracker.ReplaceNode(request.SourceFile, token, tracker.NewBigIntLiteral(text, ast.TokenFlagsNone), nil)
		return diagnostics.Convert_to_a_bigint_numeric_literal.Localize(locale.FromContext(ctx)), ctx.Err()
	})

var ReferenceNoPropertyAccessFromIndexSignatureProvider = newReferenceSyntaxFixProvider("fixNoPropertyAccessFromIndexSignature",
	[]int32{diagnostics.Property_0_comes_from_an_index_signature_so_it_must_be_accessed_with_0.Code()},
	diagnostics.Use_element_access_for_all_undeclared_properties, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil || token.Parent == nil || !ast.IsPropertyAccessExpression(token.Parent) {
			return "", nil
		}
		node := token.Parent
		if seen[node] {
			return "", nil
		}
		seen[node] = true
		access := node.AsPropertyAccessExpression()
		flags := ast.TokenFlagsNone
		if lsutil.GetQuotePreference(request.SourceFile, request.LS.UserPreferences()) == lsutil.QuotePreferenceSingle {
			flags = ast.TokenFlagsSingleQuote
		}
		argument := tracker.NewStringLiteral(access.Name().Text(), flags)
		replacement := tracker.NewElementAccessExpression(access.Expression, access.QuestionDotToken, argument, node.Flags&ast.NodeFlagsOptionalChain)
		tracker.ReplaceNode(request.SourceFile, node, replacement, nil)
		return diagnostics.Use_element_access_for_0.Localize(locale.FromContext(ctx), access.Name().Text()), ctx.Err()
	})

var ReferenceExtendsInterfaceBecomesImplementsProvider = newReferenceSyntaxFixProvider("extendsInterfaceBecomesImplements",
	[]int32{diagnostics.Cannot_extend_an_interface_0_Did_you_mean_implements.Code()},
	diagnostics.Change_all_extended_interfaces_to_implements, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil {
			return "", nil
		}
		class := ast.GetContainingClass(token)
		if class == nil {
			return "", nil
		}
		clause := ast.GetHeritageClause(class, ast.KindExtendsKeyword)
		if clause == nil {
			return "", nil
		}
		keyword := astnav.FindChildOfKind(clause, ast.KindExtendsKeyword, request.SourceFile)
		if keyword == nil || seen[keyword] {
			return "", nil
		}
		seen[keyword] = true
		tracker.ReplaceNode(request.SourceFile, keyword, tracker.NewToken(ast.KindImplementsKeyword), nil)
		implements := ast.GetHeritageClause(class, ast.KindImplementsKeyword)
		if implements != nil {
			existing := astnav.FindChildOfKind(implements, ast.KindImplementsKeyword, request.SourceFile)
			if existing != nil {
				pos := request.LS.converters.PositionToLineAndCharacter(request.SourceFile, core.TextPos(existing.Pos()))
				tracker.ReplaceRange(request.SourceFile, lsproto.Range{Start: pos, End: pos}, tracker.NewToken(ast.KindCommaToken), change.NodeOptions{})
				text := request.SourceFile.Text()
				end := existing.End()
				for end < len(text) {
					r, width := utf8.DecodeRuneInString(text[end:])
					if !stringutil.IsWhiteSpaceSingleLine(r) {
						break
					}
					end += width
				}
				tracker.DeleteRange(request.SourceFile, core.NewTextRange(scanner.GetTokenPosOfNode(existing, request.SourceFile, false), end))
			}
		}
		return diagnostics.Change_extends_to_implements.Localize(locale.FromContext(ctx)), ctx.Err()
	})

var ReferenceUnknownConversionProvider = newReferenceSyntaxFixProvider("addConvertToUnknownForNonOverlappingTypes",
	[]int32{diagnostics.Conversion_of_type_0_to_type_1_may_be_a_mistake_because_neither_type_sufficiently_overlaps_with_the_other_If_this_was_intentional_convert_the_expression_to_unknown_first.Code()},
	diagnostics.Add_unknown_to_all_conversions_of_non_overlapping_types, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		node := ast.FindAncestor(astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos()), func(n *ast.Node) bool { return ast.IsAsExpression(n) || ast.IsTypeAssertion(n) })
		if node == nil || seen[node] {
			return "", nil
		}
		seen[node] = true
		var expression, replacement *ast.Node
		unknown := tracker.NewKeywordTypeNode(ast.KindUnknownKeyword)
		if ast.IsAsExpression(node) {
			expression = node.AsAsExpression().Expression
			replacement = tracker.NewAsExpression(expression, unknown)
		} else {
			expression = node.AsTypeAssertion().Expression
			replacement = tracker.NewTypeAssertion(unknown, expression)
		}
		tracker.ReplaceNode(request.SourceFile, expression, replacement, nil)
		return diagnostics.Add_unknown_conversion_for_non_overlapping_types.Localize(locale.FromContext(ctx)), ctx.Err()
	})
var ReferencePropertyAssignmentProvider = newReferenceSyntaxFixProvider("fixPropertyAssignment",
	[]int32{diagnostics.Did_you_mean_to_use_a_Colon_An_can_only_follow_a_property_name_when_the_containing_object_literal_is_part_of_a_destructuring_pattern.Code()},
	diagnostics.Switch_each_misused_0_to_1, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil || token.Parent == nil || !ast.IsShorthandPropertyAssignment(token.Parent) {
			return "", nil
		}
		node := token.Parent
		property := node.AsShorthandPropertyAssignment()
		if property.ObjectAssignmentInitializer == nil || seen[node] {
			return "", nil
		}
		seen[node] = true
		tracker.ReplaceNode(request.SourceFile, node, tracker.NewPropertyAssignment(nil, property.Name(), nil, nil, property.ObjectAssignmentInitializer), nil)
		return diagnostics.Change_0_to_1.Localize(locale.FromContext(ctx), "=", ":"), ctx.Err()
	}, "=", ":")
var ReferenceExpectedCommaProvider = newReferenceSyntaxFixProvider("fixExpectedComma",
	[]int32{diagnostics.X_0_expected.Code()},
	diagnostics.Change_0_to_1, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		node := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if node == nil || node.Kind != ast.KindSemicolonToken || node.Parent == nil || (!ast.IsObjectLiteralExpression(node.Parent) && !ast.IsArrayLiteralExpression(node.Parent)) || seen[node] {
			return "", nil
		}
		seen[node] = true
		tracker.ReplaceNode(request.SourceFile, node, tracker.NewToken(ast.KindCommaToken), nil)
		return diagnostics.Change_0_to_1.Localize(locale.FromContext(ctx), ";", ","), ctx.Err()
	}, ";", ",")
var ReferenceMissingNewOperatorProvider = newReferenceSyntaxFixProvider("addMissingNewOperator",
	[]int32{diagnostics.Value_of_type_0_is_not_callable_Did_you_mean_to_include_new.Code()},
	diagnostics.Add_missing_new_operator_to_all_calls, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		node := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		for node != nil && node.End() < request.Span.End() {
			node = node.Parent
		}
		if node == nil || !ast.IsCallExpression(node) || seen[node] {
			return "", nil
		}
		seen[node] = true
		call := node.AsCallExpression()
		tracker.ReplaceNode(request.SourceFile, node, tracker.NewNewExpression(call.Expression, call.TypeArguments, call.Arguments), nil)
		return diagnostics.Add_missing_new_operator_to_call.Localize(locale.FromContext(ctx)), ctx.Err()
	})
var ReferenceMissingModuleTypeofProvider = newReferenceSyntaxFixProvider("fixAddModuleReferTypeMissingTypeof",
	[]int32{diagnostics.Module_0_does_not_refer_to_a_type_but_is_used_as_a_type_here_Did_you_mean_typeof_import_0.Code()},
	diagnostics.Add_missing_typeof, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil || token.Kind != ast.KindImportKeyword || token.Parent == nil || token.Parent.Kind != ast.KindImportType {
			return "", nil
		}
		node := token.Parent
		if seen[node] {
			return "", nil
		}
		seen[node] = true
		value := node.AsImportTypeNode()
		tracker.ReplaceNode(request.SourceFile, node, tracker.UpdateImportTypeNode(value, true, value.Argument, value.Attributes, value.Qualifier, value.TypeArguments), nil)
		return diagnostics.Add_missing_typeof.Localize(locale.FromContext(ctx)), ctx.Err()
	})
var ReferenceRemoveAccidentalCallParenthesesProvider = newReferenceSyntaxFixProvider("removeAccidentalCallParentheses",
	[]int32{diagnostics.This_expression_is_not_callable_because_it_is_a_get_accessor_Did_you_mean_to_use_it_without.Code()},
	nil, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		node := ast.FindAncestor(astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos()), ast.IsCallExpression)
		if node == nil || seen[node] {
			return "", nil
		}
		seen[node] = true
		tracker.DeleteRange(request.SourceFile, core.NewTextRange(node.AsCallExpression().Expression.End(), node.End()))
		return diagnostics.Remove_parentheses.Localize(locale.FromContext(ctx)), ctx.Err()
	})

var ReferenceAwaitInSyncFunctionProvider = newReferenceSyntaxFixProvider("fixAwaitInSyncFunction",
	[]int32{
		diagnostics.X_await_expressions_are_only_allowed_within_async_functions_and_at_the_top_levels_of_modules.Code(),
		diagnostics.X_await_using_statements_are_only_allowed_within_async_functions_and_at_the_top_levels_of_modules.Code(),
		diagnostics.X_for_await_loops_are_only_allowed_within_async_functions_and_at_the_top_levels_of_modules.Code(),
		diagnostics.Cannot_find_name_0_Did_you_mean_to_write_this_in_an_async_function.Code(),
	}, diagnostics.Add_all_missing_async_modifiers, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil {
			return "", nil
		}
		fn := ast.GetContainingFunction(token)
		if fn == nil {
			return "", nil
		}
		var before *ast.Node
		switch fn.Kind {
		case ast.KindMethodDeclaration:
			before = fn.Name()
		case ast.KindFunctionDeclaration, ast.KindFunctionExpression:
			before = astnav.FindChildOfKind(fn, ast.KindFunctionKeyword, request.SourceFile)
		case ast.KindArrowFunction:
			kind := ast.KindOpenParenToken
			if fn.TypeParameterList() != nil {
				kind = ast.KindLessThanToken
			}
			before = astnav.FindChildOfKind(fn, kind, request.SourceFile)
			if before == nil && len(fn.Parameters()) > 0 {
				before = fn.Parameters()[0]
			}
		default:
			return "", nil
		}
		if before == nil || seen[before] {
			return "", nil
		}
		seen[before] = true
		returnType := fn.Type()
		if returnType == nil && fn.Parent != nil && ast.IsVariableDeclaration(fn.Parent) {
			annotation := fn.Parent.Type()
			if annotation != nil && ast.IsFunctionTypeNode(annotation) {
				returnType = annotation.Type()
			}
		}
		if returnType != nil {
			var entity *ast.Node
			switch returnType.Kind {
			case ast.KindTypeReference:
				entity = returnType.AsTypeReferenceNode().TypeName
			case ast.KindIdentifier, ast.KindQualifiedName:
				entity = returnType
			case ast.KindExpressionWithTypeArguments:
				expression := returnType.AsExpressionWithTypeArguments().Expression
				if ast.IsEntityNameExpression(expression) {
					entity = expression
				}
			}
			if entity == nil || !ast.IsIdentifier(entity) || entity.Text() != "Promise" {
				replacement := tracker.NewTypeReferenceNode(tracker.NewIdentifier("Promise"), tracker.NewNodeList([]*ast.Node{returnType}))
				tracker.ReplaceNode(request.SourceFile, returnType, replacement, nil)
			}
		}
		tracker.InsertModifierBefore(request.SourceFile, ast.KindAsyncKeyword, before)
		return diagnostics.Add_async_modifier_to_containing_function.Localize(locale.FromContext(ctx)), ctx.Err()
	})

var ReferenceRemoveUnnecessaryAwaitProvider = newReferenceSyntaxFixProvider("removeUnnecessaryAwait",
	[]int32{diagnostics.X_await_has_no_effect_on_the_type_of_this_expression.Code()},
	diagnostics.Remove_all_unnecessary_uses_of_await, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil || token.Kind != ast.KindAwaitKeyword || token.Parent == nil || !ast.IsAwaitExpression(token.Parent) {
			return "", nil
		}
		node := token.Parent
		if seen[node] {
			return "", nil
		}
		seen[node] = true
		expression := node.AsAwaitExpression().Expression
		replace := node
		if node.Parent != nil && ast.IsParenthesizedExpression(node.Parent) && ast.IsIdentifier(ast.GetLeftmostExpression(expression, false)) {
			preceding := astnav.FindPrecedingToken(request.SourceFile, node.Parent.Pos())
			if preceding != nil && preceding.Kind != ast.KindNewKeyword {
				replace = node.Parent
			}
		}
		tracker.ReplaceNode(request.SourceFile, replace, expression, nil)
		return diagnostics.Remove_unnecessary_await.Localize(locale.FromContext(ctx)), ctx.Err()
	})

var ReferenceMissingCallParenthesesProvider = newReferenceSyntaxFixProvider("fixMissingCallParentheses",
	[]int32{diagnostics.This_condition_will_always_return_true_since_this_function_is_always_defined_Did_you_mean_to_call_it_instead.Code()},
	diagnostics.Add_all_missing_call_parentheses, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		node := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if node == nil {
			return "", nil
		}
		if node.Parent != nil && ast.IsPropertyAccessExpression(node.Parent) {
			current := node.Parent
			for current.Parent != nil && ast.IsPropertyAccessExpression(current.Parent) {
				current = current.Parent
			}
			node = current.Name()
		} else if !ast.IsIdentifier(node) {
			return "", nil
		}
		if seen[node] {
			return "", nil
		}
		seen[node] = true
		span := request.LS.createLspRangeFromNode(node, request.SourceFile)
		tracker.ReplaceRangeWithText(request.SourceFile, span, node.Text()+"()")
		return diagnostics.Add_missing_call_parentheses.Localize(locale.FromContext(ctx)), ctx.Err()
	})

var ReferenceForgottenThisPropertyAccessProvider = newReferenceSyntaxFixProvider("forgottenThisPropertyAccess",
	[]int32{
		diagnostics.Cannot_find_name_0_Did_you_mean_the_instance_member_this_0.Code(),
		diagnostics.Private_identifiers_are_only_allowed_in_class_bodies_and_may_only_be_used_as_part_of_a_class_member_declaration_property_access_or_on_the_left_hand_side_of_an_in_expression.Code(),
		diagnostics.Cannot_find_name_0_Did_you_mean_the_static_member_1_0.Code(),
	}, diagnostics.Add_qualifier_to_all_unresolved_variables_matching_a_member_name, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		node := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if node == nil || (!ast.IsIdentifier(node) && !ast.IsPrivateIdentifier(node)) || seen[node] {
			return "", nil
		}
		seen[node] = true
		qualifier := "this"
		var expression *ast.Node = tracker.NewKeywordExpression(ast.KindThisKeyword)
		if request.ErrorCode == diagnostics.Cannot_find_name_0_Did_you_mean_the_static_member_1_0.Code() {
			class := ast.GetContainingClass(node)
			if class == nil || class.Name() == nil {
				return "", nil
			}
			qualifier = class.Name().Text()
			expression = tracker.NewIdentifier(qualifier)
		}
		var name *ast.Node
		if ast.IsPrivateIdentifier(node) {
			name = tracker.NewPrivateIdentifier(node.Text())
		} else {
			name = tracker.NewIdentifier(node.Text())
		}
		tracker.ReplaceNode(request.SourceFile, node, tracker.NewPropertyAccessExpression(expression, nil, name, ast.NodeFlagsNone), nil)
		return diagnostics.Add_0_to_unresolved_variable.Localize(locale.FromContext(ctx), qualifier), ctx.Err()
	})

var ReferenceUnusedLabelProvider = newReferenceSyntaxFixProvider("fixUnusedLabel",
	[]int32{diagnostics.Unused_label.Code()},
	diagnostics.Remove_all_unused_labels, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil || token.Parent == nil || !ast.IsLabeledStatement(token.Parent) {
			return "", nil
		}
		node := token.Parent
		if seen[node] {
			return "", nil
		}
		seen[node] = true
		pos := scanner.GetTokenPosOfNode(token, request.SourceFile, false)
		end := scanner.GetTokenPosOfNode(node.AsLabeledStatement().Statement, request.SourceFile, false)
		if !printer.PositionsAreOnSameLine(pos, end, request.SourceFile) {
			colon := astnav.FindChildOfKind(node, ast.KindColonToken, request.SourceFile)
			if colon == nil {
				return "", nil
			}
			end = scanner.SkipTriviaEx(request.SourceFile.Text(), colon.End(), &scanner.SkipTriviaOptions{StopAfterLineBreak: true})
		}
		tracker.DeleteRange(request.SourceFile, core.NewTextRange(pos, end))
		return diagnostics.Remove_unused_label.Localize(locale.FromContext(ctx)), ctx.Err()
	})

var ReferenceQualifiedNameToIndexedAccessProvider = newReferenceSyntaxFixProvider("correctQualifiedNameToIndexedAccessType",
	[]int32{diagnostics.Cannot_access_0_1_because_0_is_a_type_but_not_a_namespace_Did_you_mean_to_retrieve_the_type_of_the_property_1_in_0_with_0_1.Code()},
	diagnostics.Rewrite_all_as_indexed_access_types, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		node := ast.FindAncestor(astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos()), ast.IsQualifiedName)
		if node == nil || seen[node] {
			return "", nil
		}
		q := node.AsQualifiedName()
		if !ast.IsIdentifier(q.Left) {
			return "", nil
		}
		seen[node] = true
		replacement := tracker.NewIndexedAccessTypeNode(tracker.NewTypeReferenceNode(q.Left, nil), tracker.NewLiteralTypeNode(tracker.NewStringLiteral(q.Right.Text(), ast.TokenFlagsNone)))
		tracker.ReplaceNode(request.SourceFile, node, replacement, nil)
		title := q.Left.Text() + "[\"" + q.Right.Text() + "\"]"
		return diagnostics.Rewrite_as_the_indexed_access_type_0.Localize(locale.FromContext(ctx), title), ctx.Err()
	})

var ReferenceMissingDeclarePropertyProvider = newReferenceSyntaxFixProvider("addMissingDeclareProperty",
	[]int32{diagnostics.Property_0_will_overwrite_the_base_property_in_1_If_this_is_intentional_add_an_initializer_Otherwise_add_a_declare_modifier_or_remove_the_redundant_declaration.Code()},
	diagnostics.Prefix_all_incorrect_property_declarations_with_declare, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil || !ast.IsIdentifier(token) || token.Parent == nil || !ast.IsPropertyDeclaration(token.Parent) || seen[token.Parent] {
			return "", nil
		}
		node := token.Parent
		seen[node] = true
		tracker.InsertModifierBefore(request.SourceFile, ast.KindDeclareKeyword, node)
		return diagnostics.Prefix_with_declare.Localize(locale.FromContext(ctx)), ctx.Err()
	})

func referenceInvalidJSXCharacterFix(useHTML bool) referenceSyntaxFix {
	return func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		start := request.Span.Pos()
		text := request.SourceFile.Text()
		if start < 0 || start >= len(text) || (text[start] != '>' && text[start] != '}') {
			return "", nil
		}
		character := string(text[start])
		replacement := "{" + quote(request.SourceFile, request.LS.UserPreferences(), character) + "}"
		title := diagnostics.Wrap_invalid_character_in_an_expression_container.Localize(locale.FromContext(ctx))
		if useHTML {
			if character == ">" {
				replacement = "&gt;"
			} else {
				replacement = "&rbrace;"
			}
			title = diagnostics.Convert_invalid_character_to_its_html_entity_code.Localize(locale.FromContext(ctx))
		}
		span := request.LS.createLspRangeFromBounds(start, start+1, request.SourceFile)
		tracker.ReplaceRangeWithText(request.SourceFile, span, replacement)
		return title, ctx.Err()
	}
}

var referenceInvalidJSXCharacterCodes = []int32{diagnostics.Unexpected_token_Did_you_mean_or_gt.Code(), diagnostics.Unexpected_token_Did_you_mean_or_rbrace.Code()}
var ReferenceInvalidJSXExpressionProvider = newReferenceSyntaxFixProvider("fixInvalidJsxCharacters_expression", referenceInvalidJSXCharacterCodes,
	diagnostics.Wrap_all_invalid_characters_in_an_expression_container, referenceInvalidJSXCharacterFix(false))
var ReferenceInvalidJSXEntityProvider = newReferenceSyntaxFixProvider("fixInvalidJsxCharacters_htmlEntity", referenceInvalidJSXCharacterCodes,
	diagnostics.Convert_all_invalid_characters_to_HTML_entity_code, referenceInvalidJSXCharacterFix(true))

var ReferenceEmptyExportProvider = newReferenceSyntaxFixProvider("addEmptyExportDeclaration",
	[]int32{
		diagnostics.X_await_expressions_are_only_allowed_at_the_top_level_of_a_file_when_that_file_is_a_module_but_this_file_has_no_imports_or_exports_Consider_adding_an_empty_export_to_make_this_file_a_module.Code(),
		diagnostics.X_await_using_statements_are_only_allowed_at_the_top_level_of_a_file_when_that_file_is_a_module_but_this_file_has_no_imports_or_exports_Consider_adding_an_empty_export_to_make_this_file_a_module.Code(),
		diagnostics.X_for_await_loops_are_only_allowed_at_the_top_level_of_a_file_when_that_file_is_a_module_but_this_file_has_no_imports_or_exports_Consider_adding_an_empty_export_to_make_this_file_a_module.Code(),
	}, nil, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		node := tracker.NewExportDeclaration(nil, false, tracker.NewNamedExports(tracker.NewNodeList(nil)), nil, nil)
		tracker.InsertReferenceStatementAtEnd(request.SourceFile, node)
		return diagnostics.Add_export_to_make_this_file_into_a_module.Localize(locale.FromContext(ctx)), ctx.Err()
	})
var ReferenceConstructorSuperCallProvider = newReferenceSyntaxFixProvider("constructorForDerivedNeedSuperCall",
	[]int32{diagnostics.Constructors_for_derived_classes_must_contain_a_super_call.Code()},
	diagnostics.Add_all_missing_super_calls, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil || token.Parent == nil || !ast.IsConstructorDeclaration(token.Parent) {
			return "", nil
		}
		constructor := token.Parent
		if constructor.Body() == nil || seen[constructor] {
			return "", nil
		}
		seen[constructor] = true
		call := tracker.NewCallExpression(tracker.NewKeywordExpression(ast.KindSuperKeyword), nil, nil, tracker.NewNodeList(nil), ast.NodeFlagsNone)
		tracker.InsertReferenceNodeAtConstructorStart(request.SourceFile, constructor, tracker.NewExpressionStatement(call))
		return diagnostics.Add_missing_super_call.Localize(locale.FromContext(ctx)), ctx.Err()
	})

var referenceOverrideAddCodes = []int32{
	diagnostics.This_member_must_have_an_override_modifier_because_it_overrides_a_member_in_the_base_class_0.Code(),
	diagnostics.This_parameter_property_must_have_an_override_modifier_because_it_overrides_a_member_in_base_class_0.Code(),
	diagnostics.This_member_must_have_an_override_modifier_because_it_overrides_an_abstract_method_that_is_declared_in_the_base_class_0.Code(),
	diagnostics.This_member_must_have_a_JSDoc_comment_with_an_override_tag_because_it_overrides_a_member_in_the_base_class_0.Code(),
	diagnostics.This_parameter_property_must_have_a_JSDoc_comment_with_an_override_tag_because_it_overrides_a_member_in_the_base_class_0.Code(),
}
var referenceOverrideRemoveCodes = []int32{
	diagnostics.This_member_cannot_have_an_override_modifier_because_it_is_not_declared_in_the_base_class_0.Code(),
	diagnostics.This_member_cannot_have_an_override_modifier_because_its_containing_class_0_does_not_extend_another_class.Code(),
	diagnostics.This_member_cannot_have_a_JSDoc_comment_with_an_override_tag_because_its_containing_class_0_does_not_extend_another_class.Code(),
	diagnostics.This_member_cannot_have_a_JSDoc_comment_with_an_override_tag_because_it_is_not_declared_in_the_base_class_0.Code(),
}

func newReferenceOverrideProvider(add bool) *CodeFixProvider {
	name := "fixAddOverrideModifier"
	codes := referenceOverrideAddCodes
	allDescription := diagnostics.Add_all_missing_override_modifiers
	if !add {
		name = "fixRemoveOverrideModifier"
		codes = referenceOverrideRemoveCodes
		allDescription = diagnostics.Remove_all_unnecessary_override_modifiers
	}
	apply := func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		node := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		for node != nil {
			if ast.IsClassLike(node) {
				return "", nil
			}
			eligible := node.Kind == ast.KindConstructor || node.Kind == ast.KindPropertyDeclaration || node.Kind == ast.KindMethodDeclaration || node.Kind == ast.KindGetAccessor || node.Kind == ast.KindSetAccessor || node.Kind == ast.KindParameter && ast.IsParameterPropertyDeclaration(node, node.Parent)
			if eligible {
				break
			}
			node = node.Parent
		}
		if node == nil || seen[node] {
			return "", nil
		}
		seen[node] = true
		var modifiers []*ast.Node
		if node.Modifiers() != nil {
			modifiers = node.Modifiers().Nodes
		}
		if !add {
			for _, modifier := range modifiers {
				if modifier.Kind == ast.KindOverrideKeyword {
					start := scanner.GetTokenPosOfNode(modifier, request.SourceFile, false)
					end := scanner.SkipTriviaEx(request.SourceFile.Text(), modifier.End(), &scanner.SkipTriviaOptions{StopAfterLineBreak: true})
					tracker.DeleteRange(request.SourceFile, core.NewTextRange(start, end))
					return diagnostics.Remove_override_modifier.Localize(locale.FromContext(ctx)), ctx.Err()
				}
			}
			return "", nil
		}
		var abstract, static, accessibility, decorator *ast.Node
		for _, modifier := range modifiers {
			switch modifier.Kind {
			case ast.KindAbstractKeyword:
				if abstract == nil {
					abstract = modifier
				}
			case ast.KindStaticKeyword:
				if static == nil {
					static = modifier
				}
			case ast.KindPublicKeyword, ast.KindPrivateKeyword, ast.KindProtectedKeyword:
				if accessibility == nil {
					accessibility = modifier
				}
			case ast.KindDecorator:
				decorator = modifier
			}
		}
		pos := scanner.GetTokenPosOfNode(node, request.SourceFile, false)
		options := change.NodeOptions{Suffix: " "}
		switch {
		case abstract != nil:
			pos = abstract.End()
		case static != nil:
			pos = static.End()
		case accessibility != nil:
			pos = accessibility.End()
		case decorator != nil:
			pos = scanner.SkipTrivia(request.SourceFile.Text(), decorator.End())
		}
		if abstract != nil || static != nil || accessibility != nil {
			options = change.NodeOptions{Prefix: " "}
		}
		tracker.InsertNodeAt(request.SourceFile, core.TextPos(pos), tracker.NewToken(ast.KindOverrideKeyword), options)
		return diagnostics.Add_override_modifier.Localize(locale.FromContext(ctx)), ctx.Err()
	}
	provider := newReferenceSyntaxFixProvider(name, codes, allDescription, apply)
	provider.Name = "fixOverrideModifier"
	provider.ReferenceFixAllErrorCodes = append(slices.Clone(referenceOverrideAddCodes), referenceOverrideRemoveCodes...)
	original := provider.GetCodeActions
	provider.GetCodeActions = func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		actions, err := original(ctx, request)
		for _, action := range actions {
			// Preserve the pinned provider's descriptions, including its abstract case.
			if request.ErrorCode == diagnostics.This_member_must_have_an_override_modifier_because_it_overrides_an_abstract_method_that_is_declared_in_the_base_class_0.Code() {
				action.FixAllDescription = diagnostics.Remove_all_unnecessary_override_modifiers.Localize(locale.FromContext(ctx))
			}
			if request.ErrorCode == diagnostics.This_member_cannot_have_a_JSDoc_comment_with_an_override_tag_because_its_containing_class_0_does_not_extend_another_class.Code() {
				action.FixAllDescription = diagnostics.Remove_override_modifier.Localize(locale.FromContext(ctx))
			}
		}
		return actions, err
	}
	return provider
}

var ReferenceAddOverrideProvider = newReferenceOverrideProvider(true)
var ReferenceRemoveOverrideProvider = newReferenceOverrideProvider(false)

func referenceDecoratorFix(invoke bool) referenceSyntaxFix {
	return func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		decorator := ast.FindAncestor(astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos()), ast.IsDecorator)
		if decorator == nil || seen[decorator] {
			return "", nil
		}
		seen[decorator] = true
		expression := decorator.AsDecorator().Expression
		replacement := tracker.NewParenthesizedExpression(expression)
		title := diagnostics.Wrap_in_parentheses.Localize(locale.FromContext(ctx))
		if invoke {
			replacement = tracker.NewCallExpression(expression, nil, nil, nil, ast.NodeFlagsNone)
			title = diagnostics.Call_decorator_expression.Localize(locale.FromContext(ctx))
		}
		tracker.ReplaceNode(request.SourceFile, expression, replacement, nil)
		return title, ctx.Err()
	}
}

var ReferenceMissingDecoratorInvocationProvider = newReferenceSyntaxFixProvider("addMissingInvocationForDecorator",
	[]int32{diagnostics.X_0_accepts_too_few_arguments_to_be_used_as_a_decorator_here_Did_you_mean_to_call_it_first_and_write_0.Code()},
	diagnostics.Add_to_all_uncalled_decorators, referenceDecoratorFix(true))
var ReferenceWrapDecoratorProvider = newReferenceSyntaxFixProvider("wrapDecoratorInParentheses",
	[]int32{diagnostics.Expression_must_be_enclosed_in_parentheses_to_be_used_as_a_decorator.Code()},
	diagnostics.Wrap_all_invalid_decorator_expressions_in_parentheses, referenceDecoratorFix(false))

var ReferenceAsyncReturnTypeProvider = newReferenceSyntaxFixProvider("fixReturnTypeInAsyncFunction",
	[]int32{diagnostics.The_return_type_of_an_async_function_or_method_must_be_the_global_Promise_T_type_Did_you_mean_to_write_Promise_0.Code()},
	diagnostics.Fix_all_incorrect_return_type_of_an_async_functions, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		function := ast.FindAncestor(astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos()), ast.IsFunctionLikeDeclaration)
		if function == nil || function.Type() == nil {
			return "", nil
		}
		annotation := function.Type()
		if seen[annotation] {
			return "", nil
		}
		seen[annotation] = true
		checker, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		returnType := checker.GetTypeFromTypeNode(annotation)
		promised := checker.GetReferenceAwaitedType(returnType)
		if promised == nil {
			promised = checker.GetVoidType()
		}
		node := checker.TypeToTypeNode(promised, annotation, nodebuilder.FlagsNone, nil)
		if node == nil {
			return "", nil
		}
		replacement := tracker.NewTypeReferenceNode(tracker.NewIdentifier("Promise"), tracker.NewNodeList([]*ast.Node{node}))
		tracker.ReplaceNode(request.SourceFile, annotation, replacement, nil)
		return diagnostics.Replace_0_with_Promise_1.Localize(locale.FromContext(ctx), checker.TypeToString(returnType), checker.TypeToString(promised)), ctx.Err()
	})

func referenceAncestorMatchingSpan(file *ast.SourceFile, span core.TextRange) *ast.Node {
	for node := astnav.GetTokenAtPosition(file, span.Pos()); node != nil; node = node.Parent {
		if scanner.GetTokenPosOfNode(node, file, false) == span.Pos() && node.End() == span.End() {
			return node
		}
	}
	return nil
}

var referenceQuotedSuggestion = regexp.MustCompile("'([^\\n\\r\\x{2028}\\x{2029}]*)'")

func referenceDiagnosticText(diagnostic *ast.Diagnostic, loc locale.Locale, indent int) string {
	text := strings.Repeat("  ", indent) + diagnostic.Localize(loc)
	for _, child := range diagnostic.MessageChain() {
		text += "\n" + referenceDiagnosticText(child, loc, indent+1)
	}
	return text
}

var ReferenceNaNEqualityProvider = newReferenceSyntaxFixProvider("fixNaNEquality",
	[]int32{diagnostics.This_condition_will_always_return_0.Code()},
	diagnostics.Use_Number_isNaN_in_all_conditions, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		var related *ast.Diagnostic
		for _, diagnostic := range request.Program.GetSemanticDiagnostics(ctx, request.SourceFile) {
			if diagnostic.Pos() != request.Span.Pos() || diagnostic.End() != request.Span.End() {
				continue
			}
			for _, info := range diagnostic.RelatedInformation() {
				if info.Code() == diagnostics.Did_you_mean_0.Code() {
					related = info
					break
				}
			}
			break
		}
		if related == nil || related.File() == nil {
			return "", nil
		}
		operand := referenceAncestorMatchingSpan(related.File(), related.Loc())
		if operand == nil || !ast.IsExpression(operand) || operand.Parent == nil || !ast.IsBinaryExpression(operand.Parent) {
			return "", nil
		}
		binary := operand.Parent
		if seen[binary] {
			return "", nil
		}
		seen[binary] = true
		access := tracker.NewPropertyAccessExpression(tracker.NewIdentifier("Number"), nil, tracker.NewIdentifier("isNaN"), ast.NodeFlagsNone)
		replacement := tracker.NewCallExpression(access, nil, nil, tracker.NewNodeList([]*ast.Node{operand}), ast.NodeFlagsNone)
		operator := binary.AsBinaryExpression().OperatorToken.Kind
		if operator == ast.KindExclamationEqualsToken || operator == ast.KindExclamationEqualsEqualsToken {
			replacement = tracker.NewPrefixUnaryExpression(ast.KindExclamationToken, replacement)
		}
		tracker.ReplaceNode(request.SourceFile, binary, replacement, nil)
		suggestion := "undefined"
		if matches := referenceQuotedSuggestion.FindStringSubmatch(referenceDiagnosticText(related, locale.FromContext(ctx), 0)); len(matches) > 1 {
			suggestion = matches[1]
		}
		return diagnostics.Use_0.Localize(locale.FromContext(ctx), suggestion), ctx.Err()
	})

var referenceAbstractClassErrorCodes = []int32{
	diagnostics.Non_abstract_class_0_does_not_implement_inherited_abstract_member_1_from_class_2.Code(),
	diagnostics.Non_abstract_class_0_is_missing_implementations_for_the_following_members_of_1_Colon_2.Code(),
	diagnostics.Non_abstract_class_0_is_missing_implementations_for_the_following_members_of_1_Colon_2_and_3_more.Code(),
	diagnostics.Non_abstract_class_expression_does_not_implement_inherited_abstract_member_0_from_class_1.Code(),
	diagnostics.Non_abstract_class_expression_is_missing_implementations_for_the_following_members_of_0_Colon_1.Code(),
	diagnostics.Non_abstract_class_expression_is_missing_implementations_for_the_following_members_of_0_Colon_1_and_2_more.Code(),
}

func referenceAddAbstractClassMembers(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, adder autoimport.ImportAdder, ch *checker.Checker, seen map[*ast.Node]bool) {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil || token.Parent == nil || !ast.IsClassLike(token.Parent) {
		return
	}
	class := token.Parent
	if seen[class] {
		return
	}
	seen[class] = true
	base := ast.GetClassExtendsHeritageElement(class)
	if base == nil {
		return
	}
	instantiated := ch.GetTypeAtLocation(base)
	if instantiated == nil {
		return
	}
	var own ast.SymbolTable
	if class.Symbol() != nil {
		own = class.Symbol().Members
	}
	fixer := newMissingMemberFixer(tracker, request.Program, ch, request.LS.UserPreferences(), adder, locale.FromContext(ctx))
	for _, symbol := range ch.GetPropertiesOfType(instantiated) {
		if symbol == nil || len(symbol.Declarations) == 0 || own[symbol.Name] != nil {
			continue
		}
		flags := symbol.Declarations[0].ModifierFlags()
		if flags&ast.ModifierFlagsPrivate != 0 || flags&ast.ModifierFlagsAbstract == 0 {
			continue
		}
		for _, node := range fixer.createMemberFromSymbol(symbol, class, request.SourceFile, nil, preserveOptionalFlagsAll) {
			tracker.InsertMemberAtStart(request.SourceFile, class, node)
		}
	}
}
func referenceAbstractClassFix(ctx context.Context, request *CodeFixContext, all bool) ([]*lsproto.TextEdit, error) {
	ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
	defer done()
	tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
	adder, err := createImportAdder(ctx, request, ch)
	if err != nil {
		return nil, err
	}
	seen := map[*ast.Node]bool{}
	if all {
		for _, diagnostic := range getAllDiagnostics(ctx, request.Program, request.SourceFile) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !slices.Contains(referenceAbstractClassErrorCodes, diagnostic.Code()) {
				continue
			}
			current := *request
			current.Span = diagnostic.Loc()
			referenceAddAbstractClassMembers(ctx, &current, tracker, adder, ch, seen)
		}
	} else {
		referenceAddAbstractClassMembers(ctx, request, tracker, adder, ch, seen)
	}
	return getChanges(tracker, adder, request.SourceFile), ctx.Err()
}

var ReferenceAbstractClassProvider = &CodeFixProvider{
	Name:       "fixClassDoesntImplementInheritedAbstractMember",
	ErrorCodes: referenceAbstractClassErrorCodes,
	FixIds:     []string{"fixClassDoesntImplementInheritedAbstractMember"},
	GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		changes, err := referenceAbstractClassFix(ctx, request, false)
		if err != nil || len(changes) == 0 {
			return nil, err
		}
		return []*CodeAction{{Description: diagnostics.Implement_inherited_abstract_class.Localize(locale.FromContext(ctx)), Changes: changes, FixID: "fixClassDoesntImplementInheritedAbstractMember", FixAllDescription: diagnostics.Implement_all_inherited_abstract_classes.Localize(locale.FromContext(ctx))}}, nil
	},
	GetAllCodeActions: func(ctx context.Context, request *CodeFixContext) (*CombinedCodeActions, error) {
		changes, err := referenceAbstractClassFix(ctx, request, true)
		if err != nil {
			return nil, err
		}
		return &CombinedCodeActions{Changes: changes}, nil
	},
}

var ReferenceUnreachableCodeProvider = newReferenceSyntaxFixProvider("fixUnreachableCode",
	[]int32{diagnostics.Unreachable_code_detected.Code()}, diagnostics.Remove_all_unreachable_code,
	func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		if len(request.Program.GetSyntacticDiagnostics(ctx, request.SourceFile)) > 0 {
			return "", ctx.Err()
		}
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		statement := ast.FindAncestor(token, ast.IsStatement)
		if token == nil || statement == nil || statement.Parent == nil || scanner.GetTokenPosOfNode(statement, request.SourceFile, false) != scanner.GetTokenPosOfNode(token, request.SourceFile, false) || seen[statement] {
			return "", nil
		}
		seen[statement] = true
		block := statement.Parent
		inBlock := ast.IsBlock(block)
		container := statement.Parent
		var statements []*ast.Node
		if inBlock {
			container = block.Parent
			if block.AsBlock().Statements != nil {
				statements = block.AsBlock().Statements.Nodes
			}
		}
		if !inBlock || len(statements) > 0 && statements[0] == statement {
			if container != nil {
				switch container.Kind {
				case ast.KindIfStatement:
					if container.AsIfStatement().ElseStatement != nil {
						if inBlock {
							break
						}
						tracker.ReplaceNode(request.SourceFile, statement, tracker.NewBlock(tracker.NewNodeList(nil), false), nil)
						return diagnostics.Remove_unreachable_code.Localize(locale.FromContext(ctx)), ctx.Err()
					}
					tracker.DeleteNode(request.SourceFile, container, change.LeadingTriviaOptionIncludeAll, change.TrailingTriviaOptionNone)
					return diagnostics.Remove_unreachable_code.Localize(locale.FromContext(ctx)), ctx.Err()
				case ast.KindWhileStatement, ast.KindForStatement:
					tracker.DeleteNode(request.SourceFile, container, change.LeadingTriviaOptionIncludeAll, change.TrailingTriviaOptionNone)
					return diagnostics.Remove_unreachable_code.Localize(locale.FromContext(ctx)), ctx.Err()
				}
			}
		}
		if inBlock {
			start := slices.Index(statements, statement)
			if start < 0 {
				return "", nil
			}
			last := statement
			for _, candidate := range statements[start:] {
				if candidate.Pos() >= request.Span.End() {
					break
				}
				last = candidate
				seen[candidate] = true
			}
			tracker.DeleteNodeRange(request.SourceFile, statement, last, change.LeadingTriviaOptionIncludeAll, change.TrailingTriviaOptionNone)
		} else {
			tracker.Delete(request.SourceFile, statement)
		}
		return diagnostics.Remove_unreachable_code.Localize(locale.FromContext(ctx)), ctx.Err()
	})

var ReferenceNamelessParameterProvider = newReferenceSyntaxFixProvider("addNameToNamelessParameter",
	[]int32{diagnostics.Parameter_has_a_name_but_no_type_Did_you_mean_0_Colon_1.Code()}, diagnostics.Add_names_to_all_parameters_without_names,
	func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil || token.Parent == nil || !ast.IsParameterDeclaration(token.Parent) {
			return "", nil
		}
		node := token.Parent
		parameter := node.AsParameterDeclaration()
		if parameter.Type != nil || parameter.Parent == nil || !ast.IsIdentifier(parameter.Name()) || seen[node] {
			return "", nil
		}
		seen[node] = true
		index := slices.Index(parameter.Parent.Parameters(), node)
		if index < 0 {
			return "", nil
		}
		end := parameter.Name().End()
		typ := tracker.NewTypeReferenceNode(parameter.Name(), nil)
		next := node
		for {
			following := astnav.FindNextToken(next.Name(), next.Parent, request.SourceFile)
			if following == nil || following.Kind != ast.KindOpenBracketToken || following.Parent == nil || !ast.IsArrayBindingPattern(following.Parent) || following.Parent.Parent == nil || !ast.IsParameterDeclaration(following.Parent.Parent) {
				break
			}
			next = following.Parent.Parent
			typ = tracker.NewArrayTypeNode(typ)
			end = next.End()
			seen[next] = true
		}
		if parameter.DotDotDotToken != nil && !ast.IsArrayTypeNode(typ) {
			typ = tracker.NewArrayTypeNode(typ)
		}
		replacement := tracker.NewParameterDeclaration(parameter.Modifiers(), parameter.DotDotDotToken, tracker.NewIdentifier("arg"+strconv.Itoa(index)), parameter.QuestionToken, typ, parameter.Initializer)
		span := request.LS.createLspRangeFromBounds(scanner.GetTokenPosOfNode(node, request.SourceFile, false), end, request.SourceFile)
		tracker.ReplaceRange(request.SourceFile, span, replacement, change.NodeOptions{})
		return diagnostics.Add_parameter_name.Localize(locale.FromContext(ctx)), ctx.Err()
	})

var ReferenceLiteralMappedTypeProvider = newReferenceSyntaxFixProvider("convertLiteralTypeToMappedType",
	[]int32{diagnostics.X_0_only_refers_to_a_type_but_is_being_used_as_a_value_here_Did_you_mean_to_use_1_in_0.Code()}, diagnostics.Convert_all_type_literals_to_mapped_type,
	func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil || !ast.IsIdentifier(token) || token.Parent == nil || token.Parent.Parent == nil {
			return "", nil
		}
		property := token.Parent.Parent
		if !ast.IsPropertySignatureDeclaration(property) || property.Parent == nil || !ast.IsTypeLiteralNode(property.Parent) {
			return "", nil
		}
		container := property.Parent
		if seen[container] {
			return "", nil
		}
		seen[container] = true
		constraint := scanner.GetTextOfNode(token)
		name := "K"
		if constraint == "K" {
			name = "P"
		}
		parameter := tracker.NewTypeParameterDeclaration(nil, tracker.NewIdentifier(name), tracker.NewTypeReferenceNode(tracker.NewIdentifier(constraint), nil), nil, nil)
		replacement := tracker.NewMappedTypeNode(nil, parameter, nil, nil, property.Type(), nil)
		tracker.ReplaceNode(request.SourceFile, container, replacement, nil)
		return diagnostics.Convert_0_to_1_in_0.Localize(locale.FromContext(ctx), constraint, name), ctx.Err()
	})

func referenceMappedObjectContainer(request *CodeFixContext) (*ast.Node, *ast.Node) {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil || token.Parent == nil || token.Parent.Parent == nil {
		return nil, nil
	}
	index := token.Parent.Parent
	if !ast.IsIndexSignatureDeclaration(index) || index.Parent == nil {
		return nil, nil
	}
	container := index.Parent
	if !ast.IsInterfaceDeclaration(container) {
		container = container.Parent
		if container == nil || !ast.IsTypeAliasDeclaration(container) {
			return nil, nil
		}
	}
	return index, container
}
func newReferenceMappedObjectProvider() *CodeFixProvider {
	provider := newReferenceSyntaxFixProvider("fixConvertToMappedObjectType",
		[]int32{diagnostics.An_index_signature_parameter_type_cannot_be_a_literal_type_or_generic_type_Consider_using_a_mapped_object_type_instead.Code()}, diagnostics.Convert_0_to_mapped_object_type,
		func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
			index, container := referenceMappedObjectContainer(request)
			if index == nil || seen[container] {
				return "", nil
			}
			seen[container] = true
			parameters := index.Parameters()
			if len(parameters) == 0 || !ast.IsIdentifier(parameters[0].Name()) {
				return "", nil
			}
			parameter := parameters[0]
			mappedParameter := tracker.NewTypeParameterDeclaration(nil, parameter.Name(), parameter.Type(), nil, nil)
			var readonly *ast.Node
			if index.ModifierFlags()&ast.ModifierFlagsReadonly != 0 {
				readonly = tracker.NewToken(ast.KindReadonlyKeyword)
			}
			mapped := tracker.NewMappedTypeNode(readonly, mappedParameter, nil, index.QuestionToken(), index.Type(), nil)
			var members []*ast.Node
			if ast.IsInterfaceDeclaration(container) {
				members = container.Members()
			} else if ast.IsTypeLiteralNode(container.Type()) {
				members = container.Type().Members()
			} else {
				return "", nil
			}
			others := []*ast.Node{}
			for _, member := range members {
				if !ast.IsIndexSignatureDeclaration(member) {
					others = append(others, member)
				}
			}
			types := []*ast.Node{}
			if ast.IsInterfaceDeclaration(container) {
				types = append(types, ast.GetExtendsHeritageClauseElements(container)...)
			}
			types = append(types, mapped)
			if len(others) > 0 {
				types = append(types, tracker.NewTypeLiteralNode(tracker.NewNodeList(others)))
			}
			replacement := tracker.NewTypeAliasDeclaration(container.Modifiers(), container.Name(), container.TypeParameterList(), tracker.NewIntersectionTypeNode(tracker.NewNodeList(types)))
			tracker.ReplaceNode(request.SourceFile, container, replacement, nil)
			return diagnostics.Convert_0_to_mapped_object_type.Localize(locale.FromContext(ctx), container.Name().Text()), ctx.Err()
		})
	original := provider.GetCodeActions
	provider.GetCodeActions = func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		actions, err := original(ctx, request)
		for _, action := range actions {
			action.FixAllDescription = action.Description
		}
		return actions, err
	}
	return provider
}

var ReferenceMappedObjectProvider = newReferenceMappedObjectProvider()

var ReferenceSplitTypeOnlyImportProvider = newReferenceSyntaxFixProvider("splitTypeOnlyImport",
	[]int32{diagnostics.A_type_only_import_can_specify_a_default_import_or_named_bindings_but_not_both.Code()}, diagnostics.Split_all_invalid_type_only_imports,
	func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		node := ast.FindAncestor(astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos()), ast.IsImportDeclaration)
		if node == nil || seen[node] {
			return "", nil
		}
		seen[node] = true
		decl := node.AsImportDeclaration()
		if decl.ImportClause == nil {
			return "", nil
		}
		clause := decl.ImportClause.AsImportClause()
		first := tracker.NewImportDeclaration(node.Modifiers(), tracker.NewImportClause(clause.PhaseModifier, clause.Name(), nil), decl.ModuleSpecifier, decl.Attributes)
		second := tracker.NewImportDeclaration(nil, tracker.NewImportClause(clause.PhaseModifier, nil, clause.NamedBindings), decl.ModuleSpecifier, decl.Attributes)
		tracker.ReplaceNode(request.SourceFile, node, first, nil)
		tracker.InsertNodeAfter(request.SourceFile, node, second)
		return diagnostics.Split_into_two_separate_import_declarations.Localize(locale.FromContext(ctx)), ctx.Err()
	})
var ReferenceDefaultImportProvider = newReferenceSyntaxFixProvider("useDefaultImport",
	[]int32{diagnostics.Import_may_be_converted_to_a_default_import.Code()}, diagnostics.Convert_all_to_default_imports,
	func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil || !ast.IsIdentifier(token) || token.Parent == nil {
			return "", nil
		}
		parent := token.Parent
		var importNode, module *ast.Node
		if ast.IsImportEqualsDeclaration(parent) {
			reference := parent.AsImportEqualsDeclaration().ModuleReference
			if reference == nil || !ast.IsExternalModuleReference(reference) {
				return "", nil
			}
			importNode = parent
			module = reference.AsExternalModuleReference().Expression
		} else if ast.IsNamespaceImport(parent) && parent.Parent != nil && parent.Parent.Parent != nil && ast.IsImportDeclaration(parent.Parent.Parent) {
			importNode = parent.Parent.Parent
			module = importNode.AsImportDeclaration().ModuleSpecifier
		} else {
			return "", nil
		}
		if seen[importNode] || module == nil {
			return "", nil
		}
		seen[importNode] = true
		replacement := tracker.NewImportDeclaration(nil, tracker.NewImportClause(ast.KindUnknown, token, nil), module, nil)
		tracker.ReplaceNode(request.SourceFile, importNode, replacement, nil)
		return diagnostics.Convert_to_default_import.Localize(locale.FromContext(ctx)), ctx.Err()
	})

var referenceUnusedCodes = []int32{
	diagnostics.X_0_is_declared_but_its_value_is_never_read.Code(),
	diagnostics.X_0_is_declared_but_never_used.Code(),
	diagnostics.Property_0_is_declared_but_its_value_is_never_read.Code(),
	diagnostics.All_imports_in_import_declaration_are_unused.Code(),
	diagnostics.All_destructured_elements_are_unused.Code(),
	diagnostics.All_variables_are_unused.Code(),
	diagnostics.All_type_parameters_are_unused.Code(),
}

const referenceUnusedDelete = "unusedIdentifier_delete"
const referenceUnusedImports = "unusedIdentifier_deleteImports"
const referenceUnusedPrefix = "unusedIdentifier_prefix"
const referenceUnusedInfer = "unusedIdentifier_infer"

func referenceUnusedIsImport(token *ast.Node) bool {
	return token != nil && (token.Kind == ast.KindImportKeyword || ast.IsIdentifier(token) && token.Parent != nil && (ast.IsImportSpecifier(token.Parent) || ast.IsImportClause(token.Parent)))
}
func referenceUnusedFullImport(token *ast.Node) *ast.Node {
	if token != nil && token.Kind == ast.KindImportKeyword && token.Parent != nil && ast.IsImportDeclaration(token.Parent) {
		return token.Parent
	}
	return nil
}
func referenceUnusedEntireVariable(token *ast.Node) bool {
	return token != nil && token.Parent != nil && ast.IsVariableDeclarationList(token.Parent) && scanner.GetTokenPosOfNode(token.Parent, ast.GetSourceFileOfNode(token), false) == scanner.GetTokenPosOfNode(token, ast.GetSourceFileOfNode(token), false)
}
func referenceUnusedDeleteVariables(tracker *change.Tracker, file *ast.SourceFile, list *ast.Node) {
	node := list
	if list.Parent != nil && ast.IsVariableStatement(list.Parent) {
		node = list.Parent
	}
	tracker.Delete(file, node)
}
func referenceUnusedDeleteTypeParameters(tracker *change.Tracker, file *ast.SourceFile, token *ast.Node) {
	if token.Parent == nil {
		return
	}
	parameters := token.Parent.TypeParameterList()
	if parameters == nil || len(parameters.Nodes) == 0 {
		return
	}
	last := parameters.Nodes[len(parameters.Nodes)-1]
	closing := astnav.FindNextToken(last, token.Parent, file)
	if closing == nil || closing.Kind != ast.KindGreaterThanToken {
		return
	}
	tracker.DeleteRange(file, core.NewTextRange(scanner.GetTokenPosOfNode(token, file, false), closing.End()))
}
func referenceUnusedSymbol(ch *checker.Checker, name *ast.Node) *ast.Symbol {
	if name == nil || !ast.IsIdentifier(name) {
		return nil
	}
	if name.Parent != nil && ast.IsParameterDeclaration(name.Parent) && name.Parent.ModifierFlags()&ast.ModifierFlagsParameterPropertyModifier != 0 {
		property, _ := ch.GetSymbolsOfParameterPropertyDeclaration(name.Parent, name.Text())
		return property
	}
	return ch.GetSymbolAtLocation(name)
}
func referenceUnusedReferences(ch *checker.Checker, file *ast.SourceFile, name *ast.Node) []*ast.Node {
	symbol := referenceUnusedSymbol(ch, name)
	if symbol == nil {
		return nil
	}
	result := ch.GetReferencesToSymbolInFile(file, symbol)
	return slices.DeleteFunc(result, func(node *ast.Node) bool { return node == name })
}
func referenceUnusedProvidedArguments(ctx context.Context, request *CodeFixContext, ch *checker.Checker, parameter *ast.Node) bool {
	owner := parameter.Parent
	name := owner.Name()
	if name == nil || !ast.IsIdentifier(name) {
		return false
	}
	index := slices.Index(owner.Parameters(), parameter)
	symbol := ch.GetSymbolAtLocation(name)
	if symbol == nil {
		return false
	}
	for _, file := range request.Program.GetSourceFiles() {
		var visit func(*ast.Node) bool
		visit = func(node *ast.Node) bool {
			if ast.IsIdentifier(node) && node != name && node.Text() == name.Text() {
				target := ch.GetSymbolAtLocation(node)
				if target != nil && slices.Contains(ch.GetRootSymbols(target), symbol) {
					called := ast.ClimbPastPropertyAccess(node)
					if called.Parent == nil || !ast.IsCallExpression(called.Parent) || called.Parent.Expression() != called || len(called.Parent.Arguments()) > index {
						return true
					}
				}
			}
			return node.ForEachChild(visit)
		}
		if visit(file.AsNode()) {
			return true
		}
		if ctx.Err() != nil {
			return true
		}
	}
	return false
}
func referenceUnusedLastParameter(ch *checker.Checker, parameter *ast.Node, all bool) bool {
	parameters := parameter.Parent.Parameters()
	index := slices.Index(parameters, parameter)
	if index < 0 {
		return false
	}
	if !all {
		return index == len(parameters)-1
	}
	for _, later := range parameters[index+1:] {
		if !ast.IsIdentifier(later.Name()) || ch.IsReferenceSymbolReferenced(later.Symbol()) {
			return false
		}
	}
	return true
}
func referenceUnusedMayDeleteParameter(ctx context.Context, request *CodeFixContext, ch *checker.Checker, parameter *ast.Node, all bool) bool {
	owner := parameter.Parent
	switch owner.Kind {
	case ast.KindMethodDeclaration, ast.KindConstructor:
		index := slices.Index(owner.Parameters(), parameter)
		referent := owner
		if ast.IsMethodDeclaration(owner) {
			referent = owner.Name()
		}
		entries := request.LS.GetReferencedSymbolsForNode(ctx, owner.Pos(), referent, request.Program.GetSourceFiles())
		for _, entry := range entries {
			for _, reference := range entry.References() {
				node := reference.Node()
				if node == nil || node.Parent == nil {
					continue
				}
				if node.Kind == ast.KindSuperKeyword && ast.IsCallExpression(node.Parent) && len(node.Parent.Arguments()) > index {
					return false
				}
				if ast.IsPropertyAccessExpression(node.Parent) && node.Parent.Expression().Kind == ast.KindSuperKeyword && node.Parent.Parent != nil && ast.IsCallExpression(node.Parent.Parent) && len(node.Parent.Parent.Arguments()) > index {
					return false
				}
				if (ast.IsMethodDeclaration(node.Parent) || ast.IsMethodSignatureDeclaration(node.Parent)) && node.Parent != owner && len(node.Parent.Parameters()) > index {
					return false
				}
			}
		}
		return ctx.Err() == nil
	case ast.KindFunctionDeclaration:
		name := owner.Name()
		if name != nil {
			for _, reference := range referenceUnusedReferences(ch, request.SourceFile, name) {
				if reference.Parent != nil && ast.IsCallExpression(reference.Parent) && slices.Contains(reference.Parent.Arguments(), reference) {
					return referenceUnusedLastParameter(ch, parameter, all)
				}
			}
		}
		return true
	case ast.KindFunctionExpression, ast.KindArrowFunction:
		return referenceUnusedLastParameter(ch, parameter, all)
	case ast.KindSetAccessor:
		return false
	case ast.KindGetAccessor:
		return true
	}
	return false
}
func referenceUnusedDeleteParameter(ctx context.Context, request *CodeFixContext, ch *checker.Checker, tracker *change.Tracker, parameter *ast.Node, all bool) {
	if !referenceUnusedMayDeleteParameter(ctx, request, ch, parameter, all) {
		return
	}
	if parameter.Modifiers() != nil && len(parameter.Modifiers().Nodes) > 0 && (!ast.IsIdentifier(parameter.Name()) || len(referenceUnusedReferences(ch, request.SourceFile, parameter.Name())) > 0) {
		for _, modifier := range parameter.Modifiers().Nodes {
			if !ast.IsModifierKind(modifier.Kind) {
				continue
			}
			span := core.NewTextRange(scanner.GetTokenPosOfNode(modifier, request.SourceFile, false), scanner.SkipTriviaEx(request.SourceFile.Text(), modifier.End(), &scanner.SkipTriviaOptions{StopAtComments: true}))
			tracker.DeleteRange(request.SourceFile, span)
		}
	} else if parameter.Initializer() == nil && !referenceUnusedProvidedArguments(ctx, request, ch, parameter) {
		tracker.Delete(request.SourceFile, parameter)
	}
}
func referenceUnusedTryDelete(ctx context.Context, request *CodeFixContext, ch *checker.Checker, tracker *change.Tracker, token *ast.Node, all bool) {
	parent := token.Parent
	if parent == nil {
		return
	}
	references := referenceUnusedReferences(ch, request.SourceFile, token)
	if ast.IsParameterDeclaration(parent) {
		referenceUnusedDeleteParameter(ctx, request, ch, tracker, parent, all)
	} else if !(all && ast.IsIdentifier(token) && len(references) > 0) {
		node := parent
		if ast.IsImportClause(parent) {
			node = token
		} else if ast.IsComputedPropertyName(parent) {
			node = parent.Parent
		}
		if node != nil && !ast.IsSourceFile(node) {
			tracker.Delete(request.SourceFile, node)
		}
	}
	if !all {
		for _, reference := range references {
			if reference.Parent != nil && ast.IsPropertyAccessExpression(reference.Parent) && reference.Parent.Name() == reference {
				reference = reference.Parent
			}
			expression := reference.Parent
			if expression == nil || expression.Parent == nil || !ast.IsExpressionStatement(expression.Parent) {
				continue
			}
			if ast.IsBinaryExpression(expression) && expression.AsBinaryExpression().Left == reference || ast.IsPostfixUnaryExpression(expression) && expression.AsPostfixUnaryExpression().Operand == reference || ast.IsPrefixUnaryExpression(expression) && expression.AsPrefixUnaryExpression().Operand == reference {
				tracker.Delete(request.SourceFile, expression.Parent)
			}
		}
	}
}
func referenceUnusedDeleteFunction(tracker *change.Tracker, file *ast.SourceFile, node *ast.Node) {
	symbol := node.Symbol()
	if symbol == nil {
		return
	}
	for _, decl := range symbol.Declarations {
		tracker.Delete(file, decl)
	}
}
func referenceUnusedDeleteDestructuring(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, pattern *ast.Node) {
	parent := pattern.Parent
	initializer := parent.Initializer()
	if ast.IsVariableDeclaration(parent) && initializer != nil && ast.IsCallLikeExpression(initializer) {
		list := parent.Parent
		if ast.IsVariableDeclarationList(list) && len(list.AsVariableDeclarationList().Declarations.Nodes) > 1 {
			statement := list.Parent
			start := scanner.GetTokenPosOfNode(statement, request.SourceFile, false)
			preceding := start
			text := request.SourceFile.Text()
			for preceding > 0 && (text[preceding-1] == ' ' || text[preceding-1] == '\t') {
				preceding--
			}
			newline := request.LS.FormatOptions().NewLineCharacter
			if newline == "" {
				newline = request.Program.Options().NewLine.GetNewLineCharacter()
				if newline == "" {
					newline = "\n"
				}
			}
			suffix := ""
			if lsutil.ProbablyUsesSemicolons(request.SourceFile) {
				suffix = ";"
			}
			tracker.Delete(request.SourceFile, parent)
			tracker.InsertNodeAt(request.SourceFile, core.TextPos(statement.End()), initializer, change.NodeOptions{Prefix: newline + text[preceding:start], Suffix: suffix})
		} else {
			tracker.ReplaceNode(request.SourceFile, list, initializer, nil)
		}
	} else {
		tracker.Delete(request.SourceFile, parent)
	}
}
func referenceUnusedPrefixDeclaration(request *CodeFixContext, tracker *change.Tracker, token *ast.Node, code int32) {
	if code == diagnostics.Property_0_is_declared_but_its_value_is_never_read.Code() {
		return
	}
	if token.Kind == ast.KindInferKeyword && token.Parent != nil && ast.IsInferTypeNode(token.Parent) {
		token = token.Parent.AsInferTypeNode().TypeParameter.Name()
	}
	if !ast.IsIdentifier(token) || token.Parent == nil {
		return
	}
	parent := token.Parent
	canPrefix := ast.IsParameterDeclaration(parent) || ast.IsTypeParameterDeclaration(parent)
	if ast.IsVariableDeclaration(parent) && parent.Parent != nil && parent.Parent.Parent != nil {
		kind := parent.Parent.Parent.Kind
		canPrefix = kind == ast.KindForOfStatement || kind == ast.KindForInStatement
	}
	if !canPrefix {
		return
	}
	tracker.ReplaceNode(request.SourceFile, token, tracker.NewIdentifier("_"+token.Text()), nil)
	if ast.IsParameterDeclaration(parent) {
		var visit func(*ast.Node) bool
		visit = func(node *ast.Node) bool {
			if node.Kind == ast.KindJSDocParameterTag && node.Name() != nil && ast.IsIdentifier(node.Name()) && node.Name().Text() == token.Text() {
				tracker.ReplaceNode(request.SourceFile, node.Name(), tracker.NewIdentifier("_"+node.Name().Text()), nil)
			}
			return node.ForEachChild(visit)
		}
		for _, doc := range parent.Parent.JSDoc(request.SourceFile) {
			visit(doc)
		}
	}
}
func referenceUnusedApplyAll(ctx context.Context, request *CodeFixContext, ch *checker.Checker, tracker *change.Tracker, token *ast.Node, mode string, code int32) {
	if token == nil || token.Parent == nil {
		return
	}
	switch mode {
	case referenceUnusedPrefix:
		referenceUnusedPrefixDeclaration(request, tracker, token, code)
	case referenceUnusedInfer:
		if token.Kind == ast.KindInferKeyword {
			tracker.ReplaceNode(request.SourceFile, token.Parent, tracker.NewKeywordTypeNode(ast.KindUnknownKeyword), nil)
		}
	case referenceUnusedImports:
		if decl := referenceUnusedFullImport(token); decl != nil {
			tracker.Delete(request.SourceFile, decl)
		} else if referenceUnusedIsImport(token) {
			referenceUnusedTryDelete(ctx, request, ch, tracker, token, true)
		}
	case referenceUnusedDelete:
		parent := token.Parent
		if token.Kind == ast.KindInferKeyword || referenceUnusedIsImport(token) {
			return
		}
		if token.Kind == ast.KindJSDocTemplateTag {
			tracker.Delete(request.SourceFile, token)
		} else if token.Kind == ast.KindLessThanToken {
			referenceUnusedDeleteTypeParameters(tracker, request.SourceFile, token)
		} else if ast.IsObjectBindingPattern(parent) {
			if parent.Parent.Initializer() != nil {
				return
			}
			if !ast.IsParameterDeclaration(parent.Parent) || !referenceUnusedProvidedArguments(ctx, request, ch, parent.Parent) {
				tracker.Delete(request.SourceFile, parent.Parent)
			}
		} else if parent.Parent != nil && ast.IsArrayBindingPattern(parent.Parent) && parent.Parent.Parent.Initializer() != nil {
			return
		} else if referenceUnusedEntireVariable(token) {
			referenceUnusedDeleteVariables(tracker, request.SourceFile, parent)
		} else if ast.IsIdentifier(token) && ast.IsFunctionDeclaration(parent) {
			referenceUnusedDeleteFunction(tracker, request.SourceFile, parent)
		} else {
			referenceUnusedTryDelete(ctx, request, ch, tracker, token, true)
		}
	}
}
func referenceUnusedGetActions(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
	ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
	defer done()
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil || token.Parent == nil {
		return nil, nil
	}
	actions := []*CodeAction{}
	add := func(title, id string, apply func(*change.Tracker)) {
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		tracker.ReferenceDeletionTrivia = true
		apply(tracker)
		changes := referenceTrackerFileChanges(tracker)
		if len(changes) == 0 {
			return
		}
		allTitle := ""
		switch id {
		case referenceUnusedDelete:
			allTitle = diagnostics.Delete_all_unused_declarations.Localize(locale.FromContext(ctx))
		case referenceUnusedImports:
			allTitle = diagnostics.Delete_all_unused_imports.Localize(locale.FromContext(ctx))
		case referenceUnusedPrefix:
			allTitle = diagnostics.Prefix_all_unused_declarations_with_where_possible.Localize(locale.FromContext(ctx))
		case referenceUnusedInfer:
			allTitle = diagnostics.Replace_all_unused_infer_with_unknown.Localize(locale.FromContext(ctx))
		}
		actions = append(actions, &CodeAction{Description: title, FixID: id, FixAllDescription: allTitle, ReferenceChanges: changes})
	}
	removeName := func(name *ast.Node) string {
		return diagnostics.Remove_unused_declaration_for_Colon_0.Localize(locale.FromContext(ctx), scanner.GetTextOfNode(name))
	}
	switch {
	case token.Kind == ast.KindJSDocTemplateTag:
		add(diagnostics.Remove_template_tag.Localize(locale.FromContext(ctx)), referenceUnusedDelete, func(t *change.Tracker) { t.Delete(request.SourceFile, token) })
		return actions, ctx.Err()
	case token.Kind == ast.KindLessThanToken:
		add(diagnostics.Remove_type_parameters.Localize(locale.FromContext(ctx)), referenceUnusedDelete, func(t *change.Tracker) { referenceUnusedDeleteTypeParameters(t, request.SourceFile, token) })
		return actions, ctx.Err()
	}
	if decl := referenceUnusedFullImport(token); decl != nil {
		specifier := decl.AsImportDeclaration().ModuleSpecifier
		add(diagnostics.Remove_import_from_0.Localize(locale.FromContext(ctx), specifier.Text()), referenceUnusedImports, func(t *change.Tracker) { t.Delete(request.SourceFile, decl) })
		return actions, ctx.Err()
	} else if referenceUnusedIsImport(token) {
		add(removeName(token), referenceUnusedImports, func(t *change.Tracker) { referenceUnusedTryDelete(ctx, request, ch, t, token, false) })
		if len(actions) > 0 {
			return actions, ctx.Err()
		}
	}
	parent := token.Parent
	if ast.IsObjectBindingPattern(parent) || ast.IsArrayBindingPattern(parent) {
		if ast.IsParameterDeclaration(parent.Parent) {
			parts := []string{}
			for _, element := range parent.Elements() {
				parts = append(parts, scanner.GetTextOfNode(element))
			}
			desc := diagnostics.Remove_unused_declaration_for_Colon_0
			if len(parts) > 1 {
				desc = diagnostics.Remove_unused_declarations_for_Colon_0
			}
			add(desc.Localize(locale.FromContext(ctx), strings.Join(parts, ", ")), referenceUnusedDelete, func(t *change.Tracker) {
				for _, element := range parent.Elements() {
					t.Delete(request.SourceFile, element)
				}
			})
		} else {
			add(diagnostics.Remove_unused_destructuring_declaration.Localize(locale.FromContext(ctx)), referenceUnusedDelete, func(t *change.Tracker) { referenceUnusedDeleteDestructuring(ctx, request, t, parent) })
		}
		return actions, ctx.Err()
	}
	if referenceUnusedEntireVariable(token) {
		add(diagnostics.Remove_variable_statement.Localize(locale.FromContext(ctx)), referenceUnusedDelete, func(t *change.Tracker) { referenceUnusedDeleteVariables(t, request.SourceFile, parent) })
		return actions, ctx.Err()
	}
	if ast.IsIdentifier(token) && ast.IsFunctionDeclaration(parent) {
		add(removeName(token), referenceUnusedDelete, func(t *change.Tracker) { referenceUnusedDeleteFunction(t, request.SourceFile, parent) })
		return actions, ctx.Err()
	}
	if token.Kind == ast.KindInferKeyword {
		name := parent.AsInferTypeNode().TypeParameter.Name().Text()
		add(diagnostics.Replace_infer_0_with_unknown.Localize(locale.FromContext(ctx), name), referenceUnusedInfer, func(t *change.Tracker) {
			t.ReplaceNode(request.SourceFile, parent, t.NewKeywordTypeNode(ast.KindUnknownKeyword), nil)
		})
	} else {
		name := token
		if ast.IsComputedPropertyName(parent) {
			name = parent
		}
		add(removeName(name), referenceUnusedDelete, func(t *change.Tracker) { referenceUnusedTryDelete(ctx, request, ch, t, token, false) })
	}
	add(diagnostics.Prefix_0_with_an_underscore.Localize(locale.FromContext(ctx), scanner.GetTextOfNode(token)), referenceUnusedPrefix, func(t *change.Tracker) { referenceUnusedPrefixDeclaration(request, t, token, request.ErrorCode) })
	return actions, ctx.Err()
}

var ReferenceUnusedIdentifierProvider = &CodeFixProvider{
	Name: "unusedIdentifier", ErrorCodes: referenceUnusedCodes,
	FixIds:         []string{referenceUnusedPrefix, referenceUnusedDelete, referenceUnusedImports, referenceUnusedInfer},
	GetCodeActions: referenceUnusedGetActions,
	GetAllCodeActions: func(ctx context.Context, request *CodeFixContext) (*CombinedCodeActions, error) {
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		tracker.ReferenceDeletionTrivia = true
		for _, diagnostic := range getAllDiagnostics(ctx, request.Program, request.SourceFile) {
			if !slices.Contains(referenceUnusedCodes, diagnostic.Code()) {
				continue
			}
			token := astnav.GetTokenAtPosition(request.SourceFile, diagnostic.Pos())
			referenceUnusedApplyAll(ctx, request, ch, tracker, token, request.FixID, diagnostic.Code())
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		return &CombinedCodeActions{ReferenceChanges: referenceTrackerFileChanges(tracker)}, ctx.Err()
	},
}

func referenceCouldDeclareArrayElement(node *ast.Node, ch *checker.Checker) bool {
	if ast.IsAssignmentExpression(node, true) {
		node = node.AsBinaryExpression().Left
	}
	return ast.IsIdentifier(node) && ch.GetSymbolAtLocation(node) == nil
}
func referenceCouldDeclareComma(node *ast.Node, ch *checker.Checker) bool {
	if !ast.IsBinaryExpression(node) {
		return false
	}
	binary := node.AsBinaryExpression()
	if binary.OperatorToken.Kind == ast.KindCommaToken {
		return referenceCouldDeclareComma(binary.Left, ch) && referenceCouldDeclareComma(binary.Right, ch)
	}
	return binary.OperatorToken.Kind == ast.KindEqualsToken && ast.IsIdentifier(binary.Left) && ch.GetSymbolAtLocation(binary.Left) == nil
}

var ReferenceMissingConstProvider = newReferenceSyntaxFixProvider("addMissingConst", []int32{
	diagnostics.Cannot_find_name_0.Code(), diagnostics.No_value_exists_in_scope_for_the_shorthand_property_0_Either_declare_one_or_provide_an_initializer.Code(),
}, diagnostics.Add_const_to_all_unresolved_variables, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil {
		return "", nil
	}
	var initializer *ast.Node
	for node := token; node != nil && node.Parent != nil; node = node.Parent {
		if ast.IsForInOrOfStatement(node.Parent) {
			if node.Parent.AsForInOrOfStatement().Initializer == node {
				initializer = node
			}
			break
		}
		switch node.Kind {
		case ast.KindIdentifier, ast.KindArrayLiteralExpression, ast.KindObjectLiteralExpression, ast.KindPropertyAssignment, ast.KindShorthandPropertyAssignment:
			continue
		}
		break
	}
	if initializer == nil && ast.IsBinaryExpression(token.Parent) && token.Parent.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken && ast.IsExpressionStatement(token.Parent.Parent) {
		initializer = token
	}
	if initializer == nil {
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		if ast.IsArrayLiteralExpression(token.Parent) {
			valid := true
			for _, element := range token.Parent.AsArrayLiteralExpression().Elements.Nodes {
				if !referenceCouldDeclareArrayElement(element, ch) {
					valid = false
					break
				}
			}
			if valid {
				initializer = token.Parent
			}
		}
		if initializer == nil {
			for node := token; node != nil && node.Parent != nil; node = node.Parent {
				if ast.IsExpressionStatement(node.Parent) {
					if referenceCouldDeclareComma(node, ch) {
						initializer = node
					}
					break
				}
				if node.Kind != ast.KindIdentifier && node.Kind != ast.KindBinaryExpression && node.Kind != ast.KindCommaToken {
					break
				}
			}
		}
	}
	if initializer == nil || seen[initializer] {
		return "", nil
	}
	seen[initializer] = true
	tracker.InsertModifierBefore(request.SourceFile, ast.KindConstKeyword, initializer)
	return diagnostics.Add_const_to_unresolved_variable.Localize(locale.FromContext(ctx)), ctx.Err()
})

func newReferenceIncorrectNamedTupleProvider() *CodeFixProvider {
	provider := newReferenceSyntaxFixProvider("fixIncorrectNamedTupleSyntax", []int32{5086, 5087}, diagnostics.Move_labeled_tuple_element_modifiers_to_labels,
		func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
			token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
			for token != nil && token.Kind != ast.KindNamedTupleMember {
				token = token.Parent
			}
			if token == nil {
				return "", nil
			}
			member := token.AsNamedTupleMember()
			typ := member.Type
			rest, optional := member.DotDotDotToken, member.QuestionToken
			for typ != nil && (typ.Kind == ast.KindOptionalType || typ.Kind == ast.KindRestType || typ.Kind == ast.KindParenthesizedType) {
				if typ.Kind == ast.KindOptionalType && optional == nil {
					optional = tracker.NewToken(ast.KindQuestionToken)
				}
				if typ.Kind == ast.KindRestType && rest == nil {
					rest = tracker.NewToken(ast.KindDotDotDotToken)
				}
				typ = typ.Type()
			}
			updated := tracker.UpdateNamedTupleMember(member, rest, token.Name(), optional, typ)
			if updated == token {
				return "", nil
			}
			tracker.ReplaceNode(request.SourceFile, token, updated, nil)
			return diagnostics.Move_labeled_tuple_element_modifiers_to_labels.Localize(locale.FromContext(ctx)), ctx.Err()
		})
	provider.GetAllCodeActions = nil
	getActions := provider.GetCodeActions
	provider.GetCodeActions = func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		actions, err := getActions(ctx, request)
		if err != nil || len(actions) > 0 {
			return actions, err
		}
		title := diagnostics.Move_labeled_tuple_element_modifiers_to_labels.Localize(locale.FromContext(ctx))
		return []*CodeAction{{Description: title, ReferenceChanges: []ReferenceFileCodeChanges{}, FixID: "fixIncorrectNamedTupleSyntax", FixAllDescription: title}}, ctx.Err()
	}
	return provider
}

var ReferenceIncorrectNamedTupleProvider = newReferenceIncorrectNamedTupleProvider()

func referenceFindSuperStatement(node *ast.Node) *ast.Node {
	if node == nil {
		return nil
	}
	if ast.IsExpressionStatement(node) && ast.IsSuperCall(node.Expression()) {
		return node
	}
	if ast.IsFunctionLike(node) {
		return nil
	}
	var found *ast.Node
	node.ForEachChild(func(child *ast.Node) bool { found = referenceFindSuperStatement(child); return found != nil })
	return found
}

var ReferenceSuperBeforeThisProvider = newReferenceSyntaxFixProvider("classSuperMustPrecedeThisAccess", []int32{17009}, diagnostics.Make_all_super_calls_the_first_statement_in_their_constructor,
	func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil || token.Kind != ast.KindThisKeyword {
			return "", nil
		}
		constructor := ast.GetContainingFunction(token)
		if constructor == nil || !ast.IsConstructorDeclaration(constructor) || constructor.Body() == nil || seen[constructor] {
			return "", nil
		}
		call := referenceFindSuperStatement(constructor.Body())
		if call == nil {
			return "", nil
		}
		for _, arg := range call.Expression().Arguments() {
			if ast.IsPropertyAccessExpression(arg) && arg.Expression() == token {
				return "", nil
			}
		}
		seen[constructor] = true
		tracker.InsertReferenceNodeAtConstructorStart(request.SourceFile, constructor, call)
		tracker.DeleteNode(request.SourceFile, call, change.LeadingTriviaOptionIncludeAll, change.TrailingTriviaOptionNone)
		return diagnostics.Make_super_call_the_first_statement_in_the_constructor.Localize(locale.FromContext(ctx)), ctx.Err()
	})
var ReferenceVoidPromiseProvider = newReferenceSyntaxFixProvider("addVoidToPromise", []int32{2794, 2810}, diagnostics.Add_void_to_all_Promises_resolved_without_a_value,
	func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		node := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if node == nil || !ast.IsIdentifier(node) || !ast.IsCallExpression(node.Parent) || node.Parent.Expression() != node || len(node.Parent.Arguments()) != 0 {
			return "", nil
		}
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		symbol := ch.GetSymbolAtLocation(node)
		if symbol == nil {
			return "", nil
		}
		decl := symbol.ValueDeclaration
		if decl == nil || !ast.IsParameterDeclaration(decl) || decl.Parent == nil || !ast.IsNewExpression(decl.Parent.Parent) {
			return "", nil
		}
		expression := decl.Parent.Parent
		// The pinned combined provider creates a new seen set for each diagnostic.
		arguments := expression.TypeArguments()
		if len(arguments) > 0 {
			typ := arguments[0]
			parens := typ.Kind == ast.KindIntersectionType || typ.Kind == ast.KindConditionalType || typ.Kind == ast.KindFunctionType || typ.Kind == ast.KindConstructorType
			if parens {
				tracker.InsertText(request.SourceFile, request.LS.converters.PositionToLineAndCharacter(request.SourceFile, core.TextPos(typ.Pos())), "(")
			}
			suffix := " | void"
			if parens {
				suffix = ") | void"
			}
			tracker.InsertText(request.SourceFile, request.LS.converters.PositionToLineAndCharacter(request.SourceFile, core.TextPos(typ.End())), suffix)
		} else {
			signature := ch.GetResolvedSignature(node.Parent)
			var parameterType *checker.Type
			if signature != nil && len(signature.Parameters()) > 0 {
				parameterType = ch.GetTypeOfSymbolAtLocation(signature.Parameters()[0], expression)
			}
			if parameterType != nil && parameterType.Flags()&checker.TypeFlagsUnknown == 0 {
				return "", nil
			}
			tracker.InsertText(request.SourceFile, request.LS.converters.PositionToLineAndCharacter(request.SourceFile, core.TextPos(expression.Expression().End())), "<void>")
		}
		return diagnostics.Add_void_to_Promise_resolved_without_a_value.Localize(locale.FromContext(ctx)), ctx.Err()
	})

func newReferenceJSDocTypeProvider(nullable bool) *CodeFixProvider {
	name := "fixJSDocTypes_plain"
	all := diagnostics.Change_all_jsdoc_style_types_to_TypeScript
	if nullable {
		name = "fixJSDocTypes_nullable"
		all = diagnostics.Change_all_jsdoc_style_types_to_TypeScript_and_add_undefined_to_nullable_types
	}
	provider := newReferenceSyntaxFixProvider(name, []int32{8020, 17019, 17020}, all, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		container := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		for container != nil {
			switch container.Kind {
			case ast.KindAsExpression, ast.KindCallSignature, ast.KindConstructSignature, ast.KindFunctionDeclaration, ast.KindGetAccessor, ast.KindIndexSignature, ast.KindMappedType, ast.KindMethodDeclaration, ast.KindMethodSignature, ast.KindParameter, ast.KindPropertyDeclaration, ast.KindPropertySignature, ast.KindSetAccessor, ast.KindTypeAliasDeclaration, ast.KindTypeAssertionExpression, ast.KindVariableDeclaration:
				goto found
			}
			container = container.Parent
		}
	found:
		if container == nil || container.Type() == nil {
			return "", nil
		}
		node := container.Type()
		if nullable && node.Kind != ast.KindJSDocNullableType && request.FixID == "" {
			return "", nil
		}
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		var typ *checker.Type
		if ast.IsJSDocNullableType(node) {
			typ = ch.GetTypeFromTypeNode(node.Type())
			if typ.Flags()&(checker.TypeFlagsNever|checker.TypeFlagsVoid) == 0 {
				types := []*checker.Type{typ, ch.GetUndefinedType()}
				if node.Pos() != node.Type().Pos() {
					types = append(types, ch.GetNullType())
				}
				typ = ch.GetUnionType(types)
			}
		} else {
			typ = ch.GetTypeFromTypeNode(node)
		}
		if request.FixID == "fixJSDocTypes_nullable" && ast.IsJSDocNullableType(node) {
			typ = ch.GetUnionType([]*checker.Type{typ, ch.GetUndefinedType()})
		}
		replacement := ch.TypeToTypeNode(typ, node, nodebuilder.FlagsNone, nil)
		if replacement == nil {
			return "", nil
		}
		tracker.ReplaceNode(request.SourceFile, node, replacement, nil)
		return diagnostics.Change_0_to_1.Localize(locale.FromContext(ctx), scanner.GetTextOfNode(node), ch.TypeToString(typ)), ctx.Err()
	})
	provider.Name = "jdocTypes"
	return provider
}

var ReferenceJSDocTypePlainProvider = newReferenceJSDocTypeProvider(false)
var ReferenceJSDocTypeNullableProvider = newReferenceJSDocTypeProvider(true)

var ReferenceTypeOnlyExportProvider = newReferenceSyntaxFixProvider("convertToTypeOnlyExport", []int32{1205}, diagnostics.Convert_all_re_exported_types_to_type_only_exports,
	func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil || !ast.IsExportSpecifier(token.Parent) {
			return "", nil
		}
		origin := token.Parent
		clause := origin.Parent
		declaration := clause.Parent
		if seen[declaration] {
			return "", nil
		}
		seen[declaration] = true
		typeExports, valueExports := []*ast.Node{}, []*ast.Node{}
		ds := request.Program.GetSemanticDiagnostics(ctx, request.SourceFile)
		for _, element := range clause.AsNamedExports().Elements.Nodes {
			isType := element == origin
			start := astnav.GetStartOfNode(element, request.SourceFile, false)
			for _, d := range ds {
				if d.Pos() == start && d.End() == element.End() && d.Code() == 1205 {
					isType = true
					break
				}
			}
			if isType {
				typeExports = append(typeExports, element)
			} else {
				valueExports = append(valueExports, element)
			}
		}
		if len(valueExports) == 0 {
			tracker.InsertModifierBefore(request.SourceFile, ast.KindTypeKeyword, clause)
		} else {
			old := declaration.AsExportDeclaration()
			valueDeclaration := tracker.UpdateExportDeclaration(old, declaration.Modifiers(), false, tracker.UpdateNamedExports(clause.AsNamedExports(), tracker.NewNodeList(valueExports)), old.ModuleSpecifier, nil)
			typeDeclaration := tracker.NewExportDeclaration(nil, true, tracker.NewNamedExports(tracker.NewNodeList(typeExports)), old.ModuleSpecifier, nil)
			tracker.ReplaceNode(request.SourceFile, declaration, valueDeclaration, &change.NodeOptions{LeadingTriviaOption: change.LeadingTriviaOptionIncludeAll, TrailingTriviaOption: change.TrailingTriviaOptionExclude})
			tracker.InsertNodeAfter(request.SourceFile, declaration, typeDeclaration)
		}
		return diagnostics.Convert_to_type_only_export.Localize(locale.FromContext(ctx)), ctx.Err()
	})

var ReferenceWrapJSXFragmentProvider = newReferenceSyntaxFixProvider("wrapJsxInFragment", []int32{2657}, diagnostics.Wrap_all_unparented_JSX_in_JSX_fragment,
	func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil || token.Parent == nil {
			return "", nil
		}
		node := token.Parent.Parent
		if !ast.IsBinaryExpression(node) && node != nil {
			node = node.Parent
		}
		if !ast.IsBinaryExpression(node) || !ast.NodeIsMissing(node.AsBinaryExpression().OperatorToken) {
			return "", nil
		}
		children := []*ast.Node{}
		current := node
		for {
			if !ast.IsBinaryExpression(current) {
				return "", nil
			}
			binary := current.AsBinaryExpression()
			if !ast.NodeIsMissing(binary.OperatorToken) || binary.OperatorToken.Kind != ast.KindCommaToken {
				return "", nil
			}
			children = append(children, binary.Left)
			if ast.IsJsxChild(binary.Right) {
				children = append(children, binary.Right)
				break
			}
			if !ast.IsBinaryExpression(binary.Right) {
				return "", nil
			}
			current = binary.Right
		}
		replacement := tracker.NewJsxFragment(tracker.NewJsxOpeningFragment(), tracker.NewNodeList(children), tracker.NewJsxClosingFragment())
		tracker.ReplaceNode(request.SourceFile, node, replacement, nil)
		return diagnostics.Wrap_in_JSX_fragment.Localize(locale.FromContext(ctx)), ctx.Err()
	})
var ReferenceMissingAsyncProvider = newReferenceSyntaxFixProvider("addMissingAsync", []int32{2345, 2322, 2678}, diagnostics.Add_all_missing_async_modifiers,
	func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		var related *ast.Diagnostic
		for _, d := range request.Program.GetSemanticDiagnostics(ctx, request.SourceFile) {
			if d.Pos() != request.Span.Pos() || d.End() != request.Span.End() || d.Code() != request.ErrorCode {
				continue
			}
			for _, info := range d.RelatedInformation() {
				if info.Code() == 1356 {
					related = info
					break
				}
			}
			break
		}
		if related == nil {
			return "", nil
		}
		node := referenceAncestorMatchingSpan(request.SourceFile, related.Loc())
		if node == nil || !(ast.IsArrowFunction(node) || ast.IsMethodDeclaration(node) || ast.IsFunctionExpression(node) || ast.IsFunctionDeclaration(node)) || seen[node] {
			return "", nil
		}
		seen[node] = true
		clone := tracker.DeepCloneReparse(node)
		modifiers := tracker.NewModifierList(ast.CreateModifiersFromModifierFlags(node.ModifierFlags()|ast.ModifierFlagsAsync, tracker.NewToken))
		replacement := ast.ReplaceModifiers(tracker.NodeFactory, clone, modifiers)
		tracker.ReplaceNode(request.SourceFile, node, replacement, nil)
		return diagnostics.Add_async_modifier_to_containing_function.Localize(locale.FromContext(ctx)), ctx.Err()
	})
var ReferenceMissingConstraintProvider = newReferenceSyntaxFixProvider("addMissingConstraint", []int32{
	diagnostics.Type_0_is_not_comparable_to_type_1.Code(),
	diagnostics.Type_0_is_not_assignable_to_type_1_Two_different_types_with_this_name_exist_but_they_are_unrelated.Code(),
	diagnostics.Type_0_is_not_assignable_to_type_1_with_exactOptionalPropertyTypes_Colon_true_Consider_adding_undefined_to_the_types_of_the_target_s_properties.Code(),
	diagnostics.Type_0_is_not_assignable_to_type_1.Code(),
	diagnostics.Argument_of_type_0_is_not_assignable_to_parameter_of_type_1_with_exactOptionalPropertyTypes_Colon_true_Consider_adding_undefined_to_the_types_of_the_target_s_properties.Code(),
	diagnostics.Property_0_is_incompatible_with_index_signature.Code(),
	diagnostics.Property_0_in_type_1_is_not_assignable_to_type_2.Code(),
	diagnostics.Type_0_does_not_satisfy_the_constraint_1.Code(),
}, diagnostics.Add_extends_constraint_to_all_type_parameters,
	func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		var related *ast.Diagnostic
		for _, d := range request.Program.GetSemanticDiagnostics(ctx, request.SourceFile) {
			if d.Pos() != request.Span.Pos() || d.End() != request.Span.End() {
				continue
			}
			for _, info := range d.RelatedInformation() {
				if info.Code() == diagnostics.This_type_parameter_might_need_an_extends_0_constraint.Code() {
					related = info
					break
				}
			}
			break
		}
		if related == nil || related.File() == nil {
			return "", nil
		}
		declaration := referenceAncestorMatchingSpan(related.File(), related.Loc())
		if ast.IsIdentifier(declaration) {
			declaration = declaration.Parent
		}
		if !ast.IsTypeParameterDeclaration(declaration) || ast.IsMappedTypeNode(declaration.Parent) || seen[declaration] {
			return "", nil
		}
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		var constraint *checker.Type
		if token != nil && ast.IsTypeNode(token.Parent) {
			constraint = ch.GetTypeArgumentConstraint(token.Parent)
		} else if ast.IsExpression(token) {
			constraint = ch.GetContextualType(token, checker.ContextFlagsNone)
		}
		if constraint == nil && token != nil && !ast.IsTypeNode(token.Parent) {
			constraint = ch.GetTypeAtLocation(token)
		}
		seen[declaration] = true
		if constraint == nil {
			message := referenceDiagnosticText(related, locale.FromContext(ctx), 0)
			start := strings.Index(message, "`extends ")
			end := strings.LastIndex(message, "`")
			if start < 0 || end < start+9 {
				return "", nil
			}
			text := message[start+9 : end]
			tracker.InsertText(request.SourceFile, request.LS.converters.PositionToLineAndCharacter(request.SourceFile, core.TextPos(declaration.Name().End())), " extends "+text)
		} else {
			adder, err := createImportAdder(ctx, request, ch)
			if err != nil {
				return "", err
			}
			node := autoimport.TypeToAutoImportableTypeNode(ch, adder, constraint, nil)
			if node == nil {
				return "", nil
			}
			updated := tracker.UpdateTypeParameterDeclaration(declaration.AsTypeParameterDeclaration(), nil, declaration.Name(), node, nil, declaration.AsTypeParameterDeclaration().DefaultType)
			tracker.ReplaceNode(request.SourceFile, declaration, updated, nil)
			for _, edit := range adder.Edits() {
				tracker.ReplaceRangeWithText(request.SourceFile, edit.Range, edit.NewText)
			}
		}
		return diagnostics.Add_extends_constraint.Localize(locale.FromContext(ctx)), ctx.Err()
	})

func referenceSingleLineTypeLiterals(file *ast.SourceFile, node *ast.Node, tracker *change.Tracker) {
	if node == nil {
		return
	}
	if ast.IsTypeLiteralNode(node) {
		start := astnav.GetStartOfNode(node, file, false)
		if start >= 0 && node.End() <= len(file.Text()) && !strings.ContainsAny(file.Text()[start:node.End()], "\r\n\u2028\u2029") {
			tracker.AddEmitFlags(node, printer.EFSingleLine)
		}
	}
	node.ForEachChild(func(child *ast.Node) bool { referenceSingleLineTypeLiterals(file, child, tracker); return false })
}
func referenceUnionConstituent(tracker *change.Tracker, node *ast.Node) *ast.Node {
	switch node.Kind {
	case ast.KindUnionType, ast.KindIntersectionType, ast.KindConditionalType, ast.KindFunctionType, ast.KindConstructorType:
		return tracker.NewParenthesizedTypeNode(node)
	}
	return node
}
func referencePropertyDefault(ch *checker.Checker, tracker *change.Tracker, typ *checker.Type) *ast.Node {
	if typ.Flags()&checker.TypeFlagsBooleanLiteral != 0 {
		kind := ast.KindTrueKeyword
		if !typ.AsLiteralType().Value().(bool) {
			kind = ast.KindFalseKeyword
		}
		return tracker.NewKeywordExpression(kind)
	}
	if typ.IsStringLiteral() {
		return tracker.NewStringLiteral(typ.AsLiteralType().Value().(string), ast.TokenFlagsNone)
	}
	if typ.IsNumberLiteral() {
		return tracker.NewNumericLiteral(typ.AsLiteralType().String(), ast.TokenFlagsNone)
	}
	if typ.IsBigIntLiteral() {
		return tracker.NewBigIntLiteral(typ.AsLiteralType().String(), ast.TokenFlagsNone)
	}
	if typ.IsUnion() {
		for _, part := range typ.Types() {
			if value := referencePropertyDefault(ch, tracker, part); value != nil {
				return value
			}
		}
		return nil
	}
	if typ.IsClass() {
		declaration := ast.GetClassLikeDeclarationOfSymbol(typ.Symbol())
		if declaration == nil || declaration.ModifierFlags()&ast.ModifierFlagsAbstract != 0 {
			return nil
		}
		constructor := ast.GetFirstConstructorWithBody(declaration)
		if constructor != nil && len(constructor.Parameters()) > 0 {
			return nil
		}
		return tracker.NewNewExpression(tracker.NewIdentifier(typ.Symbol().Name), nil, nil)
	}
	if ch.IsArrayLikeType(typ) {
		return tracker.NewArrayLiteralExpression(tracker.NewNodeList(nil), false)
	}
	return nil
}
func newReferenceStrictPropertyProvider(mode int) *CodeFixProvider {
	name := "addMissingPropertyUndefinedType"
	all := diagnostics.Add_undefined_type_to_all_uninitialized_properties
	if mode == 1 {
		name = "addMissingPropertyDefiniteAssignmentAssertions"
		all = diagnostics.Add_definite_assignment_assertions_to_all_uninitialized_properties
	}
	if mode == 2 {
		name = "addMissingPropertyInitializer"
		all = diagnostics.Add_initializers_to_all_uninitialized_properties
	}
	provider := newReferenceSyntaxFixProvider(name, []int32{2564}, all, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if !ast.IsIdentifier(token) || !ast.IsPropertyDeclaration(token.Parent) || token.Parent.Type() == nil {
			return "", nil
		}
		property := token.Parent
		data := property.AsPropertyDeclaration()
		typ := property.Type()
		referenceSingleLineTypeLiterals(request.SourceFile, typ, tracker)
		var title string
		switch mode {
		case 0:
			types := []*ast.Node{typ}
			if ast.IsUnionTypeNode(typ) {
				types = slices.Clone(typ.AsUnionTypeNode().Types.Nodes)
			}
			for i, node := range types {
				types[i] = referenceUnionConstituent(tracker, node)
			}
			types = append(types, tracker.NewKeywordTypeNode(ast.KindUndefinedKeyword))
			tracker.ReplaceNode(request.SourceFile, typ, tracker.NewUnionTypeNode(tracker.NewNodeList(types)), nil)
			title = diagnostics.Add_undefined_type_to_property_0.Localize(locale.FromContext(ctx), scanner.GetTextOfNode(property.Name()))
		case 1:
			updated := tracker.UpdatePropertyDeclaration(data, property.Modifiers(), property.Name(), tracker.NewToken(ast.KindExclamationToken), typ, data.Initializer)
			tracker.AddEmitFlags(updated, printer.EFNoLeadingComments|printer.EFNoTrailingComments)
			tracker.ReplaceNode(request.SourceFile, property, updated, nil)
			title = diagnostics.Add_definite_assignment_assertion_to_property_0.Localize(locale.FromContext(ctx), scanner.GetTextOfNode(property))
		case 2:
			ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
			defer done()
			initializer := referencePropertyDefault(ch, tracker, ch.GetTypeFromTypeNode(typ))
			if initializer == nil {
				return "", nil
			}
			updated := tracker.UpdatePropertyDeclaration(data, property.Modifiers(), property.Name(), data.PostfixToken, typ, initializer)
			tracker.AddEmitFlags(updated, printer.EFNoLeadingComments|printer.EFNoTrailingComments)
			tracker.ReplaceNode(request.SourceFile, property, updated, nil)
			title = diagnostics.Add_initializer_to_property_0.Localize(locale.FromContext(ctx), scanner.GetTextOfNode(property.Name()))
		}
		return title, ctx.Err()
	})
	provider.Name = "strictClassInitialization"
	return provider
}

var ReferenceStrictPropertyUndefinedProvider = newReferenceStrictPropertyProvider(0)
var ReferenceStrictPropertyDefiniteProvider = newReferenceStrictPropertyProvider(1)
var ReferenceStrictPropertyInitializerProvider = newReferenceStrictPropertyProvider(2)

type referenceJSONOption struct{ name, value string }

func referenceConfigSource(request *CodeFixContext) *ast.SourceFile {
	line := request.Program.CommandLine()
	if line == nil || line.ConfigFile == nil {
		return nil
	}
	return line.ConfigFile.SourceFile
}
func referenceJSONProperty(object *ast.Node, name string) *ast.Node {
	for _, property := range object.Properties() {
		if ast.IsPropertyAssignment(property) && ast.IsStringLiteral(property.Name()) && property.Name().Text() == name {
			return property
		}
	}
	return nil
}
func referenceSetJSONOptions(tracker *change.Tracker, file *ast.SourceFile, options []referenceJSONOption) {
	object := getTsConfigObjectLiteralExpression(file)
	if object == nil {
		return
	}
	create := func(name string, value *ast.Node) *ast.Node {
		return tracker.NewPropertyAssignment(nil, tracker.NewStringLiteral(name, ast.TokenFlagsNone), nil, nil, value)
	}
	compilerOptions := referenceJSONProperty(object.AsNode(), "compilerOptions")
	if compilerOptions == nil {
		properties := []*ast.Node{}
		for _, option := range options {
			properties = append(properties, create(option.name, tracker.NewStringLiteral(option.value, ast.TokenFlagsNone)))
		}
		tracker.InsertMemberAtStart(file, object.AsNode(), create("compilerOptions", tracker.NewObjectLiteralExpression(tracker.NewNodeList(properties), true)))
		return
	}
	value := compilerOptions.Initializer()
	if !ast.IsObjectLiteralExpression(value) {
		return
	}
	for _, option := range options {
		replacement := tracker.NewStringLiteral(option.value, ast.TokenFlagsNone)
		property := referenceJSONProperty(value, option.name)
		if property == nil {
			tracker.InsertMemberAtStart(file, value, create(option.name, replacement))
		} else if scanner.GetTextOfNode(property.Initializer()) != strconv.Quote(option.value) {
			tracker.ReplaceNode(file, property.Initializer(), replacement, nil)
		}
	}
}

var ReferenceEnableJSXProvider = newReferenceSyntaxFixProvider("fixEnableJsxFlag", []int32{17004}, nil,
	func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		config := referenceConfigSource(request)
		if config == nil {
			return "", nil
		}
		referenceSetJSONOptions(tracker, config, []referenceJSONOption{{"jsx", "react"}})
		return diagnostics.Enable_the_jsx_flag_in_your_configuration_file.Localize(locale.FromContext(ctx)), ctx.Err()
	})
var referenceModuleTargetCodes = []int32{
	diagnostics.Top_level_await_expressions_are_only_allowed_when_the_module_option_is_set_to_es2022_esnext_system_node16_node18_node20_nodenext_or_preserve_and_the_target_option_is_set_to_es2017_or_higher.Code(),
	diagnostics.Top_level_await_using_statements_are_only_allowed_when_the_module_option_is_set_to_es2022_esnext_system_node16_node18_node20_nodenext_or_preserve_and_the_target_option_is_set_to_es2017_or_higher.Code(),
	diagnostics.Top_level_for_await_loops_are_only_allowed_when_the_module_option_is_set_to_es2022_esnext_system_node16_node18_node20_nodenext_or_preserve_and_the_target_option_is_set_to_es2017_or_higher.Code(),
}
var ReferenceModuleOptionProvider = newReferenceSyntaxFixProvider("fixModuleOption", referenceModuleTargetCodes, nil,
	func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		config := referenceConfigSource(request)
		if config == nil {
			return "", nil
		}
		module := request.Program.Options().GetEmitModuleKind()
		if module < core.ModuleKindES2015 || module >= core.ModuleKindESNext {
			return "", nil
		}
		referenceSetJSONOptions(tracker, config, []referenceJSONOption{{"module", "esnext"}})
		return diagnostics.Set_the_module_option_in_your_configuration_file_to_0.Localize(locale.FromContext(ctx), "esnext"), ctx.Err()
	})
var ReferenceTargetOptionProvider = newReferenceSyntaxFixProvider("fixTargetOption", referenceModuleTargetCodes, nil,
	func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		config := referenceConfigSource(request)
		if config == nil {
			return "", nil
		}
		options := request.Program.Options()
		target := options.GetEmitScriptTarget()
		if target >= core.ScriptTargetES2017 && target <= core.ScriptTargetESNext {
			return "", nil
		}
		changes := []referenceJSONOption{{"target", "es2017"}}
		if options.GetEmitModuleKind() == core.ModuleKindCommonJS {
			changes = append(changes, referenceJSONOption{"module", "commonjs"})
		}
		referenceSetJSONOptions(tracker, config, changes)
		return diagnostics.Set_the_target_option_in_your_configuration_file_to_0.Localize(locale.FromContext(ctx), "es2017"), ctx.Err()
	})

func referenceSpellingInfo(ctx context.Context, request *CodeFixContext, ch *checker.Checker) (*ast.Node, *ast.Symbol, error) {
	node := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if node == nil || node.Parent == nil {
		return nil, nil, nil
	}
	parent := node.Parent
	if (request.ErrorCode == 2769 || request.ErrorCode == 2322) && !ast.IsJsxAttribute(parent) {
		return nil, nil, nil
	}
	var suggestion *ast.Symbol
	switch {
	case ast.IsPropertyAccessExpression(parent) && parent.Name() == node:
		containing := ch.GetTypeAtLocation(parent.Expression())
		if parent.Flags&ast.NodeFlagsOptionalChain != 0 {
			containing = ch.GetNonNullableType(containing)
		}
		suggestion = ch.GetReferenceSuggestedProperty(node, containing)
	case ast.IsBinaryExpression(parent) && parent.AsBinaryExpression().OperatorToken.Kind == ast.KindInKeyword && parent.AsBinaryExpression().Left == node && ast.IsPrivateIdentifier(node):
		suggestion = ch.GetReferenceSuggestedProperty(node, ch.GetTypeAtLocation(parent.AsBinaryExpression().Right))
	case ast.IsQualifiedName(parent) && parent.AsQualifiedName().Right == node:
		symbol := ch.GetSymbolAtLocation(parent.AsQualifiedName().Left)
		if symbol != nil && symbol.Flags&ast.SymbolFlagsModule != 0 {
			suggestion = ch.GetReferenceSuggestedModule(node, symbol)
		}
	case ast.IsImportSpecifier(parent) && parent.Name() == node:
		declaration := parent
		for declaration != nil && !ast.IsImportDeclaration(declaration) {
			declaration = declaration.Parent
		}
		if declaration != nil && ast.IsStringLiteralLike(declaration.AsImportDeclaration().ModuleSpecifier) {
			resolved := request.Program.GetResolvedModuleFromModuleSpecifier(request.SourceFile, declaration.AsImportDeclaration().ModuleSpecifier)
			if resolved != nil {
				file := request.Program.GetSourceFile(resolved.ResolvedFileName)
				if file != nil && file.AsNode().Symbol() != nil {
					suggestion = ch.GetReferenceSuggestedModule(node, file.AsNode().Symbol())
				}
			}
		}
	case ast.IsJsxAttribute(parent) && parent.Name() == node:
		tag := parent
		for tag != nil && !ast.IsJsxOpeningLikeElement(tag) {
			tag = tag.Parent
		}
		if tag != nil {
			props := ch.GetContextualTypeForArgumentAtIndex(tag, 0)
			if props != nil {
				suggestion = ch.GetReferenceSuggestedJSXAttribute(node.Text(), props)
			}
		}
	case parent.ModifierFlags()&ast.ModifierFlagsOverride != 0 && ast.IsClassElement(parent) && parent.Name() == node:
		class := parent.Parent
		for class != nil && !ast.IsClassLike(class) {
			class = class.Parent
		}
		if class != nil {
			base := ast.GetClassExtendsHeritageElement(class)
			if base != nil {
				suggestion = ch.GetReferenceSuggestedClassMember(scanner.GetTextOfNode(node), ch.GetTypeAtLocation(base))
			}
		}
	default:
		meaning := getMeaningFromLocation(node)
		flags := ast.SymbolFlagsNone
		if meaning&ast.SemanticMeaningNamespace != 0 {
			flags |= ast.SymbolFlagsNamespace
		}
		if meaning&ast.SemanticMeaningType != 0 {
			flags |= ast.SymbolFlagsType
		}
		if meaning&ast.SemanticMeaningValue != 0 {
			flags |= ast.SymbolFlagsValue
		}
		suggestion = ch.GetReferenceSuggestedSymbol(node, scanner.GetTextOfNode(node), flags)
	}
	return node, suggestion, ctx.Err()
}
func newReferenceSpellingProvider() *CodeFixProvider {
	provider := newReferenceSyntaxFixProvider("fixSpelling", []int32{
		diagnostics.Property_0_does_not_exist_on_type_1_Did_you_mean_2.Code(),
		diagnostics.Property_0_may_not_exist_on_type_1_Did_you_mean_2.Code(),
		diagnostics.Cannot_find_name_0_Did_you_mean_1.Code(),
		diagnostics.Could_not_find_name_0_Did_you_mean_1.Code(),
		diagnostics.Cannot_find_namespace_0_Did_you_mean_1.Code(),
		diagnostics.Cannot_find_name_0_Did_you_mean_the_instance_member_this_0.Code(),
		diagnostics.Cannot_find_name_0_Did_you_mean_the_static_member_1_0.Code(),
		diagnostics.X_0_has_no_exported_member_named_1_Did_you_mean_2.Code(),
		diagnostics.This_member_cannot_have_an_override_modifier_because_it_is_not_declared_in_the_base_class_0_Did_you_mean_1.Code(),
		diagnostics.This_member_cannot_have_a_JSDoc_comment_with_an_override_tag_because_it_is_not_declared_in_the_base_class_0_Did_you_mean_1.Code(),
		diagnostics.No_overload_matches_this_call.Code(),
		diagnostics.Type_0_is_not_assignable_to_type_1.Code(),
	}, diagnostics.Fix_all_detected_spelling_errors, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		node, symbol, err := referenceSpellingInfo(ctx, request, ch)
		if err != nil || symbol == nil {
			return "", err
		}
		name := ast.SymbolName(symbol)
		if !scanner.IsIdentifierText(name, core.LanguageVariantStandard) && ast.IsPropertyAccessExpression(node.Parent) {
			declaration := symbol.ValueDeclaration
			if declaration != nil && declaration.Name() != nil && ast.IsPrivateIdentifier(declaration.Name()) {
				tracker.ReplaceNode(request.SourceFile, node, tracker.NewIdentifier(name), nil)
			} else {
				// The pinned provider deliberately uses a plain element access here.
				replacement := tracker.NewElementAccessExpression(node.Parent.Expression(), nil, tracker.NewStringLiteral(name, ast.TokenFlagsNone), ast.NodeFlagsNone)
				tracker.ReplaceNode(request.SourceFile, node.Parent, replacement, nil)
			}
		} else {
			tracker.ReplaceNode(request.SourceFile, node, tracker.NewIdentifier(name), nil)
		}
		return diagnostics.Change_spelling_to_0.Localize(locale.FromContext(ctx), name), ctx.Err()
	})
	provider.Name = "spelling"
	return provider
}

var ReferenceSpellingProvider = newReferenceSpellingProvider()

type referenceMissingTypeMember struct {
	token, declaration *ast.Node
	call               *ast.Node
	enum               bool
	file               *ast.SourceFile
	flags              ast.ModifierFlags
}

var referenceMissingMemberCodes = []int32{
	diagnostics.Property_0_does_not_exist_on_type_1.Code(),
	diagnostics.Property_0_does_not_exist_on_type_1_Did_you_mean_2.Code(),
	diagnostics.Property_0_is_missing_in_type_1_but_required_in_type_2.Code(),
	diagnostics.Type_0_is_missing_the_following_properties_from_type_1_Colon_2.Code(),
	diagnostics.Type_0_is_missing_the_following_properties_from_type_1_Colon_2_and_3_more.Code(),
	diagnostics.Argument_of_type_0_is_not_assignable_to_parameter_of_type_1.Code(),
	diagnostics.Cannot_find_name_0.Code(),
	diagnostics.Type_0_does_not_satisfy_the_expected_type_1.Code(),
}

func referenceMissingTypeMemberInfo(request *CodeFixContext, ch *checker.Checker) *referenceMissingTypeMember {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil || (!ast.IsIdentifier(token) && !ast.IsPrivateIdentifier(token)) || !ast.IsPropertyAccessExpression(token.Parent) {
		return nil
	}
	typ := ch.GetTypeAtLocation(token.Parent.Expression())
	if typ.IsTypeParameter() {
		if constraint := ch.GetBaseConstraintOfType(typ); constraint != nil {
			typ = constraint
		}
	}
	symbol := typ.Symbol()
	if symbol == nil {
		return nil
	}
	var declaration *ast.Node
	for _, candidate := range symbol.Declarations {
		if ast.IsClassLike(candidate) {
			declaration = candidate
			break
		}
	}
	if declaration == nil && ast.IsPrivateIdentifier(token) {
		return nil
	}
	if declaration == nil {
		for _, candidate := range symbol.Declarations {
			if ast.IsInterfaceDeclaration(candidate) || ast.IsTypeLiteralNode(candidate) {
				declaration = candidate
				break
			}
		}
	}
	enum := false
	if declaration == nil && typ.Flags()&checker.TypeFlagsEnumLike == 0 {
		for _, candidate := range symbol.Declarations {
			if ast.IsEnumDeclaration(candidate) {
				declaration = candidate
				enum = true
				break
			}
		}
	}
	if declaration == nil {
		return nil
	}
	file := ast.GetSourceFileOfNode(declaration)
	if file == nil || request.Program.IsSourceFileDefaultLibrary(file.Path()) || request.Program.IsSourceFileFromExternalLibrary(file) {
		return nil
	}
	flags := ast.ModifierFlagsNone
	if !enum && !ast.IsTypeLiteralNode(declaration) {
		target := typ
		if typ.Flags()&checker.TypeFlagsObject != 0 && typ.Target() != nil {
			target = typ.Target()
		}
		makeStatic := target != ch.GetDeclaredTypeOfSymbol(symbol)
		if makeStatic && (ast.IsPrivateIdentifier(token) || ast.IsInterfaceDeclaration(declaration)) {
			return nil
		}
		if makeStatic {
			flags |= ast.ModifierFlagsStatic
		}
		if strings.HasPrefix(token.Text(), "_") {
			flags |= ast.ModifierFlagsPrivate
		}
	}
	var call *ast.Node
	if ast.IsCallExpression(token.Parent.Parent) {
		call = token.Parent.Parent
	}
	return &referenceMissingTypeMember{token: token, declaration: declaration, file: file, flags: flags, call: call, enum: enum}
}
func referenceMissingMemberTypeNode(request *CodeFixContext, ch *checker.Checker, tracker *change.Tracker, info *referenceMissingTypeMember) *ast.Node {
	parent := info.token.Parent
	var typ *checker.Type
	enclosing := info.declaration
	if ast.IsBinaryExpression(parent.Parent) {
		binary := parent.Parent.AsBinaryExpression()
		other := binary.Left
		if parent == binary.Left {
			other = binary.Right
		}
		typ = ch.GetWidenedType(ch.GetBaseTypeOfLiteralType(ch.GetTypeAtLocation(other)))
	} else {
		typ = ch.GetContextualType(parent, checker.ContextFlagsNone)
		enclosing = nil
	}
	if typ != nil {
		builder := checker.NewNodeBuilder(ch, tracker.EmitContext)
		node := builder.TypeToTypeNode(typ, enclosing, nodebuilder.FlagsNoTruncation, nodebuilder.InternalFlagsAllowUnresolvedNames, nil)
		if node != nil {
			return node
		}
	}
	return tracker.NewKeywordTypeNode(ast.KindAnyKeyword)
}
func referenceInsertMissingProperty(request *CodeFixContext, ch *checker.Checker, tracker *change.Tracker, info *referenceMissingTypeMember, flags ast.ModifierFlags) {
	typ := referenceMissingMemberTypeNode(request, ch, tracker, info)
	var property *ast.Node
	name := tracker.NewIdentifier(info.token.Text())
	if ast.IsClassLike(info.declaration) {
		var modifiers *ast.ModifierList
		if flags != 0 {
			modifiers = tracker.NewModifierList(ast.CreateModifiersFromModifierFlags(flags, tracker.NewToken))
		}
		property = tracker.NewPropertyDeclaration(modifiers, name, nil, typ, nil)
	} else {
		property = tracker.NewPropertySignatureDeclaration(nil, name, nil, typ, nil)
	}
	var last *ast.Node
	for _, member := range info.declaration.Members() {
		if !ast.IsPropertyDeclaration(member) {
			break
		}
		last = member
	}
	if last != nil {
		tracker.InsertNodeAfter(info.file, last, property)
	} else {
		tracker.InsertMemberAtStart(info.file, info.declaration, property)
	}
}
func referenceMissingTypeMemberActions(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
	ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
	defer done()
	if other := referenceOtherMissingMemberInfo(request, ch); other != nil {
		return referenceOtherMissingMemberAction(ctx, request, ch, other)
	}
	info := referenceMissingTypeMemberInfo(request, ch)
	if info == nil {
		return nil, nil
	}
	actions := []*CodeAction{}
	add := func(title string, fixAll bool, apply func(*change.Tracker)) {
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		apply(tracker)
		action := &CodeAction{Description: title, ReferenceChanges: referenceTrackerFileChanges(tracker)}
		if fixAll {
			action.FixID = "fixMissingMember"
			action.FixAllDescription = diagnostics.Add_all_missing_members.Localize(locale.FromContext(ctx))
		}
		actions = append(actions, action)
	}
	name := info.token.Text()
	static := info.flags&ast.ModifierFlagsStatic != 0
	if info.enum {
		add(diagnostics.Add_missing_enum_member_0.Localize(locale.FromContext(ctx), name), true, func(t *change.Tracker) { referenceInsertMissingEnumMember(ch, t, info) })
		return actions, ctx.Err()
	}
	if info.call != nil {
		method := func(title string, flags ast.ModifierFlags, fixAll bool) error {
			methodContext := *request
			methodContext.SourceFile = info.file
			adder, err := createImportAdder(ctx, &methodContext, ch)
			if err != nil {
				return err
			}
			add(title, fixAll, func(t *change.Tracker) { referenceInsertMissingMethod(ctx, request, ch, t, adder, info, flags) })
			return nil
		}
		if !static && info.flags&ast.ModifierFlagsPrivate != 0 {
			if err := method(diagnostics.Declare_private_method_0.Localize(locale.FromContext(ctx), name), ast.ModifierFlagsPrivate, false); err != nil {
				return nil, err
			}
		}
		title := diagnostics.Declare_method_0
		if static {
			title = diagnostics.Declare_static_method_0
		}
		if err := method(title.Localize(locale.FromContext(ctx), name), info.flags&ast.ModifierFlagsStatic, true); err != nil {
			return nil, err
		}
	}
	if !static && !ast.IsPrivateIdentifier(info.token) && info.flags&ast.ModifierFlagsPrivate != 0 {
		add(diagnostics.Declare_private_property_0.Localize(locale.FromContext(ctx), name), false, func(t *change.Tracker) {
			referenceInsertMissingProperty(request, ch, t, info, ast.ModifierFlagsPrivate)
		})
	}
	message := diagnostics.Declare_property_0
	if static {
		message = diagnostics.Declare_static_property_0
	}
	add(message.Localize(locale.FromContext(ctx), name), true, func(t *change.Tracker) {
		referenceInsertMissingProperty(request, ch, t, info, info.flags&ast.ModifierFlagsStatic)
	})
	if !static && !ast.IsPrivateIdentifier(info.token) {
		add(diagnostics.Add_index_signature_for_property_0.Localize(locale.FromContext(ctx), name), false, func(t *change.Tracker) {
			parameter := t.NewParameterDeclaration(nil, nil, t.NewIdentifier("x"), nil, t.NewKeywordTypeNode(ast.KindStringKeyword), nil)
			index := t.NewIndexSignatureDeclaration(nil, t.NewNodeList([]*ast.Node{parameter}), referenceMissingMemberTypeNode(request, ch, t, info))
			t.InsertMemberAtStart(info.file, info.declaration, index)
		})
	}
	return actions, ctx.Err()
}
func referenceMissingTypeMemberAll(ctx context.Context, request *CodeFixContext) (*CombinedCodeActions, error) {
	ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
	defer done()
	tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
	declarations := []*ast.Node{}
	groups := map[*ast.Node][]*referenceMissingTypeMember{}
	type otherKey struct {
		owner *ast.Node
		name  string
		token *ast.Node
	}
	seenOther := map[otherKey]bool{}
	for _, diagnostic := range getAllDiagnostics(ctx, request.Program, request.SourceFile) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !slices.Contains(referenceMissingMemberCodes, diagnostic.Code()) {
			continue
		}
		individual := *request
		individual.Span = diagnostic.Loc()
		individual.ErrorCode = diagnostic.Code()
		if other := referenceOtherMissingMemberInfo(&individual, ch); other != nil {
			key := otherKey{owner: other.owner, name: other.identifier}
			if other.kind == "object" {
				if key.name == "" {
					key.token = other.token
				}
			} else {
				key.name = other.token.Text()
			}
			if seenOther[key] {
				continue
			}
			seenOther[key] = true
			applicable := (request.FixID == "fixMissingProperties" && other.kind == "object") || (request.FixID == "fixMissingAttributes" && other.kind == "jsx") || (request.FixID == "fixMissingFunctionDeclaration" && (other.kind == "function" || other.kind == "signature"))
			if applicable {
				if err := referenceApplyOtherMissingMember(ctx, request, ch, tracker, other); err != nil {
					return nil, err
				}
			}
			continue
		}
		info := referenceMissingTypeMemberInfo(&individual, ch)
		if info == nil {
			continue
		}
		if info.enum {
			key := info.declaration
			if !slices.ContainsFunc(groups[key], func(other *referenceMissingTypeMember) bool { return other.token.Text() == info.token.Text() }) {
				groups[key] = append(groups[key], info)
				referenceInsertMissingEnumMember(ch, tracker, info)
			}
			continue
		}
		infos, exists := groups[info.declaration]
		if !exists {
			declarations = append(declarations, info.declaration)
		}
		if !slices.ContainsFunc(infos, func(other *referenceMissingTypeMember) bool { return other.token.Text() == info.token.Text() }) {
			groups[info.declaration] = append(infos, info)
		}
	}
	alreadyInSuper := func(declaration *ast.Node, name string) bool {
		seen := map[*ast.Node]bool{}
		for declaration != nil && !seen[declaration] {
			seen[declaration] = true
			element := ast.GetClassExtendsHeritageElement(declaration)
			if element == nil {
				break
			}
			symbol := ch.GetSymbolAtLocation(element.Expression())
			if symbol == nil {
				break
			}
			if symbol.Flags&ast.SymbolFlagsAlias != 0 {
				symbol = ch.GetAliasedSymbol(symbol)
			}
			var parent *ast.Node
			for _, node := range symbol.Declarations {
				if ast.IsClassLike(node) {
					parent = node
					break
				}
			}
			if parent == nil {
				break
			}
			if slices.ContainsFunc(groups[parent], func(info *referenceMissingTypeMember) bool { return info.token.Text() == name }) {
				return true
			}
			declaration = parent
		}
		return false
	}
	for _, declaration := range declarations {
		for _, info := range groups[declaration] {
			if !ast.IsTypeLiteralNode(declaration) && alreadyInSuper(declaration, info.token.Text()) {
				continue
			}
			if info.call != nil && !ast.IsPrivateIdentifier(info.token) {
				methodContext := *request
				methodContext.SourceFile = info.file
				adder, err := createImportAdder(ctx, &methodContext, ch)
				if err != nil {
					return nil, err
				}
				referenceInsertMissingMethod(ctx, request, ch, tracker, adder, info, info.flags&ast.ModifierFlagsStatic)
			} else {
				referenceInsertMissingProperty(request, ch, tracker, info, info.flags&ast.ModifierFlagsStatic)
			}
		}
	}
	return &CombinedCodeActions{Description: diagnostics.Add_all_missing_members.Localize(locale.FromContext(ctx)), ReferenceChanges: referenceTrackerFileChanges(tracker)}, ctx.Err()
}

// All supported TypeScript missing-member categories share the reference diagnostic group.
var ReferenceMissingMemberProvider = &CodeFixProvider{Name: "fixMissingMember", ErrorCodes: referenceMissingMemberCodes, FixIds: []string{"fixMissingMember", "fixMissingFunctionDeclaration", "fixMissingProperties", "fixMissingAttributes"}, GetCodeActions: referenceMissingTypeMemberActions, GetAllCodeActions: referenceMissingTypeMemberAll}

type referenceCallTypeParameter struct {
	name       string
	typ        *checker.Type
	constraint *ast.Node
}

func referenceTypeContainsParameter(typ *checker.Type) bool {
	if typ.IsUnion() || typ.IsIntersection() {
		return slices.ContainsFunc(typ.Types(), referenceTypeContainsParameter)
	}
	return typ.IsTypeParameter()
}
func referenceFirstTypeParameterName(typ *checker.Type) string {
	if typ.IsUnion() || typ.IsIntersection() {
		for _, part := range typ.Types() {
			if name := referenceFirstTypeParameterName(part); name != "" {
				return name
			}
		}
		return ""
	}
	if typ.IsTypeParameter() && typ.Symbol() != nil {
		return ast.SymbolName(typ.Symbol())
	}
	return ""
}
func referenceTypeParameterName(index int) string {
	if index <= 6 {
		return string(rune('T' + index))
	}
	return "T" + strconv.Itoa(index)
}
func referenceStubBody(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, message *diagnostics.Message) *ast.Node {
	flags := ast.TokenFlagsNone
	if lsutil.GetQuotePreference(request.SourceFile, request.LS.UserPreferences()) == lsutil.QuotePreferenceSingle {
		flags = ast.TokenFlagsSingleQuote
	}
	errorCall := tracker.NewNewExpression(tracker.NewIdentifier("Error"), nil, tracker.NewNodeList([]*ast.Node{tracker.NewStringLiteral(message.Localize(locale.FromContext(ctx)), flags)}))
	return tracker.NewBlock(tracker.NewNodeList([]*ast.Node{tracker.NewThrowStatement(errorCall)}), true)
}
func referenceCallSignature(ctx context.Context, request *CodeFixContext, ch *checker.Checker, tracker *change.Tracker, adder autoimport.ImportAdder, call, name, scope *ast.Node, flags ast.ModifierFlags, kind ast.Kind) *ast.Node {
	// A token from the caller cannot retain its source range when printed
	// into another document's declaration.
	if ast.IsPrivateIdentifier(name) {
		name = tracker.NewPrivateIdentifier(name.Text())
	} else {
		name = tracker.NewIdentifier(name.Text())
	}
	idToSymbol := map[*ast.Node]*ast.Symbol{}
	builder := checker.NewNodeBuilderEx(ch, tracker.EmitContext, idToSymbol)
	typeNode := func(typ *checker.Type) *ast.Node {
		node := builder.TypeToTypeNode(typ, scope, nodebuilder.FlagsNoTruncation, nodebuilder.InternalFlagsAllowUnresolvedNames, nil)
		return autoimport.TypeNodeToAutoImportableTypeNode(node, adder, idToSymbol)
	}
	types := []*ast.Node{}
	typeParameters := []referenceCallTypeParameter{}
	parameterIndices := map[string]int{}
	remember := func(parameter referenceCallTypeParameter) {
		if index, exists := parameterIndices[parameter.name]; exists {
			typeParameters[index] = parameter
		} else {
			parameterIndices[parameter.name] = len(typeParameters)
			typeParameters = append(typeParameters, parameter)
		}
	}
	names := []string{}
	for index, argument := range call.Arguments() {
		name := ""
		if ast.IsIdentifier(argument) {
			name = argument.Text()
		} else if ast.IsPropertyAccessExpression(argument) && ast.IsIdentifier(argument.Name()) {
			name = argument.Name().Text()
		}
		names = append(names, name)
		typ := ch.GetTypeAtLocation(argument)
		if (typ.IsUnion() || typ.IsIntersection()) && slices.ContainsFunc(typ.Types(), referenceTypeContainsParameter) {
			name := referenceTypeParameterName(index)
			types = append(types, tracker.NewTypeReferenceNode(tracker.NewIdentifier(name), nil))
			remember(referenceCallTypeParameter{name: name})
			continue
		}
		node := typeNode(ch.GetBaseTypeOfLiteralType(typ))
		if node == nil {
			continue
		}
		types = append(types, node)
		name = referenceFirstTypeParameterName(typ)
		var constraint *ast.Node
		if typ.IsTypeParameter() {
			value := ch.GetConstraintOfTypeParameter(typ)
			if value != nil && !(value.Flags()&checker.TypeFlagsObject != 0 && value.ObjectFlags() == checker.ObjectFlagsAnonymous) {
				constraint = typeNode(value)
			}
		}
		if name != "" {
			remember(referenceCallTypeParameter{name: name, typ: typ, constraint: constraint})
		}
	}
	newTypeArgumentCount := 0
	for _, argument := range call.TypeArguments() {
		typ := ch.GetTypeAtLocation(argument)
		if !slices.ContainsFunc(typeParameters, func(parameter referenceCallTypeParameter) bool { return parameter.typ == typ }) {
			newTypeArgumentCount++
		}
	}
	targetSize := len(typeParameters) + newTypeArgumentCount
	for i := 0; len(typeParameters) < targetSize; i++ {
		name := referenceTypeParameterName(i)
		if _, exists := parameterIndices[name]; !exists {
			remember(referenceCallTypeParameter{name: name})
		}
	}
	typeParameterNodes := []*ast.Node{}
	for _, parameter := range typeParameters {
		typeParameterNodes = append(typeParameterNodes, tracker.NewTypeParameterDeclaration(nil, tracker.NewIdentifier(parameter.name), parameter.constraint, nil, nil))
	}
	var typeParameterList *ast.NodeList
	if len(typeParameterNodes) > 0 {
		typeParameterList = tracker.NewNodeList(typeParameterNodes)
	}
	parameters := createDummyParameters(tracker.NodeFactory, len(call.Arguments()), names, types, len(call.Arguments()), false)
	var returnType *ast.Node
	if typ := ch.GetContextualType(call, checker.ContextFlagsNone); typ != nil {
		returnType = builder.TypeToTypeNode(typ, scope, nodebuilder.FlagsNone, nodebuilder.InternalFlagsNone, nil)
	}
	var modifiers *ast.ModifierList
	if flags != 0 {
		modifiers = tracker.NewModifierList(ast.CreateModifiersFromModifierFlags(flags, tracker.NewToken))
	}
	var asterisk *ast.Node
	if ast.IsYieldExpression(call.Parent) {
		asterisk = tracker.NewToken(ast.KindAsteriskToken)
	}
	switch kind {
	case ast.KindMethodDeclaration:
		return tracker.NewMethodDeclaration(modifiers, asterisk, name, nil, typeParameterList, parameters, returnType, nil, referenceStubBody(ctx, request, tracker, diagnostics.Method_not_implemented))
	case ast.KindMethodSignature:
		if returnType == nil {
			returnType = tracker.NewKeywordTypeNode(ast.KindUnknownKeyword)
		}
		return tracker.NewMethodSignatureDeclaration(modifiers, name, nil, typeParameterList, parameters, returnType)
	case ast.KindFunctionDeclaration:
		return tracker.NewFunctionDeclaration(modifiers, asterisk, name, typeParameterList, parameters, returnType, nil, referenceStubBody(ctx, request, tracker, diagnostics.Function_not_implemented))
	}
	return nil
}

func referenceInsertMissingEnumMember(ch *checker.Checker, tracker *change.Tracker, info *referenceMissingTypeMember) {
	var initializer *ast.Node
	if slices.ContainsFunc(info.declaration.Members(), func(node *ast.Node) bool { return ch.GetTypeAtLocation(node).Flags()&checker.TypeFlagsStringLike != 0 }) {
		initializer = tracker.NewStringLiteral(info.token.Text(), ast.TokenFlagsNone)
	}
	member := tracker.NewEnumMember(tracker.NewIdentifier(info.token.Text()), initializer)
	members := info.declaration.MemberList()
	if len(members.Nodes) > 0 {
		tracker.InsertNodeInListAfter(info.file, members.Nodes[len(members.Nodes)-1], member, members)
	} else {
		tracker.InsertMemberAtStart(info.file, info.declaration, member)
	}
}
func referenceInsertMissingMethod(ctx context.Context, request *CodeFixContext, ch *checker.Checker, tracker *change.Tracker, adder autoimport.ImportAdder, info *referenceMissingTypeMember, flags ast.ModifierFlags) {
	kind := ast.KindMethodDeclaration
	if !ast.IsClassLike(info.declaration) {
		kind = ast.KindMethodSignature
	}
	node := referenceCallSignature(ctx, request, ch, tracker, adder, info.call, info.token, info.declaration, flags, kind)
	var after *ast.Node
	if !ast.IsTypeLiteralNode(info.declaration) {
		for parent := info.call; parent != nil; parent = parent.Parent {
			if ast.IsMethodDeclaration(parent) || ast.IsConstructorDeclaration(parent) {
				if parent.Parent == info.declaration {
					after = parent
				}
				break
			}
		}
	}
	if after != nil {
		tracker.InsertNodeAfter(info.file, after, node)
	} else {
		tracker.InsertMemberAtStart(info.file, info.declaration, node)
	}
	if adder != nil {
		for _, edit := range adder.Edits() {
			tracker.ReplaceRangeWithText(info.file, edit.Range, edit.NewText)
		}
	}
}

// Reference non-type missing-member categories. All nodes and import additions
// belong to this request; project source nodes remain immutable.
type referenceOtherMissingMember struct {
	kind               string
	token, owner, call *ast.Node
	signature          *checker.Signature
	file               *ast.SourceFile
	flags              ast.ModifierFlags
	properties         []*ast.Symbol
	identifier         string
	zeroIndent         bool
}

func referenceMissingFunctionScope(token *ast.Node) *ast.Node {
	jsx := false
	for node := token; node != nil; node = node.Parent {
		if ast.IsJsxExpression(node) {
			jsx = true
			break
		}
	}
	if jsx {
		for node := token.Parent; node != nil; node = node.Parent {
			if ast.IsReturnStatement(node) {
				return node
			}
		}
	}
	return ast.GetSourceFileOfNode(token).AsNode()
}
func referenceOtherMissingMemberInfo(request *CodeFixContext, ch *checker.Checker) *referenceOtherMissingMember {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil || token.Parent == nil {
		return nil
	}
	parent := token.Parent
	unmatched := func(object *ast.Node, target *checker.Type) []*ast.Symbol {
		if target == nil {
			return nil
		}
		return ch.GetReferenceUnmatchedProperties(ch.GetTypeAtLocation(object), ch.GetNonNullableType(target), false, false)
	}
	objectInfo := func(object, token *ast.Node, identifier string, properties []*ast.Symbol) *referenceOtherMissingMember {
		if len(properties) == 0 {
			return nil
		}
		return &referenceOtherMissingMember{kind: "object", token: token, owner: object, file: request.SourceFile, identifier: identifier, properties: properties, zeroIndent: ast.IsReturnStatement(object.Parent) || ast.IsYieldExpression(object.Parent)}
	}
	if request.ErrorCode == diagnostics.Argument_of_type_0_is_not_assignable_to_parameter_of_type_1.Code() {
		if token.Kind != ast.KindOpenBraceToken || !ast.IsObjectLiteralExpression(parent) || !ast.IsCallExpression(parent.Parent) {
			return nil
		}
		index := slices.Index(parent.Parent.Arguments(), parent)
		if index < 0 {
			return nil
		}
		signature := ch.GetResolvedSignature(parent.Parent)
		if signature == nil || signature.Declaration() == nil || index >= len(signature.Parameters()) {
			return nil
		}
		parameter := signature.Parameters()[index].ValueDeclaration
		if parameter == nil || !ast.IsParameterDeclaration(parameter) || !ast.IsIdentifier(parameter.Name()) {
			return nil
		}
		return objectInfo(parent, parameter.Name(), parameter.Name().Text(), unmatched(parent, ch.GetTypeAtPosition(signature, index)))
	}
	if token.Kind == ast.KindOpenBraceToken || ast.IsSatisfiesExpression(parent) || ast.IsReturnStatement(parent) {
		expression := parent
		if (ast.IsSatisfiesExpression(parent) || ast.IsReturnStatement(parent)) && parent.Expression() != nil {
			expression = parent.Expression()
		}
		if ast.IsObjectLiteralExpression(expression) {
			var target *checker.Type
			if ast.IsSatisfiesExpression(parent) {
				target = ch.GetTypeFromTypeNode(parent.Type())
			} else {
				target = ch.GetContextualType(expression, checker.ContextFlagsNone)
				if target == nil {
					target = ch.GetTypeAtLocation(expression)
				}
			}
			return objectInfo(expression, parent, "", unmatched(parent, target))
		}
	}
	if !ast.IsIdentifier(token) {
		return nil
	}
	if ast.HasInitializer(parent) && parent.Initializer() != nil && ast.IsObjectLiteralExpression(parent.Initializer()) {
		target := ch.GetContextualType(token, checker.ContextFlagsNone)
		if target == nil {
			target = ch.GetTypeAtLocation(token)
		}
		return objectInfo(parent.Initializer(), token, token.Text(), unmatched(parent.Initializer(), target))
	}
	if ast.IsJsxOpeningLikeElement(parent) {
		attrs := parent.Attributes()
		target := ch.GetContextualType(attrs, checker.ContextFlagsNone)
		if target == nil {
			return nil
		}
		seen := map[string]bool{}
		for _, prop := range attrs.AsJsxAttributes().Properties.Nodes {
			if ast.IsJsxAttribute(prop) {
				seen[scanner.GetTextOfNode(prop.Name())] = true
			}
			if ast.IsJsxSpreadAttribute(prop) {
				for _, symbol := range ch.GetPropertiesOfType(ch.GetTypeAtLocation(prop.Expression())) {
					seen[symbol.Name] = true
				}
			}
		}
		properties := []*ast.Symbol{}
		for _, prop := range ch.GetPropertiesOfType(target) {
			if scanner.IsIdentifierText(ast.SymbolName(prop), core.LanguageVariantJSX) && prop.Flags&ast.SymbolFlagsOptional == 0 && prop.CheckFlags&ast.CheckFlagsPartial == 0 && !seen[prop.Name] {
				properties = append(properties, prop)
			}
		}
		if len(properties) == 0 {
			return nil
		}
		return &referenceOtherMissingMember{kind: "jsx", token: token, owner: parent, file: request.SourceFile, properties: properties}
	}
	if typ := ch.GetContextualType(token, checker.ContextFlagsNone); typ != nil {
		typ = ch.GetNonNullableType(typ)
		if typ.ObjectFlags()&checker.ObjectFlagsAnonymous != 0 {
			signatures := ch.GetCallSignatures(typ)
			if len(signatures) == 0 {
				return nil
			}
			return &referenceOtherMissingMember{kind: "signature", token: token, signature: signatures[0], owner: referenceMissingFunctionScope(token), file: request.SourceFile}
		}
	}
	if ast.IsCallExpression(parent) && parent.Expression() == token {
		return &referenceOtherMissingMember{kind: "function", token: token, call: parent, owner: referenceMissingFunctionScope(token), file: request.SourceFile}
	}
	if !ast.IsPropertyAccessExpression(parent) || !ast.IsCallExpression(parent.Parent) {
		return nil
	}
	typ := ch.GetTypeAtLocation(parent.Expression())
	if typ.IsTypeParameter() {
		if constraint := ch.GetBaseConstraintOfType(typ); constraint != nil {
			typ = constraint
		}
	}
	symbol := typ.Symbol()
	if symbol == nil {
		return nil
	}
	var owner *ast.Node
	for _, node := range symbol.Declarations {
		if ast.IsModuleDeclaration(node) {
			owner = node
			break
		}
	}
	if owner == nil {
		if request.SourceFile.CommonJSModuleIndicator != nil {
			return nil
		}
		for _, node := range symbol.Declarations {
			if ast.IsSourceFile(node) {
				owner = node
				break
			}
		}
	}
	if owner == nil {
		return nil
	}
	file := ast.GetSourceFileOfNode(owner)
	if file == nil || request.Program.IsSourceFileDefaultLibrary(file.Path()) || request.Program.IsSourceFileFromExternalLibrary(file) {
		return nil
	}
	return &referenceOtherMissingMember{kind: "function", token: token, call: parent.Parent, owner: owner, file: file, flags: ast.ModifierFlagsExport}
}

type referenceMissingValueBuilder struct {
	ctx         context.Context
	request     *CodeFixContext
	ch          *checker.Checker
	tracker     *change.Tracker
	adder       autoimport.ImportAdder
	depth, work int
	active      map[*checker.Type]bool
	err         error
}

func (b *referenceMissingValueBuilder) propertyName(symbol *ast.Symbol) *ast.Node {
	if symbol.Flags&ast.SymbolFlagsTransient != 0 {
		node := checker.NewNodeBuilder(b.ch, b.tracker.EmitContext).SymbolToNode(symbol, ast.SymbolFlagsValue, nil, nodebuilder.FlagsNone, nodebuilder.InternalFlagsWriteComputedProps, nil)
		if node != nil && ast.IsComputedPropertyName(node) {
			return node
		}
	}
	name := ast.SymbolName(symbol)
	if scanner.IsIdentifierText(name, core.LanguageVariantStandard) {
		return b.tracker.NewIdentifier(name)
	}
	return b.stringLiteral(name)
}
func (b *referenceMissingValueBuilder) stringLiteral(value string) *ast.Node {
	flags := ast.TokenFlagsNone
	if lsutil.GetQuotePreference(b.request.SourceFile, b.request.LS.UserPreferences()) == lsutil.QuotePreferenceSingle {
		flags = ast.TokenFlagsSingleQuote
	}
	return b.tracker.NewStringLiteral(value, flags)
}
func (b *referenceMissingValueBuilder) value(typ *checker.Type, enclosing *ast.Node) *ast.Node {
	t := b.tracker
	undefined := func() *ast.Node { return t.NewIdentifier("undefined") }
	if typ == nil || b.err != nil {
		return undefined()
	}
	b.work++
	if err := b.ctx.Err(); err != nil {
		b.err = err
		return undefined()
	}
	if b.depth >= 128 || b.work > 10000 || b.active[typ] {
		b.err = fmt.Errorf("missing-member initializer exceeds finite type expansion")
		return undefined()
	}
	b.active[typ] = true
	b.depth++
	defer func() { delete(b.active, typ); b.depth-- }()
	flags := typ.Flags()
	switch {
	case flags&checker.TypeFlagsAnyOrUnknown != 0:
		return undefined()
	case flags&(checker.TypeFlagsString|checker.TypeFlagsTemplateLiteral) != 0:
		return b.stringLiteral("")
	case flags&checker.TypeFlagsNumber != 0:
		return t.NewNumericLiteral("0", ast.TokenFlagsNone)
	case flags&checker.TypeFlagsBigInt != 0:
		return t.NewBigIntLiteral("0n", ast.TokenFlagsNone)
	case flags&checker.TypeFlagsBoolean != 0:
		return t.NewKeywordExpression(ast.KindFalseKeyword)
	case flags&checker.TypeFlagsEnumLike != 0:
		symbol := typ.Symbol()
		if symbol == nil {
			return t.NewNumericLiteral("0", ast.TokenFlagsNone)
		}
		member := symbol
		if symbol.Exports != nil {
			member = nil
			for _, decl := range symbol.Declarations {
				if ast.IsEnumDeclaration(decl) && len(decl.Members()) > 0 {
					member = b.ch.GetSymbolAtLocation(decl.Members()[0].Name())
					break
				}
			}
		}
		if symbol.Parent != nil && symbol.Parent.Flags&ast.SymbolFlagsRegularEnum != 0 {
			symbol = symbol.Parent
		}
		name := checker.NewNodeBuilder(b.ch, t.EmitContext).SymbolToExpression(symbol, ast.SymbolFlagsValue, nil, nodebuilder.FlagsUseFullyQualifiedType, nodebuilder.InternalFlagsNone, nil)
		if member == nil || name == nil {
			return t.NewNumericLiteral("0", ast.TokenFlagsNone)
		}
		return t.NewPropertyAccessExpression(name, nil, t.NewIdentifier(b.ch.SymbolToString(member)), ast.NodeFlagsNone)
	case flags&checker.TypeFlagsNumberLiteral != 0:
		return t.NewNumericLiteral(typ.AsLiteralType().String(), ast.TokenFlagsNone)
	case flags&checker.TypeFlagsBigIntLiteral != 0:
		return t.NewBigIntLiteral(typ.AsLiteralType().String(), ast.TokenFlagsNone)
	case flags&checker.TypeFlagsStringLiteral != 0:
		return b.stringLiteral(typ.AsLiteralType().Value().(string))
	case flags&checker.TypeFlagsBooleanLiteral != 0:
		if b.ch.TypeToString(typ) == "false" {
			return t.NewKeywordExpression(ast.KindFalseKeyword)
		}
		return t.NewKeywordExpression(ast.KindTrueKeyword)
	case flags&checker.TypeFlagsNull != 0:
		return t.NewKeywordExpression(ast.KindNullKeyword)
	case flags&checker.TypeFlagsUnion != 0:
		if len(typ.Types()) > 0 {
			return b.value(typ.Types()[0], enclosing)
		}
		return undefined()
	}
	if b.ch.IsArrayLikeType(typ) {
		return t.NewArrayLiteralExpression(nil, false)
	}
	symbol := typ.Symbol()
	objectLiteral := flags&checker.TypeFlagsObject != 0 && (typ.ObjectFlags()&checker.ObjectFlagsObjectLiteral != 0 || (symbol != nil && len(symbol.Declarations) == 1 && ast.IsTypeLiteralNode(symbol.Declarations[0])))
	if objectLiteral {
		properties := []*ast.Node{}
		for _, prop := range b.ch.GetPropertiesOfType(typ) {
			properties = append(properties, t.NewPropertyAssignment(nil, t.NewIdentifier(ast.SymbolName(prop)), nil, nil, b.value(b.ch.GetTypeOfSymbol(prop), enclosing)))
		}
		return t.NewObjectLiteralExpression(t.NewNodeList(properties), true)
	}
	if typ.ObjectFlags()&checker.ObjectFlagsAnonymous != 0 {
		if symbol == nil || !slices.ContainsFunc(symbol.Declarations, func(n *ast.Node) bool {
			return ast.IsFunctionTypeNode(n) || ast.IsMethodSignatureDeclaration(n) || ast.IsMethodDeclaration(n)
		}) {
			return undefined()
		}
		signatures := b.ch.GetCallSignatures(typ)
		if len(signatures) == 0 {
			return undefined()
		}
		fixer := newMissingMemberFixer(t, b.request.Program, b.ch, b.request.LS.UserPreferences(), b.adder, locale.FromContext(b.ctx))
		result := fixer.createSignatureDeclarationFromSignature(signatures[0], ast.KindFunctionExpression, b.request.SourceFile, enclosing, referenceStubBody(b.ctx, b.request, t, diagnostics.Function_not_implemented), nil, nil, false)
		if result != nil {
			return result
		}
		return undefined()
	}
	if typ.ObjectFlags()&checker.ObjectFlagsClass != 0 && symbol != nil {
		decl := ast.GetClassLikeDeclarationOfSymbol(symbol)
		if decl == nil || decl.ModifierFlags()&ast.ModifierFlagsAbstract != 0 {
			return undefined()
		}
		constructor := ast.GetFirstConstructorWithBody(decl)
		if constructor != nil && len(constructor.Parameters()) > 0 {
			return undefined()
		}
		return t.NewNewExpression(t.NewIdentifier(ast.SymbolName(symbol)), nil, nil)
	}
	return undefined()
}
func referenceApplyOtherMissingMember(ctx context.Context, request *CodeFixContext, ch *checker.Checker, tracker *change.Tracker, info *referenceOtherMissingMember) error {
	adder, err := createImportAdder(ctx, request, ch)
	if err != nil {
		return err
	}
	builder := &referenceMissingValueBuilder{ctx: ctx, request: request, ch: ch, tracker: tracker, adder: adder, active: map[*checker.Type]bool{}}
	switch info.kind {
	case "function", "signature":
		var node *ast.Node
		if info.kind == "function" {
			node = referenceCallSignature(ctx, request, ch, tracker, adder, info.call, info.token, info.owner, info.flags, ast.KindFunctionDeclaration)
		} else {
			fixer := newMissingMemberFixer(tracker, request.Program, ch, request.LS.UserPreferences(), adder, locale.FromContext(ctx))
			node = fixer.createSignatureDeclarationFromSignature(info.signature, ast.KindFunctionDeclaration, request.SourceFile, nil, referenceStubBody(ctx, request, tracker, diagnostics.Function_not_implemented), nil, tracker.NewIdentifier(info.token.Text()), false)
		}
		if node == nil {
			return fmt.Errorf("unable to construct missing function declaration")
		}
		if ast.IsReturnStatement(info.owner) {
			tracker.InsertNodeBefore(info.file, info.owner, node, true, change.LeadingTriviaOptionNone)
		} else {
			tracker.InsertReferenceNodeAtEndOfScope(info.file, info.owner, node)
		}
	case "object":
		properties := slices.Clone(info.owner.AsObjectLiteralExpression().Properties.Nodes)
		for _, prop := range info.properties {
			properties = append(properties, tracker.NewPropertyAssignment(nil, builder.propertyName(prop), nil, nil, builder.value(ch.GetTypeOfSymbol(prop), info.owner)))
		}
		tracker.ReplaceReferenceObjectLiteral(info.file, info.owner, tracker.NewObjectLiteralExpression(tracker.NewNodeList(properties), true), info.zeroIndent)
	case "jsx":
		attrs := info.owner.Attributes()
		old := attrs.AsJsxAttributes().Properties.Nodes
		added := []*ast.Node{}
		for _, prop := range info.properties {
			name := tracker.NewIdentifier(ast.SymbolName(prop))
			node := tracker.NewJsxAttribute(name, tracker.NewJsxExpression(nil, builder.value(ch.GetTypeOfSymbol(prop), info.owner)))
			name.Parent = node
			added = append(added, node)
		}
		if slices.ContainsFunc(old, ast.IsJsxSpreadAttribute) {
			added = append(added, old...)
		} else {
			added = append(slices.Clone(old), added...)
		}
		options := &change.NodeOptions{}
		if attrs.Pos() == attrs.End() {
			options.Prefix = " "
		}
		tracker.ReplaceNode(info.file, attrs, tracker.NewJsxAttributes(tracker.NewNodeList(added)), options)
	}
	if builder.err != nil {
		return builder.err
	}
	if adder != nil {
		for _, edit := range adder.Edits() {
			tracker.ReplaceRangeWithText(request.SourceFile, edit.Range, edit.NewText)
		}
	}
	return ctx.Err()
}
func referenceOtherMissingMemberAction(ctx context.Context, request *CodeFixContext, ch *checker.Checker, info *referenceOtherMissingMember) ([]*CodeAction, error) {
	tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
	if err := referenceApplyOtherMissingMember(ctx, request, ch, tracker, info); err != nil {
		return nil, err
	}
	var title, all *diagnostics.Message
	var fixID string
	switch info.kind {
	case "object":
		fixID = "fixMissingProperties"
		title = diagnostics.Add_missing_properties
		all = diagnostics.Add_all_missing_properties
	case "jsx":
		fixID = "fixMissingAttributes"
		title = diagnostics.Add_missing_attributes
		all = diagnostics.Add_all_missing_attributes
	default:
		fixID = "fixMissingFunctionDeclaration"
		title = diagnostics.Add_missing_function_declaration_0
		all = diagnostics.Add_all_missing_function_declarations
	}
	description := title.Localize(locale.FromContext(ctx))
	if info.kind == "function" || info.kind == "signature" {
		description = title.Localize(locale.FromContext(ctx), info.token.Text())
	}
	return []*CodeAction{{ReferenceFixName: fixID, Description: description, ReferenceChanges: referenceTrackerFileChanges(tracker), FixID: fixID, FixAllDescription: all.Localize(locale.FromContext(ctx))}}, ctx.Err()
}

func referenceTypeOnlyImportDeclaration(request *CodeFixContext) *ast.Node {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil || token.Parent == nil {
		return nil
	}
	parent := token.Parent
	if ast.IsImportSpecifier(parent) || (ast.IsImportDeclaration(parent) && parent.AsImportDeclaration().ImportClause != nil) {
		return parent
	}
	return nil
}
func referenceCanMakeWholeTypeImport(ctx context.Context, file *ast.SourceFile, ch *checker.Checker, specifier *ast.Node) bool {
	clause := specifier.Parent.Parent.AsImportClause()
	if clause.Name() != nil {
		return false
	}
	specifiers := []*ast.Node{}
	for _, element := range specifier.Parent.Elements() {
		if !element.AsImportSpecifier().IsTypeOnly {
			specifiers = append(specifiers, element)
		}
	}
	if len(specifiers) == 1 {
		return true
	}
	for _, element := range specifiers {
		definition := element.Name()
		symbol := ch.GetSymbolAtLocation(definition)
		if symbol == nil {
			continue
		}
		var visit func(*ast.Node) bool
		visit = func(node *ast.Node) bool {
			if ctx.Err() != nil {
				return true
			}
			if ast.IsIdentifier(node) && node != definition && node.Text() == definition.Text() {
				ref := ch.GetSymbolAtLocation(node)
				matches := ref == symbol
				if ast.IsShorthandPropertyAssignment(node.Parent) {
					matches = matches || ch.GetShorthandAssignmentValueSymbol(node.Parent) == symbol
				}
				if ast.IsExportSpecifier(node.Parent) {
					matches = matches || ch.GetExportSpecifierLocalTargetSymbol(node.Parent) == symbol
				}
				if matches && ((ref != nil && ch.IsReferenceSymbolValue(ref)) || !ast.IsValidTypeOnlyAliasUseSite(node)) {
					return true
				}
			}
			return node.ForEachChild(visit)
		}
		if visit(file.AsNode()) {
			return false
		}
	}
	return true
}
func referenceConvertTypeOnlyImport(request *CodeFixContext, tracker *change.Tracker, node *ast.Node) {
	file := request.SourceFile
	if ast.IsImportSpecifier(node) {
		specifier := node.AsImportSpecifier()
		tracker.ReplaceNode(file, node, tracker.UpdateImportSpecifier(specifier, true, specifier.PropertyName, specifier.Name()), nil)
		return
	}
	declaration := node.AsImportDeclaration()
	clause := declaration.ImportClause.AsImportClause()
	if clause.Name() != nil && clause.NamedBindings != nil {
		first := tracker.DeepCloneReparse(node).AsImportDeclaration()
		second := tracker.DeepCloneReparse(node).AsImportDeclaration()
		a := tracker.NewImportDeclaration(first.Modifiers(), tracker.NewImportClause(ast.KindTypeKeyword, first.ImportClause.Name(), nil), first.ModuleSpecifier, first.Attributes)
		b := tracker.NewImportDeclaration(second.Modifiers(), tracker.NewImportClause(ast.KindTypeKeyword, nil, second.ImportClause.AsImportClause().NamedBindings), second.ModuleSpecifier, second.Attributes)
		tracker.ReplaceNodeWithNodes(file, node, []*ast.Node{a, b}, nil)
		return
	}
	named := clause.NamedBindings
	if named != nil && ast.IsNamedImports(named) {
		elements := []*ast.Node{}
		for _, element := range named.Elements() {
			specifier := element.AsImportSpecifier()
			elements = append(elements, tracker.UpdateImportSpecifier(specifier, false, specifier.PropertyName, specifier.Name()))
		}
		named = tracker.UpdateNamedImports(named.AsNamedImports(), tracker.NewNodeList(elements))
	}
	updated := tracker.UpdateImportDeclaration(declaration, declaration.Modifiers(), tracker.UpdateImportClause(clause, ast.KindTypeKeyword, clause.Name(), named), declaration.ModuleSpecifier, declaration.Attributes)
	tracker.ReplaceNode(file, node, updated, nil)
}

var referenceTypeOnlyImportCodes = []int32{diagnostics.X_0_is_a_type_and_must_be_imported_using_a_type_only_import_when_verbatimModuleSyntax_is_enabled.Code(), diagnostics.X_0_resolves_to_a_type_only_declaration_and_must_be_imported_using_a_type_only_import_when_verbatimModuleSyntax_is_enabled.Code()}
var ReferenceTypeOnlyImportProvider = &CodeFixProvider{
	Name: "convertToTypeOnlyImport", ErrorCodes: referenceTypeOnlyImportCodes, FixIds: []string{"convertToTypeOnlyImport"},
	GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		node := referenceTypeOnlyImportDeclaration(request)
		if node == nil {
			return nil, nil
		}
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		actions := []*CodeAction{}
		add := func(node *ast.Node, all bool, title string) {
			tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
			referenceConvertTypeOnlyImport(request, tracker, node)
			action := &CodeAction{Description: title, ReferenceChanges: referenceTrackerFileChanges(tracker)}
			if all {
				action.FixID = "convertToTypeOnlyImport"
				action.FixAllDescription = diagnostics.Fix_all_with_type_only_imports.Localize(locale.FromContext(ctx))
			}
			actions = append(actions, action)
		}
		title := diagnostics.Use_import_type.Localize(locale.FromContext(ctx))
		if ast.IsImportSpecifier(node) {
			if ast.IsImportDeclaration(node.Parent.Parent.Parent) && referenceCanMakeWholeTypeImport(ctx, request.SourceFile, ch, node) {
				add(node.Parent.Parent.Parent, false, title)
			}
			specifier := node.AsImportSpecifier()
			name := specifier.Name()
			if specifier.PropertyName != nil {
				name = specifier.PropertyName
			}
			title = diagnostics.Use_type_0.Localize(locale.FromContext(ctx), name.Text())
		}
		add(node, true, title)
		return actions, ctx.Err()
	},
	GetAllCodeActions: func(ctx context.Context, request *CodeFixContext) (*CombinedCodeActions, error) {
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		seen := map[*ast.Node]bool{}
		for _, diagnostic := range getAllDiagnostics(ctx, request.Program, request.SourceFile) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if !slices.Contains(referenceTypeOnlyImportCodes, diagnostic.Code()) {
				continue
			}
			item := *request
			item.Span = diagnostic.Loc()
			node := referenceTypeOnlyImportDeclaration(&item)
			if node == nil {
				continue
			}
			if ast.IsImportDeclaration(node) {
				if !seen[node] {
					referenceConvertTypeOnlyImport(request, tracker, node)
					seen[node] = true
				}
			} else {
				declaration := node.Parent.Parent.Parent
				if ast.IsImportDeclaration(declaration) && !seen[declaration] && referenceCanMakeWholeTypeImport(ctx, request.SourceFile, ch, node) {
					referenceConvertTypeOnlyImport(request, tracker, declaration)
					seen[declaration] = true
				} else {
					referenceConvertTypeOnlyImport(request, tracker, node)
				}
			}
		}
		return &CombinedCodeActions{ReferenceChanges: referenceTrackerFileChanges(tracker)}, ctx.Err()
	},
}

func referenceFixableErrorSpanExpression(file *ast.SourceFile, span core.TextRange) *ast.Node {
	for node := astnav.GetTokenAtPosition(file, span.Pos()); node != nil; node = node.Parent {
		start := scanner.GetTokenPosOfNode(node, file, false)
		if start < span.Pos() || node.End() > span.End() {
			return nil
		}
		if start == span.Pos() && node.End() == span.End() && ast.IsExpression(node) {
			return node
		}
	}
	return nil
}

type referenceOptionalAssignment struct{ source, target *ast.Node }

func referenceOptionalSourceTarget(node *ast.Node, ch *checker.Checker) *referenceOptionalAssignment {
	if node == nil || node.Parent == nil {
		return nil
	}
	parent := node.Parent
	switch {
	case ast.IsBinaryExpression(parent) && parent.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken:
		return &referenceOptionalAssignment{parent.AsBinaryExpression().Right, parent.AsBinaryExpression().Left}
	case ast.IsVariableDeclaration(parent) && parent.Initializer() != nil:
		return &referenceOptionalAssignment{parent.Initializer(), parent.Name()}
	case ast.IsCallExpression(parent):
		symbol := ch.GetSymbolAtLocation(parent.Expression())
		if symbol == nil || symbol.ValueDeclaration == nil || !ast.IsFunctionLike(symbol.ValueDeclaration) {
			return nil
		}
		index := slices.Index(parent.Arguments(), node)
		if index < 0 || !ast.IsExpression(node) || index >= len(symbol.ValueDeclaration.Parameters()) {
			return nil
		}
		name := symbol.ValueDeclaration.Parameters()[index].Name()
		if ast.IsIdentifier(name) {
			return &referenceOptionalAssignment{node, name}
		}
	case (ast.IsPropertyAssignment(parent) && ast.IsIdentifier(parent.Name())) || ast.IsShorthandPropertyAssignment(parent):
		assignment := referenceOptionalSourceTarget(parent.Parent, ch)
		if assignment == nil {
			return nil
		}
		prop := ch.GetPropertyOfType(ch.GetTypeAtLocation(assignment.target), parent.Name().Text())
		if prop == nil || len(prop.Declarations) == 0 {
			return nil
		}
		source := parent.Name()
		if ast.IsPropertyAssignment(parent) {
			source = parent.Initializer()
		}
		return &referenceOptionalAssignment{source, prop.Declarations[0]}
	}
	return nil
}

var referenceOptionalPropertyUndefinedCodes = []int32{
	diagnostics.Type_0_is_not_assignable_to_type_1_with_exactOptionalPropertyTypes_Colon_true_Consider_adding_undefined_to_the_type_of_the_target.Code(),
	diagnostics.Type_0_is_not_assignable_to_type_1_with_exactOptionalPropertyTypes_Colon_true_Consider_adding_undefined_to_the_types_of_the_target_s_properties.Code(),
	diagnostics.Argument_of_type_0_is_not_assignable_to_parameter_of_type_1_with_exactOptionalPropertyTypes_Colon_true_Consider_adding_undefined_to_the_types_of_the_target_s_properties.Code(),
}

func referenceOptionalPropertyUndefined(ctx context.Context, request *CodeFixContext, tracker *change.Tracker) (string, error) {
	ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
	defer done()
	assignment := referenceOptionalSourceTarget(referenceFixableErrorSpanExpression(request.SourceFile, request.Span), ch)
	if assignment == nil {
		return "", nil
	}
	target := ch.GetTypeAtLocation(assignment.target)
	if ast.IsPropertyAccessExpression(assignment.target) && len(ch.GetReferenceExactOptionalProperties(ch.GetTypeAtLocation(assignment.target.Expression()))) > 0 && ch.GetTypeAtLocation(assignment.source) == ch.GetUndefinedType() {
		target = ch.GetTypeAtLocation(assignment.target.Expression())
	}
	if target.Symbol() != nil && slices.ContainsFunc(target.Symbol().Declarations, func(node *ast.Node) bool { return strings.HasSuffix(ast.GetSourceFileOfNode(node).FileName(), ".d.ts") }) {
		return "", nil
	}
	properties := ch.GetReferenceExactOptionalProperties(target)
	if len(properties) == 0 {
		return "", nil
	}
	for _, prop := range properties {
		decl := prop.ValueDeclaration
		if decl == nil || !(ast.IsPropertySignatureDeclaration(decl) || ast.IsPropertyDeclaration(decl)) || decl.Type() == nil {
			continue
		}
		types := []*ast.Node{decl.Type()}
		if ast.IsUnionTypeNode(decl.Type()) {
			types = slices.Clone(decl.Type().AsUnionTypeNode().Types.Nodes)
		}
		types = append(types, tracker.NewTypeReferenceNode(tracker.NewIdentifier("undefined"), nil))
		tracker.ReplaceNode(ast.GetSourceFileOfNode(decl), decl.Type(), tracker.NewUnionTypeNode(tracker.NewNodeList(types)), nil)
	}
	return diagnostics.Add_undefined_to_optional_property_type.Localize(locale.FromContext(ctx)), ctx.Err()
}

var ReferenceOptionalPropertyUndefinedProvider = &CodeFixProvider{Name: "addOptionalPropertyUndefined", ErrorCodes: referenceOptionalPropertyUndefinedCodes, GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
	tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
	title, err := referenceOptionalPropertyUndefined(ctx, request, tracker)
	if err != nil {
		return nil, err
	}
	if title == "" {
		return nil, nil
	}
	return []*CodeAction{{Description: title, ReferenceChanges: referenceTrackerFileChanges(tracker)}}, ctx.Err()
}}

var ReferenceResolutionModeAttributeProvider = newReferenceSyntaxFixProvider("addMissingResolutionModeImportAttribute", []int32{
	diagnostics.Type_only_import_of_an_ECMAScript_module_from_a_CommonJS_module_must_have_a_resolution_mode_attribute.Code(),
	diagnostics.Type_import_of_an_ECMAScript_module_from_a_CommonJS_module_must_have_a_resolution_mode_attribute.Code(),
}, diagnostics.Add_resolution_mode_import_attribute_to_all_type_only_imports_that_need_it, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
	node := ast.FindAncestor(astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos()), func(node *ast.Node) bool { return ast.IsImportDeclaration(node) || ast.IsImportTypeNode(node) })
	if node == nil {
		return "", nil
	}
	mode := "import"
	if specifier := checker.TryGetModuleSpecifierFromDeclaration(node); specifier != nil {
		imported := request.Program.ResolveModuleName(specifier.Text(), request.SourceFile.FileName(), core.ResolutionModeESM)
		original := request.Program.GetResolvedModuleFromModuleSpecifier(request.SourceFile, specifier)
		if (imported == nil) != (original == nil) || (imported != nil && original != nil && imported.ResolvedFileName != original.ResolvedFileName) {
			mode = "require"
		}
	}
	flags := ast.TokenFlagsNone
	if lsutil.GetQuotePreference(request.SourceFile, request.LS.UserPreferences()) == lsutil.QuotePreferenceSingle {
		flags = ast.TokenFlagsSingleQuote
	}
	attribute := tracker.NewImportAttribute(tracker.NewStringLiteral("resolution-mode", flags), tracker.NewStringLiteral(mode, flags))
	var attrs, old *ast.Node
	if ast.IsImportDeclaration(node) {
		old = node.AsImportDeclaration().Attributes
	} else {
		old = node.AsImportTypeNode().Attributes
	}
	if old != nil {
		original := old.AsImportAttributes()
		elements := tracker.NewNodeList(append(slices.Clone(original.Attributes.Nodes), attribute))
		if original.Attributes.HasTrailingComma() {
			elements.Loc = original.Attributes.Loc
		}
		attrs = tracker.UpdateImportAttributes(original, original.Token, elements, original.MultiLine)
	} else {
		attrs = tracker.NewImportAttributes(ast.KindWithKeyword, tracker.NewNodeList([]*ast.Node{attribute}), false)
	}
	var replacement *ast.Node
	if ast.IsImportDeclaration(node) {
		decl := node.AsImportDeclaration()
		replacement = tracker.UpdateImportDeclaration(decl, decl.Modifiers(), decl.ImportClause, decl.ModuleSpecifier, attrs)
	} else {
		decl := node.AsImportTypeNode()
		replacement = tracker.UpdateImportTypeNode(decl, decl.IsTypeOf, decl.Argument, attrs, decl.Qualifier, decl.TypeArguments)
	}
	tracker.ReplaceNode(request.SourceFile, node, replacement, nil)
	return diagnostics.Add_resolution_mode_import_attribute.Localize(locale.FromContext(ctx)), ctx.Err()
})

func referenceAllowsSyntheticDefaults(options *core.CompilerOptions) bool {
	return options.AllowSyntheticDefaultImports.IsTrueOrUnknown()
}
func referenceRequireImportDiagnostics(file *ast.SourceFile, items []*ast.Diagnostic) []*ast.Diagnostic {
	if ast.IsSourceFileJS(file) {
		return items
	}
	for _, statement := range file.Statements.Nodes {
		if !ast.IsVariableStatement(statement) {
			continue
		}
		list := statement.AsVariableStatement().DeclarationList
		if list.Flags&ast.NodeFlagsConst == 0 || len(list.AsVariableDeclarationList().Declarations.Nodes) != 1 {
			continue
		}
		declaration := list.AsVariableDeclarationList().Declarations.Nodes[0]
		if initializer := declaration.Initializer(); initializer != nil && ast.IsRequireCall(initializer, true) {
			span := core.NewTextRange(scanner.GetTokenPosOfNode(initializer, file, false), initializer.End())
			found := false
			for _, old := range items {
				if old.Code() == diagnostics.X_require_call_may_be_converted_to_an_import.Code() && old.Loc() == span {
					found = true
					break
				}
			}
			if !found {
				diagnostic := ast.NewDiagnostic(file, span, diagnostics.X_require_call_may_be_converted_to_an_import)
				diagnostic.SetCategory(diagnostics.CategorySuggestion)
				items = append(items, diagnostic)
			}
		}
	}
	return items
}

var ReferenceRequireImportProvider = newReferenceSyntaxFixProvider("requireInTs", []int32{diagnostics.X_require_call_may_be_converted_to_an_import.Code()}, diagnostics.Convert_all_require_to_import, func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil || token.Parent == nil || !ast.IsRequireCall(token.Parent, true) {
		return "", nil
	}
	call := token.Parent
	if !ast.IsVariableDeclaration(call.Parent) {
		return "", nil
	}
	declaration := call.Parent
	if declaration.Parent == nil || declaration.Parent.Parent == nil || !ast.IsVariableStatement(declaration.Parent.Parent) {
		return "", nil
	}
	var name, named *ast.Node
	if ast.IsIdentifier(declaration.Name()) {
		name = declaration.Name()
	}
	if ast.IsObjectBindingPattern(declaration.Name()) {
		specifiers := []*ast.Node{}
		for _, element := range declaration.Name().Elements() {
			binding := element.AsBindingElement()
			if !ast.IsIdentifier(binding.Name()) || binding.Initializer != nil {
				return "", nil
			}
			var original *ast.Node
			if binding.PropertyName != nil && ast.IsIdentifier(binding.PropertyName) {
				original = binding.PropertyName
			}
			specifiers = append(specifiers, tracker.NewImportSpecifier(false, original, binding.Name()))
		}
		if len(specifiers) > 0 {
			named = tracker.NewNamedImports(tracker.NewNodeList(specifiers))
		}
	}
	if name == nil && named == nil {
		return "", nil
	}
	specifier := call.Arguments()[0]
	if ast.IsNoSubstitutionTemplateLiteral(specifier) {
		flags := ast.TokenFlagsNone
		if lsutil.GetQuotePreference(request.SourceFile, request.LS.UserPreferences()) == lsutil.QuotePreferenceSingle {
			flags = ast.TokenFlagsSingleQuote
		}
		specifier = tracker.NewStringLiteral(specifier.Text(), flags)
	}
	var replacement *ast.Node
	if name != nil && !referenceAllowsSyntheticDefaults(request.Program.Options()) {
		replacement = tracker.NewImportEqualsDeclaration(nil, false, name, tracker.NewExternalModuleReference(specifier))
	} else {
		replacement = tracker.NewImportDeclaration(nil, tracker.NewImportClause(ast.KindUnknown, name, named), specifier, nil)
	}
	tracker.ReplaceNode(request.SourceFile, declaration.Parent.Parent, replacement, nil)
	return diagnostics.Convert_require_to_import.Localize(locale.FromContext(ctx)), ctx.Err()
})

func referenceInvalidImportActions(ctx context.Context, request *CodeFixContext, usage bool) ([]*CodeAction, error) {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil {
		return nil, nil
	}
	var expression *ast.Node
	if usage {
		kind := ast.KindNewExpression
		if request.ErrorCode == diagnostics.This_expression_is_not_callable.Code() {
			kind = ast.KindCallExpression
		}
		owner := ast.FindAncestor(token, func(node *ast.Node) bool { return node.Kind == kind })
		if owner == nil {
			return []*CodeAction{}, nil
		}
		expression = owner.Expression()
	} else {
		expression = referenceAncestorMatchingSpan(request.SourceFile, request.Span)
		if expression == nil {
			return []*CodeAction{}, nil
		}
	}
	ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
	defer done()
	symbol := ch.GetTypeAtLocation(expression).Symbol()
	if symbol == nil || symbol.Flags&ast.SymbolFlagsTransient == 0 {
		return []*CodeAction{}, nil
	}
	related := ch.GetReferenceOriginatingImport(symbol)
	if related == nil {
		return []*CodeAction{}, nil
	}
	actions := []*CodeAction{}
	replacement := func(file *ast.SourceFile, old *ast.Node, build func(*change.Tracker) *ast.Node) {
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		tracker.ReplaceNode(file, old, build(tracker), nil)
		files := referenceTrackerFileChanges(tracker)
		if len(files) > 0 && len(files[0].TextChanges) > 0 {
			actions = append(actions, &CodeAction{Description: diagnostics.Replace_import_with_0.Localize(locale.FromContext(ctx), files[0].TextChanges[0].NewText), ReferenceChanges: files})
		}
	}
	if !ast.IsImportCall(related) {
		if !ast.IsImportDeclaration(related) {
			return actions, ctx.Err()
		}
		namespace := ast.GetNamespaceDeclarationNode(related)
		if namespace == nil {
			return actions, ctx.Err()
		}
		file := ast.GetSourceFileOfNode(related)
		if file == nil {
			return actions, ctx.Err()
		}
		replacement(file, related, func(t *change.Tracker) *ast.Node {
			return t.NewImportDeclaration(nil, t.NewImportClause(ast.KindUnknown, namespace.Name(), nil), related.AsImportDeclaration().ModuleSpecifier, nil)
		})
		if request.Program.Options().GetEmitModuleKind() == core.ModuleKindCommonJS {
			replacement(file, related, func(t *change.Tracker) *ast.Node {
				return t.NewImportEqualsDeclaration(nil, false, namespace.Name(), t.NewExternalModuleReference(related.AsImportDeclaration().ModuleSpecifier))
			})
		}
	}
	if ast.IsExpression(expression) && (expression.Parent == nil || expression.Parent.Name() != expression) {
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		node := tracker.NewPropertyAccessExpression(expression, nil, tracker.NewIdentifier("default"), ast.NodeFlagsNone)
		tracker.ReplaceNode(request.SourceFile, expression, node, &change.NodeOptions{})
		actions = append(actions, &CodeAction{Description: diagnostics.Use_synthetic_default_member.Localize(locale.FromContext(ctx)), ReferenceChanges: referenceTrackerFileChanges(tracker)})
	}
	return actions, ctx.Err()
}

var ReferenceInvalidImportUsageProvider = &CodeFixProvider{Name: "invalidImportSyntax", ErrorCodes: []int32{
	diagnostics.This_expression_is_not_callable.Code(), diagnostics.This_expression_is_not_constructable.Code(),
}, GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
	return referenceInvalidImportActions(ctx, request, true)
}}
var ReferenceInvalidImportLocationProvider = &CodeFixProvider{Name: "invalidImportSyntax", ErrorCodes: []int32{
	diagnostics.Argument_of_type_0_is_not_assignable_to_parameter_of_type_1.Code(),
	diagnostics.Type_0_does_not_satisfy_the_constraint_1.Code(),
	diagnostics.Type_0_is_not_assignable_to_type_1.Code(),
	diagnostics.Type_0_is_not_assignable_to_type_1_Two_different_types_with_this_name_exist_but_they_are_unrelated.Code(),
	diagnostics.Type_predicate_0_is_not_assignable_to_1.Code(),
	diagnostics.Property_0_of_type_1_is_not_assignable_to_2_index_type_3.Code(),
	diagnostics.X_0_index_type_1_is_not_assignable_to_2_index_type_3.Code(),
	diagnostics.Property_0_in_type_1_is_not_assignable_to_the_same_property_in_base_type_2.Code(),
	diagnostics.Property_0_in_type_1_is_not_assignable_to_type_2.Code(),
	diagnostics.Property_0_of_JSX_spread_attribute_is_not_assignable_to_target_property.Code(),
	diagnostics.The_this_context_of_type_0_is_not_assignable_to_method_s_this_of_type_1.Code(),
}, GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
	return referenceInvalidImportActions(ctx, request, false)
}}

type referenceLocalExportName struct {
	token    *ast.Node
	typeOnly bool
}
type referenceLocalExportInfo struct {
	exportName referenceLocalExportName
	node       *ast.Node
	file       *ast.SourceFile
	module     string
}

func referenceLocalExportInfoAt(request *CodeFixContext) *referenceLocalExportInfo {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil || !ast.IsIdentifier(token) {
		return nil
	}
	declaration := ast.FindAncestor(token, ast.IsImportDeclaration)
	if declaration == nil {
		return nil
	}
	specifier := declaration.AsImportDeclaration().ModuleSpecifier
	if specifier == nil || !ast.IsStringLiteral(specifier) {
		return nil
	}
	resolved := request.Program.GetResolvedModuleFromModuleSpecifier(request.SourceFile, specifier)
	if resolved == nil {
		return nil
	}
	file := request.Program.GetSourceFile(resolved.ResolvedFileName)
	if file == nil || request.Program.IsSourceFileDefaultLibrary(file.Path()) || request.Program.IsSourceFileFromExternalLibrary(file) {
		return nil
	}
	module := file.AsNode().Symbol()
	if module == nil || module.ValueDeclaration == nil {
		return nil
	}
	symbol := module.ValueDeclaration.Locals()[token.Text()]
	if symbol == nil {
		return nil
	}
	node := symbol.ValueDeclaration
	if node == nil {
		if len(symbol.Declarations) == 0 {
			return nil
		}
		node = symbol.Declarations[0]
	} else if ast.IsVariableDeclaration(node) && node.Parent != nil && node.Parent.Parent != nil && ast.IsVariableStatement(node.Parent.Parent) && len(node.Parent.AsVariableDeclarationList().Declarations.Nodes) == 1 {
		node = node.Parent.Parent
	}
	return &referenceLocalExportInfo{exportName: referenceLocalExportName{token: token, typeOnly: ast.IsTypeDeclaration(node)}, node: node, file: file, module: specifier.Text()}
}
func referenceExistingLocalExport(file *ast.SourceFile, typeOnly bool) *ast.Node {
	for index := len(file.Statements.Nodes) - 1; index >= 0; index-- {
		node := file.Statements.Nodes[index]
		if ast.IsExportDeclaration(node) && (typeOnly && node.AsExportDeclaration().IsTypeOnly || !node.AsExportDeclaration().IsTypeOnly) {
			return node
		}
	}
	return nil
}
func referenceCanExportModifier(node *ast.Node) bool {
	return ast.IsEnumDeclaration(node) || ast.IsVariableStatement(node) || ast.IsFunctionDeclaration(node) || ast.IsClassDeclaration(node) || ast.IsInterfaceDeclaration(node) || ast.IsTypeDeclaration(node) || (ast.IsModuleDeclaration(node) && !ast.IsExternalModuleAugmentation(node) && !ast.IsGlobalScopeAugmentation(node))
}
func referenceInsertLocalExportModifier(request *CodeFixContext, tracker *change.Tracker, file *ast.SourceFile, node *ast.Node) {
	tracker.InsertText(file, request.LS.converters.PositionToLineAndCharacter(file, core.TextPos(scanner.GetTokenPosOfNode(node, file, false))), "export ")
}
func referenceChangeLocalExports(request *CodeFixContext, tracker *change.Tracker, file *ast.SourceFile, names []referenceLocalExportName, existing *ast.Node) {
	if len(names) == 0 {
		return
	}
	allowType := request.Program.Options().GetIsolatedModules()
	elements := []*ast.Node{}
	if existing != nil {
		declaration := existing.AsExportDeclaration()
		if declaration.ExportClause != nil && ast.IsNamedExports(declaration.ExportClause) {
			elements = slices.Clone(declaration.ExportClause.Elements())
		}
		allowType = !declaration.IsTypeOnly && (allowType || slices.ContainsFunc(elements, func(node *ast.Node) bool { return node.AsExportSpecifier().IsTypeOnly }))
	}
	for _, name := range names {
		elements = append(elements, tracker.NewExportSpecifier(allowType && name.typeOnly, nil, tracker.NewIdentifier(name.token.Text())))
	}
	list := tracker.NewNodeList(elements)
	if existing != nil {
		declaration := existing.AsExportDeclaration()
		if declaration.ExportClause != nil && ast.IsNamedExports(declaration.ExportClause) && declaration.ExportClause.ElementList().HasTrailingComma() {
			list.Loc = declaration.ExportClause.ElementList().Loc
		}
		updated := tracker.UpdateExportDeclaration(declaration, declaration.Modifiers(), declaration.IsTypeOnly, tracker.NewNamedExports(list), declaration.ModuleSpecifier, declaration.Attributes)
		tracker.ReplaceNode(file, existing, updated, nil)
	} else {
		tracker.InsertReferenceStatementAtEnd(file, tracker.NewExportDeclaration(nil, false, tracker.NewNamedExports(list), nil, nil))
	}
}

var ReferenceLocalExportProvider = &CodeFixProvider{Name: "fixImportNonExportedMember", ErrorCodes: []int32{diagnostics.Module_0_declares_1_locally_but_it_is_not_exported.Code()}, FixIds: []string{"fixImportNonExportedMember"},
	GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		_ = ch
		info := referenceLocalExportInfoAt(request)
		if info == nil {
			return nil, nil
		}
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		if existing := referenceExistingLocalExport(info.file, info.exportName.typeOnly); existing != nil {
			referenceChangeLocalExports(request, tracker, info.file, []referenceLocalExportName{info.exportName}, existing)
		} else if referenceCanExportModifier(info.node) {
			referenceInsertLocalExportModifier(request, tracker, info.file, info.node)
		} else {
			referenceChangeLocalExports(request, tracker, info.file, []referenceLocalExportName{info.exportName}, nil)
		}
		return []*CodeAction{{Description: diagnostics.Export_0_from_module_1.Localize(locale.FromContext(ctx), info.exportName.token.Text(), info.module), ReferenceChanges: referenceTrackerFileChanges(tracker), FixID: "fixImportNonExportedMember", FixAllDescription: diagnostics.Export_all_referenced_locals.Localize(locale.FromContext(ctx))}}, ctx.Err()
	},
	GetAllCodeActions: func(ctx context.Context, request *CodeFixContext) (*CombinedCodeActions, error) {
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		_ = ch
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		type moduleExports struct{ types, values []referenceLocalExportName }
		files := []*ast.SourceFile{}
		groups := map[*ast.SourceFile]*moduleExports{}
		for _, diagnostic := range getAllDiagnostics(ctx, request.Program, request.SourceFile) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if diagnostic.Code() != diagnostics.Module_0_declares_1_locally_but_it_is_not_exported.Code() {
				continue
			}
			item := *request
			item.Span = diagnostic.Loc()
			info := referenceLocalExportInfoAt(&item)
			if info == nil {
				continue
			}
			if referenceExistingLocalExport(info.file, info.exportName.typeOnly) == nil && referenceCanExportModifier(info.node) {
				referenceInsertLocalExportModifier(request, tracker, info.file, info.node)
			} else {
				group := groups[info.file]
				if group == nil {
					group = &moduleExports{}
					groups[info.file] = group
					files = append(files, info.file)
				}
				if info.exportName.typeOnly {
					group.types = append(group.types, info.exportName)
				} else {
					group.values = append(group.values, info.exportName)
				}
			}
		}
		for _, file := range files {
			group := groups[file]
			existing := referenceExistingLocalExport(file, true)
			if existing != nil && existing.AsExportDeclaration().IsTypeOnly {
				referenceChangeLocalExports(request, tracker, file, group.types, existing)
				referenceChangeLocalExports(request, tracker, file, group.values, referenceExistingLocalExport(file, false))
			} else {
				referenceChangeLocalExports(request, tracker, file, append(slices.Clone(group.values), group.types...), existing)
			}
		}
		return &CombinedCodeActions{ReferenceChanges: referenceTrackerFileChanges(tracker)}, ctx.Err()
	},
}

type referenceMissingParameter struct {
	pos  int
	name string
	typ  *checker.Type
}
type referenceMissingParametersInfo struct {
	name               string
	implementation     *ast.Node
	declarations       []*ast.Node
	required, optional []referenceMissingParameter
}

func referenceConvertibleParameterOwner(node *ast.Node) bool {
	return ast.IsFunctionDeclaration(node) || ast.IsFunctionExpression(node) || ast.IsMethodDeclaration(node) || ast.IsArrowFunction(node)
}
func referenceMissingParametersInfoAt(ctx context.Context, request *CodeFixContext, ch *checker.Checker) *referenceMissingParametersInfo {
	call := ast.FindAncestor(astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos()), ast.IsCallExpression)
	if call == nil || len(call.Arguments()) == 0 {
		return nil
	}
	symbol := ch.GetTypeAtLocation(call.Expression()).Symbol()
	if symbol == nil {
		return nil
	}
	declarations := []*ast.Node{}
	for _, node := range symbol.Declarations {
		if referenceConvertibleParameterOwner(node) {
			declarations = append(declarations, node)
		}
	}
	if len(declarations) == 0 {
		return nil
	}
	implementation := declarations[len(declarations)-1]
	file := ast.GetSourceFileOfNode(implementation)
	if implementation.Body() == nil || file == nil || request.Program.IsSourceFileDefaultLibrary(file.Path()) || request.Program.IsSourceFileFromExternalLibrary(file) {
		return nil
	}
	name := implementation.Name()
	if name == nil && implementation.Parent != nil {
		parent := implementation.Parent
		if (ast.IsVariableDeclaration(parent) && parent.Name() != nil && ast.IsIdentifier(parent.Name())) || ast.IsPropertyDeclaration(parent) || ast.IsParameterDeclaration(parent) {
			name = parent.Name()
		}
	}
	if name == nil {
		return nil
	}
	parameters := implementation.Parameters()
	arguments := call.Arguments()
	if len(parameters) > len(arguments) {
		return nil
	}
	overloads := []*ast.Node{}
	for _, node := range declarations {
		if node.Body() == nil {
			if len(node.Parameters()) > len(parameters) {
				overloads = nil
				break
			}
			if len(node.Parameters()) == len(parameters) {
				overloads = append(overloads, node)
			}
		}
	}
	info := &referenceMissingParametersInfo{name: scanner.DeclarationNameToString(name), implementation: implementation, declarations: append([]*ast.Node{implementation}, overloads...)}
	pos, counter := 0, 0
	for index, argument := range arguments {
		if ctx.Err() != nil {
			return nil
		}
		expression := argument
		if ast.IsPropertyAccessExpression(argument) {
			expression = argument.Name()
		} else if ast.IsElementAccessExpression(argument) {
			expression = argument.AsElementAccessExpression().ArgumentExpression
		}
		typ := ch.GetWidenedType(ch.GetBaseTypeOfLiteralType(ch.GetTypeAtLocation(argument)))
		if pos < len(parameters) && ch.IsTypeAssignableTo(typ, ch.GetTypeAtLocation(parameters[pos])) {
			pos++
			continue
		}
		name := ""
		if expression != nil && ast.IsIdentifier(expression) {
			name = expression.Text()
		} else {
			name = "p" + strconv.Itoa(counter)
			counter++
		}
		parameter := referenceMissingParameter{pos: index, name: name, typ: typ}
		info.required = append(info.required, parameter)
		blocked := slices.ContainsFunc(info.declarations, func(node *ast.Node) bool {
			return pos < len(node.Parameters()) && node.Parameters()[pos].AsParameterDeclaration().QuestionToken == nil
		})
		if !blocked {
			info.optional = append(info.optional, parameter)
		}
	}
	return info
}
func referenceApplyMissingParameters(ctx context.Context, request *CodeFixContext, ch *checker.Checker, tracker *change.Tracker, info *referenceMissingParametersInfo, optional bool) error {
	additions := info.required
	if optional {
		additions = info.optional
	}
	for _, declaration := range info.declarations {
		if err := ctx.Err(); err != nil {
			return err
		}
		file := ast.GetSourceFileOfNode(declaration)
		target := *request
		target.SourceFile = file
		adder, err := createImportAdder(ctx, &target, ch)
		if err != nil {
			return err
		}
		parameters := []*ast.Node{}
		for _, node := range declaration.Parameters() {
			p := node.AsParameterDeclaration()
			parameters = append(parameters, tracker.NewParameterDeclaration(p.Modifiers(), p.DotDotDotToken, p.Name(), p.QuestionToken, p.Type, p.Initializer))
		}
		for _, addition := range additions {
			symbols := map[*ast.Node]*ast.Symbol{}
			builder := checker.NewNodeBuilderEx(ch, tracker.EmitContext, symbols)
			typ := builder.TypeToTypeNode(ch.GetWidenedType(addition.typ), info.implementation, nodebuilder.FlagsNoTruncation, nodebuilder.InternalFlagsAllowUnresolvedNames, nil)
			if typ == nil {
				typ = tracker.NewKeywordTypeNode(ast.KindUnknownKeyword)
			}
			typ = autoimport.TypeNodeToAutoImportableTypeNode(typ, adder, symbols)
			var question *ast.Node
			if optional || (addition.pos > 0 && addition.pos <= len(parameters) && parameters[addition.pos-1].AsParameterDeclaration().QuestionToken != nil) {
				question = tracker.NewToken(ast.KindQuestionToken)
			}
			parameter := tracker.NewParameterDeclaration(nil, nil, tracker.NewIdentifier(addition.name), question, typ, nil)
			pos := min(addition.pos, len(parameters))
			parameters = append(parameters, nil)
			copy(parameters[pos+1:], parameters[pos:])
			parameters[pos] = parameter
		}
		tracker.ReplaceReferenceParameters(file, declaration, parameters)
		if adder != nil {
			for _, edit := range adder.Edits() {
				tracker.ReplaceRangeWithText(file, edit.Range, edit.NewText)
			}
		}
	}
	return ctx.Err()
}

var ReferenceMissingParametersProvider = &CodeFixProvider{Name: "addMissingParam", ErrorCodes: []int32{diagnostics.Expected_0_arguments_but_got_1.Code()}, FixIds: []string{"addMissingParam", "addOptionalParam"},
	GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		info := referenceMissingParametersInfoAt(ctx, request, ch)
		if info == nil {
			return nil, ctx.Err()
		}
		actions := []*CodeAction{}
		for _, optional := range []bool{false, true} {
			additions := info.required
			fixID := "addMissingParam"
			title := diagnostics.Add_missing_parameter_to_0
			plural := diagnostics.Add_missing_parameters_to_0
			all := diagnostics.Add_all_missing_parameters
			if optional {
				additions = info.optional
				fixID = "addOptionalParam"
				title = diagnostics.Add_optional_parameter_to_0
				plural = diagnostics.Add_optional_parameters_to_0
				all = diagnostics.Add_all_optional_parameters
			}
			if len(additions) == 0 {
				continue
			}
			if len(additions) > 1 {
				title = plural
			}
			tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
			if err := referenceApplyMissingParameters(ctx, request, ch, tracker, info, optional); err != nil {
				return nil, err
			}
			actions = append(actions, &CodeAction{ReferenceFixName: fixID, Description: title.Localize(locale.FromContext(ctx), info.name), ReferenceChanges: referenceTrackerFileChanges(tracker), FixID: fixID, FixAllDescription: all.Localize(locale.FromContext(ctx))})
		}
		return actions, ctx.Err()
	},
	GetAllCodeActions: func(ctx context.Context, request *CodeFixContext) (*CombinedCodeActions, error) {
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		for _, diagnostic := range getAllDiagnostics(ctx, request.Program, request.SourceFile) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if diagnostic.Code() != diagnostics.Expected_0_arguments_but_got_1.Code() {
				continue
			}
			item := *request
			item.Span = diagnostic.Loc()
			info := referenceMissingParametersInfoAt(ctx, &item, ch)
			if info == nil {
				continue
			}
			if err := referenceApplyMissingParameters(ctx, request, ch, tracker, info, request.FixID == "addOptionalParam"); err != nil {
				return nil, err
			}
		}
		return &CombinedCodeActions{ReferenceChanges: referenceTrackerFileChanges(tracker)}, ctx.Err()
	},
}

var referenceMissingAwaitCodes = []int32{
	diagnostics.An_arithmetic_operand_must_be_of_type_any_number_bigint_or_an_enum_type.Code(),
	diagnostics.The_left_hand_side_of_an_arithmetic_operation_must_be_of_type_any_number_bigint_or_an_enum_type.Code(),
	diagnostics.The_right_hand_side_of_an_arithmetic_operation_must_be_of_type_any_number_bigint_or_an_enum_type.Code(),
	diagnostics.Operator_0_cannot_be_applied_to_type_1.Code(), diagnostics.Operator_0_cannot_be_applied_to_types_1_and_2.Code(),
	diagnostics.This_comparison_appears_to_be_unintentional_because_the_types_0_and_1_have_no_overlap.Code(),
	diagnostics.This_condition_will_always_return_true_since_this_0_is_always_defined.Code(),
	diagnostics.Type_0_is_not_an_array_type.Code(), diagnostics.Type_0_is_not_an_array_type_or_a_string_type.Code(),
	diagnostics.Type_0_can_only_be_iterated_through_when_using_the_downlevelIteration_flag_or_with_a_target_of_es2015_or_higher.Code(),
	diagnostics.Type_0_is_not_an_array_type_or_a_string_type_or_does_not_have_a_Symbol_iterator_method_that_returns_an_iterator.Code(),
	diagnostics.Type_0_is_not_an_array_type_or_does_not_have_a_Symbol_iterator_method_that_returns_an_iterator.Code(),
	diagnostics.Type_0_must_have_a_Symbol_iterator_method_that_returns_an_iterator.Code(),
	diagnostics.Type_0_must_have_a_Symbol_asyncIterator_method_that_returns_an_async_iterator.Code(),
	diagnostics.Argument_of_type_0_is_not_assignable_to_parameter_of_type_1.Code(), diagnostics.Property_0_does_not_exist_on_type_1.Code(),
	diagnostics.This_expression_is_not_callable.Code(), diagnostics.This_expression_is_not_constructable.Code(),
}

func referenceInsideAwaitableBody(node *ast.Node) bool {
	if node == nil {
		return false
	}
	if node.Flags&ast.NodeFlagsAwaitContext != 0 {
		return true
	}
	for ancestor := node; ancestor != nil; ancestor = ancestor.Parent {
		parent := ancestor.Parent
		if parent == nil {
			continue
		}
		if ast.IsArrowFunction(parent) && parent.Body() == ancestor {
			return true
		}
		if ast.IsBlock(ancestor) && (ast.IsFunctionDeclaration(parent) || ast.IsFunctionExpression(parent) || ast.IsArrowFunction(parent) || ast.IsMethodDeclaration(parent)) {
			return true
		}
	}
	return false
}
func referenceAwaitErrorExpression(file *ast.SourceFile, code int32, span core.TextRange, items []*ast.Diagnostic) *ast.Node {
	expression := referenceFixableErrorSpanExpression(file, span)
	if expression == nil || !referenceInsideAwaitableBody(expression) {
		return nil
	}
	for _, item := range items {
		if item.Code() == code && item.Loc() == span && slices.ContainsFunc(item.RelatedInformation(), func(related *ast.Diagnostic) bool {
			return related.Code() == diagnostics.Did_you_forget_to_use_await.Code()
		}) {
			return expression
		}
	}
	return nil
}
func referenceAwaitInitializers(request *CodeFixContext, ch *checker.Checker, expression *ast.Node, items []*ast.Diagnostic) ([]*ast.Node, []*ast.Symbol, bool) {
	identifiers := []*ast.Node{}
	complete := true
	if expression.Parent != nil && ast.IsPropertyAccessExpression(expression.Parent) && ast.IsIdentifier(expression.Parent.Expression()) {
		identifiers = append(identifiers, expression.Parent.Expression())
	} else if ast.IsIdentifier(expression) {
		identifiers = append(identifiers, expression)
	} else if ast.IsBinaryExpression(expression) {
		binary := expression.AsBinaryExpression()
		for _, side := range []*ast.Node{binary.Left, binary.Right} {
			if ch.GetPromisedTypeOfPromise(ch.GetTypeAtLocation(side)) != nil {
				if ast.IsIdentifier(side) {
					identifiers = append(identifiers, side)
				} else {
					complete = false
				}
			}
		}
	}
	initializers := []*ast.Node{}
	symbols := []*ast.Symbol{}
	missingReference := func(reference *ast.Node) bool {
		node := reference
		if reference.Parent != nil && ast.IsPropertyAccessExpression(reference.Parent) {
			node = reference.Parent.Name()
		} else if reference.Parent != nil && ast.IsBinaryExpression(reference.Parent) {
			node = reference.Parent
		}
		span := core.NewTextRange(scanner.GetTokenPosOfNode(node, request.SourceFile, false), node.End())
		for _, diagnostic := range items {
			if diagnostic.Loc() == span {
				if slices.Contains(referenceMissingAwaitCodes, diagnostic.Code()) {
					return true
				}
				break
			}
		}
		return ch.GetTypeAtLocation(node).Flags()&checker.TypeFlagsAny != 0
	}
	for _, identifier := range identifiers {
		symbol := ch.GetSymbolAtLocation(identifier)
		if symbol == nil {
			continue
		}
		declaration := symbol.ValueDeclaration
		var statement *ast.Node
		if declaration != nil {
			statement = ast.FindAncestor(declaration, ast.IsVariableStatement)
		}
		if declaration == nil || !ast.IsVariableDeclaration(declaration) || statement == nil || declaration.Type() != nil || declaration.Initializer() == nil || ast.GetSourceFileOfNode(statement) != request.SourceFile || statement.ModifierFlags()&ast.ModifierFlagsExport != 0 || declaration.Name() == nil || !ast.IsIdentifier(declaration.Name()) || !referenceInsideAwaitableBody(declaration.Initializer()) {
			complete = false
			continue
		}
		used := false
		for _, reference := range referenceUnusedReferences(ch, request.SourceFile, declaration.Name()) {
			if identifier != reference && !missingReference(reference) {
				used = true
				break
			}
		}
		if used {
			complete = false
			continue
		}
		initializers = append(initializers, declaration.Initializer())
		symbols = append(symbols, symbol)
	}
	return initializers, symbols, !complete
}
func referenceAwaitLeadingSemicolon(request *CodeFixContext, tracker *change.Tracker, node *ast.Node) {
	preceding := astnav.FindPrecedingToken(request.SourceFile, node.Pos())
	if preceding != nil && lsutil.PositionIsASICandidate(preceding.End(), preceding.Parent, request.SourceFile) {
		tracker.InsertText(request.SourceFile, request.LS.converters.PositionToLineAndCharacter(request.SourceFile, core.TextPos(scanner.GetTokenPosOfNode(node, request.SourceFile, false))), ";")
	}
}
func referenceInsertAwait(request *CodeFixContext, ch *checker.Checker, tracker *change.Tracker, site *ast.Node, fixed map[*ast.Symbol]bool) {
	if site.Parent != nil && ast.IsForOfStatement(site.Parent) && site.Parent.AsForInOrOfStatement().AwaitModifier == nil && ch.IsReferenceAsyncIterable(ch.GetTypeAtLocation(site)) {
		loop := site.Parent.AsForInOrOfStatement()
		var preserve func(*ast.Node) bool
		preserve = func(node *ast.Node) bool {
			if ast.IsBlock(node) {
				start := scanner.GetTokenPosOfNode(node, request.SourceFile, false)
				if start >= 0 && node.End() <= len(request.SourceFile.Text()) && !strings.ContainsAny(request.SourceFile.Text()[start:node.End()], "\r\n\u2028\u2029") {
					tracker.AddEmitFlags(node, printer.EFSingleLine)
				}
			}
			return node.ForEachChild(preserve)
		}
		preserve(loop.Statement)
		tracker.ReplaceNode(request.SourceFile, site.Parent, tracker.UpdateForInOrOfStatement(loop, tracker.NewToken(ast.KindAwaitKeyword), loop.Initializer, loop.Expression, loop.Statement), nil)
		return
	}
	if ast.IsBinaryExpression(site) {
		binary := site.AsBinaryExpression()
		for _, side := range []*ast.Node{binary.Left, binary.Right} {
			if fixed != nil && ast.IsIdentifier(side) {
				if symbol := ch.GetSymbolAtLocation(side); symbol != nil && fixed[symbol] {
					continue
				}
			}
			replacement := side
			if ch.GetPromisedTypeOfPromise(ch.GetTypeAtLocation(side)) != nil {
				replacement = tracker.NewAwaitExpression(side)
			}
			tracker.ReplaceNode(request.SourceFile, side, replacement, nil)
		}
		return
	}
	if request.ErrorCode == diagnostics.Property_0_does_not_exist_on_type_1.Code() && site.Parent != nil && ast.IsPropertyAccessExpression(site.Parent) {
		expression := site.Parent.Expression()
		if fixed != nil && ast.IsIdentifier(expression) {
			if symbol := ch.GetSymbolAtLocation(expression); symbol != nil && fixed[symbol] {
				return
			}
		}
		tracker.ReplaceNode(request.SourceFile, expression, tracker.NewParenthesizedExpression(tracker.NewAwaitExpression(expression)), nil)
		referenceAwaitLeadingSemicolon(request, tracker, expression)
		return
	}
	if (request.ErrorCode == diagnostics.This_expression_is_not_callable.Code() || request.ErrorCode == diagnostics.This_expression_is_not_constructable.Code()) && site.Parent != nil && (ast.IsCallExpression(site.Parent) || ast.IsNewExpression(site.Parent)) {
		if fixed != nil && ast.IsIdentifier(site) {
			if symbol := ch.GetSymbolAtLocation(site); symbol != nil && fixed[symbol] {
				return
			}
		}
		tracker.ReplaceNode(request.SourceFile, site, tracker.NewParenthesizedExpression(tracker.NewAwaitExpression(site)), nil)
		referenceAwaitLeadingSemicolon(request, tracker, site)
		return
	}
	if fixed != nil && site.Parent != nil && ast.IsVariableDeclaration(site.Parent) && site.Parent.Name() != nil && ast.IsIdentifier(site.Parent.Name()) {
		if symbol := ch.GetSymbolAtLocation(site.Parent.Name()); symbol != nil {
			if fixed[symbol] {
				return
			}
			fixed[symbol] = true
		}
	}
	tracker.ReplaceNode(request.SourceFile, site, tracker.NewAwaitExpression(site), nil)
}

var ReferenceMissingAwaitProvider = &CodeFixProvider{Name: "addMissingAwait", ErrorCodes: referenceMissingAwaitCodes, FixIds: []string{"addMissingAwait"},
	GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		items := referenceMissingObjectDiagnosticHeads(request.SourceFile, request.Program.GetSemanticDiagnostics(ctx, request.SourceFile))
		expression := referenceAwaitErrorExpression(request.SourceFile, request.ErrorCode, request.Span, items)
		if expression == nil {
			return nil, ctx.Err()
		}
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		initializers, symbols, _ := referenceAwaitInitializers(request, ch, expression, items)
		actions := []*CodeAction{}
		if len(initializers) > 0 {
			tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
			for _, initializer := range initializers {
				referenceInsertAwait(request, ch, tracker, initializer, nil)
			}
			title := diagnostics.Add_await_to_initializers.Localize(locale.FromContext(ctx))
			if len(initializers) == 1 {
				title = diagnostics.Add_await_to_initializer_for_0.Localize(locale.FromContext(ctx), ast.SymbolName(symbols[0]))
			}
			actions = append(actions, &CodeAction{ReferenceFixName: "addMissingAwaitToInitializer", Description: title, ReferenceChanges: referenceTrackerFileChanges(tracker)})
		}
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		referenceInsertAwait(request, ch, tracker, expression, nil)
		actions = append(actions, &CodeAction{Description: diagnostics.Add_await.Localize(locale.FromContext(ctx)), ReferenceChanges: referenceTrackerFileChanges(tracker), FixID: "addMissingAwait", FixAllDescription: diagnostics.Fix_all_expressions_possibly_missing_await.Localize(locale.FromContext(ctx))})
		return actions, ctx.Err()
	},
	GetAllCodeActions: func(ctx context.Context, request *CodeFixContext) (*CombinedCodeActions, error) {
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		items := referenceMissingObjectDiagnosticHeads(request.SourceFile, request.Program.GetSemanticDiagnostics(ctx, request.SourceFile))
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		fixed := map[*ast.Symbol]bool{}
		for _, diagnostic := range getAllDiagnostics(ctx, request.Program, request.SourceFile) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !slices.Contains(referenceMissingAwaitCodes, diagnostic.Code()) {
				continue
			}
			expression := referenceAwaitErrorExpression(request.SourceFile, diagnostic.Code(), diagnostic.Loc(), items)
			if expression == nil {
				continue
			}
			item := *request
			item.ErrorCode = diagnostic.Code()
			item.Span = diagnostic.Loc()
			initializers, _, second := referenceAwaitInitializers(&item, ch, expression, items)
			if len(initializers) > 0 {
				for _, initializer := range initializers {
					referenceInsertAwait(&item, ch, tracker, initializer, fixed)
				}
				if second {
					referenceInsertAwait(&item, ch, tracker, expression, fixed)
				}
			} else {
				referenceInsertAwait(&item, ch, tracker, expression, fixed)
			}
		}
		return &CombinedCodeActions{ReferenceChanges: referenceTrackerFileChanges(tracker)}, ctx.Err()
	},
}

// Reference JSDoc attachment and type migration follow TypeScript 6.0.3.
func referenceJSDocFirstVariable(node *ast.Node) *ast.Node {
	if node != nil && ast.IsVariableStatement(node) {
		list := node.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
		if len(list) > 0 {
			return list[0]
		}
	}
	return nil
}
func referenceJSDocNext(node *ast.Node) *ast.Node {
	parent := node.Parent
	if parent == nil {
		return nil
	}
	if ast.IsPropertyAssignment(parent) || ast.IsExportAssignment(parent) || ast.IsPropertyDeclaration(parent) || (ast.IsExpressionStatement(parent) && ast.IsPropertyAccessExpression(node)) || ast.IsReturnStatement(parent) || (ast.IsModuleDeclaration(parent) && parent.Body() != nil && ast.IsModuleDeclaration(parent.Body())) || ast.IsAssignmentExpression(node, false) {
		return parent
	}
	grand := parent.Parent
	if grand == nil {
		return nil
	}
	if referenceJSDocFirstVariable(grand) == node || ast.IsAssignmentExpression(parent, false) {
		return grand
	}
	great := grand.Parent
	if great == nil {
		return nil
	}
	if referenceJSDocFirstVariable(great) != nil {
		return great
	}
	if (ast.IsPropertyDeclaration(great) || ast.IsPropertyAssignment(great)) && great.Initializer() == node {
		return great
	}
	if ast.IsExpressionStatement(great) && ast.IsBinaryExpression(great.Expression()) {
		expression := great.Expression().AsBinaryExpression()
		if ast.GetAssignmentDeclarationKind(great.Expression()) != ast.JSDeclarationKindNone && ast.IsBinaryExpression(expression.Right) {
			operator := expression.Right.AsBinaryExpression().OperatorToken.Kind
			if operator == ast.KindBarBarToken || operator == ast.KindQuestionQuestionToken {
				return great
			}
		}
	}
	return nil
}
func referenceJSDocTags(host *ast.Node) []*ast.Node {
	result := []*ast.Node{}
	add := func(node *ast.Node) {
		docs := node.JSDoc(nil)
		for i, doc := range docs {
			tags := doc.AsJSDoc().Tags
			if tags == nil {
				continue
			}
			for _, tag := range tags.Nodes {
				if i != len(docs)-1 && tag.Kind != ast.KindJSDocOverloadTag {
					continue
				}
				if (tag.Kind == ast.KindJSDocTypeTag || tag.Kind == ast.KindJSDocSatisfiesTag) && tag.Parent != nil && tag.Parent.Parent != nil && ast.IsParenthesizedExpression(tag.Parent.Parent) && tag.Parent.Parent != host {
					continue
				}
				result = append(result, tag)
			}
		}
	}
	if ast.IsVariableLike(host) && host.Initializer() != nil {
		add(host.Initializer())
	}
	for node := host; node != nil && node.Parent != nil; node = referenceJSDocNext(node) {
		add(node)
		if ast.IsParameterDeclaration(node) {
			if node.Name() != nil {
				tags := referenceJSDocTags(node.Parent)
				index := 0
				for _, parameter := range node.Parent.Parameters() {
					if parameter == node {
						break
					}
					index++
				}
				position := 0
				for _, tag := range tags {
					if tag.Kind != ast.KindJSDocParameterTag {
						continue
					}
					if ast.IsIdentifier(node.Name()) {
						if tag.Name() != nil && ast.IsIdentifier(tag.Name()) && tag.Name().Text() == node.Name().Text() {
							result = append(result, tag)
						}
					} else if position == index {
						result = append(result, tag)
					}
					position++
				}
			}
			break
		}
		if ast.IsTypeParameterDeclaration(node) {
			for _, tag := range referenceJSDocTags(node.Parent) {
				if tag.Kind == ast.KindJSDocTemplateTag {
					for _, parameter := range tag.AsJSDocTemplateTag().TypeParameters.Nodes {
						if parameter.Name().Text() == node.Name().Text() {
							result = append(result, tag)
							break
						}
					}
				}
			}
			break
		}
	}
	return result
}
func referenceJSDocType(node *ast.Node) *ast.Node {
	tags := referenceJSDocTags(node)
	for _, tag := range tags {
		if tag.Kind == ast.KindJSDocTypeTag {
			if expression := tag.TypeExpression(); expression != nil {
				return expression.Type()
			}
			return nil
		}
	}
	if ast.IsParameterDeclaration(node) {
		for _, tag := range tags {
			if tag.Kind == ast.KindJSDocParameterTag && tag.TypeExpression() != nil {
				return tag.TypeExpression().Type()
			}
		}
	}
	return nil
}
func referenceJSDocReturnType(node *ast.Node) *ast.Node {
	tags := referenceJSDocTags(node)
	for _, tag := range tags {
		if tag.Kind == ast.KindJSDocReturnTag {
			if expression := tag.TypeExpression(); expression != nil {
				return expression.Type()
			}
			break
		}
	}
	for _, tag := range tags {
		if tag.Kind != ast.KindJSDocTypeTag {
			continue
		}
		expression := tag.TypeExpression()
		if expression == nil {
			return nil
		}
		typ := expression.Type()
		if ast.IsTypeLiteralNode(typ) {
			for _, member := range typ.Members() {
				if ast.IsCallSignatureDeclaration(member) {
					return member.Type()
				}
			}
		} else if typ.Kind == ast.KindFunctionType {
			return typ.Type()
		}
		break
	}
	return nil
}
func referenceHasUsableJSDoc(node *ast.Node) bool {
	if ast.IsFunctionLikeDeclaration(node) {
		for _, parameter := range node.Parameters() {
			if parameter.Type() == nil && referenceJSDocType(parameter) != nil {
				return true
			}
		}
		return node.Type() == nil && referenceJSDocReturnType(node) != nil
	}
	return (ast.IsVariableDeclaration(node) || ast.IsPropertyDeclaration(node) || ast.IsPropertySignatureDeclaration(node)) && node.Type() == nil && referenceJSDocType(node) != nil
}
func referenceJSDocDiagnostics(file *ast.SourceFile, items []*ast.Diagnostic) []*ast.Diagnostic {
	if ast.IsSourceFileJS(file) {
		return items
	}
	var visit func(*ast.Node) bool
	visit = func(node *ast.Node) bool {
		if referenceHasUsableJSDoc(node) {
			target := node
			if node.Name() != nil {
				target = node.Name()
			}
			span := scanner.GetErrorRangeForNode(file, target)
			if ast.IsFunctionExpression(target) && target.Name() == nil {
				span = scanner.GetRangeOfTokenAtPosition(file, scanner.GetTokenPosOfNode(target, file, false))
			}
			found := false
			for _, item := range items {
				if item.Code() == diagnostics.JSDoc_types_may_be_moved_to_TypeScript_types.Code() && item.Loc() == span {
					found = true
					break
				}
			}
			if !found {
				item := ast.NewDiagnostic(file, span, diagnostics.JSDoc_types_may_be_moved_to_TypeScript_types)
				item.SetCategory(diagnostics.CategorySuggestion)
				items = append(items, item)
			}
		}
		for _, doc := range node.JSDoc(nil) {
			if doc.AsJSDoc().Tags == nil {
				continue
			}
			for _, tag := range doc.AsJSDoc().Tags.Nodes {
				if tag.Kind == ast.KindJSDocTypedefTag {
					item := ast.NewDiagnostic(file, scanner.GetErrorRangeForNode(file, tag), diagnostics.JSDoc_typedef_may_be_converted_to_TypeScript_type)
					item.SetCategory(diagnostics.CategorySuggestion)
					items = append(items, item)
				}
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(file.AsNode())
	return items
}
func referenceTransformJSDoc(tracker *change.Tracker, node *ast.Node) *ast.Node {
	if node == nil {
		return nil
	}
	var transform func(*ast.Node) *ast.Node
	var visitor *ast.NodeVisitor
	typeRef := func(name string) *ast.Node { return tracker.NewTypeReferenceNode(tracker.NewIdentifier(name), nil) }
	transform = func(node *ast.Node) *ast.Node {
		if node == nil {
			return nil
		}
		var result *ast.Node
		switch node.Kind {
		case ast.KindJSDocAllType:
			return typeRef("any")
		case ast.KindJSDocNonNullableType:
			return transform(node.Type())
		case ast.KindJSDocOptionalType, ast.KindJSDocNullableType:
			name := "undefined"
			if node.Kind == ast.KindJSDocNullableType {
				name = "null"
			}
			return tracker.NewUnionTypeNode(tracker.NewNodeList([]*ast.Node{transform(node.Type()), typeRef(name)}))
		case ast.KindJSDocVariadicType:
			return tracker.NewArrayTypeNode(transform(node.AsJSDocVariadicType().Type))
		case ast.KindFunctionType:
			if !strings.HasPrefix(scanner.GetTextOfNode(node), "function") {
				result = node.VisitEachChild(visitor)
				break
			}
			parameters := []*ast.Node{}
			for i, parameter := range node.Parameters() {
				p := parameter.AsParameterDeclaration()
				rest := p.Type != nil && p.Type.Kind == ast.KindJSDocVariadicType && i == len(node.Parameters())-1
				name := p.Name()
				dots := p.DotDotDotToken
				if name == nil {
					label := fmt.Sprintf("arg%d", i)
					if rest {
						label = "rest"
					}
					name = tracker.NewIdentifier(label)
				}
				if rest {
					dots = tracker.NewToken(ast.KindDotDotDotToken)
				}
				parameters = append(parameters, tracker.NewParameterDeclaration(p.Modifiers(), dots, name, p.QuestionToken, transform(p.Type), p.Initializer))
			}
			ret := node.Type()
			if ret == nil {
				ret = typeRef("any")
			}
			return tracker.NewFunctionTypeNode(nil, tracker.NewNodeList(parameters), ret)
		case ast.KindTypeReference:
			reference := node.AsTypeReferenceNode()
			name := reference.TypeName
			args := reference.TypeArguments
			if ast.IsIdentifier(name) {
				text := name.Text()
				if text == "Object" && args != nil && len(args.Nodes) == 2 && (args.Nodes[0].Kind == ast.KindStringKeyword || args.Nodes[0].Kind == ast.KindNumberKeyword) {
					label, kind := "s", "string"
					if args.Nodes[0].Kind == ast.KindNumberKeyword {
						label, kind = "n", "number"
					}
					parameter := tracker.NewParameterDeclaration(nil, nil, tracker.NewIdentifier(label), nil, typeRef(kind), nil)
					result = tracker.NewTypeLiteralNode(tracker.NewNodeList([]*ast.Node{tracker.NewIndexSignatureDeclaration(nil, tracker.NewNodeList([]*ast.Node{parameter}), args.Nodes[1])}))
					break
				}
				switch text {
				case "String", "Boolean", "Object", "Number":
					text = strings.ToLower(text)
				case "array", "date", "promise":
					text = strings.ToUpper(text[:1]) + text[1:]
				}
				name = tracker.NewIdentifier(text)
				if (text == "Array" || text == "Promise") && args == nil {
					args = tracker.NewNodeList([]*ast.Node{typeRef("any")})
				} else if args != nil {
					types := []*ast.Node{}
					for _, argument := range args.Nodes {
						types = append(types, transform(argument))
					}
					args = tracker.NewNodeList(types)
				}
			}
			return tracker.NewTypeReferenceNode(name, args)
		case ast.KindJSDocTypeLiteral:
			members := []*ast.Node{}
			for _, tag := range node.AsJSDocTypeLiteral().JSDocPropertyTags {
				property := tag.AsJSDocParameterOrPropertyTag()
				name := property.Name()
				if !ast.IsIdentifier(name) {
					name = name.AsQualifiedName().Right
				}
				var question *ast.Node
				typ := typeRef("any")
				if property.TypeExpression != nil {
					original := property.TypeExpression.Type()
					if original.Kind == ast.KindJSDocOptionalType {
						question = tracker.NewToken(ast.KindQuestionToken)
					}
					typ = transform(original)
				}
				if property.IsBracketed {
					question = tracker.NewToken(ast.KindQuestionToken)
				}
				members = append(members, tracker.NewPropertySignatureDeclaration(nil, name, question, typ, nil))
			}
			result = tracker.NewTypeLiteralNode(tracker.NewNodeList(members))
		default:
			result = node.VisitEachChild(visitor)
		}
		tracker.AddEmitFlags(result, printer.EFSingleLine)
		return result
	}
	visitor = ast.NewNodeVisitor(transform, tracker.NodeFactory, ast.NodeVisitorHooks{})
	return transform(node)
}
func referenceAnnotateJSDoc(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil || token.Parent == nil {
		return "", nil
	}
	decl := token.Parent
	if ast.IsParameterDeclaration(decl) {
		decl = decl.Parent
	}
	if decl == nil || !referenceHasUsableJSDoc(decl) {
		return "", nil
	}
	if ast.IsFunctionLikeDeclaration(decl) {
		if decl.TypeParameterList() == nil {
			parameters := []*ast.Node{}
			for _, tag := range referenceJSDocTags(decl) {
				if tag.Kind != ast.KindJSDocTemplateTag {
					continue
				}
				skip := false
				if tag.Parent != nil && tag.Parent.Kind == ast.KindJSDoc && tag.Parent.AsJSDoc().Tags != nil {
					for _, sibling := range tag.Parent.AsJSDoc().Tags.Nodes {
						if sibling.Kind == ast.KindJSDocTypedefTag || sibling.Kind == ast.KindJSDocCallbackTag || sibling.Kind == ast.KindJSDocOverloadTag {
							skip = true
							break
						}
					}
				}
				if !skip {
					parameters = append(parameters, tag.AsJSDocTemplateTag().TypeParameters.Nodes...)
				}
			}
			if len(parameters) > 0 {
				tracker.InsertReferenceTypeParameters(request.SourceFile, decl, parameters)
			}
		}
		parameters := decl.Parameters()
		parens := ast.IsArrowFunction(decl) && astnav.FindChildOfKind(decl, ast.KindOpenParenToken, request.SourceFile) == nil
		if parens && len(parameters) > 0 {
			tracker.InsertNodeBefore(request.SourceFile, parameters[0], tracker.NewToken(ast.KindOpenParenToken), false, change.LeadingTriviaOptionNone)
		}
		for _, parameter := range parameters {
			if parameter.Type() == nil {
				if typ := referenceJSDocType(parameter); typ != nil {
					tracker.TryInsertTypeAnnotation(request.SourceFile, parameter, referenceTransformJSDoc(tracker, typ))
				}
			}
		}
		if parens && len(parameters) > 0 {
			tracker.InsertNodeAfter(request.SourceFile, parameters[len(parameters)-1], tracker.NewToken(ast.KindCloseParenToken))
		}
		if decl.Type() == nil {
			if typ := referenceJSDocReturnType(decl); typ != nil {
				tracker.TryInsertTypeAnnotation(request.SourceFile, decl, referenceTransformJSDoc(tracker, typ))
			}
		}
	} else {
		tracker.TryInsertTypeAnnotation(request.SourceFile, decl, referenceTransformJSDoc(tracker, referenceJSDocType(decl)))
	}
	return diagnostics.Annotate_with_type_from_JSDoc.Localize(locale.FromContext(ctx)), ctx.Err()
}

var ReferenceAnnotateJSDocProvider = newReferenceSyntaxFixProvider("annotateWithTypeFromJSDoc", []int32{diagnostics.JSDoc_types_may_be_moved_to_TypeScript_types.Code()}, diagnostics.Annotate_everything_with_types_from_JSDoc, referenceAnnotateJSDoc)

func referenceTypedefDeclarations(tracker *change.Tracker, tag *ast.Node) *ast.Node {
	if tag.Name() == nil || tag.TypeExpression() == nil {
		return nil
	}
	name := scanner.GetTextOfNode(tag.Name())
	if name == "" {
		return nil
	}
	expression := tag.TypeExpression()
	var properties func(*ast.Node) []*ast.Node
	properties = func(literal *ast.Node) []*ast.Node {
		members := []*ast.Node{}
		for _, tag := range literal.AsJSDocTypeLiteral().JSDocPropertyTags {
			property := tag.AsJSDocParameterOrPropertyTag()
			if property.Name() == nil || property.TypeExpression == nil {
				continue
			}
			name := property.Name()
			if !ast.IsIdentifier(name) {
				name = name.AsQualifiedName().Right
			}
			typ := property.TypeExpression.Type()
			if typ == nil {
				continue
			}
			if typ.Kind == ast.KindJSDocTypeLiteral {
				typ = tracker.NewTypeLiteralNode(tracker.NewNodeList(properties(typ)))
			} else {
				typ = referenceJSDocDeepClone(tracker, typ)
			}
			var optional *ast.Node
			if property.IsBracketed {
				optional = tracker.NewToken(ast.KindQuestionToken)
			}
			members = append(members, tracker.NewPropertySignatureDeclaration(nil, tracker.NewIdentifier(name.Text()), optional, typ, nil))
		}
		return members
	}
	switch expression.Kind {
	case ast.KindJSDocTypeLiteral:
		members := properties(expression)
		if len(members) == 0 {
			return nil
		}
		return tracker.NewInterfaceDeclaration(nil, tracker.NewIdentifier(name), nil, nil, tracker.NewNodeList(members))
	case ast.KindJSDocTypeExpression:
		return tracker.NewTypeAliasDeclaration(nil, tracker.NewIdentifier(name), nil, referenceJSDocDeepClone(tracker, expression.Type()))
	}
	return nil
}
func referenceTypedefChange(request *CodeFixContext, tracker *change.Tracker, all bool) error {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil {
		return nil
	}
	tag := ast.FindAncestor(token, func(node *ast.Node) bool { return node.Kind == ast.KindJSDocTypedefTag })
	if tag == nil {
		return nil
	}
	if tag.Name() == nil || scanner.GetTextOfNode(tag.Name()) == "" || tag.TypeExpression() == nil {
		return nil
	}
	var hasClosure func(*ast.Node) bool
	hasClosure = func(node *ast.Node) bool {
		switch node.Kind {
		case ast.KindJSDocNullableType, ast.KindJSDocNonNullableType, ast.KindJSDocOptionalType, ast.KindJSDocVariadicType:
			return true
		}
		return node.Kind == ast.KindFunctionType && strings.HasPrefix(scanner.GetTextOfNode(node), "function") || node.ForEachChild(hasClosure)
	}
	if expression := tag.TypeExpression(); expression != nil && hasClosure(expression) {
		return &change.InvalidEdits{Reason: "JSDoc type cannot be formatted"}
	}
	declaration := referenceTypedefDeclarations(tracker, tag)
	if declaration == nil {
		return nil
	}
	doc := tag.Parent
	if doc == nil || doc.Kind != ast.KindJSDoc || doc.AsJSDoc().Tags == nil {
		return nil
	}
	if !referenceJSDocNavigationSelects(request.SourceFile, doc, request.Span.Pos()) {
		return nil
	}
	tags := doc.AsJSDoc().Tags.Nodes
	index := slices.Index(tags, tag)
	if index < 0 {
		return nil
	}
	var left, right *ast.Node
	if index > 0 {
		left = tags[index-1]
	}
	if index+1 < len(tags) {
		right = tags[index+1]
	}
	newline := request.LS.FormatOptions().NewLineCharacter
	if newline == "" {
		newline = request.Program.Options().NewLine.GetNewLineCharacter()
	}
	start, end := scanner.GetTokenPosOfNode(doc, request.SourceFile, false), doc.End()
	prefix, suffix := "", ""
	endOfText := func(from, to int) int {
		text := request.SourceFile.Text()
		for position := to; position > from; {
			value, size := utf8.DecodeLastRuneInString(text[from:position])
			if value != '*' && value != '/' && !stringutil.IsWhiteSpaceLike(value) {
				return position
			}
			position -= size
		}
		return to
	}
	if left == nil && doc.AsJSDoc().Comment != nil && len(doc.AsJSDoc().Comment.Nodes) > 0 {
		start = endOfText(start, tag.Pos())
		prefix = newline + " */" + newline
	}
	if left != nil {
		if all && left.Kind == ast.KindJSDocTypedefTag {
			start = tag.Pos()
		} else {
			start = endOfText(left.Pos(), tag.Pos())
			prefix = newline + " */" + newline
		}
	}
	if right != nil {
		end = right.Pos()
		if all && right.Kind == ast.KindJSDocTypedefTag {
			suffix = newline + newline
		} else {
			suffix = newline + "/**" + newline + " * "
		}
	}
	span := request.LS.converters.ToLSPRange(request.SourceFile, core.NewTextRange(start, end))
	tracker.ReplaceRange(request.SourceFile, span, declaration, change.NodeOptions{Prefix: prefix, Suffix: suffix})
	return nil
}

var ReferenceTypedefProvider = &CodeFixProvider{Name: "convertTypedefToType", ErrorCodes: []int32{diagnostics.JSDoc_typedef_may_be_converted_to_TypeScript_type.Code()}, FixIds: []string{"convertTypedefToType"},
	GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		if err := referenceTypedefChange(request, tracker, false); err != nil {
			return nil, err
		}
		files := referenceTrackerFileChanges(tracker)
		if len(files) == 0 {
			return nil, ctx.Err()
		}
		return []*CodeAction{{Description: diagnostics.Convert_typedef_to_TypeScript_type.Localize(locale.FromContext(ctx)), ReferenceChanges: files, FixID: "convertTypedefToType", FixAllDescription: diagnostics.Convert_all_typedef_to_TypeScript_types.Localize(locale.FromContext(ctx))}}, ctx.Err()
	},
	GetAllCodeActions: func(ctx context.Context, request *CodeFixContext) (*CombinedCodeActions, error) {
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		for _, diagnostic := range getAllDiagnostics(ctx, request.Program, request.SourceFile) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if diagnostic.Code() != diagnostics.JSDoc_typedef_may_be_converted_to_TypeScript_type.Code() {
				continue
			}
			item := *request
			item.Span = diagnostic.Loc()
			if err := referenceTypedefChange(&item, tracker, true); err != nil {
				return nil, err
			}
		}
		return &CombinedCodeActions{ReferenceChanges: referenceTrackerFileChanges(tracker)}, ctx.Err()
	},
}

// referenceJSDocNavigationSelects reproduces the reference's child-range search,
// including overlapping leading-trivia ranges of documentation and modifiers.
func referenceJSDocNavigationSelects(file *ast.SourceFile, doc *ast.Node, position int) bool {
	host := doc.Parent
	if host == nil {
		return false
	}
	type childRange struct {
		start, end int
		doc        *ast.Node
	}
	children := []childRange{}
	for _, comment := range host.JSDoc(file) {
		children = append(children, childRange{comment.Pos(), comment.End(), comment})
	}
	pos := host.Pos()
	addSynthetic := func(end int) {
		if pos >= end {
			return
		}
		scan := scanner.GetScannerForSourceFile(file, pos)
		for pos < end {
			next := scan.TokenEnd()
			if next <= end {
				children = append(children, childRange{pos, next, nil})
			}
			if next <= pos {
				break
			}
			pos = next
			if scan.Token() == ast.KindEndOfFile {
				break
			}
			scan.Scan()
		}
	}
	addNode := func(node *ast.Node, _ *ast.NodeVisitor) *ast.Node {
		if node != nil {
			addSynthetic(node.Pos())
			children = append(children, childRange{node.Pos(), node.End(), nil})
			pos = node.End()
		}
		return node
	}
	addList := func(list *ast.NodeList, _ *ast.NodeVisitor) *ast.NodeList {
		if list != nil {
			addSynthetic(list.Pos())
			children = append(children, childRange{list.Pos(), list.End(), nil})
			pos = list.End()
		}
		return list
	}
	visitor := ast.NewNodeVisitor(core.Identity, nil, ast.NodeVisitorHooks{VisitNode: addNode, VisitToken: addNode, VisitNodes: addList, VisitModifiers: func(list *ast.ModifierList, v *ast.NodeVisitor) *ast.ModifierList {
		if list != nil {
			addList(&list.NodeList, v)
		}
		return list
	}})
	host.VisitEachChild(visitor)
	addSynthetic(host.End())
	contains := func(child childRange) bool { return child.start <= position && position < child.end }
	low, high := 0, len(children)-1
	for low <= high {
		middle := (low + high) / 2
		child := children[middle]
		if child.end < position {
			low = middle + 1
		} else if child.start > position {
			high = middle - 1
		} else if contains(child) {
			if middle > 0 && contains(children[middle-1]) {
				high = middle - 1
			} else {
				return child.doc == doc
			}
		} else {
			low = middle + 1
		}
	}
	return false
}
func referenceJSDocDeepClone(tracker *change.Tracker, node *ast.Node) *ast.Node {
	var visitor *ast.NodeVisitor
	visitor = ast.NewNodeVisitor(func(node *ast.Node) *ast.Node {
		if node == nil {
			return nil
		}
		if node.Kind == ast.KindJSDocAllType {
			return tracker.NewTypeReferenceNode(tracker.NewIdentifier("*"), nil)
		}
		if node.Kind == ast.KindAnyKeyword && node.Flags&ast.NodeFlagsJSDoc != 0 && scanner.GetTextOfNode(node) == "?" {
			return tracker.NewTypeReferenceNode(tracker.NewIdentifier("?"), nil)
		}
		result := node.VisitEachChild(visitor)
		if result == node {
			result = node.Clone(tracker)
		}
		return result
	}, tracker.NodeFactory, ast.NodeVisitorHooks{})
	return visitor.VisitNode(node)
}

var referenceReturnValueCodes = []int32{diagnostics.A_function_whose_declared_type_is_neither_undefined_void_nor_any_must_return_a_value.Code(), diagnostics.Type_0_is_not_assignable_to_type_1.Code(), diagnostics.Argument_of_type_0_is_not_assignable_to_parameter_of_type_1.Code()}

type referenceReturnValueInfo struct {
	declaration, expression, statement, commentSource, label *ast.Node
	parentheses                                              bool
}

func referenceReturnValueGetInfo(request *CodeFixContext, ch *checker.Checker) *referenceReturnValueInfo {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil || token.Parent == nil {
		return nil
	}
	declaration := ast.FindAncestor(token.Parent, ast.IsFunctionLikeDeclaration)
	var expected *checker.Type
	functionType := false
	switch request.ErrorCode {
	case diagnostics.A_function_whose_declared_type_is_neither_undefined_void_nor_any_must_return_a_value.Code():
		if declaration == nil || declaration.Body() == nil || declaration.Type() == nil || declaration.Type().Pos() > token.Pos() || token.End() > declaration.Type().End() {
			return nil
		}
		expected = ch.GetTypeFromTypeNode(declaration.Type())
	case diagnostics.Argument_of_type_0_is_not_assignable_to_parameter_of_type_1.Code():
		if declaration == nil || declaration.Body() == nil || declaration.Parent == nil || !ast.IsCallExpression(declaration.Parent) {
			return nil
		}
		index := slices.Index(declaration.Parent.Arguments(), declaration)
		if index < 0 {
			return nil
		}
		expected = ch.GetContextualTypeForArgumentAtIndex(declaration.Parent, index)
		functionType = true
	case diagnostics.Type_0_is_not_assignable_to_type_1.Code():
		parent := token.Parent
		if !ast.IsDeclarationName(token) || (!ast.IsVariableLike(parent) && !ast.IsJsxAttribute(parent)) {
			return nil
		}
		var initializer *ast.Node
		switch parent.Kind {
		case ast.KindVariableDeclaration, ast.KindParameter, ast.KindBindingElement, ast.KindPropertyDeclaration, ast.KindPropertyAssignment:
			initializer = parent.Initializer()
		case ast.KindJsxAttribute:
			if value := parent.AsJsxAttribute().Initializer; value != nil && ast.IsJsxExpression(value) {
				initializer = value.Expression()
			}
		}
		if initializer == nil || !ast.IsFunctionLikeDeclaration(initializer) || initializer.Body() == nil {
			return nil
		}
		declaration = initializer
		expected = ch.GetTypeAtLocation(parent)
		functionType = true
	default:
		return nil
	}
	if expected == nil || declaration.Body() == nil || !ast.IsBlock(declaration.Body()) || len(declaration.Body().Statements()) != 1 {
		return nil
	}
	statement := declaration.Body().Statements()[0]
	info := &referenceReturnValueInfo{declaration: declaration, statement: statement}
	if ast.IsExpressionStatement(statement) {
		info.expression = statement.Expression()
		info.commentSource = info.expression
	} else {
		label := statement
		if ast.IsBlock(statement) {
			if len(statement.Statements()) != 1 {
				return nil
			}
			label = statement.Statements()[0]
		}
		if !ast.IsLabeledStatement(label) || !ast.IsExpressionStatement(label.AsLabeledStatement().Statement) {
			return nil
		}
		info.label = label.AsLabeledStatement().Label
		info.expression = label.AsLabeledStatement().Statement.Expression()
		info.commentSource = info.expression
		if label != statement {
			info.commentSource = label
		} else if ast.IsArrowFunction(declaration) {
			info.parentheses = true
		}
	}
	if !ch.IsReferenceFixedReturnAssignable(declaration, info.expression, info.label, expected, functionType) {
		return nil
	}
	return info
}
func referenceSuppressReturnTrivia(tracker *change.Tracker, node *ast.Node) {
	for current := node; current != nil; {
		tracker.AddEmitFlags(current, printer.EFNoLeadingComments)
		var next *ast.Node
		current.ForEachChild(func(child *ast.Node) bool { next = child; return true })
		current = next
	}
	for current := node; current != nil; {
		tracker.AddEmitFlags(current, printer.EFNoTrailingComments)
		var next *ast.Node
		current.ForEachChild(func(child *ast.Node) bool { next = child; return false })
		current = next
	}
}
func referenceCopyReturnComments(request *CodeFixContext, tracker *change.Tracker, from, to *ast.Node) {
	file := request.SourceFile
	text := file.Text()
	start, end := from.Pos(), scanner.GetTokenPosOfNode(from, file, false)
	if start < 0 || end < start || end > len(text) {
		return
	}
	leading := scanner.GetTrailingCommentRanges(tracker.NodeFactory, text, start)
	if strings.Contains(text[start:end], "\n") {
		leading = scanner.GetLeadingCommentRanges(tracker.NodeFactory, text, start)
	}
	copyComment := func(comment ast.CommentRange, trailing bool) {
		start, end := comment.Pos()+2, comment.End()
		if comment.Kind == ast.KindMultiLineCommentTrivia {
			end -= 2
		}
		if start > end || end > len(text) {
			return
		}
		if trailing {
			tracker.AddSyntheticTrailingComment(to, comment.Kind, text[start:end], comment.HasTrailingNewLine)
		} else {
			tracker.AddSyntheticLeadingComment(to, comment.Kind, text[start:end], comment.HasTrailingNewLine)
		}
	}
	for comment := range leading {
		copyComment(comment, false)
	}
	for comment := range scanner.GetTrailingCommentRanges(tracker.NodeFactory, text, from.End()) {
		copyComment(comment, true)
	}
}
func referenceApplyReturnValue(request *CodeFixContext, tracker *change.Tracker, info *referenceReturnValueInfo, fix string) {
	expression := info.expression
	if info.label != nil {
		expression = tracker.NewObjectLiteralExpression(tracker.NewNodeList([]*ast.Node{tracker.NewPropertyAssignment(nil, info.label, nil, nil, expression)}), false)
	}
	if fix == "fixAddReturnStatement" {
		referenceSuppressReturnTrivia(tracker, expression)
		suffix := ""
		if lsutil.ProbablyUsesSemicolons(request.SourceFile) {
			suffix = ";"
		}
		tracker.ReplaceNode(request.SourceFile, info.statement, tracker.NewReturnStatement(expression), &change.NodeOptions{LeadingTriviaOption: change.LeadingTriviaOptionExclude, TrailingTriviaOption: change.TrailingTriviaOptionExclude, Suffix: suffix})
		return
	}
	if !ast.IsArrowFunction(info.declaration) {
		return
	}
	if fix == "fixWrapTheBlockWithParen" {
		tracker.ReplaceNode(request.SourceFile, info.declaration.Body(), tracker.NewParenthesizedExpression(expression), nil)
		return
	}
	needsParens := ast.IsObjectLiteralExpression(expression) || (ast.IsBinaryExpression(expression) && expression.AsBinaryExpression().OperatorToken.Kind == ast.KindCommaToken) || ((ast.IsAsExpression(expression) || ast.IsSatisfiesExpression(expression)) && ast.IsObjectLiteralExpression(expression.Expression()))
	body := expression
	if needsParens {
		body = tracker.NewParenthesizedExpression(expression)
	}
	referenceSuppressReturnTrivia(tracker, info.commentSource)
	body = body.Clone(tracker)
	tracker.SetEmitFlags(body, tracker.EmitFlags(body)&^printer.EFNoComments)
	tracker.SetCommentRange(body, core.UndefinedTextRange())
	referenceCopyReturnComments(request, tracker, info.commentSource, body)
	tracker.ReplaceNode(request.SourceFile, info.declaration.Body(), body, nil)
}

var ReferenceReturnValueProvider = &CodeFixProvider{Name: "returnValueCorrect", ErrorCodes: referenceReturnValueCodes, FixIds: []string{"fixAddReturnStatement", "fixRemoveBracesFromArrowFunctionBody", "fixWrapTheBlockWithParen"},
	GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		info := referenceReturnValueGetInfo(request, ch)
		if info == nil {
			return nil, ctx.Err()
		}
		fixes := []string{"fixAddReturnStatement"}
		if info.parentheses {
			fixes = []string{"fixWrapTheBlockWithParen"}
		} else if ast.IsArrowFunction(info.declaration) {
			fixes = append(fixes, "fixRemoveBracesFromArrowFunctionBody")
		}
		actions := []*CodeAction{}
		for _, fix := range fixes {
			tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
			referenceApplyReturnValue(request, tracker, info, fix)
			title, all := referenceReturnValueTitles(fix)
			actions = append(actions, &CodeAction{Description: title.Localize(locale.FromContext(ctx)), ReferenceChanges: referenceTrackerFileChanges(tracker), FixID: fix, FixAllDescription: all.Localize(locale.FromContext(ctx))})
		}
		return actions, ctx.Err()
	},
	GetAllCodeActions: func(ctx context.Context, request *CodeFixContext) (*CombinedCodeActions, error) {
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		for _, diagnostic := range getAllDiagnostics(ctx, request.Program, request.SourceFile) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if !slices.Contains(referenceReturnValueCodes, diagnostic.Code()) {
				continue
			}
			item := *request
			item.ErrorCode = diagnostic.Code()
			item.Span = diagnostic.Loc()
			if info := referenceReturnValueGetInfo(&item, ch); info != nil {
				referenceApplyReturnValue(&item, tracker, info, request.FixID)
			}
		}
		return &CombinedCodeActions{ReferenceChanges: referenceTrackerFileChanges(tracker)}, ctx.Err()
	},
}

func referenceReturnValueTitles(fix string) (*diagnostics.Message, *diagnostics.Message) {
	switch fix {
	case "fixAddReturnStatement":
		return diagnostics.Add_a_return_statement, diagnostics.Add_all_missing_return_statement
	case "fixRemoveBracesFromArrowFunctionBody":
		return diagnostics.Remove_braces_from_arrow_function_body, diagnostics.Remove_braces_from_all_arrow_function_bodies_with_relevant_issues
	default:
		return diagnostics.Wrap_the_following_body_with_parentheses_which_should_be_an_object_literal, diagnostics.Wrap_all_object_literal_with_parentheses
	}
}

type referenceUnmatchedParamInfo struct{ host, signature, tag, name *ast.Node }

func referenceEffectiveJSDocHost(tag *ast.Node) *ast.Node {
	host := ast.GetJSDocHost(tag)
	if host == nil {
		return nil
	}
	if ast.IsExpressionStatement(host) && ast.IsBinaryExpression(host.Expression()) {
		expression := host.Expression().AsBinaryExpression()
		if ast.GetAssignmentDeclarationKind(host.Expression()) != ast.JSDeclarationKindNone && ast.IsBinaryExpression(expression.Right) {
			right := expression.Right.AsBinaryExpression()
			if right.OperatorToken.Kind == ast.KindBarBarToken || right.OperatorToken.Kind == ast.KindQuestionQuestionToken {
				return right.Right
			}
		}
		if expression.OperatorToken.Kind == ast.KindEqualsToken {
			right := expression.Right
			for ast.IsBinaryExpression(right) && right.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken {
				right = right.AsBinaryExpression().Right
			}
			return right
		}
	}
	if variable := referenceJSDocFirstVariable(host); variable != nil {
		if variable.Initializer() != nil {
			return variable.Initializer()
		}
		return variable
	}
	if (ast.IsPropertyDeclaration(host) || ast.IsPropertyAssignment(host)) && host.Initializer() != nil {
		return host.Initializer()
	}
	if ast.IsModuleDeclaration(host) && host.Body() != nil && ast.IsModuleDeclaration(host.Body()) {
		return host.Body()
	}
	return host
}
func referenceUnmatchedParamGetInfo(request *CodeFixContext) *referenceUnmatchedParamInfo {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil || token.Parent == nil || token.Parent.Kind != ast.KindJSDocParameterTag {
		return nil
	}
	tag := token.Parent
	if tag.Name() == nil || !ast.IsIdentifier(tag.Name()) {
		return nil
	}
	host := ast.GetJSDocHost(tag)
	effective := referenceEffectiveJSDocHost(tag)
	if host == nil || effective == nil {
		return nil
	}
	if !referenceJSDocNavigationSelects(request.SourceFile, tag.Parent, request.Span.Pos()) {
		return nil
	}
	signature := effective
	if ast.IsPropertySignatureDeclaration(effective) && effective.Type() != nil && ast.IsFunctionLike(effective.Type()) {
		signature = effective.Type()
	} else if !ast.IsFunctionLike(effective) {
		return nil
	}
	return &referenceUnmatchedParamInfo{host: host, signature: signature, tag: tag, name: tag.Name()}
}
func referenceUnmatchedDeleteTags(tracker *change.Tracker, file *ast.SourceFile, host *ast.Node, remove map[*ast.Node]bool) {
	tags := []*ast.Node{}
	for _, doc := range host.JSDoc(file) {
		if doc.AsJSDoc().Tags != nil {
			for _, tag := range doc.AsJSDoc().Tags.Nodes {
				if !remove[tag] {
					tags = append(tags, tag)
				}
			}
		}
	}
	tracker.ReplaceReferenceJSDocComment(file, host, tags)
}

var ReferenceUnmatchedParamProvider = &CodeFixProvider{Name: "fixUnmatchedParameter", ErrorCodes: []int32{diagnostics.JSDoc_param_tag_has_name_0_but_there_is_no_parameter_with_that_name.Code()}, FixIds: []string{"deleteUnmatchedParameter", "renameUnmatchedParameter"},
	GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		info := referenceUnmatchedParamGetInfo(request)
		if info == nil {
			return nil, ctx.Err()
		}
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		referenceUnmatchedDeleteTags(tracker, request.SourceFile, info.host, map[*ast.Node]bool{info.tag: true})
		actions := []*CodeAction{{ReferenceFixName: "deleteUnmatchedParameter", Description: diagnostics.Delete_unused_param_tag_0.Localize(locale.FromContext(ctx), scanner.GetTextOfNode(info.name)), ReferenceChanges: referenceTrackerFileChanges(tracker), FixID: "deleteUnmatchedParameter", FixAllDescription: diagnostics.Delete_all_unused_param_tags.Localize(locale.FromContext(ctx))}}
		tags := referenceJSDocTags(info.signature)
		names := map[string]bool{}
		for _, tag := range tags {
			if tag.Kind == ast.KindJSDocParameterTag && tag.Name() != nil && ast.IsIdentifier(tag.Name()) {
				names[tag.Name().Text()] = true
			}
		}
		for _, parameter := range info.signature.Parameters() {
			name := parameter.Name()
			if name == nil || !ast.IsIdentifier(name) || names[name.Text()] {
				continue
			}
			tracker = change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
			old := info.tag.AsJSDocParameterOrPropertyTag()
			changed := tracker.UpdateJSDocParameterOrPropertyTag(old, old.TagName, tracker.NewIdentifier(scanner.GetTextOfNode(name)), old.IsBracketed, old.TypeExpression, old.IsNameFirst, old.Comment)
			replacements := slices.Clone(tags)
			for i, tag := range replacements {
				if tag == info.tag {
					replacements[i] = changed
				}
			}
			tracker.ReplaceReferenceJSDocComment(request.SourceFile, info.host, replacements)
			actions = append(actions, &CodeAction{ReferenceFixName: "renameUnmatchedParameter", Description: diagnostics.Rename_param_tag_name_0_to_1.Localize(locale.FromContext(ctx), scanner.GetTextOfNode(info.name), scanner.GetTextOfNode(name)), ReferenceChanges: referenceTrackerFileChanges(tracker)})
			break
		}
		return actions, ctx.Err()
	},
	GetAllCodeActions: func(ctx context.Context, request *CodeFixContext) (*CombinedCodeActions, error) {
		order := []*ast.Node{}
		groups := map[*ast.Node]map[*ast.Node]bool{}
		for _, diagnostic := range getAllDiagnostics(ctx, request.Program, request.SourceFile) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if diagnostic.Code() != diagnostics.JSDoc_param_tag_has_name_0_but_there_is_no_parameter_with_that_name.Code() {
				continue
			}
			item := *request
			item.Span = diagnostic.Loc()
			if info := referenceUnmatchedParamGetInfo(&item); info != nil {
				if groups[info.signature] == nil {
					groups[info.signature] = map[*ast.Node]bool{}
					order = append(order, info.signature)
				}
				groups[info.signature][info.tag] = true
			}
		}
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		if request.FixID == "deleteUnmatchedParameter" {
			for _, signature := range order {
				referenceUnmatchedDeleteTags(tracker, ast.GetSourceFileOfNode(signature), signature, groups[signature])
			}
		}
		return &CombinedCodeActions{ReferenceChanges: referenceTrackerFileChanges(tracker)}, ctx.Err()
	},
}

// Native documentation attachment does not follow assignment hosts. Editor
// diagnostics use the pinned tag attachment rules without changing Check.
func referenceUnmatchedParamDiagnostics(ctx context.Context, program *compiler.Program, file *ast.SourceFile, items []*ast.Diagnostic) []*ast.Diagnostic {
	if ast.IsSourceFileJS(file) {
		return items
	}
	ch, done := program.GetTypeCheckerForFile(ctx, file)
	defer done()
	var visit func(*ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ctx.Err() != nil {
			return true
		}
		if ast.IsFunctionLikeDeclaration(node) && !ch.ReferenceContainsArguments(node) {
			names := map[string]bool{}
			bindings := map[int]bool{}
			for i, parameter := range node.Parameters() {
				if name := parameter.Name(); name != nil {
					if ast.IsIdentifier(name) {
						names[name.Text()] = true
					} else if ast.IsBindingPattern(name) {
						bindings[i] = true
					}
				}
			}
			index := 0
			for _, tag := range referenceJSDocTags(node) {
				if tag.Kind != ast.KindJSDocParameterTag {
					continue
				}
				name := tag.Name()
				slot := index
				index++
				if name == nil || !ast.IsIdentifier(name) || name.Text() == "" || bindings[slot] || names[name.Text()] || tag.AsJSDocParameterOrPropertyTag().IsNameFirst {
					continue
				}
				span := scanner.GetErrorRangeForNode(file, name)
				exists := false
				for _, item := range items {
					if item.Code() == diagnostics.JSDoc_param_tag_has_name_0_but_there_is_no_parameter_with_that_name.Code() && item.Loc() == span {
						exists = true
						break
					}
				}
				if !exists {
					item := ast.NewDiagnostic(file, span, diagnostics.JSDoc_param_tag_has_name_0_but_there_is_no_parameter_with_that_name, name.Text())
					item.SetCategory(diagnostics.CategorySuggestion)
					items = append(items, item)
				}
			}
		}
		return node.ForEachChild(visit)
	}
	visit(file.AsNode())
	return items
}

type referenceAccessorInfo struct {
	container, declaration, fieldName, accessorName, typ *ast.Node
	originalName                                         string
	static, readonly, rename                             bool
}

func referenceAccessorUniqueName(file *ast.SourceFile, base string) string {
	candidate := base
	for index := 1; ; index++ {
		if _, exists := file.Identifiers[candidate]; !exists {
			return candidate
		}
		candidate = fmt.Sprintf("%s_%d", base, index)
	}
}
func referenceAccessorConvertible(file *ast.SourceFile, ch *checker.Checker, tracker *change.Tracker, start, end int, empty bool) *referenceAccessorInfo {
	token := astnav.GetTokenAtPosition(file, start)
	if token == nil || token.Parent == nil {
		return nil
	}
	declaration := ast.FindAncestor(token.Parent, func(node *ast.Node) bool {
		return ast.IsPropertyDeclaration(node) || ast.IsPropertyAssignment(node) || (node.Parent != nil && ast.IsParameterPropertyDeclaration(node, node.Parent))
	})
	if declaration == nil || declaration.Name() == nil {
		return nil
	}
	name := declaration.Name()
	if !(start == end && empty) && max(scanner.GetTokenPosOfNode(name, file, false), start) >= min(name.End(), end) {
		return nil
	}
	if !ast.IsIdentifier(name) && !ast.IsStringLiteral(name) {
		return nil
	}
	meaning := ast.ModifierFlagsAccessibilityModifier | ast.ModifierFlagsStatic | ast.ModifierFlagsReadonly
	flags := declaration.ModifierFlags()
	if (flags&ast.ModifierFlagsModifier)|meaning != meaning {
		return nil
	}
	text := name.Text()
	underscore := strings.HasPrefix(text, "_")
	field, accessor := referenceAccessorUniqueName(file, "_"+text), text
	if underscore {
		field = text
		accessor = referenceAccessorUniqueName(file, strings.TrimPrefix(text, "_"))
	}
	makeName := func(text string) *ast.Node {
		if ast.IsIdentifier(name) {
			return tracker.NewIdentifier(text)
		}
		return tracker.NewStringLiteral(text, ast.TokenFlagsNone)
	}
	container := declaration.Parent
	if ast.IsParameterDeclaration(declaration) {
		container = declaration.Parent.Parent
	}
	if container == nil || (!ast.IsClassLike(container) && !ast.IsObjectLiteralExpression(container)) {
		return nil
	}
	var typ *ast.Node
	if !ast.IsPropertyAssignment(declaration) {
		typ = declaration.Type()
	}
	if ast.IsPropertyDeclaration(declaration) && typ != nil && declaration.AsPropertyDeclaration().PostfixToken != nil && declaration.AsPropertyDeclaration().PostfixToken.Kind == ast.KindQuestionToken && !ch.IsTypeAssignableTo(ch.GetUndefinedType(), ch.GetTypeFromTypeNode(typ)) {
		types := []*ast.Node{typ}
		if ast.IsUnionTypeNode(typ) {
			types = slices.Clone(typ.AsUnionTypeNode().Types.Nodes)
		}
		types = append(types, tracker.NewKeywordTypeNode(ast.KindUndefinedKeyword))
		typ = tracker.NewUnionTypeNode(tracker.NewNodeList(types))
	}
	return &referenceAccessorInfo{container: container, declaration: declaration, fieldName: makeName(field), accessorName: makeName(accessor), typ: typ, originalName: text, static: flags&ast.ModifierFlagsStatic != 0, readonly: flags&ast.ModifierFlagsReadonly != 0, rename: underscore}
}
func referenceAccessorAccess(tracker *change.Tracker, info *referenceAccessorInfo) *ast.Node {
	head := tracker.NewKeywordExpression(ast.KindThisKeyword)
	if info.static {
		if info.container.Name() == nil {
			panic(&change.InvalidEdits{Reason: "static accessor has no class name"})
		}
		head = tracker.NewIdentifier(info.container.Name().Text())
	}
	if ast.IsIdentifier(info.fieldName) {
		return tracker.NewPropertyAccessExpression(head, nil, tracker.NewIdentifier(info.fieldName.Text()), ast.NodeFlagsNone)
	}
	return tracker.NewElementAccessExpression(head, nil, tracker.NewStringLiteral(info.fieldName.Text(), ast.TokenFlagsNone), ast.NodeFlagsNone)
}

// GetReferenceAccessorChanges reuses the pinned accessor generator within a borrowed checker request.
func (l *LanguageService) GetReferenceAccessorChanges(ctx context.Context, ch *checker.Checker, file *ast.SourceFile, start, end int) ([]ReferenceFileCodeChanges, error) {
	request := &CodeFixContext{SourceFile: file, Span: core.NewTextRange(start, end), Program: l.GetProgram(), LS: l}
	return referenceGenerateAccessors(ctx, request, ch, file, start, end)
}

func referenceGenerateAccessors(ctx context.Context, request *CodeFixContext, ch *checker.Checker, file *ast.SourceFile, start, end int) ([]ReferenceFileCodeChanges, error) {
	tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
	info := referenceAccessorConvertible(file, ch, tracker, start, end, true)
	if info == nil {
		return nil, ctx.Err()
	}
	for _, node := range []*ast.Node{info.fieldName, info.accessorName, info.declaration, info.container} {
		referenceSuppressReturnTrivia(tracker, node)
	}
	var accessorModifiers, fieldModifiers *ast.ModifierList
	if ast.IsClassLike(info.container) {
		flags := info.declaration.ModifierFlags()
		getterFlags := flags &^ (ast.ModifierFlagsReadonly | ast.ModifierFlagsPrivate)
		if getterFlags&ast.ModifierFlagsProtected == 0 {
			getterFlags |= ast.ModifierFlagsPublic
		}
		fieldFlags := flags&^(ast.ModifierFlagsPublic|ast.ModifierFlagsProtected) | ast.ModifierFlagsPrivate
		accessorModifiers = tracker.NewModifierList(ast.CreateModifiersFromModifierFlags(getterFlags, tracker.NewToken))
		modifiers := []*ast.Node{}
		if original := info.declaration.Modifiers(); original != nil {
			for _, modifier := range original.Nodes {
				if ast.IsDecorator(modifier) {
					modifiers = append(modifiers, modifier)
				}
			}
		}
		modifiers = append(modifiers, ast.CreateModifiersFromModifierFlags(fieldFlags, tracker.NewToken)...)
		fieldModifiers = tracker.NewModifierList(modifiers)
	}
	declaration := info.declaration
	if ast.IsPropertyDeclaration(declaration) {
		property := declaration.AsPropertyDeclaration()
		replacement := tracker.UpdatePropertyDeclaration(property, fieldModifiers, info.fieldName, property.PostfixToken, info.typ, property.Initializer)
		tracker.ReplaceNode(file, declaration, replacement, nil)
	} else if ast.IsPropertyAssignment(declaration) {
		replacement := tracker.NewPropertyAssignment(nil, info.fieldName, nil, nil, declaration.Initializer())
		tracker.ReplaceReferencePropertyAssignment(file, declaration, replacement)
	} else {
		parameter := declaration.AsParameterDeclaration()
		replacement := tracker.UpdateParameterDeclaration(parameter, fieldModifiers, parameter.DotDotDotToken, info.fieldName, parameter.QuestionToken, parameter.Type, parameter.Initializer)
		tracker.ReplaceNode(file, declaration, replacement, nil)
	}
	insert := func(accessor *ast.Node) {
		referenceSuppressReturnTrivia(tracker, accessor)
		if ast.IsParameterDeclaration(declaration) {
			tracker.InsertMemberAtStart(file, info.container, accessor)
		} else if ast.IsPropertyAssignment(declaration) {
			tracker.InsertReferenceNodeAfterComma(file, declaration, accessor)
		} else {
			tracker.InsertNodeAfter(file, declaration, accessor)
		}
	}
	freshName := func() *ast.Node {
		if ast.IsIdentifier(info.accessorName) {
			return tracker.NewIdentifier(info.accessorName.Text())
		}
		return tracker.NewStringLiteral(info.accessorName.Text(), ast.TokenFlagsNone)
	}
	freshModifiers := func() *ast.ModifierList {
		if accessorModifiers == nil {
			return nil
		}
		nodes := []*ast.Node{}
		for _, modifier := range accessorModifiers.Nodes {
			nodes = append(nodes, tracker.NewToken(modifier.Kind))
		}
		return tracker.NewModifierList(nodes)
	}
	getter := tracker.NewGetAccessorDeclaration(freshModifiers(), freshName(), nil, tracker.NewNodeList(nil), info.typ, nil, tracker.NewBlock(tracker.NewNodeList([]*ast.Node{tracker.NewReturnStatement(referenceAccessorAccess(tracker, info))}), true))
	insert(getter)
	if info.readonly {
		if constructor := ast.GetFirstConstructorWithBody(info.container); constructor != nil && constructor.Body() != nil {
			var visit func(*ast.Node) bool
			visit = func(node *ast.Node) bool {
				if ast.IsElementAccessExpression(node) && node.Expression().Kind == ast.KindThisKeyword && ast.IsStringLiteral(node.AsElementAccessExpression().ArgumentExpression) && node.AsElementAccessExpression().ArgumentExpression.Text() == info.originalName && ast.IsWriteAccess(node) {
					tracker.ReplaceNode(file, node.AsElementAccessExpression().ArgumentExpression, tracker.NewStringLiteral(info.fieldName.Text(), ast.TokenFlagsNone), nil)
				}
				if ast.IsPropertyAccessExpression(node) && node.Expression().Kind == ast.KindThisKeyword && node.Name().Text() == info.originalName && ast.IsWriteAccess(node) {
					tracker.ReplaceNode(file, node.Name(), tracker.NewIdentifier(info.fieldName.Text()), nil)
				}
				if !ast.IsFunctionLike(node) && !ast.IsClassLike(node) {
					node.ForEachChild(visit)
				}
				return false
			}
			constructor.Body().ForEachChild(visit)
		}
	} else {
		parameter := tracker.NewParameterDeclaration(nil, nil, tracker.NewIdentifier("value"), nil, info.typ, nil)
		assignment := tracker.NewBinaryExpression(nil, referenceAccessorAccess(tracker, info), nil, tracker.NewToken(ast.KindEqualsToken), tracker.NewIdentifier("value"))
		setter := tracker.NewSetAccessorDeclaration(freshModifiers(), freshName(), nil, tracker.NewNodeList([]*ast.Node{parameter}), nil, nil, tracker.NewBlock(tracker.NewNodeList([]*ast.Node{tracker.NewExpressionStatement(assignment)}), true))
		insert(setter)
	}
	return referenceTrackerFileChanges(tracker), ctx.Err()
}

var referenceOverrideAccessorCodes = []int32{diagnostics.X_0_is_defined_as_an_accessor_in_class_1_but_is_overridden_here_in_2_as_an_instance_property.Code(), diagnostics.X_0_is_defined_as_a_property_in_class_1_but_is_overridden_here_in_2_as_an_accessor.Code()}

func referenceOverrideAccessorChanges(ctx context.Context, request *CodeFixContext, ch *checker.Checker) ([]ReferenceFileCodeChanges, error) {
	file, start, end := request.SourceFile, request.Span.Pos(), request.Span.End()
	if request.ErrorCode == referenceOverrideAccessorCodes[1] {
		token := astnav.GetTokenAtPosition(file, start)
		if token == nil || token.Parent == nil || ast.IsComputedPropertyName(token.Parent) || !ast.IsAccessor(token.Parent) {
			return nil, ctx.Err()
		}
		accessor := token.Parent
		if accessor.Parent == nil || !ast.IsClassLike(accessor.Parent) {
			return nil, ctx.Err()
		}
		base := ast.GetClassExtendsHeritageElement(accessor.Parent)
		if base == nil {
			return nil, ctx.Err()
		}
		expression := ast.SkipParentheses(base.Expression())
		symbol := ch.GetSymbolAtLocation(expression)
		if ast.IsClassExpression(expression) {
			symbol = expression.Symbol()
		}
		if symbol == nil {
			return nil, ctx.Err()
		}
		name := accessor.Name()
		if name == nil {
			return nil, ctx.Err()
		}
		property := ch.GetPropertyOfType(ch.GetDeclaredTypeOfSymbol(symbol), name.Text())
		if property == nil || property.ValueDeclaration == nil {
			return nil, ctx.Err()
		}
		declaration := property.ValueDeclaration
		file = ast.GetSourceFileOfNode(declaration)
		start, end = declaration.Pos(), declaration.End()
	} else if request.ErrorCode != referenceOverrideAccessorCodes[0] {
		return nil, ctx.Err()
	}
	return referenceGenerateAccessors(ctx, request, ch, file, start, end)
}

var ReferenceOverrideAccessorProvider = &CodeFixProvider{Name: "fixPropertyOverrideAccessor", ErrorCodes: referenceOverrideAccessorCodes, FixIds: []string{"fixPropertyOverrideAccessor"},
	GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		files, err := referenceOverrideAccessorChanges(ctx, request, ch)
		if err != nil || len(files) == 0 {
			return nil, err
		}
		return []*CodeAction{{Description: diagnostics.Generate_get_and_set_accessors.Localize(locale.FromContext(ctx)), ReferenceChanges: files, FixID: "fixPropertyOverrideAccessor", FixAllDescription: diagnostics.Generate_get_and_set_accessors_for_all_overriding_properties.Localize(locale.FromContext(ctx))}}, ctx.Err()
	},
	GetAllCodeActions: func(ctx context.Context, request *CodeFixContext) (*CombinedCodeActions, error) {
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		for _, diagnostic := range getAllDiagnostics(ctx, request.Program, request.SourceFile) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if !slices.Contains(referenceOverrideAccessorCodes, diagnostic.Code()) {
				continue
			}
			item := *request
			item.ErrorCode = diagnostic.Code()
			item.Span = diagnostic.Loc()
			files, err := referenceOverrideAccessorChanges(ctx, &item, ch)
			if err != nil {
				return nil, err
			}
			for _, file := range files {
				if file.FileName != request.SourceFile.FileName() {
					return nil, &change.InvalidEdits{Reason: "accessor fix-all source file mismatch"}
				}
				for _, edit := range file.TextChanges {
					tracker.ReplaceRangeWithText(request.SourceFile, edit.Range, edit.NewText)
				}
			}
		}
		return &CombinedCodeActions{ReferenceChanges: referenceTrackerFileChanges(tracker)}, ctx.Err()
	},
}

func referenceModuleIdentifier(specifier string) string {
	base := tspath.GetBaseFileName(strings.TrimSuffix(tspath.RemoveFileExtension(specifier), "/index"))
	var result strings.Builder
	lastValid := true
	for index, value := range base {
		valid := value <= 0xffff && scanner.IsIdentifierPart(value)
		if index == 0 {
			valid = value <= 0xffff && scanner.IsIdentifierStart(value)
		}
		if valid {
			text := string(value)
			if index != 0 && !lastValid {
				text = stringutil.ToUpperJS(text)
			}
			result.WriteString(text)
		}
		lastValid = valid
	}
	name := result.String()
	if name == "" || lsutil.IsNonContextualKeyword(scanner.StringToToken(name)) {
		return "_" + name
	}
	return name
}
func referenceNamedImportsToNamespace(request *CodeFixContext, ch *checker.Checker, tracker *change.Tracker, named *ast.Node, useDefault bool) {
	declaration := named.Parent.Parent
	var moduleSpecifier *ast.Node
	if ast.IsImportDeclaration(declaration) {
		moduleSpecifier = declaration.ModuleSpecifier()
	} else if declaration.Kind == ast.KindJSDocImportTag {
		moduleSpecifier = declaration.AsJSDocImportTag().ModuleSpecifier
	}
	elements := named.AsNamedImports().Elements.Nodes
	symbols := map[*ast.Symbol]bool{}
	for _, element := range elements {
		if symbol := ch.GetSymbolAtLocation(element.Name()); symbol != nil {
			symbols[symbol] = true
		}
	}
	preferred := "module"
	if moduleSpecifier != nil && ast.IsStringLiteral(moduleSpecifier) {
		preferred = referenceModuleIdentifier(moduleSpecifier.Text())
	}
	conflict := false
	for _, element := range elements {
		for _, reference := range referenceUnusedReferences(ch, request.SourceFile, element.Name()) {
			if symbol := ch.ResolveName(preferred, reference, ast.SymbolFlagsAll, true); symbol != nil && (!symbols[symbol] || (reference.Parent != nil && ast.IsExportSpecifier(reference.Parent))) {
				conflict = true
				break
			}
		}
		if conflict {
			break
		}
	}
	namespace := preferred
	if conflict {
		namespace = referenceAccessorUniqueName(request.SourceFile, preferred)
	}
	needed := []*ast.Node{}
	seen := map[*ast.Node]bool{}
	for _, element := range elements {
		specifier := element.AsImportSpecifier()
		property := specifier.PropertyName
		if property == nil {
			property = specifier.Name()
		}
		for _, reference := range referenceUnusedReferences(ch, request.SourceFile, specifier.Name()) {
			var access *ast.Node
			if ast.IsStringLiteral(property) {
				access = tracker.NewElementAccessExpression(tracker.NewIdentifier(namespace), nil, tracker.NewStringLiteral(property.Text(), ast.TokenFlagsNone), ast.NodeFlagsNone)
			} else {
				access = tracker.NewPropertyAccessExpression(tracker.NewIdentifier(namespace), nil, tracker.NewIdentifier(property.Text()), ast.NodeFlagsNone)
			}
			if reference.Parent != nil && ast.IsShorthandPropertyAssignment(reference.Parent) {
				tracker.ReplaceNode(request.SourceFile, reference.Parent, tracker.NewPropertyAssignment(nil, tracker.NewIdentifier(reference.Text()), nil, nil, access), nil)
			} else if reference.Parent != nil && ast.IsExportSpecifier(reference.Parent) {
				if !seen[element] {
					seen[element] = true
					needed = append(needed, element)
				}
			} else {
				tracker.ReplaceNode(request.SourceFile, reference, access, nil)
			}
		}
	}
	replacement := tracker.NewNamespaceImport(tracker.NewIdentifier(namespace))
	if useDefault {
		replacement = tracker.NewIdentifier(namespace)
	}
	tracker.ReplaceNode(request.SourceFile, named, replacement, nil)
	if len(needed) > 0 && ast.IsImportDeclaration(declaration) {
		names := []*ast.Node{}
		for _, element := range needed {
			specifier := element.AsImportSpecifier()
			var property *ast.Node
			if specifier.PropertyName != nil {
				if ast.IsStringLiteral(specifier.PropertyName) {
					property = tracker.NewStringLiteral(specifier.PropertyName.Text(), ast.TokenFlagsNone)
				} else {
					property = tracker.NewIdentifier(specifier.PropertyName.Text())
				}
			}
			names = append(names, tracker.NewImportSpecifier(specifier.IsTypeOnly, property, tracker.NewIdentifier(specifier.Name().Text())))
		}
		extra := tracker.NewImportDeclaration(nil, tracker.NewImportClause(ast.KindUnknown, nil, tracker.NewNamedImports(tracker.NewNodeList(names))), moduleSpecifier, nil)
		tracker.InsertNodeAfter(request.SourceFile, declaration, extra)
	}
}
func referenceDecoratorMetadataImport(request *CodeFixContext, ch *checker.Checker) *ast.Node {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil || !ast.IsIdentifier(token) || token.Parent == nil || token.Parent.Kind != ast.KindTypeReference {
		return nil
	}
	symbol := ch.GetSymbolAtLocation(token)
	if symbol == nil {
		return nil
	}
	for _, declaration := range symbol.Declarations {
		if ast.IsImportClause(declaration) || ast.IsImportSpecifier(declaration) || ast.IsImportEqualsDeclaration(declaration) {
			return declaration
		}
	}
	return nil
}
func referenceDecoratorMetadataTypeOnly(request *CodeFixContext, ch *checker.Checker, tracker *change.Tracker, declaration *ast.Node) {
	if ast.IsImportEqualsDeclaration(declaration) {
		tracker.InsertText(request.SourceFile, request.LS.converters.PositionToLineAndCharacter(request.SourceFile, core.TextPos(scanner.GetTokenPosOfNode(declaration.Name(), request.SourceFile, false))), "type ")
		return
	}
	clause := declaration
	if ast.IsImportSpecifier(declaration) {
		clause = declaration.Parent.Parent
	}
	data := clause.AsImportClause()
	if data.Name() != nil && data.NamedBindings != nil {
		return
	}
	imports := []*ast.Node{}
	if data.Name() != nil {
		imports = append(imports, clause)
	}
	if data.NamedBindings != nil {
		if ast.IsNamespaceImport(data.NamedBindings) {
			imports = append(imports, data.NamedBindings)
		} else {
			imports = append(imports, data.NamedBindings.AsNamedImports().Elements.Nodes...)
		}
	}
	for _, item := range imports {
		symbol := item.Symbol()
		if symbol == nil {
			symbol = ch.GetSymbolAtLocation(item.Name())
		}
		if symbol != nil {
			target := ch.SkipAlias(symbol)
			if target != nil && target.Flags&ast.SymbolFlagsValue != 0 {
				return
			}
		}
	}
	tracker.InsertText(request.SourceFile, request.LS.converters.PositionToLineAndCharacter(request.SourceFile, core.TextPos(scanner.GetTokenPosOfNode(clause, request.SourceFile, false))), "type ")
}

var ReferenceDecoratorMetadataProvider = &CodeFixProvider{Name: "fixUnreferenceableDecoratorMetadata", ErrorCodes: []int32{diagnostics.A_type_referenced_in_a_decorated_signature_must_be_imported_with_import_type_or_a_namespace_import_when_isolatedModules_and_emitDecoratorMetadata_are_enabled.Code()}, FixIds: []string{"fixUnreferenceableDecoratorMetadata"},
	GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		declaration := referenceDecoratorMetadataImport(request, ch)
		if declaration == nil {
			return nil, ctx.Err()
		}
		actions := []*CodeAction{}
		if ast.IsImportSpecifier(declaration) {
			named := declaration.Parent
			module := named.Parent.Parent.ModuleSpecifier()
			useDefault := false
			if referenceAllowsSyntheticDefaults(request.Program.Options()) && module != nil {
				if external := ch.ResolveExternalModuleName(module); external != nil {
					useDefault = external != ch.ResolveExternalModuleSymbol(external)
				}
			}
			tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
			referenceNamedImportsToNamespace(request, ch, tracker, named, useDefault)
			if files := referenceTrackerFileChanges(tracker); len(files) > 0 {
				actions = append(actions, &CodeAction{Description: diagnostics.Convert_named_imports_to_namespace_import.Localize(locale.FromContext(ctx)), ReferenceChanges: files})
			}
		}
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		referenceDecoratorMetadataTypeOnly(request, ch, tracker, declaration)
		if files := referenceTrackerFileChanges(tracker); len(files) > 0 {
			actions = append(actions, &CodeAction{Description: diagnostics.Use_import_type.Localize(locale.FromContext(ctx)), ReferenceChanges: files})
		}
		return actions, ctx.Err()
	},
}

func referenceClassSymbolMembers(table ast.SymbolTable) []*ast.Symbol {
	members := make([]*ast.Symbol, 0, len(table))
	for _, symbol := range table {
		members = append(members, symbol)
	}
	// TypeScript's symbol table preserves declaration insertion order. The native
	// table is a Go map, so recover that order from the bound declarations.
	slices.SortStableFunc(members, func(a, b *ast.Symbol) int {
		ap, bp := int(^uint(0)>>1), int(^uint(0)>>1)
		if len(a.Declarations) > 0 {
			ap = a.Declarations[0].Pos()
		}
		if len(b.Declarations) > 0 {
			bp = b.Declarations[0].Pos()
		}
		if ap < bp {
			return -1
		}
		if ap > bp {
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return members
}
func referenceClassCopyLeading(request *CodeFixContext, tracker *change.Tracker, from, to *ast.Node) {
	text := request.SourceFile.Text()
	if from == nil || from.Pos() < 0 {
		return
	}
	for comment := range scanner.GetLeadingCommentRanges(tracker.NodeFactory, text, from.Pos()) {
		start, end := comment.Pos()+2, comment.End()
		if comment.Kind == ast.KindMultiLineCommentTrivia {
			end -= 2
		}
		if start <= end && end <= len(text) {
			tracker.AddSyntheticLeadingComment(to, comment.Kind, text[start:end], comment.HasTrailingNewLine)
		}
	}
}
func referenceClassModifier(tracker *change.Tracker, node *ast.Node, kind ast.Kind) []*ast.Node {
	result := []*ast.Node{}
	if node != nil && node.Modifiers() != nil {
		for _, modifier := range node.Modifiers().Nodes {
			if modifier.Kind == kind {
				result = append(result, modifier)
			}
		}
	}
	return result
}
func referenceClassConstructorAssignment(node *ast.Node) bool {
	return node != nil && node.Name() != nil && ast.IsIdentifier(node.Name()) && node.Name().Text() == "constructor"
}
func referenceClassPropertyName(request *CodeFixContext, tracker *change.Tracker, node *ast.Node) *ast.Node {
	if ast.IsPropertyAccessExpression(node) {
		return node.Name()
	}
	if !ast.IsElementAccessExpression(node) {
		return nil
	}
	argument := node.AsElementAccessExpression().ArgumentExpression
	if argument == nil {
		return nil
	}
	if ast.IsNumericLiteral(argument) {
		return argument
	}
	if ast.IsStringLiteralLike(argument) {
		if scanner.IsIdentifierText(argument.Text(), core.LanguageVariantStandard) {
			return tracker.NewIdentifier(argument.Text())
		}
		if argument.Kind == ast.KindNoSubstitutionTemplateLiteral {
			return tracker.NewStringLiteral(argument.Text(), core.IfElse(lsutil.GetQuotePreference(request.SourceFile, request.LS.UserPreferences()) == lsutil.QuotePreferenceSingle, ast.TokenFlagsSingleQuote, ast.TokenFlagsNone))
		}
		return argument
	}
	return nil
}
func referenceConvertFunctionClass(request *CodeFixContext, ch *checker.Checker, tracker *change.Tracker) {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil {
		return
	}
	symbol := ch.GetSymbolAtLocation(token)
	if symbol == nil || symbol.ValueDeclaration == nil || symbol.Flags&(ast.SymbolFlagsFunction|ast.SymbolFlagsVariable) == 0 {
		return
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsFunctionDeclaration(declaration) && !ast.IsFunctionExpression(declaration) && !ast.IsVariableDeclaration(declaration) {
		return
	}
	initializer := declaration
	if ast.IsVariableDeclaration(declaration) {
		initializer = declaration.Initializer()
		if initializer == nil || !ast.IsFunctionExpression(initializer) || !ast.IsIdentifier(declaration.Name()) {
			return
		}
	}
	memberSymbol := symbol
	if ast.IsVariableDeclaration(declaration) && initializer.Symbol() != nil {
		memberSymbol = initializer.Symbol()
	}
	members := []*ast.Node{}
	var createMember func(*ast.Symbol, bool)
	createMember = func(member *ast.Symbol, static bool) {
		if member == nil {
			return
		}
		flags := member.Flags
		target := member.ValueDeclaration
		var assignment *ast.Node
		// Native expando declarations are binary assignments with Property flags;
		// TS6 records their access expression and marks function values as methods.
		if target != nil && ast.IsBinaryExpression(target) {
			assignment = target
			target = assignment.AsBinaryExpression().Left
			if flags&ast.SymbolFlagsAssignment != 0 && !ast.HasDynamicName(assignment) && assignment.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken && ast.IsFunctionLike(assignment.AsBinaryExpression().Right) {
				flags |= ast.SymbolFlagsMethod
			}
		} else if target != nil {
			assignment = target.Parent
		}
		if flags&(ast.SymbolFlagsMethod|ast.SymbolFlagsObjectLiteral) == 0 || target == nil || assignment == nil || !ast.IsBinaryExpression(assignment) {
			return
		}
		expression := assignment.AsBinaryExpression().Right
		convertible := false
		if ast.IsAccessExpression(target) {
			convertible = ast.IsPropertyAccessExpression(target) && referenceClassConstructorAssignment(target) || expression != nil && ast.IsFunctionLike(expression)
		} else if ast.IsObjectLiteralExpression(target) {
			convertible = true
			for _, property := range target.Properties() {
				if ast.IsMethodDeclaration(property) || (ast.IsGetAccessorDeclaration(property) || ast.IsSetAccessorDeclaration(property)) || referenceClassConstructorAssignment(property) {
					continue
				}
				if ast.IsPropertyAssignment(property) && property.Initializer() != nil && ast.IsFunctionExpression(property.Initializer()) && property.Name() != nil {
					continue
				}
				convertible = false
				break
			}
		}
		if !convertible {
			return
		}
		for _, created := range members {
			if name := created.Name(); name != nil && ast.IsIdentifier(name) && name.Text() == ast.SymbolName(member) {
				return
			}
		}
		deleted := assignment
		if assignment.Parent != nil && ast.IsExpressionStatement(assignment.Parent) {
			deleted = assignment.Parent
		}
		tracker.Delete(request.SourceFile, deleted)
		modifiers := func(source *ast.Node) *ast.ModifierList {
			nodes := []*ast.Node{}
			if static {
				nodes = append(nodes, tracker.NewModifier(ast.KindStaticKeyword))
			}
			nodes = append(nodes, referenceClassModifier(tracker, source, ast.KindAsyncKeyword)...)
			if len(nodes) == 0 {
				return nil
			}
			return tracker.NewModifierList(nodes)
		}
		addFunction := func(function, name *ast.Node) {
			body := function.Body()
			if ast.IsArrowFunction(function) && body != nil && !ast.IsBlock(body) {
				body = tracker.NewBlock(tracker.NewNodeList([]*ast.Node{tracker.NewReturnStatement(body)}), false)
			}
			method := tracker.NewMethodDeclaration(modifiers(function), nil, name, nil, nil, tracker.NewNodeList(function.Parameters()), nil, nil, body)
			referenceClassCopyLeading(request, tracker, assignment, method)
			members = append(members, method)
		}
		if expression == nil {
			members = append(members, tracker.NewPropertyDeclaration(modifiers(nil), tracker.NewIdentifier(member.Name), nil, nil, nil))
			return
		}
		if ast.IsAccessExpression(target) && (ast.IsFunctionExpression(expression) || ast.IsArrowFunction(expression)) {
			if name := referenceClassPropertyName(request, tracker, target); name != nil {
				addFunction(expression, name)
			}
			return
		}
		if ast.IsObjectLiteralExpression(expression) {
			for _, property := range expression.Properties() {
				if ast.IsMethodDeclaration(property) || (ast.IsGetAccessorDeclaration(property) || ast.IsSetAccessorDeclaration(property)) {
					members = append(members, property)
				}
				if ast.IsPropertyAssignment(property) && property.Initializer() != nil && ast.IsFunctionExpression(property.Initializer()) {
					addFunction(property.Initializer(), property.Name())
				}
			}
			return
		}
		if ast.IsSourceFileJS(request.SourceFile) || !ast.IsPropertyAccessExpression(target) {
			return
		}
		property := tracker.NewPropertyDeclaration(modifiers(nil), target.Name(), nil, nil, expression)
		referenceClassCopyLeading(request, tracker, assignment.Parent, property)
		members = append(members, property)
	}
	for _, member := range referenceClassSymbolMembers(memberSymbol.Exports) {
		if member.Name == "prototype" && len(member.Declarations) > 0 {
			first := member.Declarations[0]
			if len(member.Declarations) == 1 && ast.IsPropertyAccessExpression(first) && first.Parent != nil && ast.IsBinaryExpression(first.Parent) && first.Parent.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken && ast.IsObjectLiteralExpression(first.Parent.AsBinaryExpression().Right) {
				createMember(first.Parent.AsBinaryExpression().Right.Symbol(), false)
			}
		} else {
			createMember(member, true)
		}
	}
	for _, member := range referenceClassSymbolMembers(memberSymbol.Members) {
		if member.Name == "constructor" && member.ValueDeclaration != nil {
			prototype := memberSymbol.Exports["prototype"]
			drop := false
			if prototype != nil && len(prototype.Declarations) > 0 {
				assignment := prototype.Declarations[0].Parent
				if assignment != nil && ast.IsBinaryExpression(assignment) && ast.IsObjectLiteralExpression(assignment.AsBinaryExpression().Right) {
					for _, property := range assignment.AsBinaryExpression().Right.Properties() {
						if referenceClassConstructorAssignment(property) {
							drop = true
							break
						}
					}
				}
			}
			if !drop && member.ValueDeclaration.Parent != nil {
				tracker.Delete(request.SourceFile, member.ValueDeclaration.Parent)
			}
			continue
		}
		createMember(member, false)
	}
	if initializer.Body() != nil {
		ctor := tracker.NewConstructorDeclaration(nil, nil, tracker.NewNodeList(initializer.Parameters()), nil, nil, initializer.Body())
		members = append([]*ast.Node{ctor}, members...)
	}
	modifierSource := declaration
	if ast.IsVariableDeclaration(declaration) {
		modifierSource = declaration.Parent.Parent
	}
	nodes := referenceClassModifier(tracker, modifierSource, ast.KindExportKeyword)
	var modifiers *ast.ModifierList
	if len(nodes) > 0 {
		modifiers = tracker.NewModifierList(nodes)
	}
	cls := tracker.NewClassDeclaration(modifiers, declaration.Name(), nil, nil, tracker.NewNodeList(members))
	if ast.IsVariableDeclaration(declaration) {
		ancestor := declaration.Parent.Parent
		if ast.IsVariableDeclarationList(declaration.Parent) && len(declaration.Parent.AsVariableDeclarationList().Declarations.Nodes) > 1 {
			tracker.Delete(request.SourceFile, declaration)
			tracker.InsertNodeAfter(request.SourceFile, ancestor, cls)
		} else {
			tracker.ReplaceNode(request.SourceFile, ancestor, cls, nil)
		}
	} else {
		tracker.ReplaceNode(request.SourceFile, declaration, cls, nil)
	}
}

var ReferenceFunctionClassProvider = &CodeFixProvider{Name: "convertFunctionToEs6Class", ErrorCodes: []int32{diagnostics.This_constructor_function_may_be_converted_to_a_class_declaration.Code()}, FixIds: []string{"convertFunctionToEs6Class"},
	GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		referenceConvertFunctionClass(request, ch, tracker)
		return []*CodeAction{{Description: diagnostics.Convert_function_to_an_ES2015_class.Localize(locale.FromContext(ctx)), ReferenceChanges: referenceTrackerFileChanges(tracker), FixID: "convertFunctionToEs6Class", FixAllDescription: diagnostics.Convert_all_constructor_functions_to_classes.Localize(locale.FromContext(ctx))}}, ctx.Err()
	},
	GetAllCodeActions: func(ctx context.Context, request *CodeFixContext) (*CombinedCodeActions, error) {
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		for _, diagnostic := range getAllDiagnostics(ctx, request.Program, request.SourceFile) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if diagnostic.Code() != diagnostics.This_constructor_function_may_be_converted_to_a_class_declaration.Code() {
				continue
			}
			item := *request
			item.Span = diagnostic.Loc()
			referenceConvertFunctionClass(&item, ch, tracker)
		}
		return &CombinedCodeActions{ReferenceChanges: referenceTrackerFileChanges(tracker)}, ctx.Err()
	},
}

// TS6 treats a function's built-in prototype as initialized independently of
// later expando assignments. Keep this compatibility rule in editor diagnostics.
func referenceClassPrototypeDiagnostics(ctx context.Context, program *compiler.Program, file *ast.SourceFile, items []*ast.Diagnostic) []*ast.Diagnostic {
	if !slices.ContainsFunc(items, func(item *ast.Diagnostic) bool { return item.Code() == 2565 }) {
		return items
	}
	ch, done := program.GetTypeCheckerForFile(ctx, file)
	defer done()
	result := make([]*ast.Diagnostic, 0, len(items))
	for _, item := range items {
		if item.Code() == 2565 {
			token := astnav.GetTokenAtPosition(file, item.Pos())
			if token != nil && ast.IsIdentifier(token) && token.Text() == "prototype" && token.Parent != nil && ast.IsPropertyAccessExpression(token.Parent) {
				symbol := ch.GetSymbolAtLocation(token.Parent.Expression())
				if symbol != nil {
					if symbol.Flags&ast.SymbolFlagsAlias != 0 {
						symbol = ch.SkipAlias(symbol)
					}
					if symbol != nil {
						declaration := symbol.ValueDeclaration
						if declaration != nil && (ast.IsFunctionDeclaration(declaration) || ast.IsFunctionExpression(declaration) || ast.IsVariableDeclaration(declaration) && declaration.Initializer() != nil && ast.IsFunctionExpression(declaration.Initializer())) {
							continue
						}
					}
				}
			}
		}
		result = append(result, item)
	}
	return result
}

// ReferenceAsyncConversionState mirrors the reference provider's success flag.
// The facade owns it per code-action enumeration and serializes its resolves.
// No process-global state or compiler nodes are retained.
type ReferenceAsyncConversionState struct{ Succeeded bool }
type referenceAsyncConversionStateKey struct{}

func WithReferenceAsyncConversionState(ctx context.Context, state *ReferenceAsyncConversionState) context.Context {
	return context.WithValue(ctx, referenceAsyncConversionStateKey{}, state)
}
func referenceAsyncState(ctx context.Context) *ReferenceAsyncConversionState {
	if state, ok := ctx.Value(referenceAsyncConversionStateKey{}).(*ReferenceAsyncConversionState); ok && state != nil {
		return state
	}
	return &ReferenceAsyncConversionState{Succeeded: true}
}

// These transforms keep all semantic queries on the source snapshot. Only the
// edit trees are cloned and renamed; the checker-owned tree is never modified.
type referenceAsyncBinding struct {
	identifier, pattern  *ast.Node
	elements             []*referenceAsyncBinding
	types                []*checker.Type
	declared, referenced bool
}
type referenceAsyncTransform struct {
	request *CodeFixContext
	ch      *checker.Checker
	tracker *change.Tracker
	names   map[*ast.Symbol]*referenceAsyncBinding
	renames map[*ast.Symbol]*ast.Node
	returns map[*ast.Node]bool
	failed  bool
	state   *ReferenceAsyncConversionState
}

func referenceAsyncConvertible(node *ast.Node) bool {
	return node != nil && (ast.IsFunctionDeclaration(node) || ast.IsFunctionExpression(node) || ast.IsArrowFunction(node) || ast.IsMethodDeclaration(node))
}
func referenceAsyncReturnsPromise(ch *checker.Checker, node *ast.Node) bool {
	signature := ch.GetSignatureFromDeclaration(node)
	return signature != nil && ch.GetPromisedTypeOfPromise(ch.GetReturnTypeOfSignature(signature)) != nil
}
func referenceAsyncHandlerName(node *ast.Node) string {
	if node == nil || !ast.IsCallExpression(node) || !ast.IsPropertyAccessExpression(node.Expression()) {
		return ""
	}
	name := node.Expression().Name().Text()
	if name == "then" || name == "catch" || name == "finally" {
		return name
	}
	return ""
}
func referenceAsyncSupportedArguments(node *ast.Node) bool {
	maximum := 1
	if referenceAsyncHandlerName(node) == "then" {
		maximum = 2
	}
	args := node.Arguments()
	if len(args) != maximum {
		return len(args) < maximum
	}
	if maximum == 1 {
		return true
	}
	for _, arg := range args {
		if arg.Kind == ast.KindNullKeyword || ast.IsIdentifier(arg) && arg.Text() == "undefined" {
			return true
		}
	}
	return false
}
func referenceAsyncFixableArgument(ch *checker.Checker, node *ast.Node, visited map[*ast.Node]bool) bool {
	if ast.IsFunctionDeclaration(node) || ast.IsFunctionExpression(node) {
		if ast.GetFunctionFlags(node)&ast.FunctionFlagsGenerator != 0 {
			return false
		}
		if visited != nil {
			visited[node] = true
		}
		return true
	}
	if ast.IsArrowFunction(node) {
		if visited != nil {
			visited[node] = true
		}
		return true
	}
	if node.Kind == ast.KindNullKeyword {
		return true
	}
	if ast.IsIdentifier(node) || ast.IsPropertyAccessExpression(node) {
		symbol := ch.GetSymbolAtLocation(node)
		if symbol == nil {
			return false
		}
		if ch.IsUndefinedSymbol(symbol) {
			return true
		}
		symbol = ch.SkipAlias(symbol)
		if symbol != nil {
			for _, declaration := range symbol.Declarations {
				if ast.IsFunctionLike(declaration) || declaration.Initializer() != nil && ast.IsFunctionLike(declaration.Initializer()) {
					return true
				}
			}
		}
	}
	return false
}
func referenceAsyncFixable(ch *checker.Checker, node *ast.Node, visited map[*ast.Node]bool) bool {
	if referenceAsyncHandlerName(node) == "" {
		return false
	}
	valid := func(call *ast.Node) bool {
		if !referenceAsyncSupportedArguments(call) {
			return false
		}
		for _, arg := range call.Arguments() {
			if !referenceAsyncFixableArgument(ch, arg, visited) {
				return false
			}
		}
		return true
	}
	if !valid(node) {
		return false
	}
	current := node.Expression().Expression()
	for current != nil && (referenceAsyncHandlerName(current) != "" || ast.IsPropertyAccessExpression(current)) {
		if ast.IsCallExpression(current) {
			if !valid(current) {
				return false
			}
			current = current.Expression().Expression()
		} else {
			current = current.Expression()
		}
	}
	return true
}
func referenceAsyncReturnStatements(node *ast.Node) []*ast.Node {
	result := []*ast.Node{}
	var visit func(*ast.Node) bool
	visit = func(node *ast.Node) bool {
		if node == nil {
			return false
		}
		if ast.IsReturnStatement(node) {
			result = append(result, node)
			return false
		}
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			return false
		}
		node.ForEachChild(visit)
		return false
	}
	visit(node)
	return result
}
func (t *referenceAsyncTransform) fail() []*ast.Node {
	t.failed = true
	t.state.Succeeded = false
	return nil
}
func (t *referenceAsyncTransform) promised(node *ast.Node) bool {
	return node != nil && ast.IsExpression(node) && t.ch.GetPromisedTypeOfPromise(t.ch.GetTypeAtLocation(node)) != nil
}
func (t *referenceAsyncTransform) promiseCall(node *ast.Node, name string) bool {
	return referenceAsyncHandlerName(node) == name && t.promised(node)
}
func (t *referenceAsyncTransform) lastSignature(node *ast.Node) *checker.Signature {
	if node == nil {
		return nil
	}
	signatures := t.ch.GetSignaturesOfType(t.ch.GetTypeAtLocation(node), checker.SignatureKindCall)
	if len(signatures) == 0 {
		return nil
	}
	return signatures[len(signatures)-1]
}
func (t *referenceAsyncTransform) unique(name string) *ast.Node {
	return t.tracker.EmitContext.Factory.NewUniqueNameEx(name, printer.AutoGenerateOptions{Flags: printer.GeneratedIdentifierFlagsOptimistic})
}
func (t *referenceAsyncTransform) clone(node *ast.Node) *ast.Node {
	var visitor *ast.NodeVisitor
	visitor = ast.NewNodeVisitor(func(node *ast.Node) *ast.Node {
		if node == nil {
			return nil
		}
		if ast.IsBindingElement(node) && ast.IsIdentifier(node.Name()) && node.Parent != nil && ast.IsObjectBindingPattern(node.Parent) {
			if renamed := t.renames[t.ch.GetSymbolAtLocation(node.Name())]; renamed != nil && renamed.Text() != node.Name().Text() {
				property := node.AsBindingElement().PropertyName
				if property == nil {
					property = node.Name()
				}
				return t.tracker.NewBindingElement(node.AsBindingElement().DotDotDotToken, property, renamed, node.Initializer())
			}
		}
		if ast.IsIdentifier(node) {
			if renamed := t.renames[t.ch.GetSymbolAtLocation(node)]; renamed != nil {
				result := renamed.Clone(t.tracker)
				t.tracker.SetOriginal(result, renamed)
				return result
			}
		}
		result := node.VisitEachChild(visitor)
		if result == node {
			result = node.Clone(t.tracker)
		}
		t.tracker.SetOriginal(result, node)
		return result
	}, t.tracker.NodeFactory, ast.NodeVisitorHooks{})
	return visitor.VisitNode(node)
}
func (t *referenceAsyncTransform) prepare(function *ast.Node) {
	var collect func(*ast.Node) bool
	collect = func(node *ast.Node) bool {
		if t.promiseCall(node, "then") {
			t.returns[node] = true
			for _, argument := range node.Arguments() {
				collect(argument)
			}
		} else if t.promiseCall(node, "catch") || t.promiseCall(node, "finally") {
			t.returns[node] = true
			node.ForEachChild(collect)
		} else if t.promised(node) {
			t.returns[node] = true
		} else {
			node.ForEachChild(collect)
		}
		return false
	}
	if function.Body() != nil {
		function.Body().ForEachChild(collect)
	}
	collisions := map[string][]*ast.Symbol{}
	newName := func(name *ast.Node) *referenceAsyncBinding {
		count := len(collisions[name.Text()])
		if count > 0 {
			name = t.tracker.NewIdentifier(name.Text() + "_" + strconv.Itoa(count))
		}
		return &referenceAsyncBinding{identifier: name}
	}
	var visit func(*ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !ast.IsIdentifier(node) {
			node.ForEachChild(visit)
			return false
		}
		symbol := t.ch.GetSymbolAtLocation(node)
		if symbol == nil {
			return false
		}
		signature := t.lastSignature(node)
		parent := node.Parent
		if signature != nil && parent != nil && !ast.IsParameterDeclaration(parent) && !ast.IsFunctionLike(parent) && t.names[symbol] == nil {
			var name *ast.Node
			parameters := signature.Parameters()
			if len(parameters) > 0 && parameters[0].ValueDeclaration != nil && ast.IsParameterDeclaration(parameters[0].ValueDeclaration) && ast.IsIdentifier(parameters[0].ValueDeclaration.Name()) {
				name = parameters[0].ValueDeclaration.Name()
			}
			if name == nil {
				name = t.unique("result")
			}
			t.names[symbol] = newName(name)
			collisions[name.Text()] = append(collisions[name.Text()], symbol)
		} else if parent != nil && (ast.IsParameterDeclaration(parent) || ast.IsVariableDeclaration(parent) || ast.IsBindingElement(parent)) {
			previous := collisions[node.Text()]
			conflict := false
			for _, other := range previous {
				if other != symbol {
					conflict = true
					break
				}
			}
			if conflict {
				name := newName(node)
				t.renames[symbol] = name.identifier
				t.names[symbol] = name
			} else {
				t.names[symbol] = &referenceAsyncBinding{identifier: node.Clone(t.tracker)}
			}
			collisions[node.Text()] = append(previous, symbol)
		}
		return false
	}
	function.ForEachChild(visit)
}
func (t *referenceAsyncTransform) binding(node *ast.Node) *referenceAsyncBinding {
	var mapped func(*ast.Node) *referenceAsyncBinding
	mapped = func(name *ast.Node) *referenceAsyncBinding {
		if ast.IsIdentifier(name) {
			symbol := t.ch.GetSymbolAtLocation(name)
			if result := t.names[symbol]; result != nil {
				return result
			}
			return &referenceAsyncBinding{identifier: name}
		}
		result := &referenceAsyncBinding{pattern: name}
		for _, element := range name.AsBindingPattern().Elements.Nodes {
			if !ast.IsOmittedExpression(element) {
				result.elements = append(result.elements, mapped(element.Name()))
			}
		}
		return result
	}
	var name *referenceAsyncBinding
	if ast.IsFunctionLike(node) {
		if parameters := node.Parameters(); len(parameters) > 0 {
			name = mapped(parameters[0].Name())
		}
	} else if ast.IsIdentifier(node) {
		name = mapped(node)
	} else if ast.IsPropertyAccessExpression(node) && ast.IsIdentifier(node.Name()) {
		name = mapped(node.Name())
	}
	if name == nil || name.identifier != nil && name.identifier.Text() == "undefined" {
		return nil
	}
	return name
}
func (t *referenceAsyncTransform) bindingNode(name *referenceAsyncBinding, declare bool) *ast.Node {
	if name == nil {
		return nil
	}
	if name.identifier != nil {
		if declare {
			name.declared = true
		} else {
			name.referenced = true
		}
		result := name.identifier.Clone(t.tracker)
		t.tracker.SetOriginal(result, name.identifier)
		return result
	}
	for _, element := range name.elements {
		t.bindingNode(element, declare)
	}
	return t.clone(name.pattern)
}
func referenceAsyncBindingEmpty(name *referenceAsyncBinding) bool {
	if name == nil {
		return true
	}
	if name.identifier != nil {
		return name.identifier.Text() == ""
	}
	for _, element := range name.elements {
		if !referenceAsyncBindingEmpty(element) {
			return false
		}
	}
	return true
}
func (t *referenceAsyncTransform) variable(name *referenceAsyncBinding, rhs, typeNode *ast.Node) []*ast.Node {
	f := t.tracker
	if referenceAsyncBindingEmpty(name) {
		return []*ast.Node{f.NewExpressionStatement(rhs)}
	}
	if name.identifier != nil && name.declared {
		return []*ast.Node{f.NewExpressionStatement(f.NewBinaryExpression(nil, t.bindingNode(name, false), nil, f.NewToken(ast.KindEqualsToken), rhs))}
	}
	declaration := f.NewVariableDeclaration(t.bindingNode(name, true), nil, typeNode, rhs)
	return []*ast.Node{f.NewVariableStatement(nil, f.NewVariableDeclarationList(f.NewNodeList([]*ast.Node{declaration}), ast.NodeFlagsConst))}
}
func (t *referenceAsyncTransform) annotatedReturn(expression, typeNode *ast.Node) []*ast.Node {
	if expression != nil && typeNode != nil {
		name := &referenceAsyncBinding{identifier: t.unique("result")}
		statements := t.variable(name, expression, typeNode)
		return append(statements, t.tracker.NewReturnStatement(t.bindingNode(name, false)))
	}
	return []*ast.Node{t.tracker.NewReturnStatement(expression)}
}
func (t *referenceAsyncTransform) explicitType(parent, callback *ast.Node) *ast.Node {
	if referenceAsyncHandlerName(parent) == "finally" {
		return nil
	}
	promise := t.ch.GetTypeAtLocation(parent.Expression().Expression())
	if !t.ch.ReferenceIsGlobalPromise(promise) {
		return nil
	}
	index := 0
	if referenceAsyncHandlerName(parent) == "then" && len(parent.Arguments()) > 1 && callback == parent.Arguments()[1] {
		index = 1
	}
	if arguments := parent.TypeArguments(); len(arguments) > index {
		return arguments[index]
	}
	return nil
}
func (t *referenceAsyncTransform) awaited(expression *ast.Node, typ *checker.Type) *ast.Node {
	expression = t.clone(expression)
	if typ != nil && t.ch.GetPromisedTypeOfPromise(typ) != nil {
		return t.tracker.NewAwaitExpression(expression)
	}
	return expression
}
func (t *referenceAsyncTransform) pushType(name *referenceAsyncBinding, typ *checker.Type) {
	if name == nil || typ == nil {
		return
	}
	if awaited := t.ch.GetReferenceAwaitedType(typ); awaited != nil {
		typ = awaited
	}
	name.types = append(name.types, typ)
}
func (t *referenceAsyncTransform) nullish(node *ast.Node) bool {
	if node == nil || node.Kind == ast.KindNullKeyword {
		return true
	}
	if ast.IsIdentifier(node) && node.Text() == "undefined" {
		symbol := t.ch.GetSymbolAtLocation(node)
		return symbol == nil || t.ch.IsUndefinedSymbol(symbol)
	}
	return false
}
func (t *referenceAsyncTransform) expression(returnContext, node *ast.Node, continuation bool, next *referenceAsyncBinding) []*ast.Node {
	for _, name := range []string{"then", "catch", "finally"} {
		if t.promiseCall(node, name) {
			arguments := node.Arguments()
			var fulfilled, rejected *ast.Node
			if len(arguments) > 0 {
				fulfilled = arguments[0]
			}
			if len(arguments) > 1 {
				rejected = arguments[1]
			}
			if name == "then" {
				if t.nullish(fulfilled) {
					return t.catchFinally(node, rejected, continuation, next, false)
				}
				if !t.nullish(rejected) {
					return t.fail()
				}
				input := t.binding(fulfilled)
				left := node.Expression().Expression()
				statements := t.expression(left, left, true, input)
				if t.failed {
					return nil
				}
				return append(statements, t.callback(fulfilled, continuation, next, input, node)...)
			}
			return t.catchFinally(node, fulfilled, continuation, next, name == "finally")
		}
	}
	if ast.IsPropertyAccessExpression(node) {
		return t.expression(returnContext, node.Expression(), continuation, next)
	}
	if t.promised(node) {
		if t.returns[returnContext] {
			value := t.clone(node)
			if continuation {
				value = t.tracker.NewAwaitExpression(value)
			}
			return []*ast.Node{t.tracker.NewReturnStatement(value)}
		}
		return t.variable(next, t.tracker.NewAwaitExpression(t.clone(node)), nil)
	}
	return t.fail()
}
func (t *referenceAsyncTransform) catchFinally(node, callback *ast.Node, continuation bool, next *referenceAsyncBinding, finally bool) []*ast.Node {
	if t.nullish(callback) {
		return t.expression(node, node.Expression().Expression(), continuation, next)
	}
	var variable *referenceAsyncBinding
	if next != nil && !t.returns[node] {
		if next.identifier != nil {
			variable = next
			for symbol, value := range t.names {
				if value.identifier != nil && value.identifier.Text() == next.identifier.Text() {
					t.names[symbol] = &referenceAsyncBinding{identifier: t.unique(next.identifier.Text())}
				}
			}
		} else {
			variable = &referenceAsyncBinding{identifier: t.unique("result"), types: next.types}
		}
		variable.declared = true
	}
	var input *referenceAsyncBinding
	if !finally {
		input = t.binding(callback)
	}
	statements := t.expression(node, node.Expression().Expression(), true, variable)
	if t.failed {
		return nil
	}
	callbackNext := variable
	if finally {
		callbackNext = nil
	}
	body := t.callback(callback, continuation, callbackNext, input, node)
	if t.failed {
		return nil
	}
	f := t.tracker
	var catchClause, finallyBlock *ast.Node
	if finally {
		finallyBlock = f.NewBlock(f.NewNodeList(body), false)
	} else {
		var declaration *ast.Node
		if input != nil {
			declaration = f.NewVariableDeclaration(t.bindingNode(input, true), nil, nil, nil)
		}
		catchClause = f.NewCatchClause(declaration, f.NewBlock(f.NewNodeList(body), false))
	}
	try := f.NewTryStatement(f.NewBlock(f.NewNodeList(statements), false), catchClause, finallyBlock)
	result := []*ast.Node{}
	var identifier *ast.Node
	if variable != nil && !t.returns[node] {
		identifier = t.bindingNode(variable, true)
		typ := t.ch.GetUnionTypeEx(variable.types, checker.UnionReductionSubtype)
		annotation := t.ch.TypeToTypeNode(typ, nil, nodebuilder.FlagsNone, nil)
		declaration := f.NewVariableDeclaration(identifier, nil, annotation, nil)
		result = append(result, f.NewVariableStatement(nil, f.NewVariableDeclarationList(f.NewNodeList([]*ast.Node{declaration}), ast.NodeFlagsLet)))
	}
	result = append(result, try)
	if next != nil && next.pattern != nil && identifier != nil {
		result = append(result, t.variable(next, identifier.Clone(f), nil)...)
	}
	return result
}
func (t *referenceAsyncTransform) callback(function *ast.Node, continuation bool, next, input *referenceAsyncBinding, parent *ast.Node) []*ast.Node {
	f := t.tracker
	if function.Kind == ast.KindNullKeyword {
		return nil
	}
	if ast.IsIdentifier(function) || ast.IsPropertyAccessExpression(function) {
		if input == nil {
			return nil
		}
		args := []*ast.Node{}
		if input.identifier != nil {
			args = append(args, t.bindingNode(input, false))
		}
		call := f.NewCallExpression(t.clone(function), nil, nil, f.NewNodeList(args), ast.NodeFlagsNone)
		if t.returns[parent] {
			return t.annotatedReturn(call, t.explicitType(parent, function))
		}
		signatures := t.ch.GetSignaturesOfType(t.ch.GetTypeAtLocation(function), checker.SignatureKindCall)
		if len(signatures) == 0 {
			return t.fail()
		}
		result := t.variable(next, f.NewAwaitExpression(call), t.explicitType(parent, function))
		t.pushType(next, t.ch.GetReturnTypeOfSignature(signatures[0]))
		return result
	}
	if !ast.IsFunctionExpression(function) && !ast.IsArrowFunction(function) {
		return t.fail()
	}
	body := function.Body()
	signature := t.lastSignature(function)
	var returnType *checker.Type
	if signature != nil {
		returnType = t.ch.GetReturnTypeOfSignature(signature)
	}
	if ast.IsBlock(body) {
		statements := []*ast.Node{}
		seenReturn := false
		for _, statement := range body.Statements() {
			if ast.IsReturnStatement(statement) {
				seenReturn = true
				if statement.Expression() != nil && referenceAsyncFixable(t.ch, statement.Expression(), nil) {
					statements = append(statements, t.innerReturn(statement, continuation, next)...)
				} else {
					expression := statement.Expression()
					if returnType != nil && expression != nil {
						expression = t.awaited(expression, returnType)
					} else {
						expression = t.clone(expression)
					}
					statements = append(statements, t.annotatedReturn(expression, t.explicitType(parent, function))...)
				}
			} else if continuation && len(referenceAsyncReturnStatements(statement)) > 0 {
				return t.fail()
			} else {
				statements = append(statements, t.clone(statement))
			}
		}
		if t.returns[parent] {
			return statements
		}
		return t.removeReturns(statements, next, seenReturn)
	}
	if referenceAsyncFixable(t.ch, body, nil) {
		if statements := t.innerReturn(f.NewReturnStatement(body), continuation, next); len(statements) > 0 {
			return statements
		}
	}
	if returnType == nil {
		return t.fail()
	}
	expression := t.awaited(body, returnType)
	if !t.returns[parent] {
		result := t.variable(next, expression, nil)
		t.pushType(next, returnType)
		return result
	}
	return t.annotatedReturn(expression, t.explicitType(parent, function))
}
func (t *referenceAsyncTransform) removeReturns(statements []*ast.Node, next *referenceAsyncBinding, seenReturn bool) []*ast.Node {
	result := []*ast.Node{}
	for _, statement := range statements {
		if ast.IsReturnStatement(statement) {
			expression := statement.Expression()
			if expression == nil {
				continue
			}
			original := t.tracker.MostOriginal(expression)
			if original != nil && original.Flags&ast.NodeFlagsSynthesized == 0 && t.promised(original) {
				expression = t.tracker.NewAwaitExpression(expression)
			}
			result = append(result, t.variable(next, expression, nil)...)
		} else {
			result = append(result, statement)
		}
	}
	if !seenReturn && next != nil {
		result = append(result, t.variable(next, t.tracker.NewIdentifier("undefined"), nil)...)
	}
	return result
}
func (t *referenceAsyncTransform) innerReturn(statement *ast.Node, continuation bool, next *referenceAsyncBinding) []*ast.Node {
	result := []*ast.Node{}
	var visit func(*ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ast.IsCallExpression(node) {
			result = append(result, t.expression(node, node, continuation, next)...)
			if len(result) > 0 {
				return false
			}
		} else if !ast.IsFunctionLike(node) {
			node.ForEachChild(visit)
		}
		return false
	}
	statement.ForEachChild(visit)
	return result
}
func referenceConvertAsync(request *CodeFixContext, ch *checker.Checker, tracker *change.Tracker, state *ReferenceAsyncConversionState) bool {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	var function *ast.Node
	if token != nil && ast.IsIdentifier(token) && token.Parent != nil && ast.IsVariableDeclaration(token.Parent) && referenceAsyncConvertible(token.Parent.Initializer()) {
		function = token.Parent.Initializer()
	} else {
		for current := token; current != nil; current = current.Parent {
			if ast.IsFunctionLike(current) {
				if referenceAsyncConvertible(current) {
					function = current
				}
				break
			}
		}
	}
	if function == nil {
		return true
	}
	t := &referenceAsyncTransform{request: request, ch: ch, tracker: tracker, names: map[*ast.Symbol]*referenceAsyncBinding{}, renames: map[*ast.Symbol]*ast.Node{}, returns: map[*ast.Node]bool{}, state: state, failed: !state.Succeeded}
	t.prepare(function)
	if !referenceAsyncReturnsPromise(ch, function) {
		return true
	}
	returns := []*ast.Node{}
	if function.Body() != nil && ast.IsBlock(function.Body()) {
		for _, statement := range referenceAsyncReturnStatements(function.Body()) {
			if statement.Expression() != nil && referenceAsyncFixable(ch, statement.Expression(), nil) {
				returns = append(returns, statement)
			}
		}
	}
	if len(returns) == 0 {
		return true
	}
	position := function.Pos()
	if function.Modifiers() != nil && len(function.Modifiers().Nodes) > 0 {
		position = function.Modifiers().Nodes[len(function.Modifiers().Nodes)-1].End()
	}
	position = scanner.SkipTrivia(request.SourceFile.Text(), position)
	tracker.InsertText(request.SourceFile, request.LS.converters.PositionToLineAndCharacter(request.SourceFile, core.TextPos(position)), "async ")
	for _, statement := range returns {
		var visit func(*ast.Node) bool
		visit = func(node *ast.Node) bool {
			if ast.IsCallExpression(node) {
				statements := t.expression(node, node, false, nil)
				if !t.failed {
					tracker.ReplaceNodeWithNodes(request.SourceFile, statement, statements, nil)
				}
			} else if !ast.IsFunctionLike(node) {
				node.ForEachChild(visit)
			}
			return t.failed
		}
		statement.ForEachChild(visit)
		if t.failed {
			return false
		}
	}
	return true
}

var ReferenceAsyncConversionProvider = &CodeFixProvider{Name: "convertToAsyncFunction", ErrorCodes: []int32{diagnostics.This_may_be_converted_to_an_async_function.Code()}, FixIds: []string{"convertToAsyncFunction"},
	GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		state := referenceAsyncState(ctx)
		state.Succeeded = true
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		if !referenceConvertAsync(request, ch, tracker, state) {
			return nil, ctx.Err()
		}
		return []*CodeAction{{Description: diagnostics.Convert_to_async_function.Localize(locale.FromContext(ctx)), ReferenceChanges: referenceTrackerFileChanges(tracker), FixID: "convertToAsyncFunction", FixAllDescription: diagnostics.Convert_all_to_async_functions.Localize(locale.FromContext(ctx))}}, ctx.Err()
	},
	GetAllCodeActions: func(ctx context.Context, request *CodeFixContext) (*CombinedCodeActions, error) {
		state := referenceAsyncState(ctx)
		ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
		defer done()
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		for _, diagnostic := range getAllDiagnostics(ctx, request.Program, request.SourceFile) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if diagnostic.Code() != diagnostics.This_may_be_converted_to_an_async_function.Code() {
				continue
			}
			item := *request
			item.Span = diagnostic.Loc()
			referenceConvertAsync(&item, ch, tracker, state)
		}
		return &CombinedCodeActions{ReferenceChanges: referenceTrackerFileChanges(tracker)}, ctx.Err()
	},
}

func referenceAsyncConversionDiagnostics(ctx context.Context, program *compiler.Program, file *ast.SourceFile, items []*ast.Diagnostic) []*ast.Diagnostic {
	ch, done := program.GetTypeCheckerForFile(ctx, file)
	defer done()
	visited := map[*ast.Node]bool{}
	var visit func(*ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ctx.Err() != nil {
			return true
		}
		if referenceAsyncConvertible(node) && ast.GetFunctionFlags(node)&ast.FunctionFlagsAsync == 0 && node.Body() != nil && ast.IsBlock(node.Body()) {
			convertible := false
			for _, statement := range referenceAsyncReturnStatements(node.Body()) {
				if statement.Expression() != nil && referenceAsyncFixable(ch, statement.Expression(), visited) {
					convertible = true
					break
				}
			}
			if convertible && referenceAsyncReturnsPromise(ch, node) && !visited[node] {
				target := node
				if node.Name() == nil && node.Parent != nil && ast.IsVariableDeclaration(node.Parent) && ast.IsIdentifier(node.Parent.Name()) {
					target = node.Parent.Name()
				}
				span := scanner.GetErrorRangeForNode(file, target)
				if ast.IsFunctionExpression(target) && target.Name() == nil {
					span = scanner.GetRangeOfTokenAtPosition(file, scanner.GetTokenPosOfNode(target, file, false))
				}
				found := false
				for _, item := range items {
					if item.Code() == diagnostics.This_may_be_converted_to_an_async_function.Code() && item.Loc() == span {
						found = true
						break
					}
				}
				if !found {
					item := ast.NewDiagnostic(file, span, diagnostics.This_may_be_converted_to_an_async_function)
					item.SetCategory(diagnostics.CategorySuggestion)
					items = append(items, item)
				}
			}
		}
		return node.ForEachChild(visit)
	}
	visit(file.AsNode())
	return items
}

type referenceESImports struct {
	nodes []*ast.Node
	uses  map[*ast.Node]*ast.Node
	order []*ast.Node
}
type referenceESModule struct {
	request    *CodeFixContext
	ch         *checker.Checker
	t          *change.Tracker
	file       *ast.SourceFile
	original   map[string][]*ast.Node
	additional map[string]bool
	exports    map[string]string
	uses       map[*ast.Node]*ast.Node
	useOrder   []*ast.Node
	quote      ast.TokenFlags
}

func referenceESExports(file *ast.SourceFile, node *ast.Node) bool {
	queue := []*ast.Node{node}
	for i := 0; i < len(queue) && i < 100; i++ {
		node := queue[i]
		if node == nil {
			continue
		}
		if ast.IsIdentifier(node) && node.Text() == "exports" {
			return true
		}
		if ast.IsAccessExpression(node) && ast.IsIdentifier(node.Expression()) && node.Expression().Text() == "module" {
			if ast.IsPropertyAccessExpression(node) && node.Name().Text() == "exports" {
				return true
			}
			if ast.IsElementAccessExpression(node) {
				argument := node.AsElementAccessExpression().ArgumentExpression
				if argument != nil && ast.IsStringLiteralLike(argument) && argument.Text() == "exports" {
					return true
				}
			}
		}
		if ast.IsIdentifier(node) {
			symbol := file.AsNode().Locals()[node.Text()]
			if symbol != nil && symbol.ValueDeclaration != nil && ast.IsVariableDeclaration(symbol.ValueDeclaration) {
				if initializer := symbol.ValueDeclaration.Initializer(); initializer != nil {
					queue = append(queue, initializer)
					if ast.IsBinaryExpression(initializer) && initializer.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken {
						queue = append(queue, initializer.AsBinaryExpression().Left, initializer.AsBinaryExpression().Right)
					}
				}
			}
		}
	}
	return false
}
func (m *referenceESModule) unique(name string) string {
	for m.original[name] != nil || m.additional[name] {
		name = "_" + name
	}
	m.additional[name] = true
	return name
}
func (m *referenceESModule) exportReferences(visit func(*ast.Node, bool)) {
	var walk func(*ast.Node) bool
	walk = func(node *ast.Node) bool {
		if ast.IsPropertyAccessExpression(node) && ast.IsIdentifier(node.Name()) && referenceESExports(m.file, node.Expression()) {
			assignment := node.Parent
			lhs := assignment != nil && ast.IsBinaryExpression(assignment) && assignment.AsBinaryExpression().Left == node && assignment.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken
			visit(node, lhs)
		}
		return node.ForEachChild(walk)
	}
	m.file.AsNode().ForEachChild(walk)
}
func (m *referenceESModule) clone(node *ast.Node, replace bool) *ast.Node {
	var visitor *ast.NodeVisitor
	visitor = ast.NewNodeVisitor(func(node *ast.Node) *ast.Node {
		if node == nil {
			return nil
		}
		if replace && ast.IsPropertyAccessExpression(node) {
			replacement := m.uses[node]
			delete(m.uses, node)
			if replacement != nil {
				result := m.t.NewIdentifier(replacement.Text())
				m.t.SetOriginal(result, node)
				return result
			}
		}
		result := node.VisitEachChild(visitor)
		if result == node {
			result = node.Clone(m.t)
		}
		m.t.SetOriginal(result, node)
		return result
	}, m.t.NodeFactory, ast.NodeVisitorHooks{})
	return visitor.VisitNode(node)
}
func (m *referenceESModule) replaceUses(node *ast.Node) *ast.Node {
	if node == nil {
		return nil
	}
	for _, original := range m.useOrder {
		if m.uses[original] != nil && node.Pos() <= original.Pos() && original.End() <= node.End() {
			return m.clone(node, true)
		}
	}
	return node
}
func (m *referenceESModule) cloneList(list *ast.NodeList) *ast.NodeList {
	if list == nil {
		return nil
	}
	nodes := make([]*ast.Node, 0, len(list.Nodes))
	for _, node := range list.Nodes {
		nodes = append(nodes, m.clone(node, false))
	}
	result := m.t.NewNodeList(nodes)
	result.Loc = list.Loc
	return result
}
func (m *referenceESModule) modifiers(nodes []*ast.Node, original *ast.ModifierList) *ast.ModifierList {
	for _, node := range func() []*ast.Node {
		if original != nil {
			return original.Nodes
		}
		return nil
	}() {
		nodes = append(nodes, m.clone(node, false))
	}
	if len(nodes) == 0 {
		return nil
	}
	return m.t.NewModifierList(nodes)
}
func (m *referenceESModule) constant(modifiers *ast.ModifierList, name, initializer *ast.Node) *ast.Node {
	return m.t.NewVariableStatement(modifiers, m.t.NewVariableDeclarationList(m.t.NewNodeList([]*ast.Node{m.t.NewVariableDeclaration(name, nil, nil, initializer)}), ast.NodeFlagsConst))
}
func (m *referenceESModule) importNode(name *ast.Node, specifiers []*ast.Node, module *ast.Node) *ast.Node {
	var clause *ast.Node
	if name != nil || specifiers != nil {
		var bindings *ast.Node
		if len(specifiers) > 0 {
			bindings = m.t.NewNamedImports(m.t.NewNodeList(specifiers))
		}
		clause = m.t.NewImportClause(ast.KindUnknown, name, bindings)
	}
	return m.t.NewImportDeclaration(nil, clause, module, nil)
}
func (m *referenceESModule) importSpecifier(property, name string) *ast.Node {
	var original *ast.Node
	if property != "" && property != name {
		original = m.t.NewIdentifier(property)
	}
	return m.t.NewImportSpecifier(false, original, m.t.NewIdentifier(name))
}
func (m *referenceESModule) singleImport(local, property string, module *ast.Node) *ast.Node {
	if property == "default" {
		return m.importNode(m.t.NewIdentifier(local), nil, module)
	}
	return m.importNode(nil, []*ast.Node{m.importSpecifier(property, local)}, module)
}
func (m *referenceESModule) exportDeclaration(names []*ast.Node, module string, hasModule bool) *ast.Node {
	var clause, specifier *ast.Node
	if names != nil {
		clause = m.t.NewNamedExports(m.t.NewNodeList(names))
	}
	if hasModule {
		specifier = m.t.NewStringLiteral(module, ast.TokenFlagsNone)
	}
	return m.t.NewExportDeclaration(nil, false, clause, specifier, nil)
}
func (m *referenceESModule) function(name string, expression *ast.Node) *ast.Node {
	modifiers := m.modifiers([]*ast.Node{m.t.NewModifier(ast.KindExportKeyword)}, expression.Modifiers())
	var asterisk *ast.Node
	if ast.IsFunctionExpression(expression) {
		asterisk = expression.AsFunctionExpression().AsteriskToken
	} else if ast.IsMethodDeclaration(expression) {
		asterisk = expression.AsMethodDeclaration().AsteriskToken
	}
	asterisk = m.clone(asterisk, false)
	body := m.replaceUses(expression.Body())
	if body != nil && !ast.IsBlock(body) {
		original := body
		result := m.t.NewReturnStatement(body)
		result.Loc = original.Loc
		body = m.t.NewBlock(m.t.NewNodeList([]*ast.Node{result}), false)
		body.Loc = original.Loc
	}
	var typ *ast.Node
	if expression.Type() != nil {
		typ = m.clone(expression.Type(), false)
	}
	return m.t.NewFunctionDeclaration(modifiers, asterisk, m.t.NewIdentifier(name), m.cloneList(expression.TypeParameterList()), m.cloneList(expression.ParameterList()), typ, nil, body)
}
func (m *referenceESModule) class(name string, expression *ast.Node) *ast.Node {
	members := expression.AsClassExpression().Members
	list := members
	for _, original := range m.useOrder {
		if m.uses[original] != nil && members.Pos() <= original.Pos() && original.End() <= members.End() {
			nodes := make([]*ast.Node, 0, len(members.Nodes))
			for _, member := range members.Nodes {
				nodes = append(nodes, m.clone(member, true))
			}
			list = m.t.NewNodeList(nodes)
			break
		}
	}

	return m.t.NewClassDeclaration(m.modifiers([]*ast.Node{m.t.NewModifier(ast.KindExportKeyword)}, expression.Modifiers()), m.t.NewIdentifier(name), m.cloneList(expression.TypeParameterList()), m.cloneList(expression.AsClassExpression().HeritageClauses), list)
}
func (m *referenceESModule) exportValue(name string, expression *ast.Node) *ast.Node {
	if ast.IsFunctionExpression(expression) && expression.Name() != nil && expression.Name().Text() != name {
		return m.constant(m.t.NewModifierList([]*ast.Node{m.t.NewModifier(ast.KindExportKeyword)}), m.t.NewIdentifier(name), m.replaceUses(expression))
	}
	if ast.IsFunctionExpression(expression) || ast.IsArrowFunction(expression) {
		return m.function(name, expression)
	}
	if ast.IsClassExpression(expression) {
		return m.class(name, expression)
	}
	return m.constant(m.t.NewModifierList([]*ast.Node{m.t.NewModifier(ast.KindExportKeyword)}), m.t.NewIdentifier(name), m.replaceUses(expression))
}
func (m *referenceESModule) textRange(start, end int) lsproto.Range {
	return lsproto.Range{Start: m.request.LS.converters.PositionToLineAndCharacter(m.file, core.TextPos(start)), End: m.request.LS.converters.PositionToLineAndCharacter(m.file, core.TextPos(end))}
}
func (m *referenceESModule) text(start, end int, text string) {
	m.t.ReplaceRangeWithText(m.file, m.textRange(start, end), text)
}
func (m *referenceESModule) insertName(node *ast.Node, name string) {
	if ast.IsArrowFunction(node) {
		arrow := astnav.FindChildOfKind(node, ast.KindEqualsGreaterThanToken, m.file)
		lparen := astnav.FindChildOfKind(node, ast.KindOpenParenToken, m.file)
		if lparen != nil {
			m.text(scanner.GetTokenPosOfNode(lparen, m.file, false), scanner.GetTokenPosOfNode(lparen, m.file, false), "function "+name)
			m.t.DeleteRange(m.file, core.NewTextRange(arrow.Pos(), arrow.End()))
		} else {
			parameters := node.Parameters()
			if len(parameters) == 0 {
				return
			}
			position := scanner.GetTokenPosOfNode(parameters[0], m.file, false)
			m.text(position, position, "function "+name+"(")
			m.t.ReplaceRange(m.file, m.textRange(arrow.Pos(), arrow.End()), m.t.NewToken(ast.KindCloseParenToken), change.NodeOptions{})
		}
		if node.Body() != nil && !ast.IsBlock(node.Body()) {
			start, end := scanner.GetTokenPosOfNode(node.Body(), m.file, false), node.Body().End()
			m.text(start, start, "{ return ")
			m.text(end, end, "; }")
		}
		return
	}
	kind := ast.KindClassKeyword
	if ast.IsFunctionExpression(node) {
		kind = ast.KindFunctionKeyword
	}
	if token := astnav.FindChildOfKind(node, kind, m.file); token != nil {
		m.t.InsertNodeAt(m.file, core.TextPos(token.End()), m.t.NewIdentifier(name), change.NodeOptions{Prefix: " "})
	}
}
func (m *referenceESModule) namedAssignment(assignment *ast.Node) {
	data := assignment.AsBinaryExpression()
	left, right := data.Left, data.Right
	name := left.Name().Text()
	if renamed := m.exports[name]; renamed != "" {
		declaration := m.constant(nil, m.t.NewIdentifier(renamed), right)
		export := m.exportDeclaration([]*ast.Node{m.t.NewExportSpecifier(false, m.t.NewIdentifier(renamed), m.t.NewIdentifier(name))}, "", false)
		m.t.ReplaceNodeWithNodes(m.file, assignment.Parent, []*ast.Node{declaration, export}, nil)
		return
	}
	if (ast.IsFunctionExpression(right) || ast.IsArrowFunction(right) || ast.IsClassExpression(right)) && (right.Name() == nil || right.Name().Text() == name) {
		m.t.ReplaceRange(m.file, m.textRange(scanner.GetTokenPosOfNode(left, m.file, false), scanner.GetTokenPosOfNode(right, m.file, false)), m.t.NewToken(ast.KindExportKeyword), change.NodeOptions{Suffix: " "})
		if right.Name() == nil {
			m.insertName(right, name)
		}
		if semicolon := astnav.FindChildOfKind(assignment.Parent, ast.KindSemicolonToken, m.file); semicolon != nil {
			m.t.Delete(m.file, semicolon)
		}
	} else {
		dot := astnav.FindChildOfKind(left, ast.KindDotToken, m.file)
		if dot != nil {
			m.text(scanner.GetTokenPosOfNode(left.Expression(), m.file, false), dot.End(), "export const ")
		}
	}
}
func (m *referenceESModule) objectExports(object *ast.Node) ([]*ast.Node, bool) {
	nodes := []*ast.Node{}
	for _, property := range object.Properties() {
		if property.Name() == nil || !ast.IsIdentifier(property.Name()) {
			return nil, false
		}
		switch property.Kind {
		case ast.KindPropertyAssignment:
			nodes = append(nodes, m.exportValue(property.Name().Text(), property.Initializer()))
		case ast.KindMethodDeclaration:
			nodes = append(nodes, m.function(property.Name().Text(), property))
		default:
			return nil, false
		}
	}
	return nodes, true
}
func (m *referenceESModule) reexport(module *ast.Node) ([]*ast.Node, bool) {
	symbol := m.ch.GetSymbolAtLocation(module)
	var exports ast.SymbolTable
	if symbol != nil {
		exports = symbol.Exports
	}
	star := func() *ast.Node { return m.exportDeclaration(nil, module.Text(), true) }
	def := func() *ast.Node {
		return m.exportDeclaration([]*ast.Node{m.t.NewExportSpecifier(false, nil, m.t.NewIdentifier("default"))}, module.Text(), true)
	}
	if exports[ast.InternalSymbolNameExportEquals] != nil {
		return []*ast.Node{def()}, true
	}
	if exports[ast.InternalSymbolNameDefault] == nil {
		return []*ast.Node{star()}, false
	}
	if len(exports) > 1 {
		return []*ast.Node{star(), def()}, true
	}
	return []*ast.Node{def()}, true
}
func (m *referenceESModule) assignment(assignment *ast.Node) bool {
	data := assignment.AsBinaryExpression()
	left, right := data.Left, data.Right
	if !ast.IsPropertyAccessExpression(left) {
		return false
	}
	if referenceESExports(m.file, left) {
		if referenceESExports(m.file, right) {
			m.t.Delete(m.file, assignment.Parent)
			return false
		}
		var nodes []*ast.Node
		var ok, changed bool
		if ast.IsObjectLiteralExpression(right) {
			nodes, ok = m.objectExports(right)
		} else if ast.IsRequireCall(right, true) {
			nodes, changed = m.reexport(right.Arguments()[0])
			ok = true
		}
		if ok {
			m.t.ReplaceNodeWithNodes(m.file, assignment.Parent, nodes, nil)
			return changed
		}
		m.text(scanner.GetTokenPosOfNode(left, m.file, false), right.Pos(), "export default")
		return true
	}
	if referenceESExports(m.file, left.Expression()) {
		m.namedAssignment(assignment)
	}
	return false
}
func (m *referenceESModule) singleIdentifier(name, module *ast.Node) referenceESImports {
	symbol := m.ch.GetSymbolAtLocation(name)
	names := map[string]string{}
	order := []string{}
	needDefault := false
	result := referenceESImports{uses: map[*ast.Node]*ast.Node{}}
	for _, use := range m.original[name.Text()] {
		if use == name || m.ch.GetSymbolAtLocation(use) != symbol {
			continue
		}
		parent := use.Parent
		if parent != nil && ast.IsPropertyAccessExpression(parent) {
			property := parent.Name().Text()
			var local string
			if property == "default" {
				needDefault = true
				local = scanner.GetTextOfNode(use)
			} else {
				local = names[property]
				if local == "" {
					local = m.unique(property)
					names[property] = local
					order = append(order, property)
				}
			}
			result.uses[parent] = m.t.NewIdentifier(local)
			result.order = append(result.order, parent)
		} else {
			needDefault = true
		}
	}
	var specifiers []*ast.Node
	for _, property := range order {
		specifiers = append(specifiers, m.importSpecifier(property, names[property]))
	}
	if len(specifiers) == 0 {
		needDefault = true
	}
	var def *ast.Node
	if needDefault {
		def = m.clone(name, false)
	}
	result.nodes = []*ast.Node{m.importNode(def, specifiers, module)}
	return result
}
func (m *referenceESModule) singleImportBinding(name, module *ast.Node) referenceESImports {
	if ast.IsIdentifier(name) {
		return m.singleIdentifier(name, module)
	}
	if ast.IsObjectBindingPattern(name) {
		specifiers := []*ast.Node{}
		valid := true
		for _, element := range name.AsBindingPattern().Elements.Nodes {
			binding := element.AsBindingElement()
			if binding.DotDotDotToken != nil || binding.Initializer != nil || binding.PropertyName != nil && !ast.IsIdentifier(binding.PropertyName) || !ast.IsIdentifier(binding.Name()) {
				valid = false
				break
			}
			property := ""
			if binding.PropertyName != nil {
				property = binding.PropertyName.Text()
			}
			specifiers = append(specifiers, m.importSpecifier(property, binding.Name().Text()))
		}
		if valid {
			return referenceESImports{nodes: []*ast.Node{m.importNode(nil, specifiers, module)}}
		}
	}
	temporary := m.unique(referenceModuleIdentifier(module.Text()))
	return referenceESImports{nodes: []*ast.Node{m.importNode(m.t.NewIdentifier(temporary), nil, module), m.constant(nil, m.clone(name, false), m.t.NewIdentifier(temporary))}}
}
func (m *referenceESModule) propertyImport(name *ast.Node, property string, module *ast.Node) referenceESImports {
	if ast.IsIdentifier(name) {
		return referenceESImports{nodes: []*ast.Node{m.singleImport(name.Text(), property, module)}}
	}
	temporary := m.unique(property)
	return referenceESImports{nodes: []*ast.Node{m.singleImport(temporary, property, module), m.constant(nil, name, m.t.NewIdentifier(temporary))}}
}
func (m *referenceESModule) variable(statement *ast.Node) {
	converted := []referenceESImports{}
	found := false
	for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
		name, initializer := declaration.Name(), declaration.Initializer()
		var result referenceESImports
		switch {
		case initializer != nil && referenceESExports(m.file, initializer):
			found = true
		case initializer != nil && ast.IsRequireCall(initializer, true):
			found = true
			result = m.singleImportBinding(name, initializer.Arguments()[0])
		case initializer != nil && ast.IsPropertyAccessExpression(initializer) && ast.IsRequireCall(initializer.Expression(), true):
			found = true
			result = m.propertyImport(name, initializer.Name().Text(), initializer.Expression().Arguments()[0])
		default:
			result.nodes = []*ast.Node{m.t.NewVariableStatement(nil, m.t.NewVariableDeclarationList(m.t.NewNodeList([]*ast.Node{declaration}), statement.AsVariableStatement().DeclarationList.Flags))}
		}
		converted = append(converted, result)
	}
	if !found {
		return
	}
	nodes := []*ast.Node{}
	for _, result := range converted {
		nodes = append(nodes, result.nodes...)
	}
	m.t.ReplaceNodeWithNodes(m.file, statement, nodes, nil)
	for _, result := range converted {
		for _, original := range result.order {
			if m.uses[original] == nil {
				m.useOrder = append(m.useOrder, original)
			}
			m.uses[original] = result.uses[original]
		}
	}
}
func (m *referenceESModule) fixImporters() {
	for _, file := range m.request.Program.GetSourceFiles() {
		quote := ast.TokenFlagsNone
		if lsutil.GetQuotePreference(file, m.request.LS.UserPreferences()) == lsutil.QuotePreferenceSingle {
			quote = ast.TokenFlagsSingleQuote
		}
		for _, module := range file.Imports() {
			resolved := m.request.Program.GetResolvedModuleFromModuleSpecifier(file, module)
			if resolved == nil || resolved.ResolvedFileName != m.file.FileName() {
				continue
			}
			node := module.Parent
			if node == nil {
				continue
			}
			if ast.IsExternalModuleReference(node) && node.Parent != nil && ast.IsImportEqualsDeclaration(node.Parent) {
				declaration := node.Parent
				clause := m.t.NewImportClause(ast.KindUnknown, declaration.Name(), nil)
				replacement := m.t.NewImportDeclaration(nil, clause, module, nil)
				m.t.ReplaceNode(file, declaration, replacement, nil)
			} else if ast.IsRequireCall(node, false) {
				replacement := m.t.NewPropertyAccessExpression(m.clone(node, false), nil, m.t.NewIdentifier("default"), ast.NodeFlagsNone)
				m.t.ReplaceNode(file, node, replacement, nil)
			}
			_ = quote
		}
	}
}
func referenceConvertESModule(ctx context.Context, request *CodeFixContext) ([]ReferenceFileCodeChanges, error) {
	ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
	defer done()
	t := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
	m := &referenceESModule{request: request, ch: ch, t: t, file: request.SourceFile, original: map[string][]*ast.Node{}, additional: map[string]bool{}, exports: map[string]string{}, uses: map[*ast.Node]*ast.Node{}}
	if lsutil.GetQuotePreference(m.file, request.LS.UserPreferences()) == lsutil.QuotePreferenceSingle {
		m.quote = ast.TokenFlagsSingleQuote
	}
	var collect func(*ast.Node) bool
	collect = func(node *ast.Node) bool {
		if ast.IsIdentifier(node) {
			parent := node.Parent
			free := parent == nil || !ast.IsPropertyAccessExpression(parent) || parent.Name() != node
			if parent != nil && ast.IsBindingElement(parent) && parent.AsBindingElement().PropertyName == node {
				free = false
			}
			if parent != nil && ast.IsImportSpecifier(parent) && parent.AsImportSpecifier().PropertyName == node {
				free = false
			}
			if free {
				m.original[node.Text()] = append(m.original[node.Text()], node)
			}
		}
		return node.ForEachChild(collect)
	}
	collect(m.file.AsNode())
	m.exportReferences(func(node *ast.Node, _ bool) {
		name := node.Name().Text()
		if m.exports[name] == "" && (lsutil.IsNonContextualKeyword(scanner.StringToToken(name)) || ch.ResolveName(name, node, ast.SymbolFlagsValue, true) != nil) {
			m.exports[name] = m.unique("_" + name)
		}
	})
	m.exportReferences(func(node *ast.Node, lhs bool) {
		if !lhs {
			name := node.Name().Text()
			if rename := m.exports[name]; rename != "" {
				name = rename
			}
			t.ReplaceNode(m.file, node, t.NewIdentifier(name), nil)
		}
	})
	for _, statement := range m.file.Statements.Nodes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if ast.IsVariableStatement(statement) {
			m.variable(statement)
		}
	}
	changed := false
	for _, statement := range m.file.Statements.Nodes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if ast.IsExpressionStatement(statement) {
			expression := statement.Expression()
			if ast.IsRequireCall(expression, true) {
				t.ReplaceNode(m.file, statement, m.importNode(nil, nil, expression.Arguments()[0]), nil)
			} else if ast.IsBinaryExpression(expression) && expression.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken {
				changed = m.assignment(expression) || changed
			}
		}
	}
	for _, original := range m.useOrder {
		if replacement := m.uses[original]; replacement != nil {
			t.ReplaceNode(m.file, original, replacement, nil)
		}
	}
	if changed {
		m.fixImporters()
	}
	return referenceTrackerFileChanges(t), ctx.Err()
}

var ReferenceESModuleProvider = &CodeFixProvider{Name: "convertToEsModule", ErrorCodes: []int32{diagnostics.File_is_a_CommonJS_module_it_may_be_converted_to_an_ES_module.Code()}, GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
	changes, err := referenceConvertESModule(ctx, request)
	if err != nil {
		return nil, err
	}
	return []*CodeAction{{Description: diagnostics.Convert_to_ES_module.Localize(locale.FromContext(ctx)), ReferenceChanges: changes}}, nil
}}

// Usage evidence is owned by one inference. Ordered property names reproduce
// the reference's Map traversal without depending on Go map iteration order.
type referenceInferenceCall struct {
	arguments []*checker.Type
	result    *referenceInferenceUsage
}
type referenceInferenceUsage struct {
	number, string_, ambiguous bool
	candidates, thisTypes      []*checker.Type
	properties                 map[string]*referenceInferenceUsage
	names                      []string
	calls, constructs          []*referenceInferenceCall
	numberIndex, stringIndex   *referenceInferenceUsage
}
type referenceUsageInference struct {
	ctx     context.Context
	request *CodeFixContext
	ch      *checker.Checker
}

func (i *referenceUsageInference) property(u *referenceInferenceUsage, name string) *referenceInferenceUsage {
	if u.properties == nil {
		u.properties = map[string]*referenceInferenceUsage{}
	}
	if u.properties[name] == nil {
		u.properties[name] = &referenceInferenceUsage{}
		u.names = append(u.names, name)
	}
	return u.properties[name]
}
func referenceInferenceCandidate(u *referenceInferenceUsage, t *checker.Type, this bool) {
	if t != nil && t.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsNever) == 0 {
		if this {
			u.thisTypes = append(u.thisTypes, t)
		} else {
			u.candidates = append(u.candidates, t)
		}
	}
}
func (i *referenceUsageInference) combineUsages(usages []*referenceInferenceUsage) *referenceInferenceUsage {
	u := &referenceInferenceUsage{properties: map[string]*referenceInferenceUsage{}, calls: []*referenceInferenceCall{}, constructs: []*referenceInferenceCall{}}
	groups := map[string][]*referenceInferenceUsage{}
	for _, v := range usages {
		u.number = u.number || v.number
		u.string_ = u.string_ || v.string_
		u.ambiguous = u.ambiguous || v.ambiguous
		u.candidates = append(u.candidates, v.candidates...)
		u.thisTypes = append(u.thisTypes, v.thisTypes...)
		u.calls = append(u.calls, v.calls...)
		u.constructs = append(u.constructs, v.constructs...)
		if u.numberIndex == nil {
			u.numberIndex = v.numberIndex
		}
		if u.stringIndex == nil {
			u.stringIndex = v.stringIndex
		}
		for _, name := range v.names {
			if groups[name] == nil {
				u.names = append(u.names, name)
			}
			groups[name] = append(groups[name], v.properties[name])
		}
	}
	for _, name := range u.names {
		u.properties[name] = i.combineUsages(groups[name])
	}
	return u
}
func (i *referenceUsageInference) combine(types []*checker.Type) *checker.Type {
	if len(types) == 0 {
		return i.ch.GetAnyType()
	}
	stringNumber := i.ch.GetUnionType([]*checker.Type{i.ch.GetStringType(), i.ch.GetNumberType()})
	dropAmbiguous, dropAny, dropAnonymous := false, false, false
	for _, t := range types {
		dropAmbiguous = dropAmbiguous || t == i.ch.GetStringType() || t == i.ch.GetNumberType()
		dropAny = dropAny || t.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsVoid) == 0
		dropAnonymous = dropAnonymous || t.Flags()&(checker.TypeFlagsNullable|checker.TypeFlagsAny|checker.TypeFlagsVoid) == 0 && t.ObjectFlags()&checker.ObjectFlagsAnonymous == 0
	}
	good, anonymous := []*checker.Type{}, []*checker.Type{}
	for _, t := range types {
		if dropAmbiguous && t == stringNumber || dropAny && t.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsVoid) != 0 || dropAnonymous && t.ObjectFlags()&checker.ObjectFlagsAnonymous != 0 {
			continue
		}
		if t.ObjectFlags()&checker.ObjectFlagsAnonymous != 0 {
			anonymous = append(anonymous, t)
		} else {
			good = append(good, t)
		}
	}
	if len(anonymous) > 0 {
		good = append(good, i.combineAnonymous(anonymous))
	}
	for j, t := range good {
		good[j] = i.ch.GetBaseTypeOfLiteralType(t)
	}
	return i.ch.GetWidenedType(i.ch.GetUnionTypeEx(good, checker.UnionReductionSubtype))
}
func (i *referenceUsageInference) symbolType(symbol *ast.Symbol) *checker.Type {
	if symbol.ValueDeclaration != nil {
		return i.ch.GetTypeOfSymbolAtLocation(symbol, symbol.ValueDeclaration)
	}
	return i.ch.GetAnyType()
}
func (i *referenceUsageInference) combineAnonymous(types []*checker.Type) *checker.Type {
	if len(types) == 1 {
		return types[0]
	}
	properties := map[string][]*checker.Type{}
	names := []string{}
	calls, constructs := []*checker.Signature{}, []*checker.Signature{}
	stringTypes, numberTypes := []*checker.Type{}, []*checker.Type{}
	stringReadonly, numberReadonly := false, false
	for _, t := range types {
		for _, p := range i.ch.GetPropertiesOfType(t) {
			if properties[p.Name] == nil {
				names = append(names, p.Name)
			}
			properties[p.Name] = append(properties[p.Name], i.symbolType(p))
		}
		calls = append(calls, i.ch.GetSignaturesOfType(t, checker.SignatureKindCall)...)
		constructs = append(constructs, i.ch.GetSignaturesOfType(t, checker.SignatureKindConstruct)...)
		if info := i.ch.GetIndexInfoOfType(t, i.ch.GetStringType()); info != nil {
			stringTypes = append(stringTypes, info.ValueType())
			stringReadonly = stringReadonly || info.IsReadonly()
		}
		if info := i.ch.GetIndexInfoOfType(t, i.ch.GetNumberType()); info != nil {
			numberTypes = append(numberTypes, info.ValueType())
			numberReadonly = numberReadonly || info.IsReadonly()
		}
	}
	members := []*ast.Symbol{}
	for _, name := range names {
		flags := ast.SymbolFlagsProperty
		if len(properties[name]) < len(types) {
			flags |= ast.SymbolFlagsOptional
		}
		members = append(members, i.ch.NewReferenceInferenceSymbol(flags, name, i.ch.GetUnionType(properties[name])))
	}
	indices := []*checker.IndexInfo{}
	if len(stringTypes) > 0 {
		indices = append(indices, i.ch.NewReferenceInferenceIndex(i.ch.GetStringType(), i.ch.GetUnionType(stringTypes), stringReadonly))
	}
	if len(numberTypes) > 0 {
		indices = append(indices, i.ch.NewReferenceInferenceIndex(i.ch.GetNumberType(), i.ch.GetUnionType(numberTypes), numberReadonly))
	}
	return i.ch.NewReferenceInferenceObject(types[0].Symbol(), members, calls, constructs, indices)
}
func (i *referenceUsageInference) fromUsage(u *referenceInferenceUsage) *checker.Type {
	return i.combine(i.types(u))
}
func (i *referenceUsageInference) types(u *referenceInferenceUsage) []*checker.Type {
	types := []*checker.Type{}
	if u.number {
		types = append(types, i.ch.GetNumberType())
	}
	if u.string_ {
		types = append(types, i.ch.GetStringType())
	}
	if u.ambiguous {
		types = append(types, i.ch.GetUnionType([]*checker.Type{i.ch.GetStringType(), i.ch.GetNumberType()}))
	}
	if u.numberIndex != nil {
		types = append(types, i.ch.NewReferenceInferenceArray(i.fromUsage(u.numberIndex)))
	}
	if len(u.names) > 0 || len(u.constructs) > 0 || u.stringIndex != nil {
		types = append(types, i.structural(u))
	}
	candidates := make([]*checker.Type, 0, len(u.candidates))
	for _, t := range u.candidates {
		candidates = append(candidates, i.ch.GetBaseTypeOfLiteralType(t))
	}
	if len(u.calls) > 0 {
		types = append(types, i.ch.GetUnionTypeEx(append([]*checker.Type{i.structural(u)}, candidates...), checker.UnionReductionSubtype))
	} else {
		types = append(types, candidates...)
	}
	return append(types, i.named(u)...)
}
func (i *referenceUsageInference) structural(u *referenceInferenceUsage) *checker.Type {
	members := []*ast.Symbol{}
	for _, name := range u.names {
		members = append(members, i.ch.NewReferenceInferenceSymbol(ast.SymbolFlagsProperty, name, i.fromUsage(u.properties[name])))
	}
	calls, constructs := []*checker.Signature{}, []*checker.Signature{}
	if u.calls != nil {
		calls = append(calls, i.signature(u.calls))
	}
	if u.constructs != nil {
		constructs = append(constructs, i.signature(u.constructs))
	}
	indices := []*checker.IndexInfo{}
	if u.stringIndex != nil {
		indices = append(indices, i.ch.NewReferenceInferenceIndex(i.ch.GetStringType(), i.fromUsage(u.stringIndex), false))
	}
	return i.ch.NewReferenceInferenceObject(nil, members, calls, constructs, indices)
}
func (i *referenceUsageInference) signature(calls []*referenceInferenceCall) *checker.Signature {
	count := 0
	for _, call := range calls {
		count = max(count, len(call.arguments))
	}
	parameters := []*ast.Symbol{}
	for index := 0; index < count; index++ {
		types := []*checker.Type{}
		flags := ast.SymbolFlagsFunctionScopedVariable
		for _, call := range calls {
			if index < len(call.arguments) {
				types = append(types, call.arguments[index])
			} else {
				types = append(types, i.ch.GetUndefinedType())
				flags |= ast.SymbolFlagsOptional
			}
		}
		parameters = append(parameters, i.ch.NewReferenceInferenceSymbol(flags, fmt.Sprintf("arg%d", index), i.combine(types)))
	}
	usages := []*referenceInferenceUsage{}
	for _, call := range calls {
		usages = append(usages, call.result)
	}
	return i.ch.NewReferenceInferenceSignature(parameters, i.fromUsage(i.combineUsages(usages)), count)
}
func (i *referenceUsageInference) functionType(calls []*referenceInferenceCall) *checker.Type {
	return i.ch.NewReferenceInferenceObject(nil, nil, []*checker.Signature{i.signature(calls)}, nil, nil)
}
func (i *referenceUsageInference) named(u *referenceInferenceUsage) []*checker.Type {
	if len(u.names) == 0 {
		return nil
	}
	builtins := []*checker.Type{i.ch.GetStringType(), i.ch.GetNumberType(), i.ch.NewReferenceInferenceArray(i.ch.GetAnyType()), i.ch.NewReferenceInferencePromise(i.ch.GetAnyType())}
	matches := []*checker.Type{}
	for _, t := range builtins {
		if t == nil {
			continue
		}
		valid := true
		for _, name := range u.names {
			p := u.properties[name]
			source := i.ch.GetTypeOfPropertyOfType(t, name)
			if source == nil {
				valid = false
				break
			}
			if p.calls != nil {
				if len(i.ch.GetSignaturesOfType(source, checker.SignatureKindCall)) == 0 || !i.ch.IsTypeAssignableTo(source, i.functionType(p.calls)) {
					valid = false
					break
				}
			} else if !i.ch.IsTypeAssignableTo(source, i.fromUsage(p)) {
				valid = false
				break
			}
		}
		if valid {
			matches = append(matches, t)
		}
	}
	if len(matches) == 0 || len(matches) >= 3 {
		return nil
	}
	for index, t := range matches {
		matches[index] = i.instantiation(t, u)
	}
	return matches
}
func (i *referenceUsageInference) instantiation(t *checker.Type, u *referenceInferenceUsage) *checker.Type {
	if t.ObjectFlags()&checker.ObjectFlagsReference == 0 {
		return t
	}
	generic := t.Target()
	params := generic.AsInterfaceType().TypeParameters()
	if len(params) != 1 {
		return t
	}
	types := []*checker.Type{}
	for _, name := range u.names {
		types = append(types, i.inferTypeParameters(i.ch.GetTypeOfPropertyOfType(generic, name), i.fromUsage(u.properties[name]), params[0])...)
	}
	element := i.combine(types)
	switch t.Symbol().Name {
	case "Array":
		return i.ch.NewReferenceInferenceArray(element)
	case "Promise":
		return i.ch.NewReferenceInferencePromise(element)
	case "string":
		return i.ch.GetStringType()
	case "number":
		return i.ch.GetNumberType()
	}
	panic("unexpected inference builtin")
}
func (i *referenceUsageInference) inferTypeParameters(generic, usage, parameter *checker.Type) []*checker.Type {
	if generic == parameter {
		return []*checker.Type{usage}
	}
	if generic.Flags()&checker.TypeFlagsUnionOrIntersection != 0 {
		types := []*checker.Type{}
		for _, t := range generic.Types() {
			types = append(types, i.inferTypeParameters(t, usage, parameter)...)
		}
		return types
	}
	if generic.ObjectFlags()&checker.ObjectFlagsReference != 0 && usage.ObjectFlags()&checker.ObjectFlagsReference != 0 {
		a, b := i.ch.GetTypeArguments(generic), i.ch.GetTypeArguments(usage)
		types := []*checker.Type{}
		for index, t := range a {
			if index < len(b) {
				types = append(types, i.inferTypeParameters(t, b[index], parameter)...)
			}
		}
		return types
	}
	a, b := i.ch.GetSignaturesOfType(generic, checker.SignatureKindCall), i.ch.GetSignaturesOfType(usage, checker.SignatureKindCall)
	if len(a) != 1 || len(b) != 1 {
		return nil
	}
	types := []*checker.Type{}
	for index, p := range a[0].Parameters() {
		if index >= len(b[0].Parameters()) {
			break
		}
		genericType := i.symbolType(p)
		if declaration := a[0].Declaration(); declaration != nil && index < len(declaration.Parameters()) && declaration.Parameters()[index].AsParameterDeclaration().DotDotDotToken != nil {
			if element := i.ch.GetReferenceInferenceArrayElement(genericType); element != nil {
				genericType = element
			}
		}
		target := b[0].Parameters()[index]
		targetType := i.symbolType(target)
		if target.Flags&ast.SymbolFlagsTransient != 0 {
			if known := i.ch.GetReferenceInferenceSymbolType(target); known != nil {
				targetType = known
			}
		}
		types = append(types, i.inferTypeParameters(genericType, targetType, parameter)...)
	}
	return append(types, i.inferTypeParameters(i.ch.GetReturnTypeOfSignature(a[0]), i.ch.GetReturnTypeOfSignature(b[0]), parameter)...)
}
func (i *referenceUsageInference) contextual(node *ast.Node, u *referenceInferenceUsage) {
	if ast.IsExpressionNode(node) {
		referenceInferenceCandidate(u, i.ch.GetContextualType(node, checker.ContextFlagsNone), false)
	}
}
func (i *referenceUsageInference) calculate(node *ast.Node, u *referenceInferenceUsage) {
	if node == nil || i.ctx.Err() != nil {
		return
	}
	for node.Parent != nil && (ast.IsQualifiedName(node.Parent) || ast.IsPropertyAccessExpression(node.Parent)) && node.Parent.Name() == node {
		node = node.Parent
	}
	parent := node.Parent
	if parent == nil {
		return
	}
	switch parent.Kind {
	case ast.KindExpressionStatement:
		typ := i.ch.GetAnyType()
		if ast.IsCallExpression(node) {
			typ = i.ch.GetVoidType()
		}
		referenceInferenceCandidate(u, typ, false)
	case ast.KindPostfixUnaryExpression:
		u.number = true
	case ast.KindPrefixUnaryExpression:
		switch parent.AsPrefixUnaryExpression().Operator {
		case ast.KindPlusPlusToken, ast.KindMinusMinusToken, ast.KindMinusToken, ast.KindTildeToken:
			u.number = true
		case ast.KindPlusToken:
			u.ambiguous = true
		}
	case ast.KindBinaryExpression:
		i.binary(node, parent, u)
	case ast.KindCaseClause, ast.KindDefaultClause:
		if parent.Parent != nil && parent.Parent.Parent != nil {
			referenceInferenceCandidate(u, i.ch.GetTypeAtLocation(parent.Parent.Parent.Expression()), false)
		}
	case ast.KindCallExpression, ast.KindNewExpression:
		if parent.Expression() != node {
			i.contextual(node, u)
			return
		}
		call := &referenceInferenceCall{result: &referenceInferenceUsage{}}
		for _, argument := range parent.Arguments() {
			call.arguments = append(call.arguments, i.ch.GetTypeAtLocation(argument))
		}
		i.calculate(parent, call.result)
		if ast.IsCallExpression(parent) {
			u.calls = append(u.calls, call)
		} else {
			u.constructs = append(u.constructs, call)
		}
	case ast.KindPropertyAccessExpression:
		i.calculate(parent, i.property(u, parent.Name().Text()))
	case ast.KindElementAccessExpression:
		if parent.AsElementAccessExpression().ArgumentExpression == node {
			u.ambiguous = true
			return
		}
		index := i.ch.GetTypeAtLocation(parent.AsElementAccessExpression().ArgumentExpression)
		use := &referenceInferenceUsage{}
		i.calculate(parent, use)
		if index.Flags()&checker.TypeFlagsNumberLike != 0 {
			u.numberIndex = use
		} else {
			u.stringIndex = use
		}
	case ast.KindPropertyAssignment, ast.KindShorthandPropertyAssignment:
		target := parent.Parent
		if target != nil && target.Parent != nil && ast.IsVariableDeclaration(target.Parent) {
			target = target.Parent
		}
		if target != nil {
			referenceInferenceCandidate(u, i.ch.GetTypeAtLocation(target), true)
		}
	case ast.KindPropertyDeclaration:
		referenceInferenceCandidate(u, i.ch.GetTypeAtLocation(parent.Parent), true)
	case ast.KindVariableDeclaration:
		if parent.Name() == node {
			if initializer := parent.Initializer(); initializer != nil {
				referenceInferenceCandidate(u, i.ch.GetTypeAtLocation(initializer), false)
			}
		} else {
			i.contextual(node, u)
		}
	default:
		i.contextual(node, u)
	}
}
func (i *referenceUsageInference) binary(node, parent *ast.Node, u *referenceInferenceUsage) {
	data := parent.AsBinaryExpression()
	other := data.Left
	if other == node {
		other = data.Right
	}
	switch data.OperatorToken.Kind {
	case ast.KindAsteriskAsteriskToken, ast.KindAsteriskToken, ast.KindSlashToken, ast.KindPercentToken, ast.KindLessThanLessThanToken, ast.KindGreaterThanGreaterThanToken, ast.KindGreaterThanGreaterThanGreaterThanToken, ast.KindAmpersandToken, ast.KindBarToken, ast.KindCaretToken, ast.KindMinusEqualsToken, ast.KindAsteriskAsteriskEqualsToken, ast.KindAsteriskEqualsToken, ast.KindSlashEqualsToken, ast.KindPercentEqualsToken, ast.KindAmpersandEqualsToken, ast.KindBarEqualsToken, ast.KindCaretEqualsToken, ast.KindLessThanLessThanEqualsToken, ast.KindGreaterThanGreaterThanGreaterThanEqualsToken, ast.KindGreaterThanGreaterThanEqualsToken, ast.KindMinusToken, ast.KindLessThanToken, ast.KindLessThanEqualsToken, ast.KindGreaterThanToken, ast.KindGreaterThanEqualsToken:
		t := i.ch.GetTypeAtLocation(other)
		if t.Flags()&checker.TypeFlagsEnumLike != 0 {
			referenceInferenceCandidate(u, t, false)
		} else {
			u.number = true
		}
	case ast.KindPlusEqualsToken, ast.KindPlusToken:
		t := i.ch.GetTypeAtLocation(other)
		switch {
		case t.Flags()&checker.TypeFlagsEnumLike != 0:
			referenceInferenceCandidate(u, t, false)
		case t.Flags()&checker.TypeFlagsNumberLike != 0:
			u.number = true
		case t.Flags()&checker.TypeFlagsStringLike != 0:
			u.string_ = true
		case t.Flags()&checker.TypeFlagsAny != 0:
		default:
			u.ambiguous = true
		}
	case ast.KindEqualsToken, ast.KindEqualsEqualsToken, ast.KindEqualsEqualsEqualsToken, ast.KindExclamationEqualsEqualsToken, ast.KindExclamationEqualsToken, ast.KindAmpersandAmpersandEqualsToken, ast.KindQuestionQuestionEqualsToken, ast.KindBarBarEqualsToken:
		referenceInferenceCandidate(u, i.ch.GetTypeAtLocation(other), false)
	case ast.KindInKeyword:
		if node == data.Left {
			u.string_ = true
		}
	case ast.KindBarBarToken, ast.KindQuestionQuestionToken:
		outer := parent.Parent
		if node == data.Left && outer != nil && (ast.IsVariableDeclaration(outer) || ast.IsBinaryExpression(outer) && outer.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken) {
			referenceInferenceCandidate(u, i.ch.GetTypeAtLocation(data.Right), false)
		}
	}
}
func (i *referenceUsageInference) usage(references []*ast.Node) *referenceInferenceUsage {
	u := &referenceInferenceUsage{}
	for _, reference := range references {
		i.calculate(reference, u)
	}
	return u
}
func (i *referenceUsageInference) variable(name *ast.Node) *checker.Type {
	return i.fromUsage(i.usage(i.references(name)))
}
func (i *referenceUsageInference) functionReferences(fn *ast.Node) ([]*ast.Node, bool) {
	var name *ast.Node
	switch fn.Kind {
	case ast.KindConstructor:
		name = astnav.FindChildOfKind(fn, ast.KindConstructorKeyword, ast.GetSourceFileOfNode(fn))
	case ast.KindArrowFunction, ast.KindFunctionExpression:
		parent := fn.Parent
		if parent != nil && (ast.IsVariableDeclaration(parent) || ast.IsPropertyDeclaration(parent)) && ast.IsIdentifier(parent.Name()) {
			name = parent.Name()
		} else {
			name = fn.Name()
		}
	case ast.KindFunctionDeclaration, ast.KindMethodDeclaration, ast.KindMethodSignature:
		name = fn.Name()
	}
	if name == nil {
		return nil, false
	}
	return i.references(name), true
}
func (i *referenceUsageInference) parameters(fn *ast.Node) []*checker.Type {
	references, found := i.functionReferences(fn)
	parameters := fn.Parameters()
	result := make([]*checker.Type, len(parameters))
	if !found || len(references) == 0 {
		for index, p := range parameters {
			result[index] = i.ch.GetAnyType()
			if ast.IsIdentifier(p.Name()) {
				result[index] = i.variable(p.Name())
			}
		}
		return result
	}
	usage := i.usage(references)
	calls := append(append([]*referenceInferenceCall{}, usage.constructs...), usage.calls...)
	for index, p := range parameters {
		types := []*checker.Type{}
		rest := p.AsParameterDeclaration().DotDotDotToken != nil
		for _, call := range calls {
			if index >= len(call.arguments) {
				types = append(types, i.ch.GetUndefinedType())
			} else if rest {
				for _, t := range call.arguments[index:] {
					types = append(types, i.ch.GetBaseTypeOfLiteralType(t))
				}
			} else {
				types = append(types, i.ch.GetBaseTypeOfLiteralType(call.arguments[index]))
			}
		}
		if ast.IsIdentifier(p.Name()) {
			inferred := i.types(i.usage(i.references(p.Name())))
			for _, t := range inferred {
				if rest {
					t = i.ch.GetReferenceInferenceArrayElement(t)
				}
				if t != nil {
					types = append(types, t)
				}
			}
		}
		result[index] = i.combine(types)
		if rest {
			result[index] = i.ch.NewReferenceInferenceArray(result[index])
		}
	}
	return result
}

func referenceInferenceSuggestToError(code int32) int32 {
	switch code {
	case 7046:
		return 7034
	case 7043:
		return 7005
	case 7044:
		return 7006
	case 7047:
		return 7019
	case 7048:
		return 7033
	case 7050:
		return 7010
	case 7049:
		return 7032
	case 7045:
		return 7008
	}
	return code
}
func referenceInferenceTitle(code int32, token, name *ast.Node, ctx context.Context) string {
	message := diagnostics.Infer_type_of_0_from_usage
	switch referenceInferenceSuggestToError(code) {
	case 7006:
		if fn := ast.GetContainingFunction(token); fn == nil || !ast.IsSetAccessorDeclaration(fn) {
			message = diagnostics.Infer_parameter_types_from_usage
		}
	case 7019:
		message = diagnostics.Infer_parameter_types_from_usage
	case 2683:
		message = diagnostics.Infer_this_type_of_0_from_usage
	}
	return message.Localize(locale.FromContext(ctx), scanner.GetTextOfNode(name))
}

type referenceInferenceFix struct {
	*referenceUsageInference
	tracker *change.Tracker
	imports autoimport.ImportAdder
}

func (f *referenceInferenceFix) annotate(file *ast.SourceFile, declaration *ast.Node, typ *checker.Type) bool {
	ids := map[*ast.Node]*ast.Symbol{}
	node := f.ch.GetReferenceInferenceTypeNode(typ, declaration, ids, f.tracker.EmitContext)
	if node == nil {
		return false
	}
	if replacement, symbols := autoimport.TryGetAutoImportableReferenceFromTypeNode(node, ids); replacement != nil && f.tracker.TryInsertTypeAnnotation(file, declaration, replacement) {
		for _, symbol := range symbols {
			if f.imports != nil {
				f.imports.AddImportFromExportedSymbol(symbol, true)
			}
		}
		return true
	}
	return f.tracker.TryInsertTypeAnnotation(file, declaration, node)
}
func (f *referenceInferenceFix) setAccessor(file *ast.SourceFile, fn *ast.Node) {
	parameters := fn.Parameters()
	if len(parameters) == 0 || !ast.IsIdentifier(fn.Name()) || !ast.IsIdentifier(parameters[0].Name()) {
		return
	}
	typ := f.variable(fn.Name())
	if typ == f.ch.GetAnyType() {
		typ = f.variable(parameters[0].Name())
	}
	f.annotate(file, parameters[0], typ)
}
func referenceInferenceChange(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (*ast.Node, error) {
	token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
	if token == nil || token.Parent == nil || (!ast.IsParameterPropertyModifier(token.Kind) && token.Kind != ast.KindIdentifier && token.Kind != ast.KindDotDotDotToken && token.Kind != ast.KindThisKeyword) {
		return nil, nil
	}
	ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
	defer done()
	imports, err := createImportAdder(ctx, request, ch)
	if err != nil {
		return nil, err
	}
	f := &referenceInferenceFix{referenceUsageInference: &referenceUsageInference{ctx: ctx, request: request, ch: ch}, tracker: tracker, imports: imports}
	writeImports := func() {
		if imports != nil {
			for _, edit := range imports.Edits() {
				tracker.ReplaceRangeWithText(request.SourceFile, edit.Range, edit.NewText)
			}
		}
	}
	parent := token.Parent
	file := request.SourceFile
	code := referenceInferenceSuggestToError(request.ErrorCode)
	mark := func(node *ast.Node) bool {
		if seen[node] {
			return false
		}
		seen[node] = true
		return true
	}
	switch code {
	case 7008, 7034:
		if ast.IsVariableDeclaration(parent) && mark(parent) || ast.IsPropertyDeclaration(parent) || ast.IsPropertySignatureDeclaration(parent) {
			if ast.IsIdentifier(parent.Name()) {
				f.annotate(file, parent, f.variable(parent.Name()))
			}
			writeImports()
			return parent, ctx.Err()
		}
		if ast.IsPropertyAccessExpression(parent) {
			typ := f.variable(parent.Name())
			node := ch.GetReferenceInferenceTypeNode(typ, parent, nil, tracker.EmitContext)
			if node != nil {
				if parent.Parent == nil || parent.Parent.Parent == nil || !ast.IsExpressionStatement(parent.Parent.Parent) {
					panic(&change.InvalidEdits{Reason: "inference property access has no expression statement"})
				}
				host := parent.Parent.Parent
				tags := []*ast.Node{}
				for _, doc := range host.JSDoc(file) {
					if doc.AsJSDoc().Tags != nil {
						tags = append(tags, doc.AsJSDoc().Tags.Nodes...)
					}
				}
				merged := false
				for index, tag := range tags {
					if tag.Kind == ast.KindJSDocTypeTag {
						tags[index] = tracker.NewJSDocTypeTag(tracker.NewIdentifier("type"), tracker.NewJSDocTypeExpression(node), tag.AsJSDocTypeTag().Comment)
						merged = true
						break
					}
				}
				if !merged {
					tags = append(tags, tracker.NewJSDocTypeTag(tracker.NewIdentifier("type"), tracker.NewJSDocTypeExpression(node), nil))
				}
				tracker.ReplaceReferenceJSDocComment(file, host, tags)
			}
			writeImports()
			return parent, ctx.Err()
		}
		return nil, nil
	case 7005:
		symbol := ch.GetSymbolAtLocation(token)
		if symbol != nil && symbol.ValueDeclaration != nil && ast.IsVariableDeclaration(symbol.ValueDeclaration) && mark(symbol.ValueDeclaration) {
			declaration := symbol.ValueDeclaration
			if ast.IsIdentifier(declaration.Name()) {
				f.annotate(ast.GetSourceFileOfNode(declaration), declaration, f.variable(declaration.Name()))
			}
			writeImports()
			return declaration, ctx.Err()
		}
		return nil, nil
	}
	fn := ast.GetContainingFunction(token)
	if fn == nil {
		return nil, nil
	}
	var declaration *ast.Node
	switch code {
	case 7006, 7019:
		if code == 7006 && ast.IsSetAccessorDeclaration(fn) {
			f.setAccessor(file, fn)
			declaration = fn
			break
		}
		if mark(fn) {
			if !ast.IsParameterDeclaration(parent) {
				panic(&change.InvalidEdits{Reason: "inference diagnostic does not identify a parameter"})
			}
			if ast.IsIdentifier(parent.Name()) {
				parameters := fn.Parameters()
				types := f.parameters(fn)
				needParens := ast.IsArrowFunction(fn) && astnav.FindChildOfKind(fn, ast.KindOpenParenToken, file) == nil
				if needParens && len(parameters) > 0 {
					tracker.InsertNodeBefore(file, parameters[0], tracker.NewToken(ast.KindOpenParenToken), false, change.LeadingTriviaOptionNone)
				}
				for index, p := range parameters {
					if p.Type() == nil && p.Initializer() == nil {
						f.annotate(file, p, types[index])
					}
				}
				if needParens && len(parameters) > 0 {
					tracker.InsertNodeAfter(file, parameters[len(parameters)-1], tracker.NewToken(ast.KindCloseParenToken))
				}
			}
			declaration = parent
		}
	case 7033, 7010:
		if ast.IsGetAccessorDeclaration(fn) && ast.IsIdentifier(fn.Name()) {
			f.annotate(file, fn, f.variable(fn.Name()))
			declaration = fn
		}
	case 7032:
		if ast.IsSetAccessorDeclaration(fn) {
			f.setAccessor(file, fn)
			declaration = fn
		}
	case 2683:
		if (ast.IsFunctionExpression(fn) || ast.IsFunctionDeclaration(fn)) && mark(fn) {
			references, found := f.functionReferences(fn)
			if found && len(references) > 0 {
				typ := f.combine(f.usage(references).thisTypes)
				node := ch.GetReferenceInferenceTypeNode(typ, fn, nil, tracker.EmitContext)
				if node != nil {
					lparen := astnav.FindChildOfKind(fn, ast.KindOpenParenToken, file)
					if lparen != nil {
						suffix := ""
						if len(fn.Parameters()) > 0 {
							suffix = ", "
						}
						tracker.InsertNodeAt(file, core.TextPos(scanner.GetTokenPosOfNode(lparen, file, false)+1), node, change.NodeOptions{Prefix: "this: ", Suffix: suffix})
					}
				}
			}
			declaration = fn
		}
	}
	writeImports()
	return declaration, ctx.Err()
}

var referenceInferenceCodes = []int32{7034, 7005, 7006, 7019, 7033, 7010, 7032, 7008, 7046, 7043, 7044, 7047, 7048, 7050, 7049, 7045, 2683}
var ReferenceInferFromUsageProvider = &CodeFixProvider{Name: "inferFromUsage", ErrorCodes: referenceInferenceCodes, FixIds: []string{"inferFromUsage"},
	GetCodeActions: func(ctx context.Context, request *CodeFixContext) ([]*CodeAction, error) {
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		declaration, err := referenceInferenceChange(ctx, request, tracker, map[*ast.Node]bool{})
		if err != nil {
			return nil, err
		}
		if declaration == nil || ast.GetNameOfDeclaration(declaration) == nil {
			return nil, ctx.Err()
		}
		changes := referenceTrackerFileChanges(tracker)
		if len(changes) == 0 {
			return nil, ctx.Err()
		}
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		title := referenceInferenceTitle(request.ErrorCode, token, ast.GetNameOfDeclaration(declaration), ctx)
		return []*CodeAction{{Description: title, ReferenceChanges: changes, FixID: "inferFromUsage", FixAllDescription: diagnostics.Infer_all_types_from_usage.Localize(locale.FromContext(ctx))}}, ctx.Err()
	},
	GetAllCodeActions: func(ctx context.Context, request *CodeFixContext) (*CombinedCodeActions, error) {
		tracker := change.NewTracker(ctx, request.Program.Options(), request.LS.FormatOptions(), request.LS.converters)
		seen := map[*ast.Node]bool{}
		for _, diagnostic := range getAllDiagnostics(ctx, request.Program, request.SourceFile) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if !slices.Contains(referenceInferenceCodes, diagnostic.Code()) {
				continue
			}
			individual := *request
			individual.Span = core.NewTextRange(diagnostic.Pos(), diagnostic.End())
			individual.ErrorCode = diagnostic.Code()
			if _, err := referenceInferenceChange(ctx, &individual, tracker, seen); err != nil {
				return nil, err
			}
		}
		return &CombinedCodeActions{Description: diagnostics.Infer_all_types_from_usage.Localize(locale.FromContext(ctx)), ReferenceChanges: referenceTrackerFileChanges(tracker)}, ctx.Err()
	}}

// Reference 2683 related information points at the first anonymous-function token,
// while the native diagnostic head can point at its assigned variable name.
func referenceInferenceDiagnosticHeads(file *ast.SourceFile, items []*ast.Diagnostic) []*ast.Diagnostic {
	result := make([]*ast.Diagnostic, 0, len(items))
	for _, item := range items {
		if item.Code() == 2683 && len(item.RelatedInformation()) > 0 {
			token := astnav.GetTokenAtPosition(file, item.Pos())
			if token != nil && token.Kind == ast.KindThisKeyword {
				fn := ast.GetThisContainer(token, false, false)
				if fn != nil && ast.IsFunctionExpression(fn) && fn.Name() == nil {
					keyword := astnav.GetTokenAtPosition(file, scanner.GetTokenPosOfNode(fn, file, false))
					if keyword != nil {
						related := slices.Clone(item.RelatedInformation())
						changed := false
						for index, info := range related {
							if info.Code() == diagnostics.An_outer_value_of_this_is_shadowed_by_this_container.Code() {
								info = info.Clone()
								info.SetLocation(core.NewTextRange(scanner.GetTokenPosOfNode(keyword, file, false), keyword.End()))
								related[index] = info
								changed = true
							}
						}
						if changed {
							item = item.Clone().SetRelatedInfo(related)
						}
					}
				}
			}
		}
		result = append(result, item)
	}
	return result
}
func referenceInferenceSuggestionOrder(a, b *ast.Diagnostic) int {
	if result := a.Pos() - b.Pos(); result != 0 {
		return result
	}
	// Computed editor suggestions precede checker inference suggestions in TS 6.
	inferredA, inferredB := a.Code() >= 7043 && a.Code() <= 7050, b.Code() >= 7043 && b.Code() <= 7050
	if inferredA != inferredB {
		if inferredA {
			return 1
		}
		return -1
	}
	return 0
}

func (l *LanguageService) getReferenceInferenceSymbols(ctx context.Context, position int, node *ast.Node, program *compiler.Program, sourceFiles []*ast.SourceFile, options refOptions, checker *checker.Checker) []*SymbolAndEntries {
	// !!! cancellationToken
	sourceFilesSet := collections.NewSetWithSizeHint[string](len(sourceFiles))
	for _, file := range sourceFiles {
		sourceFilesSet.Add(file.FileName())
	}

	if options.use == referenceUseReferences || options.use == referenceUseRename {
		node = getAdjustedLocation(node, options.use == referenceUseRename, ast.GetSourceFileOfNode(node))
	}

	if node.Kind == ast.KindSourceFile {
		resolvedRef := getReferenceAtPosition(node.AsSourceFile(), position, program)
		if resolvedRef == nil || resolvedRef.file == nil {
			return nil
		}

		if moduleSymbol := checker.GetMergedSymbol(resolvedRef.file.Symbol); moduleSymbol != nil {
			return l.getReferencedSymbolsForModule(ctx, program, moduleSymbol /*excludeImportTypeOfExportEquals*/, false, sourceFiles, sourceFilesSet)
		}

		// !!! not implemented
		// fileIncludeReasons := program.getFileIncludeReasons();
		// if (!fileIncludeReasons) {
		// 	return nil
		// }
		return []*SymbolAndEntries{{
			definition: &Definition{Kind: definitionKindTripleSlashReference, tripleSlashFileRef: &tripleSlashDefinition{reference: resolvedRef.reference}},
			references: getReferencesForNonModule(resolvedRef.file, program /*fileIncludeReasons,*/),
		}}
	}

	if !options.implementations {
		// !!! cancellationToken
		if special := getReferencedSymbolsSpecial(node, sourceFiles); special != nil {
			return special
		}
	}

	// constructors should use the class symbol, detected by name, if present
	symbol := checker.GetSymbolAtLocation(core.IfElse(node.Kind == ast.KindConstructor && node.Parent.Name() != nil, node.Parent.Name(), node))
	// Could not find a symbol e.g. unknown identifier
	if symbol == nil {
		// String literal might be a property (and thus have a symbol), so do this here rather than in getReferencedSymbolsSpecial.
		if !options.implementations && ast.IsStringLiteralLike(node) {
			if isModuleSpecifierLike(node) {
				// !!! not implemented
				// fileIncludeReasons := program.GetFileIncludeReasons()
				// if referencedFile := program.GetResolvedModuleFromModuleSpecifier(node, nil /*sourceFile*/); referencedFile != nil {
				// return []*SymbolAndEntries{{
				// 	definition: &Definition{Kind: definitionKindString, node: node},
				// 	references: getReferencesForNonModule(referencedFile, program /*fileIncludeReasons,*/),
				// }}
				// }
				// Fall through to string literal references. This is not very likely to return
				// anything useful, but I guess it's better than nothing, and there's an existing
				// test that expects this to happen (fourslash/cases/untypedModuleImport.ts).
			}
			return l.getReferencesForStringLiteral(ctx, node, sourceFiles, checker)
		}
		return nil
	}

	if symbol.Name == ast.InternalSymbolNameExportEquals {
		if symbol.Parent == nil {
			return nil
		}
		return l.getReferencedSymbolsForModule(ctx, program, symbol.Parent, false /*excludeImportTypeOfExportEquals*/, sourceFiles, sourceFilesSet)
	}

	moduleReferences := l.getReferencedSymbolsForModuleIfDeclaredBySourceFile(ctx, symbol, program, sourceFiles, checker, options, sourceFilesSet)
	if moduleReferences != nil && symbol.Flags&ast.SymbolFlagsTransient == 0 {
		return moduleReferences
	}

	aliasedSymbol := getMergedAliasedSymbolOfNamespaceExportDeclaration(node, symbol, checker)
	moduleReferencesOfExportTarget := l.getReferencedSymbolsForModuleIfDeclaredBySourceFile(ctx, aliasedSymbol, program, sourceFiles, checker, options, sourceFilesSet)

	references := getReferencedSymbolsForSymbol(ctx, program, symbol, node, sourceFiles, sourceFilesSet, checker, options)
	return l.mergeReferences(program, moduleReferences, references, moduleReferencesOfExportTarget)
}

func (i *referenceUsageInference) references(node *ast.Node) []*ast.Node {
	if node == nil || i.ctx.Err() != nil {
		return nil
	}
	if node.Kind == ast.KindConstructorKeyword && node.Parent != nil && ast.IsConstructorDeclaration(node.Parent) {
		node = node.Parent
	}
	groups := i.request.LS.getReferenceInferenceSymbols(i.ctx, -1, node, i.request.Program, i.request.Program.GetSourceFiles(), refOptions{use: referenceUseOther}, i.ch)
	result := []*ast.Node{}
	for _, group := range groups {
		for _, entry := range group.references {
			if entry.node != nil && ast.IsIdentifier(entry.node) {
				result = append(result, entry.node)
			}
		}
	}
	return result
}

var ReferenceImplicitThisProvider = newReferenceSyntaxFixProvider("fixImplicitThis", []int32{2683}, diagnostics.Fix_all_implicit_this_errors,
	func(ctx context.Context, request *CodeFixContext, tracker *change.Tracker, seen map[*ast.Node]bool) (string, error) {
		token := astnav.GetTokenAtPosition(request.SourceFile, request.Span.Pos())
		if token == nil || token.Kind != ast.KindThisKeyword {
			return "", nil
		}
		fn := ast.GetThisContainer(token, false, false)
		if !ast.IsFunctionDeclaration(fn) && !ast.IsFunctionExpression(fn) {
			return "", nil
		}
		outer := ast.GetThisContainer(fn, false, false)
		if outer == nil || ast.IsSourceFile(outer) {
			return "", nil
		}
		keyword := astnav.FindChildOfKind(fn, ast.KindFunctionKeyword, request.SourceFile)
		if keyword == nil || fn.Body() == nil {
			return "", nil
		}
		name := fn.Name()
		body := fn.Body()
		if ast.IsFunctionExpression(fn) {
			if name != nil {
				ch, done := request.Program.GetTypeCheckerForFile(ctx, request.SourceFile)
				references := referenceUnusedReferences(ch, request.SourceFile, name)
				done()
				for _, ref := range references {
					if ref.Pos() >= body.Pos() && ref.End() <= body.End() {
						return "", nil
					}
				}
			}
			tracker.DeleteRange(request.SourceFile, core.NewTextRange(scanner.GetTokenPosOfNode(keyword, request.SourceFile, false), keyword.End()))
			if name != nil {
				tracker.DeleteRange(request.SourceFile, core.NewTextRange(name.Pos(), name.End()))
			}
			tracker.InsertText(request.SourceFile, request.LS.converters.PositionToLineAndCharacter(request.SourceFile, core.TextPos(body.Pos())), " =>")
			text := "anonymous function"
			if name != nil {
				text = name.Text()
			}
			return diagnostics.Convert_function_expression_0_to_arrow_function.Localize(locale.FromContext(ctx), text), ctx.Err()
		}
		if name == nil {
			return "", nil
		}
		tracker.ReplaceNode(request.SourceFile, keyword, tracker.NewToken(ast.KindConstKeyword), nil)
		tracker.InsertText(request.SourceFile, request.LS.converters.PositionToLineAndCharacter(request.SourceFile, core.TextPos(name.End())), " = ")
		tracker.InsertText(request.SourceFile, request.LS.converters.PositionToLineAndCharacter(request.SourceFile, core.TextPos(body.Pos())), " =>")
		return diagnostics.Convert_function_declaration_0_to_arrow_function.Localize(locale.FromContext(ctx), name.Text()), ctx.Err()
	})

// CodeFixProvider represents a provider for a specific type of code fix
type CodeFixProvider struct {
	Name                      string
	ReferenceFixAllErrorCodes []int32
	ErrorCodes                []int32
	GetCodeActions            func(ctx context.Context, fixContext *CodeFixContext) ([]*CodeAction, error)
	FixIds                    []string
	GetAllCodeActions         func(ctx context.Context, fixContext *CodeFixContext) (*CombinedCodeActions, error)
}

// CodeFixContext contains the context needed to generate code fixes
type CodeFixContext struct {
	FixID      string
	SourceFile *ast.SourceFile
	Span       core.TextRange
	ErrorCode  int32
	Program    *compiler.Program
	LS         *LanguageService
	Diagnostic *lsproto.Diagnostic
	Params     *lsproto.CodeActionParams
}

// CodeAction represents a single code action fix
type CodeAction struct {
	ReferenceFixName  string
	ReferenceChanges  []ReferenceFileCodeChanges
	Description       string
	Changes           []*lsproto.TextEdit
	FixID             string
	FixAllDescription string
}

// Compare defines a total ordering for CodeAction values, comparing description
// then text edits lexicographically. Used with slices.BinarySearchFunc.
func (a *CodeAction) Compare(b *CodeAction) int {
	if c := strings.Compare(a.Description, b.Description); c != 0 {
		return c
	}
	if c := cmp.Compare(len(a.Changes), len(b.Changes)); c != 0 {
		return c
	}
	for i, edit := range a.Changes {
		if c := edit.Compare(b.Changes[i]); c != 0 {
			return c
		}
	}
	return 0
}

// CombinedCodeActions represents combined code actions for fix-all scenarios
type CombinedCodeActions struct {
	ReferenceChanges []ReferenceFileCodeChanges
	Description      string
	Changes          []*lsproto.TextEdit
}

// codeFixProviders is the list of all registered code fix providers
var codeFixProviders = []*CodeFixProvider{
	ReferenceImplicitThisProvider,
	ReferenceESModuleProvider,
	ReferenceAsyncConversionProvider,
	ReferenceFunctionClassProvider,
	ReferenceDecoratorMetadataProvider,
	ReferenceOverrideAccessorProvider,
	ReferenceUnmatchedParamProvider,
	ReferenceReturnValueProvider,
	ReferenceTypedefProvider,
	ReferenceAnnotateJSDocProvider,
	ReferenceInferFromUsageProvider,
	ReferenceMissingAwaitProvider,
	ReferenceMissingParametersProvider,
	ReferenceLocalExportProvider,
	ReferenceInvalidImportUsageProvider,
	ReferenceInvalidImportLocationProvider,
	ReferenceResolutionModeAttributeProvider,
	ReferenceRequireImportProvider,
	ReferenceTypeOnlyImportProvider,
	ReferenceOptionalPropertyUndefinedProvider,
	ReferenceEnableJSXProvider,
	ReferenceModuleOptionProvider,
	ReferenceTargetOptionProvider,
	ReferenceStrictPropertyUndefinedProvider,
	ReferenceStrictPropertyDefiniteProvider,
	ReferenceStrictPropertyInitializerProvider,
	ReferenceWrapJSXFragmentProvider,
	ReferenceMissingAsyncProvider,
	ReferenceMissingConstraintProvider,
	ReferenceJSDocTypePlainProvider,
	ReferenceJSDocTypeNullableProvider,
	ReferenceTypeOnlyExportProvider,
	ReferenceMissingConstProvider,
	ReferenceIncorrectNamedTupleProvider,
	ReferenceSuperBeforeThisProvider,
	ReferenceVoidPromiseProvider,
	ReferenceUnknownConversionProvider,
	ReferenceEmptyExportProvider,
	ReferenceMissingDeclarePropertyProvider,
	ReferenceMissingDecoratorInvocationProvider,
	ReferenceNamelessParameterProvider,
	ReferenceQualifiedNameToIndexedAccessProvider,
	ReferenceConvertConstToLetProvider,
	ReferenceLiteralMappedTypeProvider,
	ReferenceMappedObjectProvider,
	ReferenceMissingNewOperatorProvider,
	ReferenceAwaitInSyncFunctionProvider,
	ReferenceConstructorSuperCallProvider,
	ReferenceAbstractClassProvider,
	ReferenceMissingModuleTypeofProvider,
	ReferenceExpectedCommaProvider,
	ReferenceExtendsInterfaceBecomesImplementsProvider,
	ReferenceForgottenThisPropertyAccessProvider,
	ReferenceSpellingProvider,
	ReferenceMissingMemberProvider,
	ReferenceMissingCallParenthesesProvider,
	ReferenceInvalidJSXExpressionProvider,
	ReferenceInvalidJSXEntityProvider,
	ReferenceNaNEqualityProvider,
	ReferenceAsyncReturnTypeProvider,
	ReferenceNoPropertyAccessFromIndexSignatureProvider,
	ReferenceAddOverrideProvider,
	ReferenceRemoveOverrideProvider,
	ReferencePropertyAssignmentProvider,
	ReferenceUnreachableCodeProvider,
	ReferenceUnusedIdentifierProvider,
	ReferenceUnusedLabelProvider,
	ReferenceRemoveAccidentalCallParenthesesProvider,
	ReferenceRemoveUnnecessaryAwaitProvider,
	ReferenceSplitTypeOnlyImportProvider,
	ReferenceDefaultImportProvider,
	ReferenceUseBigintLiteralProvider,
	ReferenceWrapDecoratorProvider,
	ImportFixProvider,
	IsolatedDeclarationsFixProvider,
	FixClassIncorrectlyImplementsInterfaceProvider,
	// Add more code fix providers here as they are implemented
}

// ProvideCodeActions returns code actions for the given range and context
func (l *LanguageService) ProvideCodeActions(ctx context.Context, params *lsproto.CodeActionParams) (lsproto.CodeActionResponse, error) {
	program, file := l.getProgramAndFile(params.TextDocument.Uri)

	var actions []lsproto.CommandOrCodeAction

	if params.Context != nil && params.Context.Only != nil {
		for _, kind := range *params.Context.Only {
			matchingKinds := getOrganizeImportsActionsForKind(kind)
			for _, matchingKind := range matchingKinds {
				organizeAction := l.createOrganizeImportsAction(ctx, program, file, matchingKind)
				actions = append(actions, *organizeAction)
			}

			if isFixAllKind(kind) {
				fixAllAction, err := l.createFixAllAction(ctx, program, file, params.TextDocument.Uri)
				if err != nil {
					return lsproto.CodeActionResponse{}, err
				}
				if fixAllAction != nil {
					actions = append(actions, *fixAllAction)
				}
			}
		}
	}

	if params.Context != nil && params.Context.Diagnostics != nil && wantsQuickFixes(params.Context.Only) {
		fixIdSeen := make(map[string]*CodeFixProvider)

		var seen []*CodeAction // sorted for binary search dedup, dedup across all diagnostics and providers so if multiple diags produce the same codefix, only one is returned

		for _, diag := range params.Context.Diagnostics {
			if diag.Code == nil || diag.Code.Integer == nil {
				continue
			}

			errorCode := *diag.Code.Integer

			for _, provider := range codeFixProviders {
				if !containsErrorCode(provider.ErrorCodes, errorCode) {
					continue
				}

				position := l.converters.LineAndCharacterToPosition(file, diag.Range.Start)
				endPosition := l.converters.LineAndCharacterToPosition(file, diag.Range.End)
				fixContext := &CodeFixContext{
					SourceFile: file,
					Span:       core.NewTextRange(int(position), int(endPosition)),
					ErrorCode:  errorCode,
					Program:    program,
					LS:         l,
					Diagnostic: diag,
					Params:     params,
				}

				providerActions, err := provider.GetCodeActions(ctx, fixContext)
				if err != nil {
					return lsproto.CodeActionResponse{}, err
				}
				for _, action := range providerActions {
					i, found := slices.BinarySearchFunc(seen, action, (*CodeAction).Compare)
					if found {
						continue
					}
					seen = slices.Insert(seen, i, action)
					actions = append(actions, convertToLSPCodeAction(action, diag, params.TextDocument.Uri))
					if action.FixID != "" {
						fixIdSeen[action.FixID] = provider
					}
				}
			}
		}

		fixAllActions, err := l.getFixAllQuickFixes(ctx, program, file, params.TextDocument.Uri, fixIdSeen)
		if err != nil {
			return lsproto.CodeActionResponse{}, err
		}
		actions = append(actions, fixAllActions...)
	}

	return lsproto.CommandOrCodeActionArrayOrNull{CommandOrCodeActionArray: &actions}, nil
}

// getFixAllQuickFixes returns per-provider "Fix all in file" quickfix entries for providers
// that matched at least 2 diagnostics in the full file.
func (l *LanguageService) getFixAllQuickFixes(
	ctx context.Context,
	program *compiler.Program,
	file *ast.SourceFile,
	uri lsproto.DocumentUri,
	fixIdSeen map[string]*CodeFixProvider,
) ([]lsproto.CommandOrCodeAction, error) {
	var actions []lsproto.CommandOrCodeAction

	// Deduplicate providers; multiple fixIds may map to the same provider.
	var seen collections.Set[*CodeFixProvider]
	for _, provider := range fixIdSeen {
		if seen.Has(provider) {
			continue
		}
		seen.Add(provider)

		if provider.GetAllCodeActions == nil {
			continue
		}

		if !hasMultipleFixableDiagnostics(ctx, program, file, provider.ErrorCodes) {
			continue
		}

		fixContext := &CodeFixContext{
			SourceFile: file,
			Program:    program,
			LS:         l,
		}
		combined, err := provider.GetAllCodeActions(ctx, fixContext)
		if err != nil {
			return nil, err
		}
		if combined != nil && len(combined.Changes) > 0 {
			kind := lsproto.CodeActionKindQuickFix
			changes := map[lsproto.DocumentUri][]*lsproto.TextEdit{
				uri: combined.Changes,
			}
			actions = append(actions, lsproto.CommandOrCodeAction{
				CodeAction: &lsproto.CodeAction{
					Title: combined.Description,
					Kind:  &kind,
					Edit:  &lsproto.WorkspaceEdit{Changes: &changes},
				},
			})
		}
	}

	return actions, nil
}

// hasMultipleFixableDiagnostics returns true if the file has at least 2 diagnostics
// matching the given error codes. Checks all diagnostic sources (semantic,
// syntactic, suggestion, declaration) to match ProvideDiagnostics.
func hasMultipleFixableDiagnostics(ctx context.Context, program *compiler.Program, file *ast.SourceFile, errorCodes []int32) bool {
	allDiags := getAllDiagnostics(ctx, program, file)
	count := 0
	for _, d := range allDiags {
		if containsErrorCode(errorCodes, d.Code()) {
			count++
			if count >= 2 {
				return true
			}
		}
	}
	return false
}

// codeActionKindContains returns true if the requested kind equals or is a
// hierarchical parent of actionKind, using '.' as the separator. This matches
// the semantics of VS Code's HierarchicalKind.contains.
func codeActionKindContains(requestedKind, actionKind lsproto.CodeActionKind) bool {
	return requestedKind == actionKind ||
		requestedKind == "" ||
		strings.HasPrefix(string(actionKind), string(requestedKind)+".")
}

// isFixAllKind returns true if the requested kind matches source.fixAll
func isFixAllKind(kind lsproto.CodeActionKind) bool {
	return codeActionKindContains(kind, lsproto.CodeActionKindSourceFixAll)
}

// wantsQuickFixes returns true if the Only filter is nil/empty (meaning all kinds are wanted)
// or explicitly includes the quickfix kind.
func wantsQuickFixes(only *[]lsproto.CodeActionKind) bool {
	if only == nil || len(*only) == 0 {
		return true
	}
	for _, kind := range *only {
		if codeActionKindContains(kind, lsproto.CodeActionKindQuickFix) {
			return true
		}
	}
	return false
}

// createFixAllAction creates a source.fixAll code action that applies all auto-fixable
// code fixes across the file.
func (l *LanguageService) createFixAllAction(
	ctx context.Context,
	program *compiler.Program,
	file *ast.SourceFile,
	uri lsproto.DocumentUri,
) (*lsproto.CommandOrCodeAction, error) {
	kind := lsproto.CodeActionKindSourceFixAll
	lspChanges := make(map[lsproto.DocumentUri][]*lsproto.TextEdit)

	for _, provider := range codeFixProviders {
		if provider.GetAllCodeActions == nil {
			continue
		}

		fixContext := &CodeFixContext{
			SourceFile: file,
			Program:    program,
			LS:         l,
		}

		combined, err := provider.GetAllCodeActions(ctx, fixContext)
		if err != nil {
			return nil, err
		}
		if combined != nil && len(combined.Changes) > 0 {
			lspChanges[uri] = append(lspChanges[uri], combined.Changes...)
		}
	}

	if len(lspChanges) == 0 {
		return nil, nil
	}

	return &lsproto.CommandOrCodeAction{
		CodeAction: &lsproto.CodeAction{
			Title: diagnostics.Fix_All.Localize(locale.FromContext(ctx)),
			Kind:  &kind,
			Edit:  &lsproto.WorkspaceEdit{Changes: &lspChanges},
		},
	}, nil
}

// getOrganizeImportsActionTitle returns the appropriate title for the given organize imports kind
func getOrganizeImportsActionTitle(ctx context.Context, kind lsproto.CodeActionKind) string {
	loc := locale.FromContext(ctx)
	switch kind {
	case lsproto.CodeActionKindSourceRemoveUnusedImports:
		return diagnostics.Remove_Unused_Imports.Localize(loc)
	case lsproto.CodeActionKindSourceSortImports:
		return diagnostics.Sort_Imports.Localize(loc)
	default:
		return diagnostics.Organize_Imports.Localize(loc)
	}
}

// getOrganizeImportsActionsForKind returns the organize imports code action kinds that should be
// returned for the given requested kind.
func getOrganizeImportsActionsForKind(requestedKind lsproto.CodeActionKind) []lsproto.CodeActionKind {
	organizeImportsKinds := []lsproto.CodeActionKind{
		lsproto.CodeActionKindSourceOrganizeImports,
		lsproto.CodeActionKindSourceRemoveUnusedImports,
		lsproto.CodeActionKindSourceSortImports,
	}

	var result []lsproto.CodeActionKind
	for _, organizeKind := range organizeImportsKinds {
		if codeActionKindContains(requestedKind, organizeKind) {
			result = append(result, organizeKind)
		}
	}

	if slices.Contains(result, requestedKind) {
		return []lsproto.CodeActionKind{requestedKind}
	}

	return result
}

// createOrganizeImportsAction creates the organize imports code action
func (l *LanguageService) createOrganizeImportsAction(
	ctx context.Context,
	program *compiler.Program,
	file *ast.SourceFile,
	kind lsproto.CodeActionKind,
) *lsproto.CommandOrCodeAction {
	title := getOrganizeImportsActionTitle(ctx, kind)
	changes := l.OrganizeImports(
		ctx,
		file,
		program,
		kind,
	)
	if len(changes) == 0 {
		return &lsproto.CommandOrCodeAction{
			CodeAction: &lsproto.CodeAction{
				Title: title,
				Kind:  &kind,
				Edit:  &lsproto.WorkspaceEdit{Changes: &map[lsproto.DocumentUri][]*lsproto.TextEdit{}},
			},
		}
	}

	lspChanges := make(map[lsproto.DocumentUri][]*lsproto.TextEdit)
	for fileName, edits := range changes {
		fileURI := lsconv.FileNameToDocumentURI(fileName)
		lspChanges[fileURI] = edits
	}

	return &lsproto.CommandOrCodeAction{
		CodeAction: &lsproto.CodeAction{
			Title: title,
			Kind:  &kind,
			Edit:  &lsproto.WorkspaceEdit{Changes: &lspChanges},
		},
	}
}

// containsErrorCode checks if the error code is in the list
func containsErrorCode(codes []int32, code int32) bool {
	return slices.Contains(codes, code)
}

// convertToLSPCodeAction converts an internal CodeAction to an LSP CodeAction
func convertToLSPCodeAction(action *CodeAction, diag *lsproto.Diagnostic, uri lsproto.DocumentUri) lsproto.CommandOrCodeAction {
	kind := lsproto.CodeActionKindQuickFix
	changes := map[lsproto.DocumentUri][]*lsproto.TextEdit{
		uri: action.Changes,
	}
	diagnostics := []*lsproto.Diagnostic{diag}

	return lsproto.CommandOrCodeAction{
		CodeAction: &lsproto.CodeAction{
			Title:       action.Description,
			Kind:        &kind,
			Edit:        &lsproto.WorkspaceEdit{Changes: &changes},
			Diagnostics: &diagnostics,
		},
	}
}
