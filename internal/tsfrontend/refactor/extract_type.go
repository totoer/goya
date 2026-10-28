package refactor

import (
	"fmt"
	"slices"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/printer"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"github.com/totoer/goya/lsp/protocol"
)

const extractTypeName = "Extract type"
const extractAliasName = "Extract to type alias"
const extractInterfaceName = "Extract to interface"
const extractTypedefName = "Extract to typedef"

var extractTypeProvider = provider{name: extractTypeName, kinds: []string{"refactor.extract.type", "refactor.extract.interface", "refactor.extract.typedef"}, available: availableExtractType, edits: editExtractType}

type extractTypeInfo struct {
	selection        []*ast.Node
	multiple         bool
	enclosing        *ast.Node
	parameters       []*ast.Node
	members          []*ast.Node // nil means interface extraction is unavailable; empty means an empty interface.
	singleLineTuples []*ast.Node
	affected         core.TextRange
}

func tokenStart(r *request, node *ast.Node) int {
	return scanner.GetTokenPosOfNode(node, r.file, false)
}
func containsText(r *request, outer, inner core.TextRange) bool {
	return outer.Pos() <= scanner.SkipTrivia(r.file.Text(), inner.Pos()) && inner.End() <= outer.End()
}
func containsNode(r *request, outer, inner *ast.Node) bool {
	return containsText(r, outer.Loc, inner.Loc)
}
func overlapsSpan(r *request, node *ast.Node) bool {
	return tokenStart(r, node) < r.span.End() && r.span.Pos() < node.End()
}
func ancestor(node *ast.Node, accept func(*ast.Node) bool) *ast.Node {
	for ; node != nil; node = node.Parent {
		if accept(node) {
			return node
		}
	}
	return nil
}
func firstExtractType(r *request, considerEmpty bool) *ast.Node {
	for _, current := range []*ast.Node{astnav.GetTokenAtPosition(r.file, r.span.Pos()), astnav.GetReferenceTouchingToken(r.file, r.span.Pos())} {
		overlap := overlapsSpan(r, current)
		for node := current; node != nil; node = node.Parent {
			if node.Parent != nil && ast.IsTypeNode(node) && !containsText(r, r.span, node.Parent.Loc) && (r.span.Pos() == r.span.End() && considerEmpty || overlap) {
				return node
			}
		}
	}
	return nil
}
func extractRange(r *request, considerEmpty bool) (*extractTypeInfo, string) {
	language := locale.FromContext(r.ctx)
	first := firstExtractType(r, considerEmpty)
	if first == nil || !ast.IsTypeNode(first) {
		return nil, diagnostics.Selection_is_not_a_valid_type_node.Localize(language)
	}
	enclosing := ancestor(first, ast.IsStatement)
	if enclosing == nil {
		return nil, diagnostics.No_type_could_be_extracted_from_this_type_node.Localize(language)
	}
	expanded := first
	for node := first; node != nil && node != enclosing; node = node.Parent {
		if node.Parent != nil && (ast.IsUnionTypeNode(node.Parent) || ast.IsIntersectionTypeNode(node.Parent)) {
			expanded = node
			break
		}
	}
	if !ast.IsTypeNode(expanded) {
		return nil, diagnostics.Selection_is_not_a_valid_type_node.Localize(language)
	}
	selected := []*ast.Node{expanded}
	if expanded.Parent != nil && (ast.IsUnionTypeNode(expanded.Parent) || ast.IsIntersectionTypeNode(expanded.Parent)) && r.span.End() > first.End() {
		list := []*ast.Node{}
		for _, node := range unionIntersectionParts(expanded.Parent) {
			if overlapsSpan(r, node) {
				list = append(list, node)
			}
		}
		if len(list) > 1 {
			selected = list
		}
	}
	info := &extractTypeInfo{selection: selected, multiple: len(selected) > 1, enclosing: enclosing, affected: core.NewTextRange(tokenStart(r, selected[0]), selected[len(selected)-1].End())}
	var visit func(*ast.Node) bool
	visit = func(node *ast.Node) bool {
		switch node.Kind {
		case ast.KindTypeReference:
			name := node.AsTypeReferenceNode().TypeName
			if ast.IsIdentifier(name) {
				symbol := r.types().ResolveName(name.Text(), name, ast.SymbolFlagsTypeParameter, true)
				if symbol != nil {
					for _, decl := range symbol.Declarations {
						if ast.IsTypeParameterDeclaration(decl) && ast.GetSourceFileOfNode(decl) == r.file {
							if decl.Name().Text() == name.Text() && containsText(r, decl.Loc, info.affected) {
								return true
							}
							if containsNode(r, enclosing, decl) && !containsText(r, info.affected, decl.Loc) {
								if !slices.Contains(info.parameters, decl) {
									info.parameters = append(info.parameters, decl)
								}
								break
							}
						}
					}
				}
			}
		case ast.KindInferType:
			conditional := ancestor(node, func(n *ast.Node) bool {
				return ast.IsConditionalTypeNode(n) && containsNode(r, n.AsConditionalTypeNode().ExtendsType, node)
			})
			if conditional == nil || !containsText(r, info.affected, conditional.Loc) {
				return true
			}
		case ast.KindTypePredicate, ast.KindThisType:
			fn := ancestor(node.Parent, ast.IsFunctionLike)
			if fn != nil && fn.Type() != nil && containsNode(r, fn.Type(), node) && !containsText(r, info.affected, fn.Loc) {
				return true
			}
		case ast.KindTypeQuery:
			name := node.AsTypeQueryNode().ExprName
			if ast.IsIdentifier(name) {
				symbol := r.types().ResolveName(name.Text(), name, ast.SymbolFlagsValue, false)
				if symbol != nil && symbol.ValueDeclaration != nil && containsNode(r, enclosing, symbol.ValueDeclaration) && !containsText(r, info.affected, symbol.ValueDeclaration.Loc) {
					return true
				}
			} else if ast.IsQualifiedName(name) && ast.IsThisIdentifier(name.AsQualifiedName().Left) && !containsText(r, info.affected, node.Parent.Loc) {
				return true
			}
		}
		if ast.IsTupleTypeNode(node) && scanner.GetECMALineOfPosition(r.file, node.Pos()) == scanner.GetECMALineOfPosition(r.file, node.End()) {
			info.singleLineTuples = append(info.singleLineTuples, node)
		}
		return node.ForEachChild(visit)
	}
	for _, node := range selected {
		if visit(node) {
			return nil, diagnostics.No_type_could_be_extracted_from_this_type_node.Localize(language)
		}
	}
	if info.multiple {
		info.members = []*ast.Node{}
		for _, node := range selected {
			members := flattenTypeMembers(node)
			if members == nil {
				info.members = nil
				break
			}
			info.members = append(info.members, members...)
		}
	} else {
		info.members = flattenTypeMembers(selected[0])
	}
	return info, ""
}
func flattenTypeMembers(node *ast.Node) []*ast.Node {
	switch node.Kind {
	case ast.KindTypeLiteral:
		return append([]*ast.Node{}, node.Members()...)
	case ast.KindParenthesizedType:
		return flattenTypeMembers(node.Type())
	case ast.KindIntersectionType:
		members := []*ast.Node{}
		seen := map[string]bool{}
		for _, part := range unionIntersectionParts(node) {
			items := flattenTypeMembers(part)
			if items == nil {
				return nil
			}
			for _, member := range items {
				if member.Name() == nil {
					return nil
				}
				name := referencePropertyName(member.Name())
				if seen[name] {
					return nil
				}
				seen[name] = true
			}
			members = append(members, items...)
		}
		return members
	}
	return nil
}
func extractTypeAction(r *request, name string) protocol.RefactorActionInfo {
	action := protocol.RefactorActionInfo{Name: name}
	switch name {
	case extractAliasName:
		action.Description = diagnostics.Extract_to_type_alias.Localize(locale.FromContext(r.ctx))
		action.Kind = "refactor.extract.type"
	case extractInterfaceName:
		action.Description = diagnostics.Extract_to_interface.Localize(locale.FromContext(r.ctx))
		action.Kind = "refactor.extract.interface"
	case extractTypedefName:
		action.Description = diagnostics.Extract_to_typedef.Localize(locale.FromContext(r.ctx))
		action.Kind = "refactor.extract.typedef"
	}
	return action
}
func availableExtractType(r *request) []protocol.ApplicableRefactorInfo {
	info, reason := extractRange(r, r.options.Invoked)
	group := protocol.ApplicableRefactorInfo{Name: extractTypeName, Description: diagnostics.Extract_type.Localize(locale.FromContext(r.ctx)), Actions: []protocol.RefactorActionInfo{}}
	if info == nil {
		if !r.preferences.ProvideRefactorNotApplicableReason.IsTrue() {
			return nil
		}
		// The reference presents all three disabled descriptions even in TypeScript.
		for _, name := range []string{extractTypedefName, extractAliasName, extractInterfaceName} {
			a := extractTypeAction(r, name)
			a.NotApplicableReason = reason
			group.Actions = append(group.Actions, a)
		}
	} else {
		names := []string{extractAliasName}
		if info.members != nil {
			names = append(names, extractInterfaceName)
		}
		span := r.converters.ToLSPRange(r.file, info.affected)
		for _, name := range names {
			a := extractTypeAction(r, name)
			a.Range = &protocol.Range{Start: protocol.Position(span.Start), End: protocol.Position(span.End)}
			group.Actions = append(group.Actions, a)
		}
	}
	return []protocol.ApplicableRefactorInfo{group}
}
func uniqueName(base string, file *ast.SourceFile) string {
	name := base
	for i := 1; !printer.IsFileLevelUniqueName(file, name, nil); i++ {
		name = fmt.Sprintf("%s_%d", base, i)
	}
	return name
}
func editExtractType(r *request, action string, _ Arguments) (*EditInfo, error) {
	info, reason := extractRange(r, true)
	if info == nil {
		return nil, &change.InvalidEdits{Reason: "type extraction is not applicable: " + reason}
	}
	t := r.tracker()
	name := uniqueName("NewType", r.file)
	for _, tuple := range info.singleLineTuples {
		t.AddEmitFlags(tuple, printer.EFSingleLine)
	}
	arguments := []*ast.Node{}
	parameters := []*ast.Node{}
	for _, node := range info.parameters {
		p := node.AsTypeParameterDeclaration()
		parameters = append(parameters, t.UpdateTypeParameterDeclaration(p, node.Modifiers(), node.Name(), p.Constraint, p.Expression, nil))
		arguments = append(arguments, t.NewTypeReferenceNode(node.Name(), nil))
	}
	first, last := info.selection[0], info.selection[len(info.selection)-1]
	body := first
	if info.multiple {
		if ast.IsUnionTypeNode(first.Parent) {
			body = t.NewUnionTypeNode(t.NewNodeList(info.selection))
		} else {
			body = t.NewIntersectionTypeNode(t.NewNodeList(info.selection))
		}
	}
	var declaration *ast.Node
	switch action {
	case extractAliasName:
		declaration = t.NewTypeAliasDeclaration(nil, t.NewIdentifier(name), t.NewNodeList(parameters), body)
	case extractInterfaceName:
		if info.members == nil {
			return nil, &change.InvalidEdits{Reason: "type extraction has no interface members"}
		}
		declaration = t.NewInterfaceDeclaration(nil, t.NewIdentifier(name), t.NewNodeList(info.parameters), nil, t.NewNodeList(info.members))
		if len(info.members) > 0 {
			declaration.Loc = info.members[0].Parent.Loc
		}
	default:
		return nil, &change.InvalidEdits{Reason: "invalid TypeScript type extraction action"}
	}
	t.AddEmitFlags(declaration, printer.EFReferenceIgnoreSourceNewlines)
	t.InsertNodeBefore(r.file, info.enclosing, declaration, true, change.LeadingTriviaOptionNone)
	reference := t.NewTypeReferenceNode(t.NewIdentifier(name), t.NewNodeList(arguments))
	span := t.GetAdjustedRange(r.file, first, last, change.LeadingTriviaOptionExclude, change.TrailingTriviaOptionExcludeWhitespace)
	t.ReplaceRange(r.file, span, reference, change.NodeOptions{LeadingTriviaOption: change.LeadingTriviaOptionExclude, TrailingTriviaOption: change.TrailingTriviaOptionExcludeWhitespace})
	result := finish(t)
	if err := setRenameLocation(r, result, r.file.FileName(), name, false); err != nil {
		return nil, err
	}
	return result, r.ctx.Err()
}

// Positions are byte offsets internally; all edit ranges remain stable UTF-16.
// The source scans changes in their emitted order and stops at the first name.
func setRenameLocation(r *request, result *EditInfo, file, name string, preferLast bool) error {
	name = printer.EscapeString(name, printer.QuoteCharDoubleQuote)
	delta, last := 0, -1
	for _, group := range result.Changes {
		if group.FileName != file {
			return &change.InvalidEdits{Reason: "rename location spans multiple files"}
		}
		for _, edit := range group.TextChanges {
			start := int(r.converters.LineAndCharacterToPosition(r.file, edit.Range.Start))
			end := int(r.converters.LineAndCharacterToPosition(r.file, edit.Range.End))
			index := -1
			if strings.HasPrefix(edit.NewText, name) {
				index = 0
			} else {
				for _, prefix := range []string{" ", ".", "\""} {
					if i := strings.Index(edit.NewText, prefix+name); i >= 0 {
						index = i + 1
						break
					}
				}
			}
			if index >= 0 {
				last = start + delta + index
				if !preferLast {
					result.RenameFilename = file
					result.RenamePosition = &last
					return nil
				}
			}
			delta += len(edit.NewText) - (end - start)
		}
	}
	if preferLast && last >= 0 {
		result.RenameFilename = file
		result.RenamePosition = &last
		return nil
	}
	return &change.InvalidEdits{Reason: "refactor rename location was not emitted"}
}

// TypeScript treats all nonliteral computed names as the same undefined key.
func referencePropertyName(node *ast.Node) string {
	if ast.IsComputedPropertyName(node) {
		expression := node.Expression()
		if ast.IsStringOrNumericLiteralLike(expression) {
			return "literal:" + expression.Text()
		}
		return "undefined"
	}
	return "literal:" + node.Text()
}

func unionIntersectionParts(node *ast.Node) []*ast.Node {
	if ast.IsUnionTypeNode(node) {
		return node.AsUnionTypeNode().Types.Nodes
	}
	return node.AsIntersectionTypeNode().Types.Nodes
}
