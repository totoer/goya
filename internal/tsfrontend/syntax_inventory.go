package tsfrontend

import (
	"fmt"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
)

type SyntaxCoverage struct {
	Kind     string   `json:"kind"`
	Handling string   `json:"handling"`
	Owner    string   `json:"owner"`
	Fixtures []string `json:"fixtures,omitempty"`
}

type SyntaxFeatureCoverage struct {
	Feature  string   `json:"feature"`
	Encoding string   `json:"encoding"`
	Owner    string   `json:"owner"`
	Fixtures []string `json:"fixtures"`
}

func SyntaxFeatures() []SyntaxFeatureCoverage {
	return []SyntaxFeatureCoverage{
		{Feature: "async functions and async generators", Encoding: "async modifier and function asterisk", Owner: "5.7", Fixtures: []string{"typescript_async_test.go"}},
		{Feature: "for-await and top-level await", Encoding: "await modifier/expression and source-file scope", Owner: "5.7,6.4", Fixtures: []string{"typescript_async_test.go", "typescript_module_async_test.go"}},
		{Feature: "using and await using", Encoding: "variable declaration list using/await-using flags", Owner: "5.8", Fixtures: []string{"typescript_disposal_test.go"}},
		{Feature: "class auto-accessors", Encoding: "accessor modifier on a property declaration", Owner: "5.3,5.4", Fixtures: []string{"typescript_syntax_test.go", "typescript_decorators_test.go"}},
		{Feature: "parameter properties", Encoding: "constructor parameter accessibility/readonly modifiers", Owner: "5.2", Fixtures: []string{"typescript_transforms_test.go"}},
		{Feature: "const enums", Encoding: "const enum modifier and isolated/preserve options", Owner: "5.1", Fixtures: []string{"typescript_transforms_test.go"}},
		{Feature: "optional chains and logical assignments", Encoding: "optional-chain node flag and compound operator kind", Owner: "4.2", Fixtures: []string{"typescript_syntax_test.go", "typescript_compile_test.go"}},
		{Feature: "standard and legacy decorators", Encoding: "decorator nodes and experimental/metadata compiler options", Owner: "5.4,5.5", Fixtures: []string{"typescript_decorators_test.go"}},
		{Feature: "JSX factory/runtime modes", Encoding: "TSX source kind and JSX compiler options", Owner: "5.6", Fixtures: []string{"typescript_jsx_test.go"}},
		{Feature: "type-only module syntax", Encoding: "type-only flags on clauses/specifiers", Owner: "4.5,6.6", Fixtures: []string{"typescript_modules_test.go", "typescript_dynamic_test.go"}},
		{Feature: "deferred evaluation", Encoding: "defer import-clause modifier and import.defer meta-property", Owner: "6.9", Fixtures: []string{"typescript_deferred_test.go"}},
		{Feature: "import attributes and JSON source", Encoding: "import-attribute nodes and JSON source kind", Owner: "6.10", Fixtures: []string{"typescript_json_test.go"}},
		{Feature: "source string compilation disabled", Encoding: "eval absent; intrinsic constructors reject code generation", Owner: "execution profile", Fixtures: []string{"testdata/typescript/runtime.json:eval", "testdata/typescript/runtime.json:function-constructor"}},
	}
}

// SyntaxInventory accounts for every native syntax kind, including kinds that
// must never become execution operations. Adding an upstream executable kind
// without an implementation owner makes inventory regeneration fail.
func SyntaxInventory() ([]SyntaxCoverage, error) {
	entries := map[ast.Kind]SyntaxCoverage{}
	add := func(handling, owner string, fixtures []string, kinds ...ast.Kind) {
		for _, kind := range kinds {
			entries[kind] = SyntaxCoverage{Kind: kind.String(), Handling: handling, Owner: owner, Fixtures: fixtures}
		}
	}
	add("direct expression lowering", "4.2", []string{"typescript_compile_test.go", "typescript_syntax_test.go"},
		ast.KindNumericLiteral, ast.KindBigIntLiteral, ast.KindStringLiteral, ast.KindRegularExpressionLiteral,
		ast.KindIdentifier, ast.KindPrivateIdentifier, ast.KindThisKeyword, ast.KindSuperKeyword, ast.KindTrueKeyword, ast.KindFalseKeyword, ast.KindNullKeyword,
		ast.KindArrayLiteralExpression, ast.KindObjectLiteralExpression, ast.KindPropertyAccessExpression, ast.KindElementAccessExpression,
		ast.KindCallExpression, ast.KindNewExpression, ast.KindTaggedTemplateExpression, ast.KindParenthesizedExpression,
		ast.KindDeleteExpression, ast.KindTypeOfExpression, ast.KindVoidExpression, ast.KindPrefixUnaryExpression, ast.KindPostfixUnaryExpression,
		ast.KindBinaryExpression, ast.KindConditionalExpression, ast.KindTemplateExpression, ast.KindNoSubstitutionTemplateLiteral, ast.KindTemplateSpan,
		ast.KindSpreadElement, ast.KindOmittedExpression, ast.KindPropertyAssignment, ast.KindShorthandPropertyAssignment, ast.KindSpreadAssignment,
		ast.KindComputedPropertyName, ast.KindExpressionWithTypeArguments, ast.KindPartiallyEmittedExpression)
	add("direct statement lowering", "4.2", []string{"typescript_compile_test.go", "typescript_syntax_test.go"},
		ast.KindBlock, ast.KindEmptyStatement, ast.KindVariableStatement, ast.KindExpressionStatement, ast.KindIfStatement,
		ast.KindDoStatement, ast.KindWhileStatement, ast.KindForStatement, ast.KindForInStatement, ast.KindForOfStatement,
		ast.KindContinueStatement, ast.KindBreakStatement, ast.KindReturnStatement, ast.KindWithStatement, ast.KindSwitchStatement,
		ast.KindLabeledStatement, ast.KindThrowStatement, ast.KindTryStatement, ast.KindDebuggerStatement, ast.KindCaseBlock,
		ast.KindCaseClause, ast.KindDefaultClause, ast.KindCatchClause)
	add("function/binding lowering", "4.3", []string{"typescript_compile_test.go", "typescript_syntax_test.go"},
		ast.KindParameter, ast.KindVariableDeclaration, ast.KindVariableDeclarationList, ast.KindFunctionDeclaration, ast.KindFunctionExpression,
		ast.KindArrowFunction, ast.KindObjectBindingPattern, ast.KindArrayBindingPattern, ast.KindBindingElement)
	add("checked erasure", "4.5", []string{"internal/tsfrontend/ir_test.go", "typescript_compile_test.go"},
		ast.KindTypeAssertionExpression, ast.KindAsExpression, ast.KindNonNullExpression, ast.KindSatisfiesExpression, ast.KindNotEmittedStatement,
		ast.KindTypeParameter, ast.KindPropertySignature, ast.KindMethodSignature, ast.KindCallSignature, ast.KindConstructSignature,
		ast.KindIndexSignature, ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration, ast.KindNamespaceExportDeclaration, ast.KindNotEmittedTypeElement)
	add("native runtime-syntax normalization", "5.1,5.2", []string{"typescript_transforms_test.go", "typescript_commonjs_test.go"},
		ast.KindEnumDeclaration, ast.KindEnumMember, ast.KindModuleDeclaration, ast.KindModuleBlock, ast.KindQualifiedName)
	add("class normalization and lowering", "5.3", []string{"typescript_transforms_test.go", "typescript_syntax_test.go"},
		ast.KindClassDeclaration, ast.KindClassExpression, ast.KindPropertyDeclaration, ast.KindMethodDeclaration, ast.KindConstructor,
		ast.KindGetAccessor, ast.KindSetAccessor, ast.KindClassStaticBlockDeclaration, ast.KindSemicolonClassElement, ast.KindHeritageClause)
	add("native decorator normalization and Go helpers", "5.4,5.5", []string{"typescript_decorators_test.go"}, ast.KindDecorator)
	add("native JSX normalization", "5.6", []string{"typescript_jsx_test.go"},
		ast.KindJsxElement, ast.KindJsxSelfClosingElement, ast.KindJsxOpeningElement, ast.KindJsxClosingElement, ast.KindJsxFragment,
		ast.KindJsxOpeningFragment, ast.KindJsxClosingFragment, ast.KindJsxAttribute, ast.KindJsxAttributes, ast.KindJsxSpreadAttribute,
		ast.KindJsxExpression, ast.KindJsxNamespacedName, ast.KindJsxText, ast.KindJsxTextAllWhiteSpaces)
	add("continuation lowering", "5.7,6.4", []string{"typescript_async_test.go", "typescript_module_async_test.go"}, ast.KindAwaitExpression, ast.KindYieldExpression)
	add("module records and private loader operations", "6.1-6.10", []string{"typescript_modules_test.go", "typescript_commonjs_test.go", "typescript_dynamic_test.go", "typescript_deferred_test.go", "typescript_json_test.go"},
		ast.KindSourceFile, ast.KindImportKeyword, ast.KindMetaProperty, ast.KindImportEqualsDeclaration, ast.KindImportDeclaration,
		ast.KindImportClause, ast.KindNamespaceImport, ast.KindNamedImports, ast.KindImportSpecifier, ast.KindExportAssignment,
		ast.KindExportDeclaration, ast.KindNamedExports, ast.KindNamespaceExport, ast.KindExportSpecifier, ast.KindExternalModuleReference,
		ast.KindImportAttributes, ast.KindImportAttribute)
	add("upstream checker/recovery/list node; cannot reach checked execution", "4.1,4.2", []string{"internal/tsfrontend/ir_test.go"},
		ast.KindSyntheticExpression, ast.KindSyntheticReferenceExpression, ast.KindMissingDeclaration, ast.KindSyntaxList)
	add("JavaScript-only reparsed type node; JS source is excluded", "2.5,9.3", []string{"internal/tsfrontend/project_test.go"}, ast.KindJSTypeAliasDeclaration, ast.KindJSImportDeclaration)
	var result []SyntaxCoverage
	for kind := ast.Kind(0); kind < ast.KindCount; kind++ {
		entry, ok := entries[kind]
		if !ok {
			switch {
			case kind <= ast.KindLastToken:
				entry = SyntaxCoverage{Kind: kind.String(), Handling: "scanner token or modifier; semantics owned by enclosing checked node", Owner: "1.2,5.9"}
			case kind >= ast.KindFirstJSDocNode && kind <= ast.KindLastJSDocNode:
				entry = SyntaxCoverage{Kind: kind.String(), Handling: "documentation/type-only", Owner: "2.6,4.5"}
			case ast.IsTypeNodeKind(kind):
				entry = SyntaxCoverage{Kind: kind.String(), Handling: "native type analysis and erasure", Owner: "2.6,4.5", Fixtures: []string{"testdata/typescript/types.json", "internal/tsfrontend/reference_test.go"}}
			default:
				return nil, fmt.Errorf("unaccounted TypeScript syntax family: %s", kind)
			}
		}
		result = append(result, entry)
	}
	return result, nil
}
