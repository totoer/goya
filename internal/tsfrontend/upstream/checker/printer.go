// Modified for Goya: internal import paths relocated by cmd/importupstream.

// Modified for Goya: embedded-language-service.

package checker

import (
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/nodebuilder"
	"github.com/totoer/goya/internal/tsfrontend/upstream/printer"
)

// TODO: Memoize once per checker to retain threadsafety
// These constructors serve request-local reference inference only. In particular,
// their property order belongs to the synthetic type, not to cached source types.
func (c *Checker) NewReferenceInferenceArray(t *Type) *Type   { return c.createArrayType(t) }
func (c *Checker) NewReferenceInferencePromise(t *Type) *Type { return c.createPromiseType(t) }
func (c *Checker) GetReferenceInferenceArrayElement(t *Type) *Type {
	return c.getElementTypeOfArrayType(t)
}
func (c *Checker) NewReferenceInferenceSymbol(flags ast.SymbolFlags, name string, t *Type) *ast.Symbol {
	symbol := c.newSymbol(flags, name)
	c.valueSymbolLinks.Get(symbol).resolvedType = t
	return symbol
}
func (c *Checker) GetReferenceInferenceSymbolType(symbol *ast.Symbol) *Type {
	return c.valueSymbolLinks.Get(symbol).resolvedType
}
func (c *Checker) NewReferenceInferenceIndex(key, value *Type, readonly bool) *IndexInfo {
	return c.newIndexInfo(key, value, readonly, nil, nil)
}
func (c *Checker) NewReferenceInferenceSignature(parameters []*ast.Symbol, result *Type, count int) *Signature {
	return c.newSignature(SignatureFlagsNone, nil, nil, nil, parameters, result, nil, count)
}
func (c *Checker) NewReferenceInferenceObject(symbol *ast.Symbol, properties []*ast.Symbol, calls, constructs []*Signature, indices []*IndexInfo) *Type {
	members := make(ast.SymbolTable, len(properties))
	for _, p := range properties {
		members[p.Name] = p
	}
	t := c.newAnonymousType(symbol, members, calls, constructs, indices)
	t.AsStructuredType().properties = properties
	return t
}

type referenceInferenceSymbolTracker struct {
	checker    *Checker
	accessible bool
}

func (t *referenceInferenceSymbolTracker) TrackSymbol(symbol *ast.Symbol, scope *ast.Node, meaning ast.SymbolFlags) bool {
	t.accessible = t.accessible && t.checker.IsSymbolAccessible(symbol, scope, meaning, false).Accessibility == printer.SymbolAccessibilityAccessible
	return !t.accessible
}
func (t *referenceInferenceSymbolTracker) ReportInaccessibleThisError() { t.accessible = false }
func (t *referenceInferenceSymbolTracker) ReportPrivateInBaseOfClassExpression(string) {
	t.accessible = false
}
func (t *referenceInferenceSymbolTracker) ReportInaccessibleUniqueSymbolError()                 { t.accessible = false }
func (t *referenceInferenceSymbolTracker) ReportCyclicStructureError()                          {}
func (t *referenceInferenceSymbolTracker) ReportLikelyUnsafeImportRequiredError(string, string) {}
func (t *referenceInferenceSymbolTracker) ReportTruncationError()                               {}
func (t *referenceInferenceSymbolTracker) ReportNonlocalAugmentation(*ast.SourceFile, *ast.Symbol, *ast.Symbol) {
}
func (t *referenceInferenceSymbolTracker) ReportNonSerializableProperty(string) {}
func (t *referenceInferenceSymbolTracker) ReportInferenceFallback(*ast.Node)    {}
func (t *referenceInferenceSymbolTracker) PushErrorFallbackNode(*ast.Node)      {}
func (t *referenceInferenceSymbolTracker) PopErrorFallbackNode()                {}
func (c *Checker) GetReferenceInferenceTypeNode(t *Type, scope *ast.Node, ids map[*ast.Node]*ast.Symbol, emit *printer.EmitContext) *ast.Node {
	tracker := &referenceInferenceSymbolTracker{checker: c, accessible: true}
	node := NewNodeBuilderEx(c, emit, ids).TypeToTypeNode(t, scope, nodebuilder.FlagsNoTruncation, nodebuilder.InternalFlagsAllowUnresolvedNames, tracker)
	if !tracker.accessible {
		return nil
	}
	return node
}

type referenceIsolatedSymbolTracker struct {
	referenceInferenceSymbolTracker
	truncated bool
}

func (*referenceIsolatedSymbolTracker) TrackSymbol(*ast.Symbol, *ast.Node, ast.SymbolFlags) bool {
	return true
}
func (t *referenceIsolatedSymbolTracker) ReportTruncationError() { t.truncated = true }
func (c *Checker) HasReferenceIsolatedConstraintCache(t *Type) bool {
	return t.AsTypeParameter().constraint != nil
}
func (c *Checker) ReferenceIsolatedTypeNode(t *Type, predicate *TypePredicate, scope *ast.Node, flags nodebuilder.Flags, ids map[*ast.Node]*ast.Symbol, emit *printer.EmitContext) (*ast.Node, bool) {
	tracker := &referenceIsolatedSymbolTracker{}
	builder := NewNodeBuilderEx(c, emit, ids)
	builder.impl.referenceIsolatedTypeOfFunction = true
	var node *ast.Node
	if predicate != nil {
		node = builder.TypePredicateToTypePredicateNode(predicate, scope, flags, nodebuilder.InternalFlagsWriteComputedProps, tracker)
	} else {
		node = builder.TypeToTypeNode(t, scope, flags, nodebuilder.InternalFlagsWriteComputedProps, tracker)
	}
	return node, tracker.truncated
}

func createPrinterWithDefaults(emitContext *printer.EmitContext) *printer.Printer {
	return printer.NewPrinter(printer.PrinterOptions{}, printer.PrintHandlers{}, emitContext)
}

func createPrinterWithRemoveComments(emitContext *printer.EmitContext) *printer.Printer {
	return printer.NewPrinter(printer.PrinterOptions{RemoveComments: true}, printer.PrintHandlers{}, emitContext)
}

func createPrinterWithRemoveCommentsOmitTrailingSemicolonNeverAsciiEscape(emitContext *printer.EmitContext) *printer.Printer {
	// TODO: OmitTrailingSemicolon support
	return printer.NewPrinter(printer.PrinterOptions{
		RemoveComments:   true,
		NeverAsciiEscape: true,
	}, printer.PrintHandlers{}, emitContext)
}

func createPrinterWithRemoveCommentsNeverAsciiEscape(emitContext *printer.EmitContext) *printer.Printer {
	return printer.NewPrinter(printer.PrinterOptions{
		RemoveComments:   true,
		NeverAsciiEscape: true,
	}, printer.PrintHandlers{}, emitContext)
}

type semicolonRemoverWriter struct {
	hasPendingSemicolon bool
	inner               printer.EmitTextWriter
}

func (s *semicolonRemoverWriter) commitSemicolon() {
	if s.hasPendingSemicolon {
		s.inner.WriteTrailingSemicolon(";")
		s.hasPendingSemicolon = false
	}
}

func (s *semicolonRemoverWriter) Clear() {
	s.inner.Clear()
}

func (s *semicolonRemoverWriter) DecreaseIndent() {
	s.commitSemicolon()
	s.inner.DecreaseIndent()
}

func (s *semicolonRemoverWriter) GetColumn() core.UTF16Offset {
	return s.inner.GetColumn()
}

func (s *semicolonRemoverWriter) GetIndent() int {
	return s.inner.GetIndent()
}

func (s *semicolonRemoverWriter) GetLine() int {
	return s.inner.GetLine()
}

func (s *semicolonRemoverWriter) GetTextPos() int {
	return s.inner.GetTextPos()
}

func (s *semicolonRemoverWriter) HasTrailingComment() bool {
	return s.inner.HasTrailingComment()
}

func (s *semicolonRemoverWriter) HasTrailingWhitespace() bool {
	return s.inner.HasTrailingWhitespace()
}

func (s *semicolonRemoverWriter) IncreaseIndent() {
	s.commitSemicolon()
	s.inner.IncreaseIndent()
}

func (s *semicolonRemoverWriter) IsAtStartOfLine() bool {
	return s.inner.IsAtStartOfLine()
}

func (s *semicolonRemoverWriter) RawWrite(s1 string) {
	s.commitSemicolon()
	s.inner.RawWrite(s1)
}

func (s *semicolonRemoverWriter) String() string {
	s.commitSemicolon()
	return s.inner.String()
}

func (s *semicolonRemoverWriter) Write(s1 string) {
	s.commitSemicolon()
	s.inner.Write(s1)
}

func (s *semicolonRemoverWriter) WriteComment(text string) {
	s.commitSemicolon()
	s.inner.WriteComment(text)
}

func (s *semicolonRemoverWriter) WriteKeyword(text string) {
	s.commitSemicolon()
	s.inner.WriteKeyword(text)
}

func (s *semicolonRemoverWriter) WriteLine() {
	s.commitSemicolon()
	s.inner.WriteLine()
}

func (s *semicolonRemoverWriter) WriteLineForce(force bool) {
	s.commitSemicolon()
	s.inner.WriteLineForce(force)
}

func (s *semicolonRemoverWriter) WriteLiteral(s1 string) {
	s.commitSemicolon()
	s.inner.WriteLiteral(s1)
}

func (s *semicolonRemoverWriter) WriteOperator(text string) {
	s.commitSemicolon()
	s.inner.WriteOperator(text)
}

func (s *semicolonRemoverWriter) WriteParameter(text string) {
	s.commitSemicolon()
	s.inner.WriteParameter(text)
}

func (s *semicolonRemoverWriter) WriteProperty(text string) {
	s.commitSemicolon()
	s.inner.WriteProperty(text)
}

func (s *semicolonRemoverWriter) WritePunctuation(text string) {
	s.commitSemicolon()
	s.inner.WritePunctuation(text)
}

func (s *semicolonRemoverWriter) WriteSpace(text string) {
	s.commitSemicolon()
	s.inner.WriteSpace(text)
}

func (s *semicolonRemoverWriter) WriteStringLiteral(text string) {
	s.commitSemicolon()
	s.inner.WriteStringLiteral(text)
}

func (s *semicolonRemoverWriter) WriteSymbol(text string, symbol *ast.Symbol) {
	s.commitSemicolon()
	s.inner.WriteSymbol(text, symbol)
}

func (s *semicolonRemoverWriter) WriteTrailingSemicolon(text string) {
	s.hasPendingSemicolon = true
}

func getTrailingSemicolonDeferringWriter(writer printer.EmitTextWriter) printer.EmitTextWriter {
	return &semicolonRemoverWriter{false, writer}
}

func (c *Checker) TypeToString(t *Type) string {
	return c.typeToString(t, nil)
}

func (c *Checker) typeToString(t *Type, enclosingDeclaration *ast.Node) string {
	return c.typeToStringEx(t, enclosingDeclaration, TypeFormatFlagsAllowUniqueESSymbolType|TypeFormatFlagsUseAliasDefinedOutsideCurrentScope, nil)
}

func toNodeBuilderFlags(flags TypeFormatFlags) nodebuilder.Flags {
	return nodebuilder.Flags(flags & TypeFormatFlagsNodeBuilderFlagsMask)
}

func (c *Checker) TypeToStringEx(t *Type, enclosingDeclaration *ast.Node, flags TypeFormatFlags, vc *VerbosityContext) string {
	return c.typeToStringEx(t, enclosingDeclaration, flags, vc)
}

func (c *Checker) typeToStringEx(t *Type, enclosingDeclaration *ast.Node, flags TypeFormatFlags, vc *VerbosityContext) string {
	// Serialization of types can lead to (lazy) resolution of members, which can cause diagnostics that again require
	// serialization of types. This can potentially result in infinite recursion and stack overflows. To prevent that,
	// after a certain number of recursive invocations the function simply returns "?".
	if c.serializationLevel >= maxSerializationLevel {
		return "?"
	}
	newLine := ""
	if flags&TypeFormatFlagsMultilineObjectLiterals != 0 {
		newLine = "\n"
	}
	writer := printer.NewTextWriter(newLine, 0)
	noTruncation := ((vc == nil || vc.MaxTruncationLength == 0) && c.compilerOptions.NoErrorTruncation == core.TSTrue) || (flags&TypeFormatFlagsNoTruncation != 0)
	combinedFlags := toNodeBuilderFlags(flags) | nodebuilder.FlagsIgnoreErrors
	if noTruncation {
		combinedFlags = combinedFlags | nodebuilder.FlagsNoTruncation
	}
	nodeBuilder, release := c.getNodeBuilder()
	defer release()
	oldVerbosity := nodeBuilder.verbosity
	nodeBuilder.verbosity = vc
	defer func() {
		nodeBuilder.verbosity = oldVerbosity
	}()
	c.serializationLevel++
	typeNode := nodeBuilder.TypeToTypeNode(t, enclosingDeclaration, combinedFlags, nodebuilder.InternalFlagsNone, nil)
	c.serializationLevel--
	if typeNode == nil {
		panic("should always get typenode")
	}
	// The unresolved type gets a synthesized comment on `any` to hint to users that it's not a plain `any`.
	// Otherwise, we always strip comments out.
	var p *printer.Printer
	if t == c.unresolvedType {
		p = createPrinterWithDefaults(nodeBuilder.EmitContext())
	} else {
		p = createPrinterWithRemoveComments(nodeBuilder.EmitContext())
	}
	var sourceFile *ast.SourceFile
	if enclosingDeclaration != nil {
		sourceFile = ast.GetSourceFileOfNode(enclosingDeclaration)
	}
	p.Write(typeNode, sourceFile, writer, nil)
	result := writer.String()

	maxLength := defaultMaximumTruncationLength * 2
	if vc != nil && vc.MaxTruncationLength > 0 {
		maxLength = vc.MaxTruncationLength * 10 // hard cutoff matching Strada's absoluteMaximumLength
	}
	if noTruncation {
		maxLength = noTruncationMaximumTruncationLength * 2
	}
	if maxLength > 0 && result != "" && len(result) >= maxLength {
		if vc != nil {
			vc.Truncated = true
		}
		return result[0:maxLength-len("...")] + "..."
	}
	return result
}

func (c *Checker) SymbolToString(s *ast.Symbol) string {
	return c.symbolToString(s)
}

func (c *Checker) symbolToString(symbol *ast.Symbol) string {
	return c.symbolToStringEx(symbol, nil, ast.SymbolFlagsAll, SymbolFormatFlagsAllowAnyNodeKind)
}

func (c *Checker) SymbolToStringEx(symbol *ast.Symbol, enclosingDeclaration *ast.Node, meaning ast.SymbolFlags, flags SymbolFormatFlags) string {
	return c.symbolToStringEx(symbol, enclosingDeclaration, meaning, flags)
}

func (c *Checker) symbolToStringEx(symbol *ast.Symbol, enclosingDeclaration *ast.Node, meaning ast.SymbolFlags, flags SymbolFormatFlags) string {
	writer, putWriter := printer.GetSingleLineStringWriter()
	defer putWriter()

	nodeFlags := nodebuilder.FlagsIgnoreErrors
	internalNodeFlags := nodebuilder.InternalFlagsNone
	if flags&SymbolFormatFlagsUseOnlyExternalAliasing != 0 {
		nodeFlags |= nodebuilder.FlagsUseOnlyExternalAliasing
	}
	if flags&SymbolFormatFlagsWriteTypeParametersOrArguments != 0 {
		nodeFlags |= nodebuilder.FlagsWriteTypeParametersInQualifiedName
	}
	if flags&SymbolFormatFlagsUseAliasDefinedOutsideCurrentScope != 0 {
		nodeFlags |= nodebuilder.FlagsUseAliasDefinedOutsideCurrentScope
	}
	if flags&SymbolFormatFlagsDoNotIncludeSymbolChain != 0 {
		internalNodeFlags |= nodebuilder.InternalFlagsDoNotIncludeSymbolChain
	}
	if flags&SymbolFormatFlagsWriteComputedProps != 0 {
		internalNodeFlags |= nodebuilder.InternalFlagsWriteComputedProps
	}

	nodeBuilder, release := c.getNodeBuilder()
	defer release()
	var sourceFile *ast.SourceFile
	if enclosingDeclaration != nil {
		sourceFile = ast.GetSourceFileOfNode(enclosingDeclaration)
	}
	var printer_ *printer.Printer
	// add neverAsciiEscape for GH#39027
	if enclosingDeclaration != nil && enclosingDeclaration.Kind == ast.KindSourceFile {
		printer_ = createPrinterWithRemoveCommentsNeverAsciiEscape(nodeBuilder.EmitContext())
	} else {
		printer_ = createPrinterWithRemoveComments(nodeBuilder.EmitContext())
	}

	var builder func(symbol *ast.Symbol, meaning ast.SymbolFlags, enclosingDeclaration *ast.Node, flags nodebuilder.Flags, internalFlags nodebuilder.InternalFlags, tracker nodebuilder.SymbolTracker) *ast.Node
	if flags&SymbolFormatFlagsAllowAnyNodeKind != 0 {
		builder = nodeBuilder.SymbolToNode
	} else {
		builder = nodeBuilder.SymbolToEntityName
	}
	entity := builder(symbol, meaning, enclosingDeclaration, nodeFlags, internalNodeFlags, nil)         // TODO: GH#18217
	printer_.Write(entity /*sourceFile*/, sourceFile, getTrailingSemicolonDeferringWriter(writer), nil) // TODO: GH#18217
	return writer.String()
}

func (c *Checker) signatureToString(signature *Signature) string {
	return c.signatureToStringEx(signature, nil, TypeFormatFlagsNone, nil)
}

func (c *Checker) SignatureToStringEx(signature *Signature, enclosingDeclaration *ast.Node, flags TypeFormatFlags, vc *VerbosityContext) string {
	return c.signatureToStringEx(signature, enclosingDeclaration, flags, vc)
}

func (c *Checker) signatureToStringEx(signature *Signature, enclosingDeclaration *ast.Node, flags TypeFormatFlags, vc *VerbosityContext) string {
	isConstructor := signature.flags&SignatureFlagsConstruct != 0 && flags&TypeFormatFlagsWriteCallStyleSignature == 0
	var sigOutput ast.Kind
	if flags&TypeFormatFlagsWriteArrowStyleSignature != 0 {
		if isConstructor {
			sigOutput = ast.KindConstructorType
		} else {
			sigOutput = ast.KindFunctionType
		}
	} else {
		if isConstructor {
			sigOutput = ast.KindConstructSignature
		} else {
			sigOutput = ast.KindCallSignature
		}
	}

	nodeBuilder, release := c.getNodeBuilder()
	defer release()
	oldVerbosity := nodeBuilder.verbosity
	nodeBuilder.verbosity = vc
	defer func() {
		nodeBuilder.verbosity = oldVerbosity
	}()
	combinedFlags := toNodeBuilderFlags(flags) | nodebuilder.FlagsIgnoreErrors | nodebuilder.FlagsWriteTypeParametersInQualifiedName
	sig := nodeBuilder.SignatureToSignatureDeclaration(signature, sigOutput, enclosingDeclaration, combinedFlags, nodebuilder.InternalFlagsNone, nil)
	p := createPrinterWithRemoveCommentsOmitTrailingSemicolonNeverAsciiEscape(nodeBuilder.EmitContext())
	var sourceFile *ast.SourceFile
	if enclosingDeclaration != nil {
		sourceFile = ast.GetSourceFileOfNode(enclosingDeclaration)
	}
	if flags&TypeFormatFlagsMultilineObjectLiterals != 0 {
		writer := printer.NewTextWriter("\n", 0)
		p.Write(sig, sourceFile, getTrailingSemicolonDeferringWriter(writer), nil)
		return writer.String()
	}
	writer, putWriter := printer.GetSingleLineStringWriter()
	defer putWriter()
	p.Write(sig, sourceFile, getTrailingSemicolonDeferringWriter(writer), nil)
	return writer.String()
}

func (c *Checker) typePredicateToString(typePredicate *TypePredicate) string {
	return c.typePredicateToStringEx(typePredicate, nil, TypeFormatFlagsUseAliasDefinedOutsideCurrentScope)
}

func (c *Checker) typePredicateToStringEx(typePredicate *TypePredicate, enclosingDeclaration *ast.Node, flags TypeFormatFlags) string {
	writer, putWriter := printer.GetSingleLineStringWriter()
	defer putWriter()
	nodeBuilder, release := c.getNodeBuilder()
	defer release()
	combinedFlags := toNodeBuilderFlags(flags) | nodebuilder.FlagsIgnoreErrors | nodebuilder.FlagsWriteTypeParametersInQualifiedName
	predicate := nodeBuilder.TypePredicateToTypePredicateNode(typePredicate, enclosingDeclaration, combinedFlags, nodebuilder.InternalFlagsNone, nil) // TODO: GH#18217
	printer_ := createPrinterWithRemoveComments(nodeBuilder.EmitContext())
	var sourceFile *ast.SourceFile
	if enclosingDeclaration != nil {
		sourceFile = ast.GetSourceFileOfNode(enclosingDeclaration)
	}
	printer_.Write(predicate /*sourceFile*/, sourceFile, writer, nil)
	return writer.String()
}

func (c *Checker) valueToString(value any) string {
	return ValueToString(value)
}

func (c *Checker) formatUnionTypes(types []*Type, expandingEnum bool) []*Type {
	var result []*Type
	var flags TypeFlags
	for i := 0; i < len(types); i++ {
		t := types[i]
		flags |= t.flags
		if t.flags&TypeFlagsNullable == 0 {
			if t.flags&TypeFlagsBooleanLiteral != 0 || (!expandingEnum && t.flags&TypeFlagsEnumLike != 0) {
				var baseType *Type
				if t.flags&TypeFlagsBooleanLiteral != 0 {
					baseType = c.booleanType
				} else {
					baseType = c.getBaseTypeOfEnumLikeType(t)
				}
				if baseType.flags&TypeFlagsUnion != 0 {
					count := len(baseType.AsUnionType().types)
					if i+count <= len(types) && c.getRegularTypeOfLiteralType(types[i+count-1]) == c.getRegularTypeOfLiteralType(baseType.AsUnionType().types[count-1]) {
						result = append(result, baseType)
						i += count - 1
						continue
					}
				}
			}
			result = append(result, t)
		}
	}
	if flags&TypeFlagsNull != 0 {
		result = append(result, c.nullType)
	}
	if flags&TypeFlagsUndefined != 0 {
		result = append(result, c.undefinedType)
	}
	return result
}

func (c *Checker) TypeToTypeNode(t *Type, enclosingDeclaration *ast.Node, flags nodebuilder.Flags, idToSymbol map[*ast.IdentifierNode]*ast.Symbol) *ast.TypeNode {
	nodeBuilder := c.getNodeBuilderEx(idToSymbol)
	return nodeBuilder.TypeToTypeNode(t, enclosingDeclaration, flags, nodebuilder.InternalFlagsNone, nil)
}

func (c *Checker) SignatureToSignatureDeclaration(signature *Signature, kind ast.Kind, enclosingDeclaration *ast.Node, flags nodebuilder.Flags) *ast.Node {
	nodeBuilder, release := c.getNodeBuilder()
	defer release()
	return nodeBuilder.SignatureToSignatureDeclaration(signature, kind, enclosingDeclaration, flags, nodebuilder.InternalFlagsNone, nil)
}

// ExpandSymbolForHover produces declaration strings for a symbol with verbosity support for expandable hover.
func (c *Checker) ExpandSymbolForHover(symbol *ast.Symbol, meaning ast.SymbolFlags, vc *VerbosityContext) string {
	nodeBuilder, release := c.getNodeBuilder()
	defer release()
	oldVerbosity := nodeBuilder.verbosity
	nodeBuilder.verbosity = vc
	defer func() {
		nodeBuilder.verbosity = oldVerbosity
	}()
	nodes := nodeBuilder.ExpandSymbolForHover(symbol, meaning)
	if len(nodes) == 0 {
		return ""
	}
	p := createPrinterWithRemoveComments(nodeBuilder.EmitContext())
	var sourceFile *ast.SourceFile
	if symbol.ValueDeclaration != nil {
		sourceFile = ast.GetSourceFileOfNode(symbol.ValueDeclaration)
	}
	var b strings.Builder
	for i, node := range nodes {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(p.Emit(node, sourceFile))
	}
	return b.String()
}

// TypeParameterToStringEx renders a type parameter declaration (e.g. "T extends Foo") with optional verbosity support.
func (c *Checker) TypeParameterToStringEx(t *Type, enclosingDeclaration *ast.Node, vc *VerbosityContext) string {
	nodeBuilder, release := c.getNodeBuilder()
	defer release()
	oldVerbosity := nodeBuilder.verbosity
	nodeBuilder.verbosity = vc
	defer func() {
		nodeBuilder.verbosity = oldVerbosity
	}()
	typeParamNode := nodeBuilder.TypeParameterToDeclaration(t, enclosingDeclaration, nodebuilder.FlagsIgnoreErrors, nodebuilder.InternalFlagsNone, nil)
	if typeParamNode == nil {
		return c.TypeToString(t)
	}
	p := createPrinterWithRemoveComments(nodeBuilder.EmitContext())
	var sourceFile *ast.SourceFile
	if enclosingDeclaration != nil {
		sourceFile = ast.GetSourceFileOfNode(enclosingDeclaration)
	}
	return p.Emit(typeParamNode, sourceFile)
}

func (c *Checker) TypeToTypeNodeEx(t *Type, enclosingDeclaration *ast.Node, flags nodebuilder.Flags, internalFlags nodebuilder.InternalFlags, idToSymbol map[*ast.IdentifierNode]*ast.Symbol) *ast.TypeNode {
	nodeBuilder := c.getNodeBuilderEx(idToSymbol)
	return nodeBuilder.TypeToTypeNode(t, enclosingDeclaration, flags, internalFlags, nil)
}

func (c *Checker) ReferencePredicateTypeNode(t *TypePredicate, enclosingDeclaration *ast.Node) *ast.Node {
	b := c.getNodeBuilderEx(nil)
	return b.TypePredicateToTypePredicateNode(t, enclosingDeclaration, nodebuilder.FlagsNoTruncation, nodebuilder.InternalFlagsAllowUnresolvedNames, nil)
}

func (c *Checker) TypePredicateToTypePredicateNode(t *TypePredicate, enclosingDeclaration *ast.Node, flags nodebuilder.Flags, idToSymbol map[*ast.IdentifierNode]*ast.Symbol) *ast.TypePredicateNodeNode {
	nodeBuilder := c.getNodeBuilderEx(idToSymbol)
	return nodeBuilder.TypePredicateToTypePredicateNode(t, enclosingDeclaration, flags, nodebuilder.InternalFlagsNone, nil)
}
