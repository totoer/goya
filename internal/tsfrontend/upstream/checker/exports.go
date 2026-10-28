// Modified for Goya: internal import paths relocated by cmd/importupstream.

// Modified for Goya: embedded-language-service.

package checker

import (
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
)

// ReferenceWalkTypeParameters follows the pinned TypeScript symbol walker.
// Results belong to the current checker lease, including types reached through
// signatures, exports, constraints and authored typeof queries.
func (c *Checker) ReferenceWalkTypeParameters(root *Type, cancelled func() bool) []*Type {
	types := map[*Type]bool{}
	symbols := map[*ast.Symbol]bool{}
	result := []*Type{}
	var visitType func(*Type)
	var visitSymbol func(*ast.Symbol)
	var visitSignature func(*Signature)
	var visitObject func(*Type)
	visitSignature = func(sig *Signature) {
		if p := c.getTypePredicateOfSignature(sig); p != nil {
			visitType(p.t)
		}
		for _, t := range sig.typeParameters {
			visitType(t)
		}
		for _, p := range sig.parameters {
			visitSymbol(p)
		}
		visitType(c.getRestTypeOfSignature(sig))
		visitType(c.getReturnTypeOfSignature(sig))
	}
	visitObject = func(t *Type) {
		resolved := c.resolveStructuredTypeMembers(t)
		for _, info := range resolved.indexInfos {
			visitType(info.keyType)
			visitType(info.valueType)
		}
		for _, sig := range resolved.CallSignatures() {
			visitSignature(sig)
		}
		for _, sig := range resolved.ConstructSignatures() {
			visitSignature(sig)
		}
		for _, p := range resolved.properties {
			visitSymbol(p)
		}
	}
	visitSymbol = func(s *ast.Symbol) {
		if s == nil || symbols[s] || cancelled() {
			return
		}
		symbols[s] = true
		visitType(c.getTypeOfSymbol(s))
		for _, export := range s.Exports {
			visitSymbol(export)
		}
		for _, decl := range s.Declarations {
			if decl.Type() != nil && ast.IsTypeQueryNode(decl.Type()) {
				visitSymbol(c.getResolvedSymbol(ast.GetFirstIdentifier(decl.Type().AsTypeQueryNode().ExprName)))
			}
		}
	}
	visitType = func(t *Type) {
		if t == nil || types[t] || cancelled() {
			return
		}
		types[t] = true
		if t.flags&TypeFlagsTypeParameter != 0 {
			result = append(result, t)
		}
		visitSymbol(t.symbol)
		if t.flags&TypeFlagsObject != 0 {
			flags := t.ObjectFlags()
			if flags&ObjectFlagsReference != 0 {
				visitType(t.Target())
				for _, arg := range c.getTypeArguments(t) {
					visitType(arg)
				}
			}
			if flags&ObjectFlagsMapped != 0 {
				m := t.AsMappedType()
				visitType(m.typeParameter)
				visitType(m.constraintType)
				visitType(m.templateType)
				visitType(m.modifiersType)
			}
			if flags&(ObjectFlagsClass|ObjectFlagsInterface) != 0 {
				visitObject(t)
				for _, p := range t.AsInterfaceType().TypeParameters() {
					visitType(p)
				}
				for _, b := range c.getBaseTypes(t) {
					visitType(b)
				}
				visitType(t.AsInterfaceType().thisType)
			}
			if flags&(ObjectFlagsTuple|ObjectFlagsAnonymous) != 0 {
				visitObject(t)
			}
		}
		if t.flags&TypeFlagsTypeParameter != 0 {
			visitType(c.getConstraintOfTypeParameter(t))
		}
		if t.flags&TypeFlagsUnionOrIntersection != 0 {
			for _, part := range t.Types() {
				visitType(part)
			}
		}
		if t.flags&TypeFlagsIndex != 0 {
			visitType(t.AsIndexType().target)
		}
		if t.flags&TypeFlagsIndexedAccess != 0 {
			a := t.AsIndexedAccessType()
			visitType(a.objectType)
			visitType(a.indexType)
			visitType(a.resolvedBaseConstraint)
		}
	}
	visitType(root)
	return result
}

func (c *Checker) ReferenceIsOptionalParameter(node *ast.Node) bool {
	return c.isOptionalParameter(node)
}
func (c *Checker) GetReferenceSymbolTarget(symbol *ast.Symbol) *ast.Symbol {
	for symbol != nil {
		if symbol.Flags&ast.SymbolFlagsTransient != 0 && c.valueSymbolLinks.Has(symbol) {
			if target := c.valueSymbolLinks.Get(symbol).target; target != nil {
				symbol = target
				continue
			}
		}
		if symbol.Flags&ast.SymbolFlagsAlias != 0 {
			symbol = c.resolveAlias(symbol)
			continue
		}
		return symbol
	}
	return nil
}
func (c *Checker) ReferenceIsImplementationOfOverload(node *ast.Node) bool {
	if !ast.NodeIsPresent(node.Body()) || ast.IsGetAccessorDeclaration(node) || ast.IsSetAccessorDeclaration(node) {
		return false
	}
	signatures := c.getSignaturesOfSymbol(c.getSymbolOfDeclaration(node))
	if len(signatures) > 1 {
		return true
	}
	return len(signatures) == 1 && signatures[0].declaration != node && signatures[0].declaration.Flags&ast.NodeFlagsJSDoc == 0
}
func (c *Checker) IsReferenceSymbolReferenced(symbol *ast.Symbol) bool {
	return symbol != nil && c.isReferenced(symbol)
}

func (c *Checker) GetStringType() *Type {
	return c.stringType
}

func (c *Checker) GetNumberType() *Type {
	return c.numberType
}

func (c *Checker) ReferenceIsGlobalPromise(t *Type) bool {
	return t != nil && t.ObjectFlags()&ObjectFlagsReference != 0 && (t.Target() == c.getGlobalPromiseTypeChecked() || t.Target() == c.getGlobalPromiseLikeType())
}
func (c *Checker) ReferenceContainsArguments(node *ast.Node) bool {
	return c.containsArgumentsReference(node)
}
func (c *Checker) IsReferenceFixedReturnAssignable(declaration, expression, label *ast.Node, expected *Type, functionType bool) bool {
	result := c.GetTypeAtLocation(expression)
	if label != nil {
		member := c.newSymbol(ast.SymbolFlagsProperty, label.Text())
		c.valueSymbolLinks.Get(member).resolvedType = result
		result = c.newAnonymousType(nil, ast.SymbolTable{label.Text(): member}, nil, nil, nil)
	}
	if functionType {
		signature := c.getSignatureFromDeclaration(declaration)
		if signature == nil {
			result = c.anyType
		} else {
			if declaration.ModifierFlags()&ast.ModifierFlagsAsync != 0 {
				result = c.createPromiseType(result)
			}
			fixed := c.newSignature(signature.flags, declaration, signature.typeParameters, signature.thisParameter, signature.parameters, result, nil, int(signature.minArgumentCount))
			result = c.newAnonymousType(nil, nil, []*Signature{fixed}, nil, nil)
		}
	}
	return c.isTypeAssignableTo(result, expected)
}

func (c *Checker) IsReferenceAsyncIterable(typ *Type) bool {
	target := c.getGlobalAsyncIterableType()
	if target == c.emptyGenericType {
		return false
	}
	return c.isTypeAssignableTo(typ, c.createTypeFromGenericGlobalType(target, []*Type{c.anyType, c.anyType, c.anyType}))
}

func (c *Checker) GetReferenceOriginatingImport(symbol *ast.Symbol) *ast.Node {
	if symbol == nil || !c.exportTypeLinks.Has(symbol) {
		return nil
	}
	return c.exportTypeLinks.Get(symbol).originatingImport
}

func (c *Checker) IsReferenceSymbolValue(symbol *ast.Symbol) bool { return c.symbolIsValue(symbol) }
func (c *Checker) GetReferenceExactOptionalProperties(typ *Type) []*ast.Symbol {
	properties := []*ast.Symbol{}
	for _, prop := range c.GetPropertiesOfType(typ) {
		if c.containsMissingType(c.GetTypeOfSymbol(prop)) {
			properties = append(properties, prop)
		}
	}
	return properties
}

func (c *Checker) GetReferenceUnmatchedProperties(source, target *Type, requireOptionalProperties, matchDiscriminantProperties bool) []*ast.Symbol {
	return c.getUnmatchedProperties(source, target, requireOptionalProperties, matchDiscriminantProperties)
}

// Reference spelling bridges reuse checker lookup algorithms under the same
// snapshot lease; symbols and types never enter the public editor API.
func (c *Checker) GetReferenceSuggestedProperty(name *ast.Node, containing *Type) *ast.Symbol {
	return c.getSuggestedSymbolForNonexistentProperty(name, containing)
}
func (c *Checker) GetReferenceSuggestedModule(name *ast.Node, target *ast.Symbol) *ast.Symbol {
	return c.getSuggestedSymbolForNonexistentModule(name, target)
}
func (c *Checker) GetReferenceSuggestedJSXAttribute(name string, containing *Type) *ast.Symbol {
	return c.getSuggestedSymbolForNonexistentJSXAttribute(name, containing)
}
func (c *Checker) GetReferenceSuggestedClassMember(name string, base *Type) *ast.Symbol {
	return c.getSuggestedSymbolForNonexistentClassMember(name, base)
}
func (c *Checker) GetReferenceSuggestedSymbol(location *ast.Node, name string, meaning ast.SymbolFlags) *ast.Symbol {
	return c.getSuggestedSymbolForNonexistentSymbol(location, name, meaning)
}

func (c *Checker) GetBooleanType() *Type {
	return c.booleanType
}

// GetReferenceAwaitedType exposes the existing awaited-type algorithm only to
// the embedded language-service bridge; checker values never leave a lease.
func (c *Checker) GetReferenceAwaitedType(t *Type) *Type { return c.getAwaitedType(t) }

func (c *Checker) GetVoidType() *Type {
	return c.voidType
}

func (c *Checker) GetUndefinedType() *Type {
	return c.undefinedType
}

func (c *Checker) GetNullType() *Type {
	return c.nullType
}

func (c *Checker) GetAnyType() *Type {
	return c.anyType
}

func (c *Checker) GetErrorType() *Type {
	return c.errorType
}

func (c *Checker) GetNeverType() *Type {
	return c.neverType
}

func (c *Checker) GetUnknownType() *Type {
	return c.unknownType
}

func (c *Checker) GetBigIntType() *Type {
	return c.bigintType
}

func (c *Checker) GetESSymbolType() *Type {
	return c.esSymbolType
}

func (c *Checker) GetBaseTypeOfLiteralType(t *Type) *Type {
	return c.getBaseTypeOfLiteralType(t)
}

func (c *Checker) GetUnknownSymbol() *ast.Symbol {
	return c.unknownSymbol
}

func (c *Checker) GetUndefinedSymbol() *ast.Symbol {
	return c.undefinedSymbol
}

func (c *Checker) GetArgumentsSymbol() *ast.Symbol {
	return c.argumentsSymbol
}

func (c *Checker) GetUnionType(types []*Type) *Type {
	return c.getUnionType(types)
}

func (c *Checker) GetNameTypeOfSymbol(symbol *ast.Symbol) *Type {
	if !c.valueSymbolLinks.Has(symbol) {
		return nil
	}
	return c.valueSymbolLinks.TryGet(symbol).nameType
}

func IsTypeUsableAsPropertyName(t *Type) bool {
	return isTypeUsableAsPropertyName(t)
}

func GetPropertyNameFromType(t *Type) string {
	return getPropertyNameFromType(t)
}

func (c *Checker) GetGlobalSymbol(name string, meaning ast.SymbolFlags, diagnostic *diagnostics.Message) *ast.Symbol {
	return c.getGlobalSymbol(name, meaning, diagnostic)
}

func (c *Checker) GetMergedSymbol(symbol *ast.Symbol) *ast.Symbol {
	return c.getMergedSymbol(symbol)
}

func (c *Checker) TryFindAmbientModule(moduleName string) *ast.Symbol {
	return c.tryFindAmbientModule(moduleName, true /* withAugmentations */)
}

func (c *Checker) GetImmediateAliasedSymbol(symbol *ast.Symbol) *ast.Symbol {
	return c.getImmediateAliasedSymbol(symbol)
}

func (c *Checker) GetTypeOnlyAliasDeclaration(symbol *ast.Symbol) *ast.Node {
	return c.getTypeOnlyAliasDeclaration(symbol)
}

func (c *Checker) ResolveExternalModuleName(moduleSpecifier *ast.Node) *ast.Symbol {
	return c.resolveExternalModuleName(moduleSpecifier, moduleSpecifier, true /*ignoreErrors*/)
}

func (c *Checker) ResolveExternalModuleSymbol(moduleSymbol *ast.Symbol) *ast.Symbol {
	return c.resolveExternalModuleSymbol(moduleSymbol, false /*dontResolveAlias*/)
}

func (c *Checker) GetTypeFromTypeNode(node *ast.Node) *Type {
	return c.getTypeFromTypeNode(node)
}

func (c *Checker) IsArrayLikeType(t *Type) bool {
	return c.isArrayLikeType(t)
}

func (c *Checker) GetPropertiesOfType(t *Type) []*ast.Symbol {
	return c.getPropertiesOfType(t)
}

func (c *Checker) GetPropertyOfType(t *Type, name string) *ast.Symbol {
	return c.getPropertyOfType(t, name)
}

func (c *Checker) TypeHasCallOrConstructSignatures(t *Type) bool {
	return c.typeHasCallOrConstructSignatures(t)
}

// Checks if a property can be accessed in a location.
// The location is given by the `node` parameter.
// The node does not need to be a property access.
// @param node location where to check property accessibility
// @param isSuper whether to consider this a `super` property access, e.g. `super.foo`.
// @param isWrite whether this is a write access, e.g. `++foo.x`.
// @param containingType type where the property comes from.
// @param property property symbol.
func (c *Checker) IsPropertyAccessible(node *ast.Node, isSuper bool, isWrite bool, containingType *Type, property *ast.Symbol) bool {
	return c.isPropertyAccessible(node, isSuper, isWrite, containingType, property)
}

func (c *Checker) GetTypeOfPropertyOfContextualType(t *Type, name string) *Type {
	return c.getTypeOfPropertyOfContextualType(t, name)
}

func GetDeclarationModifierFlagsFromSymbol(s *ast.Symbol) ast.ModifierFlags {
	return getDeclarationModifierFlagsFromSymbol(s)
}

func (c *Checker) WasCanceled() bool {
	return c.wasCanceled
}

func (c *Checker) GetSignaturesOfType(t *Type, kind SignatureKind) []*Signature {
	return c.getSignaturesOfType(t, kind)
}

func (c *Checker) GetDeclaredTypeOfSymbol(symbol *ast.Symbol) *Type {
	return c.getDeclaredTypeOfSymbol(symbol)
}

func (c *Checker) GetTypeOfSymbol(symbol *ast.Symbol) *Type {
	return c.getTypeOfSymbol(symbol)
}

func (c *Checker) GetConstraintOfTypeParameter(typeParameter *Type) *Type {
	return c.getConstraintOfTypeParameter(typeParameter)
}

func (c *Checker) GetTrueTypeOfConditionalType(t *Type) *Type {
	return c.getTrueTypeFromConditionalType(t)
}

func (c *Checker) GetFalseTypeOfConditionalType(t *Type) *Type {
	return c.getFalseTypeFromConditionalType(t)
}

func (c *Checker) GetDefaultFromTypeParameter(typeParameter *Type) *Type {
	return c.getDefaultFromTypeParameter(typeParameter)
}

func (c *Checker) GetResolutionModeOverride(node *ast.ImportAttributes, reportErrors bool) core.ResolutionMode {
	return c.getResolutionModeOverride(node, reportErrors)
}

func (c *Checker) GetEffectiveDeclarationFlags(n *ast.Node, flagsToCheck ast.ModifierFlags) ast.ModifierFlags {
	return c.getEffectiveDeclarationFlags(n, flagsToCheck)
}

func (c *Checker) GetBaseConstraintOfType(t *Type) *Type {
	return c.getBaseConstraintOfType(t)
}

func (c *Checker) GetTypePredicateOfSignature(sig *Signature) *TypePredicate {
	return c.getTypePredicateOfSignature(sig)
}

func IsTupleType(t *Type) bool {
	return isTupleType(t)
}

func (c *Checker) IsArrayType(t *Type) bool {
	return c.isArrayType(t)
}

func (c *Checker) GetReturnTypeOfSignature(sig *Signature) *Type {
	return c.getReturnTypeOfSignature(sig)
}

func (c *Checker) HasEffectiveRestParameter(signature *Signature) bool {
	return c.hasEffectiveRestParameter(signature)
}

func (c *Checker) GetLocalTypeParametersOfClassOrInterfaceOrTypeAlias(symbol *ast.Symbol) []*Type {
	return c.getLocalTypeParametersOfClassOrInterfaceOrTypeAlias(symbol)
}

func (c *Checker) GetContextualTypeForObjectLiteralElement(element *ast.Node, contextFlags ContextFlags) *Type {
	return c.getContextualTypeForObjectLiteralElement(element, contextFlags)
}

func (c *Checker) TypePredicateToString(t *TypePredicate) string {
	return c.typePredicateToString(t)
}

func (c *Checker) GetExpandedParameters(signature *Signature, skipUnionExpanding bool) [][]*ast.Symbol {
	return c.getExpandedParameters(signature, skipUnionExpanding)
}

func (c *Checker) GetResolvedSignature(node *ast.Node) *Signature {
	return c.getResolvedSignature(node, nil, CheckModeNormal)
}

// Return the type of the given property in the given type, or nil if no such property exists
func (c *Checker) GetTypeOfPropertyOfType(t *Type, name string) *Type {
	return c.getTypeOfPropertyOfType(t, name)
}

func (c *Checker) GetContextualTypeForArgumentAtIndex(node *ast.Node, argIndex int) *Type {
	return c.getContextualTypeForArgumentAtIndex(node, argIndex)
}

func (c *Checker) GetIndexSignaturesAtLocation(node *ast.Node) []*ast.Node {
	return c.getIndexSignaturesAtLocation(node)
}

func (c *Checker) GetResolvedSymbol(node *ast.Node) *ast.Symbol {
	return c.getResolvedSymbol(node)
}

func (c *Checker) GetJsxNamespace(location *ast.Node) string {
	return c.getJsxNamespace(location)
}

func (c *Checker) GetJsxFragmentFactory(location *ast.Node) string {
	entity := c.getJsxFragmentFactoryEntity(location)
	if entity != nil {
		return ast.GetFirstIdentifier(entity).Text()
	}
	return ""
}

func (c *Checker) ResolveName(name string, location *ast.Node, meaning ast.SymbolFlags, excludeGlobals bool) *ast.Symbol {
	return c.resolveName(location, name, meaning, nil, true, excludeGlobals)
}

func (c *Checker) GetSymbolFlags(symbol *ast.Symbol) ast.SymbolFlags {
	return c.getSymbolFlags(symbol)
}

func (c *Checker) GetBaseTypes(t *Type) []*Type {
	return c.getBaseTypes(t)
}

func (c *Checker) GetApparentType(t *Type) *Type {
	return c.getApparentType(t)
}

func (c *Checker) GetBaseConstructorTypeOfClass(t *Type) *Type {
	return c.getBaseConstructorTypeOfClass(t)
}

func (c *Checker) GetRestTypeOfSignature(sig *Signature) *Type {
	return c.getRestTypeOfSignature(sig)
}

func (c *Checker) GetTypeArguments(t *Type) []*Type {
	return c.getTypeArguments(t)
}

func (c *Checker) GetIndexInfoOfType(t *Type, keyType *Type) *IndexInfo {
	return c.getIndexInfoOfType(t, keyType)
}

func (c *Checker) GetIndexInfosOfType(t *Type) []*IndexInfo {
	return c.getIndexInfosOfType(t)
}

func (c *Checker) IsContextSensitive(node *ast.Node) bool {
	return c.isContextSensitive(node)
}

func (c *Checker) FillMissingTypeArguments(typeArguments []*Type, typeParameters []*Type, minTypeArgumentCount int, isJavaScriptImplicitAny bool) []*Type {
	return c.fillMissingTypeArguments(typeArguments, typeParameters, minTypeArgumentCount, isJavaScriptImplicitAny)
}

func (c *Checker) GetMinTypeArgumentCount(typeParameters []*Type) int {
	return c.getMinTypeArgumentCount(typeParameters)
}

func (c *Checker) GetWidenedLiteralType(t *Type) *Type {
	return c.getWidenedLiteralType(t)
}

func (c *Checker) IsTypeAssignableTo(source *Type, target *Type) bool {
	return c.isTypeAssignableTo(source, target)
}

func (c *Checker) GetUnionTypeEx(types []*Type, unionReduction UnionReduction) *Type {
	return c.getUnionTypeEx(types, unionReduction, nil, nil)
}

func (c *Checker) RequiresAddingImplicitUndefined(node *ast.Node) bool {
	enclosingDeclaration := ast.FindAncestor(node, ast.IsDeclaration)
	if enclosingDeclaration == nil {
		enclosingDeclaration = ast.GetSourceFileOfNode(node).AsNode()
	}
	symbol := node.Symbol()
	if symbol == nil {
		return false
	}
	return c.GetEmitResolver().RequiresAddingImplicitUndefined(node, symbol, enclosingDeclaration)
}

func (c *Checker) RemoveMissingOrUndefinedType(t *Type) *Type {
	return c.removeMissingOrUndefinedType(t)
}

func (c *Checker) GetWidenedType(t *Type) *Type {
	return c.getWidenedType(t)
}

func (c *Checker) CompareSymbols(s1, s2 *ast.Symbol) int {
	return c.compareSymbols(s1, s2)
}
