// Modified for Goya: internal import paths relocated by cmd/importupstream.

// Modified for Goya: embedded-language-service.

package ls

import (
	"context"
	"github.com/totoer/goya/internal/tsfrontend/editorpattern"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/collections"
	"github.com/totoer/goya/internal/tsfrontend/upstream/compiler"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsconv"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsutil"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/internal/tsfrontend/upstream/printer"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"github.com/totoer/goya/internal/tsfrontend/upstream/stringutil"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
)

func (l *LanguageService) ProvideDocumentSymbols(ctx context.Context, documentURI lsproto.DocumentUri) (lsproto.DocumentSymbolResponse, error) {
	_, file := l.getProgramAndFile(documentURI)
	if lsproto.GetClientCapabilities(ctx).TextDocument.DocumentSymbol.HierarchicalDocumentSymbolSupport {
		symbols := l.getDocumentSymbolsForChildren(ctx, file.AsNode(), file)
		return lsproto.SymbolInformationsOrDocumentSymbolsOrNull{DocumentSymbols: &symbols}, nil
	}
	// Client doesn't support hierarchical document symbols, return flat SymbolInformation array
	symbolInfos := l.getDocumentSymbolInformations(ctx, file, documentURI)
	symbolInfoPtrs := make([]*lsproto.SymbolInformation, len(symbolInfos))
	for i := range symbolInfos {
		symbolInfoPtrs[i] = &symbolInfos[i]
	}
	return lsproto.SymbolInformationsOrDocumentSymbolsOrNull{SymbolInformations: &symbolInfoPtrs}, nil
}

// getDocumentSymbolInformations converts hierarchical DocumentSymbols to a flat SymbolInformation array
func (l *LanguageService) getDocumentSymbolInformations(ctx context.Context, file *ast.SourceFile, documentURI lsproto.DocumentUri) []lsproto.SymbolInformation {
	// First get hierarchical symbols
	docSymbols := l.getDocumentSymbolsForChildren(ctx, file.AsNode(), file)

	// Flatten the hierarchy
	var result []lsproto.SymbolInformation
	var flatten func(symbols []*lsproto.DocumentSymbol, containerName *string)
	flatten = func(symbols []*lsproto.DocumentSymbol, containerName *string) {
		for _, symbol := range symbols {
			info := lsproto.SymbolInformation{
				Name: symbol.Name,
				Kind: symbol.Kind,
				Location: lsproto.Location{
					Uri:   documentURI,
					Range: symbol.Range,
				},
				ContainerName: containerName,
				Tags:          symbol.Tags,
				Deprecated:    symbol.Deprecated,
			}
			result = append(result, info)

			// Recursively flatten children with this symbol as container
			if symbol.Children != nil && len(*symbol.Children) > 0 {
				flatten(*symbol.Children, &symbol.Name)
			}
		}
	}
	flatten(docSymbols, nil)
	return result
}

func (l *LanguageService) getDocumentSymbolsForChildren(ctx context.Context, node *ast.Node, file *ast.SourceFile) []*lsproto.DocumentSymbol {
	var symbols []*lsproto.DocumentSymbol
	expandoTargets := collections.Set[string]{}
	addSymbolForNode := func(node *ast.Node, name *ast.Node, children []*lsproto.DocumentSymbol) {
		state, _ := ctx.Value(referenceSymbolContextKey{}).(*referenceSymbolState)
		if state != nil {
			switch node.Kind {
			case ast.KindImportEqualsDeclaration, ast.KindImportSpecifier, ast.KindExportSpecifier, ast.KindNamespaceImport:
				return
			}
			if ast.IsIdentifier(node) && ast.IsImportClause(node.Parent) {
				return
			}
		}
		if node.Flags&ast.NodeFlagsReparsed == 0 {
			symbol := l.newDocumentSymbol(node, name, children)
			if symbol != nil {
				if state != nil {
					state.nodes[symbol] = node
					symbol.Kind = referenceSymbolKind(node)
					if n := node.Name(); n != nil && !ast.IsIdentifier(n) && !ast.IsPrivateIdentifier(n) && !ast.IsModuleDeclaration(node) {
						symbol.Name = cleanCallbackText(scanner.GetTextOfNode(n))
					}
				}
				symbols = append(symbols, symbol)
			}
		}
	}
	var visit func(*ast.Node) bool
	getSymbolsForChildren := func(node *ast.Node) []*lsproto.DocumentSymbol {
		var result []*lsproto.DocumentSymbol
		if node != nil {
			saveExpandoTargets := expandoTargets
			expandoTargets = collections.Set[string]{}
			saveSymbols := symbols
			symbols = nil
			node.ForEachChild(visit)
			result = symbols
			symbols = saveSymbols
			expandoTargets = saveExpandoTargets
		}
		return result
	}
	startNode := func(node *ast.Node, name *ast.Node) func() {
		if node == nil {
			return func() {}
		}
		saveExpandoTargets := expandoTargets
		expandoTargets = collections.Set[string]{}
		saveSymbols := symbols
		symbols = nil
		return func() {
			result := symbols
			symbols = saveSymbols
			expandoTargets = saveExpandoTargets
			addSymbolForNode(node, name, result)
		}
	}
	getSymbolsForNode := func(node *ast.Node) []*lsproto.DocumentSymbol {
		var result []*lsproto.DocumentSymbol
		if node != nil {
			saveSymbols := symbols
			symbols = nil
			visit(node)
			result = symbols
			symbols = saveSymbols
		}
		return result
	}
	visit = func(node *ast.Node) bool {
		if ctx.Err() != nil {
			return true
		}
		if node.Flags&ast.NodeFlagsReparsed == 0 {
			if jsdocs := node.JSDoc(file); len(jsdocs) > 0 {
				for _, jsdoc := range jsdocs {
					if tagList := jsdoc.AsJSDoc().Tags; tagList != nil {
						for _, tag := range tagList.Nodes {
							if ast.IsJSDocTypedefTag(tag) || ast.IsJSDocCallbackTag(tag) {
								addSymbolForNode(tag, nil /*name*/, nil /*children*/)
							}
						}
					}
				}
			}
		}
		switch node.Kind {
		case ast.KindClassDeclaration, ast.KindClassExpression, ast.KindInterfaceDeclaration, ast.KindEnumDeclaration:
			if ast.IsClassLike(node) && ast.GetDeclarationName(node) != "" {
				expandoTargets.Add(ast.GetDeclarationName(node))
			}
			addSymbolForNode(node, nil /*name*/, getSymbolsForChildren(node))
		case ast.KindModuleDeclaration:
			addSymbolForNode(node, nil /*name*/, getSymbolsForChildren(getInteriorModule(node)))
		case ast.KindConstructor:
			addSymbolForNode(node, nil /*name*/, getSymbolsForChildren(node.Body()))
			for _, param := range node.Parameters() {
				if ast.IsParameterPropertyDeclaration(param, node) {
					addSymbolForNode(param, nil /*name*/, nil /*children*/)
				}
			}
		case ast.KindFunctionDeclaration, ast.KindFunctionExpression, ast.KindArrowFunction, ast.KindMethodDeclaration, ast.KindGetAccessor,
			ast.KindSetAccessor:
			declName := ast.GetDeclarationName(node)
			if declName != "" {
				expandoTargets.Add(declName)
			}
			addSymbolForNode(node, nil /*name*/, getSymbolsForChildren(node.Body()))
		case ast.KindVariableDeclaration, ast.KindBindingElement, ast.KindPropertyAssignment, ast.KindPropertyDeclaration:
			nodeName := node.Name()
			if nodeName != nil {
				if ast.IsBindingPattern(nodeName) {
					visit(nodeName)
				} else {
					addSymbolForNode(node, nil /*name*/, getSymbolsForChildren(node.Initializer()))
				}
			}
		case ast.KindSpreadAssignment:
			addSymbolForNode(node, node.Expression(), nil /*children*/)
		case ast.KindMethodSignature, ast.KindPropertySignature, ast.KindCallSignature, ast.KindConstructSignature, ast.KindIndexSignature,
			ast.KindEnumMember, ast.KindShorthandPropertyAssignment, ast.KindTypeAliasDeclaration, ast.KindImportEqualsDeclaration, ast.KindExportSpecifier:
			addSymbolForNode(node, nil /*name*/, nil /*children*/)
		case ast.KindImportClause:
			// Handle default import case e.g.:
			//    import d from "mod";
			if node.Name() != nil {
				addSymbolForNode(node.Name(), node.Name(), nil /*children*/)
			}
			// Handle named bindings in imports e.g.:
			//    import * as NS from "mod";
			//    import {a, b as B} from "mod";
			if namedBindings := node.AsImportClause().NamedBindings; namedBindings != nil {
				if namedBindings.Kind == ast.KindNamespaceImport {
					addSymbolForNode(namedBindings, nil /*name*/, nil /*children*/)
				} else {
					for _, element := range namedBindings.Elements() {
						addSymbolForNode(element, nil /*name*/, nil /*children*/)
					}
				}
			}
		case ast.KindBinaryExpression, ast.KindCallExpression:
			assignmentKind := ast.GetAssignmentDeclarationKind(node)
			switch assignmentKind {
			// `module.exports = ...`` should be reparsed into a JSExportAssignment,
			// and `exports.a = ...`` into a CommonJSExport.
			case ast.JSDeclarationKindNone, ast.JSDeclarationKindThisProperty,
				ast.JSDeclarationKindModuleExports, ast.JSDeclarationKindExportsProperty,
				ast.JSDeclarationKindObjectDefinePropertyExports:
				node.ForEachChild(visit)
			case ast.JSDeclarationKindProperty, ast.JSDeclarationKindObjectDefinePropertyValue:
				var target *ast.Expression
				var targetFunction *ast.Expression
				var definition *ast.Node
				var propertyName *ast.Node
				// `A.b = ... ` or `A.prototype.b = ...`
				if ast.IsBinaryExpression(node) {
					binaryExpr := node.AsBinaryExpression()
					target = binaryExpr.Left
					targetFunction = target.Expression()
					definition = binaryExpr.Right
					// `A.b` or `A.prototype.b`
					if ast.IsPropertyAccessExpression(target) {
						propertyName = target.AsPropertyAccessExpression().Name()
					} else { // `A["b"]` or `A.prototype["b"]`
						propertyName = target.AsElementAccessExpression().ArgumentExpression
					}
				} else { // `Object.defineProperty(A, "b", {...})`
					args := node.Arguments()
					targetFunction = args[0]
					target = args[1]
					propertyName = target
					definition = args[2]
				}
				if isPrototypeExpando(targetFunction) {
					targetFunction = targetFunction.Expression()
					// If we see a prototype assignment, start tracking the target as an expando target.
					if ast.IsIdentifier(targetFunction) {
						expandoTargets.Add(targetFunction.Text())
					}
				}
				if ast.IsIdentifier(targetFunction) &&
					expandoTargets.Has(targetFunction.Text()) {
					endNode := startNode(node, targetFunction)
					addSymbolForNode(target, propertyName, getSymbolsForNode(definition))
					endNode()
				} else {
					node.ForEachChild(visit)
				}
			}
		case ast.KindExportAssignment:
			if node.AsExportAssignment().IsExportEquals {
				addSymbolForNode(node, nil /*name*/, getSymbolsForNode(node.Expression()))
			} else {
				node.ForEachChild(visit)
			}
		default:
			node.ForEachChild(visit)
		}
		return false
	}
	node.ForEachChild(visit)
	if ctx.Value(referenceSymbolContextKey{}) != nil {
		return symbols
	}
	return mergeExpandos(symbols)
}

// Target is `f.prototype`.
func isPrototypeExpando(target *ast.Node) bool {
	if ast.IsAccessExpression(target) {
		accessName := ast.GetElementOrPropertyAccessName(target)
		return accessName != nil && accessName.Text() == "prototype"
	}
	return false
}

const maxLength = 150

func (l *LanguageService) newDocumentSymbol(node *ast.Node, name *ast.Node, children []*lsproto.DocumentSymbol) *lsproto.DocumentSymbol {
	result := new(lsproto.DocumentSymbol)
	file := ast.GetSourceFileOfNode(node)
	nodeStartPos := scanner.SkipTrivia(file.Text(), node.Pos())
	if name == nil {
		name = ast.GetNameOfDeclaration(node)
	}
	var text string
	var nameStartPos, nameEndPos int
	if ast.IsModuleDeclaration(node) && !ast.IsAmbientModule(node) {
		text = getModuleName(node)
		nameStartPos = scanner.SkipTrivia(file.Text(), name.Pos())
		nameEndPos = getInteriorModule(node).Name().End()
	} else if ast.IsAnyExportAssignment(node) && node.AsExportAssignment().IsExportEquals {
		text = "export="
		if !ast.NodeIsMissing(name) {
			nameStartPos = scanner.SkipTrivia(file.Text(), name.Pos())
			nameEndPos = name.End()
		} else {
			nameStartPos = nodeStartPos
			nameEndPos = node.End()
		}
	} else if name != nil {
		text = getTextOfName(name)
		nameStartPos = max(scanner.SkipTrivia(file.Text(), name.Pos()), nodeStartPos)
		nameEndPos = max(name.End(), nodeStartPos)
	} else {
		text = getUnnamedNodeLabel(node)
		nameStartPos = nodeStartPos
		nameEndPos = nodeStartPos
	}
	if text == "" {
		return nil
	}
	truncatedText := stringutil.TruncateByRunes(text, maxLength)
	if len(truncatedText) < len(text) {
		text = truncatedText + "..."
	}
	result.Name = text
	result.Kind = getSymbolKindFromNode(node)
	result.Range = lsproto.Range{
		Start: l.converters.PositionToLineAndCharacter(file, core.TextPos(nodeStartPos)),
		End:   l.converters.PositionToLineAndCharacter(file, core.TextPos(node.End())),
	}
	result.SelectionRange = lsproto.Range{
		Start: l.converters.PositionToLineAndCharacter(file, core.TextPos(nameStartPos)),
		End:   l.converters.PositionToLineAndCharacter(file, core.TextPos(nameEndPos)),
	}
	if children == nil {
		children = []*lsproto.DocumentSymbol{}
	}
	result.Children = &children
	return result
}

// Merges expando symbols into their target symbols, and namespaces of same name.
// Modifies the input slice.
func mergeExpandos(symbols []*lsproto.DocumentSymbol) []*lsproto.DocumentSymbol {
	mergedSymbols := make([]*lsproto.DocumentSymbol, 0, len(symbols))
	// Collect symbols that can be an expando target.
	nameToExpandoTargetIndex := collections.MultiMap[string, int]{}
	// Collect namespaces.
	nameToNamespaceIndex := map[string]int{}
	for i, symbol := range symbols {
		if isAnonymousName(symbol.Name) {
			continue
		}
		if symbol.Kind == lsproto.SymbolKindClass || symbol.Kind == lsproto.SymbolKindFunction || symbol.Kind == lsproto.SymbolKindVariable {
			nameToExpandoTargetIndex.Add(symbol.Name, i)
		}
		if symbol.Kind == lsproto.SymbolKindNamespace {
			if _, ok := nameToNamespaceIndex[symbol.Name]; !ok {
				nameToNamespaceIndex[symbol.Name] = i
			}
		}
	}
	for i, symbol := range symbols {
		if symbol.Children != nil {
			children := mergeExpandos(*symbol.Children)
			symbol.Children = &children
		}

		// Anonymous symbols never merge.
		if isAnonymousName(symbol.Name) {
			continue
		}

		// Merge expandos.
		if symbol.Kind == lsproto.SymbolKindProperty {
			symbolsWithSameName := nameToExpandoTargetIndex.Get(symbol.Name)
			for j := len(symbolsWithSameName) - 1; j >= 0; j-- {
				targetIndex := symbolsWithSameName[j]
				targetSymbol := symbols[targetIndex]
				mergeChildren(targetSymbol, symbol)
				// Mark this symbol as merged.
				symbols[i] = nil
			}
		}
		// Merge namespaces.
		if symbol.Kind == lsproto.SymbolKindNamespace {
			if targetIndex, ok := nameToNamespaceIndex[symbol.Name]; ok && targetIndex != i {
				targetSymbol := symbols[targetIndex]
				mergeChildren(targetSymbol, symbol)
				// Mark this symbol as merged.
				symbols[i] = nil
			}
		}
	}
	for _, symbol := range symbols {
		if symbol != nil {
			mergedSymbols = append(mergedSymbols, symbol)
		}
	}
	return mergedSymbols
}

func mergeChildren(target *lsproto.DocumentSymbol, source *lsproto.DocumentSymbol) {
	if source.Children != nil {
		if target.Children == nil {
			target.Children = source.Children
		} else {
			*target.Children = mergeExpandos(append(*target.Children, *source.Children...))
			slices.SortFunc(*target.Children, func(a, b *lsproto.DocumentSymbol) int {
				return lsproto.CompareRanges(a.Range, b.Range)
			})
		}
	}
}

// See `getUnnamedNodeLabel`.
func isAnonymousName(name string) bool {
	return name == "<function>" || name == "<class>" || name == "export=" || name == "default" ||
		name == "constructor" || name == "()" || name == "new()" || name == "[]" || strings.HasSuffix(name, ") callback")
}

func getTextOfName(node *ast.Node) string {
	switch node.Kind {
	case ast.KindIdentifier, ast.KindPrivateIdentifier, ast.KindNumericLiteral:
		return node.Text()
	case ast.KindStringLiteral:
		return "\"" + printer.EscapeString(node.Text(), '"') + "\""
	case ast.KindNoSubstitutionTemplateLiteral:
		return "`" + printer.EscapeString(node.Text(), '`') + "`"
	case ast.KindComputedPropertyName:
		if ast.IsStringOrNumericLiteralLike(node.Expression()) {
			return getTextOfName(node.Expression())
		}
	}
	return scanner.GetTextOfNode(node)
}

func getUnnamedNodeLabel(node *ast.Node) string {
	if parent := ast.WalkUpParenthesizedExpressions(node.Parent); parent != nil && ast.IsExportAssignment(parent) {
		if parent.AsExportAssignment().IsExportEquals {
			return "export="
		}
		return "default"
	}
	switch node.Kind {
	case ast.KindFunctionDeclaration, ast.KindFunctionExpression, ast.KindArrowFunction:
		if node.ModifierFlags()&ast.ModifierFlagsDefault != 0 {
			return "default"
		}
		if ast.IsCallExpression(node.Parent) {
			name := getCallExpressionName(node.Parent.Expression())
			if name != "" {
				name = cleanCallbackText(name)
				if len(name) > maxLength {
					return name + " callback"
				}
				args := cleanCallbackText(getCallExpressionLiteralArgs(node.Parent))
				return name + "(" + args + ") callback"
			}
		}
		return "<function>"
	case ast.KindClassDeclaration, ast.KindClassExpression:
		if node.ModifierFlags()&ast.ModifierFlagsDefault != 0 {
			return "default"
		}
		return "<class>"
	case ast.KindConstructor:
		return "constructor"
	case ast.KindCallSignature:
		return "()"
	case ast.KindConstructSignature:
		return "new()"
	case ast.KindIndexSignature:
		return "[]"
	}
	return ""
}

func getCallExpressionName(node *ast.Node) string {
	switch node.Kind {
	case ast.KindIdentifier, ast.KindPrivateIdentifier:
		return node.Text()
	case ast.KindPropertyAccessExpression:
		left := getCallExpressionName(node.Expression())
		right := getCallExpressionName(node.Name())
		if left != "" {
			return left + "." + right
		}
		return right
	}
	return ""
}

func getCallExpressionLiteralArgs(callExpr *ast.Node) string {
	var parts []string
	for _, arg := range callExpr.Arguments() {
		if ast.IsStringLiteralLike(arg) || ast.IsTemplateExpression(arg) {
			parts = append(parts, scanner.GetTextOfNode(arg))
		}
	}
	return strings.Join(parts, ", ")
}

func cleanCallbackText(text string) string {
	truncated := stringutil.TruncateByRunes(text, maxLength)
	if len(truncated) < len(text) {
		text = truncated + "..."
	}
	return strings.Map(func(r rune) rune {
		if stringutil.IsLineBreak(r) {
			return -1
		}
		return r
	}, text)
}

func getInteriorModule(node *ast.Node) *ast.Node {
	for node.Body() != nil && ast.IsModuleDeclaration(node.Body()) {
		node = node.Body()
	}
	return node
}

func getModuleName(node *ast.Node) string {
	result := node.Name().Text()
	for node.Body() != nil && ast.IsModuleDeclaration(node.Body()) {
		node = node.Body()
		result = result + "." + node.Name().Text()
	}
	return result
}

type DeclarationInfo struct {
	name        string
	declaration *ast.Node
	matchScore  int
}

func ProvideWorkspaceSymbols(
	ctx context.Context,
	programs []*compiler.Program,
	converters *lsconv.Converters,
	preferences lsutil.UserPreferences,
	query string,
) (lsproto.WorkspaceSymbolResponse, error) {
	excludeLibrarySymbols := preferences.ExcludeLibrarySymbolsInNavTo.IsTrue()
	// Obtain set of non-declaration source files from all active programs.
	sourceFiles := map[tspath.Path]*ast.SourceFile{}
	for _, program := range programs {
		for _, sourceFile := range program.SourceFiles() {
			if (program.HasTSFile() || !sourceFile.IsDeclarationFile) &&
				!shouldExcludeFile(sourceFile, program, excludeLibrarySymbols) {
				sourceFiles[sourceFile.Path()] = sourceFile
			}
		}
	}
	// Create DeclarationInfos for all declarations in the source files.
	var infos []DeclarationInfo
	for _, sourceFile := range sourceFiles {
		if ctx.Err() != nil {
			return lsproto.SymbolInformationsOrWorkspaceSymbolsOrNull{}, nil
		}
		declarationMap := sourceFile.GetDeclarationMap()
		for name, declarations := range declarationMap {
			score := getMatchScore(name, query)
			if score >= 0 {
				for _, declaration := range declarations {
					infos = append(infos, DeclarationInfo{name, declaration, score})
				}
			}
		}
	}
	// Sort the DeclarationInfos and return the top 256 matches.
	slices.SortFunc(infos, compareDeclarationInfos)
	count := min(len(infos), 256)
	symbols := make([]*lsproto.SymbolInformation, count)
	for i, info := range infos[0:count] {
		node := info.declaration
		sourceFile := ast.GetSourceFileOfNode(node)
		container := getContainerNode(info.declaration)
		var containerName *string
		if container != nil {
			containerName = strPtrTo(ast.GetDeclarationName(container))
		}
		// Use the name node's span so that VS selects just the symbol name (matching
		// the TS5 navto behaviour). GetNameOfDeclaration is always non-nil here because
		// computeDeclarationMap only adds declarations whose GetDeclarationName (string
		// form) is non-empty, which implies a name node exists.
		nameNode := ast.GetNameOfDeclaration(node)
		nameStart := astnav.GetStartOfNode(nameNode, sourceFile, false /*includeJsDoc*/)
		nameRange := core.NewTextRange(nameStart, nameNode.End())
		var symbol lsproto.SymbolInformation
		symbol.Name = info.name
		symbol.Kind = getSymbolKindFromNode(info.declaration)
		symbol.Location = converters.ToLSPLocation(sourceFile, nameRange)
		symbol.ContainerName = containerName
		symbols[i] = &symbol
	}

	return lsproto.SymbolInformationsOrWorkspaceSymbolsOrNull{SymbolInformations: &symbols}, nil
}

func shouldExcludeFile(file *ast.SourceFile, program *compiler.Program, excludeLibrarySymbols bool) bool {
	return excludeLibrarySymbols && (isInsideNodeModules(file.FileName()) || program.IsLibFile(file))
}

func isInsideNodeModules(fileName string) bool {
	return strings.Contains(fileName, "/node_modules/")
}

// Return a score for matching `s` against `pattern`. In order to match, `s` must contain each of the characters in
// `pattern` in the same order. Upper case characters in `pattern` must match exactly, whereas lower case characters
// in `pattern` match either case in `s`. If `s` doesn't match, -1 is returned. Otherwise, the returned score is the
// number of characters in `s` that weren't matched. Thus, zero represents an exact match, and higher values represent
// increasingly less specific partial matches.
func getMatchScore(s string, pattern string) int {
	score := 0
	for _, p := range pattern {
		exact := unicode.IsUpper(p)
		for {
			c, size := utf8.DecodeRuneInString(s)
			if size == 0 {
				return -1
			}
			s = s[size:]
			if exact && c == p || !exact && unicode.ToLower(c) == unicode.ToLower(p) {
				break
			}
			score++
		}
	}
	return score
}

// Sort DeclarationInfos by ascending match score, then ascending case insensitive name, then
// ascending case sensitive name, and finally by source file name and position.
func compareDeclarationInfos(d1, d2 DeclarationInfo) int {
	if d1.matchScore != d2.matchScore {
		return d1.matchScore - d2.matchScore
	}
	if c := stringutil.CompareStringsCaseInsensitive(d1.name, d2.name); c != 0 {
		return c
	}
	if c := strings.Compare(d1.name, d2.name); c != 0 {
		return c
	}
	s1 := ast.GetSourceFileOfNode(d1.declaration)
	s2 := ast.GetSourceFileOfNode(d2.declaration)
	if s1 != s2 {
		return strings.Compare(string(s1.Path()), string(s2.Path()))
	}
	return d1.declaration.Pos() - d2.declaration.Pos()
}

// Reference symbol presentation mirrors the pinned TS6 navigation tree and
// navigateTo ranges. All AST metadata below is request-local.
type referenceSymbolContextKey struct{}
type referenceSymbolState struct {
	nodes   map[*lsproto.DocumentSymbol]*ast.Node
	origins map[*lsproto.DocumentSymbol]*lsproto.DocumentSymbol
}

func referenceSymbolKind(node *ast.Node) lsproto.SymbolKind {
	switch node.Kind {
	case ast.KindModuleDeclaration:
		return lsproto.SymbolKindModule
	case ast.KindTypeAliasDeclaration, ast.KindJSDocTypedefTag, ast.KindJSDocCallbackTag, ast.KindTypeParameter,
		ast.KindIndexSignature, ast.KindConstructSignature, ast.KindCallSignature:
		return lsproto.SymbolKindVariable
	case ast.KindVariableDeclaration, ast.KindBindingElement:
		if ast.IsVarConst(node) {
			return lsproto.SymbolKindConstant
		}
		return lsproto.SymbolKindVariable
	case ast.KindEnumMember:
		return lsproto.SymbolKindConstant
	case ast.KindGetAccessor, ast.KindSetAccessor:
		return lsproto.SymbolKindMethod
	case ast.KindPropertyAssignment:
		if ast.IsFunctionLike(node.Initializer()) {
			return lsproto.SymbolKindMethod
		}
	}
	return getSymbolKindFromNode(node)
}

func (l *LanguageService) ProvideReferenceDocumentSymbols(ctx context.Context, uri lsproto.DocumentUri) ([]*lsproto.DocumentSymbol, error) {
	symbols, _, err := l.referenceDocumentSymbols(ctx, uri)
	return symbols, err
}
func (l *LanguageService) referenceDocumentSymbols(ctx context.Context, uri lsproto.DocumentUri) ([]*lsproto.DocumentSymbol, *referenceSymbolState, error) {
	state := &referenceSymbolState{nodes: map[*lsproto.DocumentSymbol]*ast.Node{}, origins: map[*lsproto.DocumentSymbol]*lsproto.DocumentSymbol{}}
	ctx = context.WithValue(ctx, referenceSymbolContextKey{}, state)
	_, file := l.getProgramAndFile(uri)
	raw := l.getDocumentSymbolsForChildren(ctx, file.AsNode(), file)
	var finish func([]*lsproto.DocumentSymbol) []*lsproto.DocumentSymbol
	finish = func(raw []*lsproto.DocumentSymbol) []*lsproto.DocumentSymbol {
		type group struct {
			symbols  []*lsproto.DocumentSymbol
			sortName string
			kind     ast.Kind
		}
		var groups []*group
		for _, symbol := range raw {
			node := state.nodes[symbol]
			if node == nil {
				continue
			}
			var found *group
			for _, g := range groups {
				first := g.symbols[0]
				firstNode := state.nodes[first]
				if first.Name == symbol.Name && firstNode.Kind == node.Kind && firstNode.Parent == node.Parent && firstNode.ModifierFlags()&ast.ModifierFlagsStatic == node.ModifierFlags()&ast.ModifierFlagsStatic {
					found = g
					break
				}
			}
			if found == nil {
				name := symbol.Name
				if node.Name() == nil {
					name = ""
					if strings.HasSuffix(symbol.Name, ") callback") {
						name = strings.TrimSuffix(symbol.Name, " callback")
					}
				} else if ast.IsComputedPropertyName(node.Name()) {
					expression := node.Name().Expression()
					name = ""
					if ast.IsStringOrNumericLiteralLike(expression) {
						name = expression.Text()
					}
				} else if ast.IsStringLiteralLike(node.Name()) {
					name = node.Name().Text()
				}
				found = &group{sortName: name, kind: node.Kind}
				groups = append(groups, found)
			}
			found.symbols = append(found.symbols, symbol)
		}
		order := collate.New(language.English, collate.Numeric)
		slices.SortStableFunc(groups, func(a, b *group) int {
			if a.sortName == "" && b.sortName != "" {
				return -1
			}
			if b.sortName == "" && a.sortName != "" {
				return 1
			}
			if c := order.CompareString(a.sortName, b.sortName); c != 0 {
				return c
			}
			return int(a.kind) - int(b.kind)
		})
		result := []*lsproto.DocumentSymbol{}
		for _, g := range groups {
			var children []*lsproto.DocumentSymbol
			for _, symbol := range g.symbols {
				if symbol.Children != nil {
					children = append(children, (*symbol.Children)...)
				}
			}
			children = finish(children)
			first := g.symbols[0]
			for _, symbol := range g.symbols {
				included := []*lsproto.DocumentSymbol{}
				for _, child := range children {
					if lsproto.ComparePositions(child.Range.Start, symbol.Range.End) <= 0 && lsproto.ComparePositions(child.Range.End, symbol.Range.Start) >= 0 {
						included = append(included, child)
					}
				}
				if (symbol.Name == "<function>" || symbol.Name == "<class>") && len(included) == 0 {
					continue
				}
				copy := *symbol
				state.nodes[&copy] = state.nodes[symbol]
				state.origins[&copy] = first
				copy.Children = &included
				if state.nodes[symbol].Name() == nil {
					copy.SelectionRange = copy.Range
				} else if lsproto.ComparePositions(first.SelectionRange.Start, symbol.Range.End) <= 0 && lsproto.ComparePositions(first.SelectionRange.End, symbol.Range.Start) >= 0 {
					copy.SelectionRange = first.SelectionRange
				} else {
					copy.SelectionRange = copy.Range
				}
				result = append(result, &copy)
			}
		}
		return result
	}
	result := finish(raw)
	return result, state, ctx.Err()
}

func ProvideReferenceWorkspaceSymbols(ctx context.Context, programs []*compiler.Program, converters *lsconv.Converters, preferences lsutil.UserPreferences, query string) ([]*lsproto.SymbolInformation, error) {
	matcher := editorpattern.New(query)
	if matcher == nil {
		return []*lsproto.SymbolInformation{}, nil
	}
	type item struct {
		name  string
		node  *ast.Node
		match int
	}
	var items []item
	seen := map[tspath.Path]bool{}
	for _, program := range programs {
		for _, file := range program.SourceFiles() {
			if seen[file.Path()] || shouldExcludeFile(file, program, preferences.ExcludeLibrarySymbolsInNavTo.IsTrue()) {
				continue
			}
			seen[file.Path()] = true
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			declarations := make(map[string][]*ast.Node)
			for name, nodes := range file.GetDeclarationMap() {
				declarations[name] = nodes
			}
			// Native named-declaration indexing omits default import identifiers.
			// Keep the name span (TS6's import-clause item), without mutating its cache.
			for _, statement := range file.Statements.Nodes {
				if !ast.IsImportDeclaration(statement) {
					continue
				}
				clause := statement.AsImportDeclaration().ImportClause
				if clause != nil && clause.Name() != nil {
					n := clause.Name()
					name := n.Text()
					if !slices.Contains(declarations[name], n) {
						declarations[name] = append(slices.Clone(declarations[name]), n)
					}
				}
			}
			// Map iteration must not decide ordering of equal-named declarations.
			var names []string
			for name := range declarations {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				for _, node := range declarations[name] {
					importNode := node
					if ast.IsIdentifier(node) && ast.IsImportClause(node.Parent) {
						importNode = node.Parent
					}
					if ast.IsImportClause(importNode) || ast.IsImportSpecifier(importNode) || ast.IsImportEqualsDeclaration(importNode) {
						ch, done := program.GetTypeChecker(ctx)
						symbol := ch.GetSymbolAtLocation(importNode.Name())
						keep := false
						if symbol != nil {
							aliased := ch.GetAliasedSymbol(symbol)
							if aliased != nil && (ast.IsImportClause(importNode) || aliased.Name != symbol.Name) {
								keep = true
								allExcluded := len(aliased.Declarations) > 0
								for _, decl := range aliased.Declarations {
									sf := ast.GetSourceFileOfNode(decl)
									if !shouldExcludeFile(sf, program, preferences.ExcludeLibrarySymbolsInNavTo.IsTrue()) {
										allExcluded = false
										break
									}
								}
								keep = keep && !allExcluded
							}
						}
						done()
						if !keep {
							continue
						}
					}
					var containers []string
					for parent := getContainerNode(node); parent != nil; parent = getContainerNode(parent) {
						n := ast.GetNameOfDeclaration(parent)
						if n == nil {
							containers = nil
							break
						}
						if ast.IsIdentifier(n) || ast.IsStringOrNumericLiteralLike(n) {
							containers = append(containers, n.Text())
						} else {
							containers = nil
							break
						}
					}
					slices.Reverse(containers)
					match := matcher.Match(name, containers)
					if match != nil {
						items = append(items, item{name, node, match.Kind})
					}
				}
			}
		}
	}
	order := collate.New(language.English, collate.Numeric)
	slices.SortStableFunc(items, func(a, b item) int {
		if a.match != b.match {
			return a.match - b.match
		}
		return order.CompareString(a.name, b.name)
	})
	result := make([]*lsproto.SymbolInformation, 0, len(items))
	for _, i := range items {
		file := ast.GetSourceFileOfNode(i.node)
		result = append(result, &lsproto.SymbolInformation{Name: i.name, Kind: referenceSymbolKind(i.node), Location: converters.ToLSPLocation(file, core.NewTextRange(astnav.GetStartOfNode(i.node, file, false), i.node.End()))})
	}
	return result, ctx.Err()
}

// Adapted from typescript-language-server 6.0.2 features/code-lens.
// Copyright (C) 2023 TypeFox and others. Licensed under Apache-2.0.
// Copyright (c) Microsoft Corporation. Licensed under MIT.
// See docs/licenses/typescript-language-server-6.0.2/LICENSE.
type ReferenceCodeLensTarget struct {
	Range           lsproto.Range
	References      bool
	Implementations bool
}

func (l *LanguageService) ProvideReferenceCodeLensTargets(ctx context.Context, uri lsproto.DocumentUri, showOnAllFunctions bool) ([]ReferenceCodeLensTarget, error) {
	symbols, state, err := l.referenceDocumentSymbols(ctx, uri)
	if err != nil {
		return nil, err
	}
	_, file := l.getProgramAndFile(uri)
	result := []ReferenceCodeLensTarget{}
	seen := map[*lsproto.DocumentSymbol]bool{}
	var visit func([]*lsproto.DocumentSymbol, *lsproto.DocumentSymbol)
	visit = func(items []*lsproto.DocumentSymbol, parent *lsproto.DocumentSymbol) {
		for _, symbol := range items {
			if ctx.Err() != nil {
				return
			}
			first := state.origins[symbol]
			if first == nil {
				continue
			}
			node := state.nodes[first]
			if !seen[first] {
				seen[first] = true
				references := false
				implementations := false
				var parentNode *ast.Node
				var parentFirst *lsproto.DocumentSymbol
				if parent != nil {
					parentFirst = state.origins[parent]
					if parentFirst != nil {
						parentNode = state.nodes[parentFirst]
					}
				}
				if parentNode != nil && ast.IsEnumDeclaration(parentNode) {
					references = true
				} else {
					switch node.Kind {
					case ast.KindFunctionDeclaration, ast.KindFunctionExpression, ast.KindArrowFunction:
						references = showOnAllFunctions || ast.GetCombinedModifierFlags(node)&ast.ModifierFlagsExport != 0
					case ast.KindVariableDeclaration, ast.KindBindingElement:
						references = ast.GetCombinedModifierFlags(node)&ast.ModifierFlagsExport != 0
					case ast.KindClassDeclaration, ast.KindClassExpression:
						references = symbol.Name != "<class>"
					case ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration, ast.KindEnumDeclaration:
						references = true
					case ast.KindMethodDeclaration, ast.KindMethodSignature, ast.KindConstructor, ast.KindGetAccessor, ast.KindSetAccessor, ast.KindPropertyDeclaration, ast.KindPropertySignature, ast.KindPropertyAssignment, ast.KindParameter:
						if parentNode != nil && parentFirst.Range.Start != first.Range.Start {
							references = ast.IsClassLike(parentNode) || ast.IsInterfaceDeclaration(parentNode) || ast.IsTypeAliasDeclaration(parentNode)
						}
					}
				}
				switch node.Kind {
				case ast.KindInterfaceDeclaration:
					implementations = true
				case ast.KindClassDeclaration, ast.KindClassExpression, ast.KindMethodDeclaration, ast.KindMethodSignature, ast.KindGetAccessor, ast.KindSetAccessor, ast.KindPropertyDeclaration, ast.KindPropertySignature:
					implementations = ast.GetCombinedModifierFlags(node)&ast.ModifierFlagsAbstract != 0
				}
				if references || implementations {
					r := first.SelectionRange
					if node.Name() == nil {
						start := int(l.converters.LineAndCharacterToPosition(file, first.Range.Start))
						end := int(l.converters.LineAndCharacterToPosition(file, first.Range.End))
						// JS multiline ^ and ASCII word boundaries, with literal symbol text.
						pattern := `(?m)^([^\n\r\x{2028}\x{2029}]*?(\b|\W))` + regexp.QuoteMeta(symbol.Name) + `(\b|\W)`
						if match := regexp.MustCompile(pattern).FindStringSubmatchIndex(file.Text()[start:end]); match != nil {
							start += match[3]
						}
						units := 0
						last := start
						for _, r := range symbol.Name {
							units++
							if r > 0xffff {
								units++
							}
						}
						partial := uint32(0)
						for last < len(file.Text()) && units > 0 {
							scalar, size := utf8.DecodeRuneInString(file.Text()[last:])
							width := 1
							if scalar > 0xffff {
								width = 2
							}
							if units < width {
								partial = uint32(units)
								break
							}
							units -= width
							last += size
						}
						endPosition := l.converters.PositionToLineAndCharacter(file, core.TextPos(last))
						endPosition.Character += partial
						r = lsproto.Range{Start: l.converters.PositionToLineAndCharacter(file, core.TextPos(start)), End: endPosition}
					}
					result = append(result, ReferenceCodeLensTarget{Range: r, References: references, Implementations: implementations})
				}
			}
			if symbol.Children != nil {
				visit(*symbol.Children, symbol)
			}
		}
	}
	visit(symbols, nil)
	return result, ctx.Err()
}

// getSymbolKindFromNode converts an AST node to an LSP SymbolKind.
// Combines getNodeKind with VS Code's fromProtocolScriptElementKind.
func getSymbolKindFromNode(node *ast.Node) lsproto.SymbolKind {
	switch node.Kind {
	case ast.KindSourceFile:
		if ast.IsExternalModule(node.AsSourceFile()) {
			return lsproto.SymbolKindModule
		}
		return lsproto.SymbolKindFile
	case ast.KindModuleDeclaration:
		return lsproto.SymbolKindNamespace
	case ast.KindClassDeclaration, ast.KindClassExpression:
		return lsproto.SymbolKindClass
	case ast.KindInterfaceDeclaration:
		return lsproto.SymbolKindInterface
	case ast.KindTypeAliasDeclaration, ast.KindJSDocTypedefTag, ast.KindJSDocCallbackTag:
		return lsproto.SymbolKindClass
	case ast.KindEnumDeclaration:
		return lsproto.SymbolKindEnum
	case ast.KindVariableDeclaration:
		return lsproto.SymbolKindVariable
	case ast.KindArrowFunction, ast.KindFunctionDeclaration, ast.KindFunctionExpression:
		return lsproto.SymbolKindFunction
	case ast.KindGetAccessor, ast.KindSetAccessor:
		return lsproto.SymbolKindProperty
	case ast.KindMethodDeclaration, ast.KindMethodSignature:
		return lsproto.SymbolKindMethod
	case ast.KindPropertyDeclaration, ast.KindPropertySignature, ast.KindPropertyAssignment,
		ast.KindShorthandPropertyAssignment, ast.KindSpreadAssignment, ast.KindIndexSignature:
		return lsproto.SymbolKindProperty
	case ast.KindCallSignature:
		return lsproto.SymbolKindMethod
	case ast.KindConstructSignature:
		return lsproto.SymbolKindConstructor
	case ast.KindConstructor, ast.KindClassStaticBlockDeclaration:
		return lsproto.SymbolKindConstructor
	case ast.KindTypeParameter:
		return lsproto.SymbolKindTypeParameter
	case ast.KindEnumMember:
		return lsproto.SymbolKindEnumMember
	case ast.KindParameter:
		if ast.HasSyntacticModifier(node, ast.ModifierFlagsParameterPropertyModifier) {
			return lsproto.SymbolKindProperty
		}
		return lsproto.SymbolKindVariable
	case ast.KindBinaryExpression, ast.KindCallExpression:
		kind := ast.GetAssignmentDeclarationKind(node)
		switch kind {
		case ast.JSDeclarationKindThisProperty, ast.JSDeclarationKindProperty, ast.JSDeclarationKindObjectDefinePropertyValue:
			return lsproto.SymbolKindProperty
		}
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral, ast.KindNumericLiteral:
		// String literals used as property names (e.g., in Object.defineProperty)
		return lsproto.SymbolKindProperty
	}
	return lsproto.SymbolKindVariable
}
