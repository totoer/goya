// Modified for Goya: internal import paths relocated by cmd/importupstream.

// Modified for Goya: embedded-language-service.

package ls

import (
	"context"
	"github.com/totoer/goya/internal/tsfrontend/upstream/stringutil"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
)

func (l *LanguageService) ProvideSelectionRanges(ctx context.Context, params *lsproto.SelectionRangeParams) (lsproto.SelectionRangeResponse, error) {
	_, sourceFile := l.getProgramAndFile(params.TextDocument.Uri)
	if sourceFile == nil {
		return lsproto.SelectionRangesOrNull{}, nil
	}

	var results []*lsproto.SelectionRange
	for _, position := range params.Positions {
		pos := l.converters.LineAndCharacterToPosition(sourceFile, position)
		selectionRange := getSmartSelectionRange(l, sourceFile, int(pos))
		if selectionRange != nil {
			results = append(results, selectionRange)
		}
	}

	return lsproto.SelectionRangesOrNull{SelectionRanges: &results}, nil
}

// Adapted from TypeScript 6.0.3 smartSelection.ts (Microsoft, Apache-2.0).
// Synthetic SyntaxLists group tokens without changing native AST parent links.
func (l *LanguageService) ProvideReferenceSelectionRanges(ctx context.Context, params *lsproto.SelectionRangeParams) (lsproto.SelectionRangeResponse, error) {
	_, file := l.getProgramAndFile(params.TextDocument.Uri)
	results := make([]*lsproto.SelectionRange, 0, len(params.Positions))
	for _, p := range params.Positions {
		if err := ctx.Err(); err != nil {
			return lsproto.SelectionRangeResponse{}, err
		}
		results = append(results, l.referenceSmartSelection(ctx, file, int(l.converters.LineAndCharacterToPosition(file, p))))
	}
	return lsproto.SelectionRangeResponse{SelectionRanges: &results}, ctx.Err()
}

func referenceSelectionList(factory *ast.NodeFactory, children []*ast.Node) *ast.Node {
	node := factory.NewSyntaxList(children)
	node.Loc = core.NewTextRange(children[0].Pos(), children[len(children)-1].End())
	return node
}
func referenceSelectionChildren(ctx context.Context, node *ast.Node, file *ast.SourceFile, factory *ast.NodeFactory) []*ast.Node {
	if ast.IsTokenKind(node.Kind) {
		return nil
	}
	var children []*ast.Node
	last := node.Pos()
	scanGap := func(end int) {
		scan := scanner.GetScannerForSourceFile(file, last)
		for last < end {
			if ctx.Err() != nil {
				return
			}
			kind := scan.Token()
			if kind == ast.KindLessThanLessThanToken && ast.IsJsxChild(node) {
				kind = scan.ReScanJsxToken(true)
			}
			next := scan.TokenEnd()
			if kind == ast.KindEndOfFile || next <= last || next > end {
				break
			}
			text := file.Text()[scan.TokenFullStart():next]
			flags := scan.TokenFlags()
			var token *ast.Node
			switch kind {
			case ast.KindIdentifier:
				token = factory.NewIdentifier(text)
			case ast.KindPrivateIdentifier:
				token = factory.NewPrivateIdentifier(text)
			case ast.KindNumericLiteral:
				token = factory.NewNumericLiteral(text, flags)
			case ast.KindBigIntLiteral:
				token = factory.NewBigIntLiteral(text, flags)
			case ast.KindStringLiteral:
				token = factory.NewStringLiteral(text, flags)
			case ast.KindRegularExpressionLiteral:
				token = factory.NewRegularExpressionLiteral(text, flags)
			case ast.KindNoSubstitutionTemplateLiteral:
				token = factory.NewNoSubstitutionTemplateLiteral(text, flags)
			case ast.KindTemplateHead:
				token = factory.NewTemplateHead(text, "", flags)
			case ast.KindTemplateMiddle:
				token = factory.NewTemplateMiddle(text, "", flags)
			case ast.KindTemplateTail:
				token = factory.NewTemplateTail(text, "", flags)
			case ast.KindJsxText, ast.KindJsxTextAllWhiteSpaces:
				token = factory.NewJsxText(text, kind == ast.KindJsxTextAllWhiteSpaces)
			default:
				token = factory.NewToken(kind)
			}
			token.Loc = core.NewTextRange(scan.TokenFullStart(), next)
			token.Parent = node
			// Tokens with synthetic parents must never enter SourceFile.tokenCache.
			children = append(children, token)
			last = next
			scan.Scan()
		}
	}
	add := func(child *ast.Node) {
		if ctx.Err() != nil || child == nil || child.Flags&ast.NodeFlagsReparsed != 0 {
			return
		}
		scanGap(child.Pos())
		children = append(children, child)
		last = child.End()
	}
	if node.Kind == ast.KindSyntaxList {
		for _, child := range node.AsSyntaxList().Children {
			add(child)
		}
	} else {
		astnav.VisitEachChildAndJSDoc(node, file, func(n *ast.Node, _ *ast.NodeVisitor) *ast.Node { add(n); return n }, func(nodes *ast.NodeList, _ *ast.NodeVisitor) *ast.NodeList {
			if nodes != nil {
				list := factory.NewSyntaxList(nodes.Nodes)
				list.Loc = nodes.Loc
				add(list)
			}
			return nodes
		})
	}
	scanGap(node.End())
	group := func(input []*ast.Node, predicate func(*ast.Node) bool) []*ast.Node {
		var result, part []*ast.Node
		flush := func() {
			if len(part) > 0 {
				result = append(result, referenceSelectionList(factory, part))
				part = nil
			}
		}
		for _, n := range input {
			if predicate(n) {
				part = append(part, n)
			} else {
				flush()
				result = append(result, n)
			}
		}
		flush()
		return result
	}
	split := func(input []*ast.Node, kind ast.Kind) []*ast.Node {
		if len(input) < 2 {
			return input
		}
		index := -1
		for i, n := range input {
			if n.Kind == kind {
				index = i
				break
			}
		}
		if index < 0 {
			return input
		}
		var result []*ast.Node
		if index > 0 {
			result = append(result, referenceSelectionList(factory, input[:index]))
		}
		result = append(result, input[index])
		end := len(input)
		semicolon := input[end-1].Kind == ast.KindSemicolonToken
		if semicolon {
			end--
		}
		if index+1 < end {
			result = append(result, referenceSelectionList(factory, input[index+1:end]))
		}
		if semicolon {
			result = append(result, input[len(input)-1])
		}
		return result
	}
	switch node.Kind {
	case ast.KindSourceFile:
		for _, n := range children {
			if n.Kind == ast.KindSyntaxList {
				return group(referenceSelectionChildren(ctx, n, file, factory), func(n *ast.Node) bool { return ast.IsImportDeclaration(n) || ast.IsImportEqualsDeclaration(n) })
			}
		}
		return nil
	case ast.KindMappedType:
		if len(children) < 2 {
			return children
		}
		open, close := children[0], children[len(children)-1]
		middle := children[1 : len(children)-1]
		m := node.AsMappedTypeNode()
		middle = group(middle, func(n *ast.Node) bool {
			return n == m.ReadonlyToken || n.Kind == ast.KindReadonlyKeyword || n == m.QuestionToken || n.Kind == ast.KindQuestionToken
		})
		middle = group(middle, func(n *ast.Node) bool {
			return n.Kind == ast.KindOpenBracketToken || n.Kind == ast.KindTypeParameter || n.Kind == ast.KindCloseBracketToken
		})
		middle = split(middle, ast.KindColonToken)
		if len(middle) == 0 {
			return children
		}
		return []*ast.Node{open, referenceSelectionList(factory, middle), close}
	case ast.KindPropertySignature:
		grouped := group(children, func(n *ast.Node) bool { return n == node.Name() || ast.IsModifierKind(n.Kind) })
		if len(grouped) > 0 && grouped[0].Kind == ast.KindJSDoc {
			first := grouped[0]
			rest := split(grouped[1:], ast.KindColonToken)
			if len(rest) > 0 {
				return []*ast.Node{first, referenceSelectionList(factory, rest)}
			}
			return grouped
		}
		return split(grouped, ast.KindColonToken)
	case ast.KindParameter:
		p := node.AsParameterDeclaration()
		grouped := group(children, func(n *ast.Node) bool { return n == p.DotDotDotToken || n == node.Name() })
		if len(grouped) > 0 {
			first := grouped[0]
			grouped = group(grouped, func(n *ast.Node) bool { return n == first || n == p.QuestionToken })
		}
		return split(grouped, ast.KindEqualsToken)
	case ast.KindBindingElement:
		return split(children, ast.KindEqualsToken)
	}
	return children
}

func (l *LanguageService) referenceSmartSelection(ctx context.Context, file *ast.SourceFile, pos int) *lsproto.SelectionRange {
	factory := &ast.NodeFactory{}
	protocolPosition := func(offset int) lsproto.Position {
		return l.converters.PositionToLineAndCharacter(file, core.TextPos(offset))
	}
	toRange := func(start, end int) lsproto.Range {
		return lsproto.Range{Start: protocolPosition(start), End: protocolPosition(end)}
	}
	result := &lsproto.SelectionRange{Range: toRange(file.Pos(), file.End())}
	push := func(start, end int) {
		if start == end || start > pos || pos > end {
			return
		}
		r := toRange(start, end)
		if result.Range != r {
			result = &lsproto.SelectionRange{Range: r, Parent: result}
		}
	}
	commentRange := func(start, end int) {
		push(start, end)
		for start < end && file.Text()[start] == '/' {
			start++
		}
		push(start, end)
	}
	sameLine := func(a, b int) bool {
		return l.converters.PositionToLineAndCharacter(file, core.TextPos(a)).Line == l.converters.PositionToLineAndCharacter(file, core.TextPos(b)).Line
	}
	opener := func(n *ast.Node) bool {
		return n != nil && (n.Kind == ast.KindOpenBraceToken || n.Kind == ast.KindOpenBracketToken || n.Kind == ast.KindOpenParenToken || n.Kind == ast.KindJsxOpeningElement)
	}
	closer := func(n *ast.Node) bool {
		return n != nil && (n.Kind == ast.KindCloseBraceToken || n.Kind == ast.KindCloseBracketToken || n.Kind == ast.KindCloseParenToken || n.Kind == ast.KindJsxClosingElement)
	}
	parent := file.AsNode()
outer:
	for {
		if ctx.Err() != nil {
			break
		}
		children := referenceSelectionChildren(ctx, parent, file, factory)
		if len(children) == 0 {
			break
		}
		for i, node := range children {
			var previous, next *ast.Node
			if i > 0 {
				previous = children[i-1]
			}
			if i+1 < len(children) {
				next = children[i+1]
			}
			if scanner.GetTokenPosOfNode(node, file, true) > pos {
				break outer
			}
			var comment *ast.CommentRange
			count := 0
			for c := range scanner.GetTrailingCommentRanges(factory, file.Text(), node.End()) {
				copy := c
				comment = &copy
				count++
			}
			if count == 1 && comment.Kind == ast.KindSingleLineCommentTrivia {
				commentRange(comment.Pos(), comment.End())
			}
			snap := pos < node.End()
			if node.End() == pos {
				snap = astnav.GetTouchingPropertyName(file, pos).Pos() < node.End()
			}
			if snap {
				start := astnav.GetStartOfNode(node, file, false)
				end := node.End()
				if ast.IsBlock(node) && ast.IsFunctionLikeDeclaration(parent) && !sameLine(start, end) {
					push(start, end)
				}
				skip := ast.IsBlock(node) || ast.IsTemplateSpan(node) || ast.IsTemplateHead(node) || ast.IsTemplateTail(node) || previous != nil && ast.IsTemplateHead(previous) || ast.IsVariableDeclarationList(node) && ast.IsVariableStatement(parent) || node.Kind == ast.KindSyntaxList && ast.IsVariableDeclarationList(parent) || ast.IsVariableDeclaration(node) && parent.Kind == ast.KindSyntaxList && len(children) == 1 || ast.IsJSDocTypeExpression(node) || ast.IsJSDocSignature(node) || ast.IsJSDocTypeLiteral(node)
				if skip {
					parent = node
					continue outer
				}
				if ast.IsTemplateSpan(parent) && next != nil && (ast.IsTemplateMiddle(next) || ast.IsTemplateTail(next)) {
					push(node.Pos()-2, astnav.GetStartOfNode(next, file, false)+1)
				}
				if node.Kind == ast.KindSyntaxList && opener(previous) && closer(next) && !sameLine(astnav.GetStartOfNode(previous, file, false), astnav.GetStartOfNode(next, file, false)) {
					start = previous.End()
					end = astnav.GetStartOfNode(next, file, false)
				}
				switch node.Kind {
				case ast.KindJSDocParameterTag, ast.KindJSDocCallbackTag, ast.KindJSDocPropertyTag, ast.KindJSDocTypedefTag, ast.KindJSDocThisTag:
					end = start
					for end < len(file.Text()) && !stringutil.IsLineBreak(rune(file.Text()[end])) {
						end++
					}
				}
				docs := node.JSDoc(file)
				if len(docs) > 0 {
					push(astnav.GetStartOfNode(docs[0], file, false), end)
				}
				if node.Kind == ast.KindSyntaxList && len(node.AsSyntaxList().Children) > 0 {
					first := node.AsSyntaxList().Children[0]
					docs := first.JSDoc(file)
					if len(docs) > 0 && astnav.GetStartOfNode(first, file, false) != node.Pos() {
						start = min(start, astnav.GetStartOfNode(docs[0], file, false))
					}
				}
				push(start, end)
				if ast.IsStringLiteral(node) || ast.IsTemplateExpression(node) || ast.IsNoSubstitutionTemplateLiteral(node) {
					push(start+1, end-1)
				}
				parent = node
				continue outer
			}
			if i == len(children)-1 {
				break outer
			}
		}
	}
	return result
}

func getSmartSelectionRange(l *LanguageService, sourceFile *ast.SourceFile, pos int) *lsproto.SelectionRange {
	factory := &ast.NodeFactory{}

	nodeContainsPosition := func(node *ast.Node) bool {
		if node == nil {
			return false
		}
		start := scanner.GetTokenPosOfNode(node, sourceFile, true /*includeJSDoc*/)
		end := node.End()
		return start <= pos && pos < end
	}

	pushSelectionRange := func(current *lsproto.SelectionRange, start, end int) *lsproto.SelectionRange {
		if start == end {
			return current
		}

		if !(start <= pos && pos <= end) {
			return current
		}

		lspRange := l.converters.ToLSPRange(sourceFile, core.NewTextRange(start, end))

		if current != nil && current.Range == lspRange {
			return current
		}

		return &lsproto.SelectionRange{
			Range:  lspRange,
			Parent: current,
		}
	}

	pushSelectionCommentRange := func(current *lsproto.SelectionRange, start, end int) *lsproto.SelectionRange {
		current = pushSelectionRange(current, start, end)

		commentPos := start
		text := sourceFile.Text()
		for commentPos < end && commentPos < len(text) && text[commentPos] == '/' {
			commentPos++
		}
		current = pushSelectionRange(current, commentPos, end)

		return current
	}

	positionsAreOnSameLine := func(pos1, pos2 int) bool {
		if pos1 == pos2 {
			return true
		}
		lspPos1 := l.converters.PositionToLineAndCharacter(sourceFile, core.TextPos(pos1))
		lspPos2 := l.converters.PositionToLineAndCharacter(sourceFile, core.TextPos(pos2))
		return lspPos1.Line == lspPos2.Line
	}

	shouldSkipNode := func(node *ast.Node, parent *ast.Node) bool {
		if ast.IsBlock(node) {
			return true
		}

		if ast.IsTemplateSpan(node) || ast.IsTemplateHead(node) || ast.IsTemplateTail(node) {
			return true
		}

		if parent != nil && ast.IsVariableDeclarationList(node) && ast.IsVariableStatement(parent) {
			return true
		}

		// Skip lone variable declarations
		if parent != nil && ast.IsVariableDeclaration(node) && ast.IsVariableDeclarationList(parent) {
			decl := parent.AsVariableDeclarationList()
			if decl != nil && len(decl.Declarations.Nodes) == 1 {
				return true
			}
		}

		if ast.IsJSDocTypeExpression(node) || ast.IsJSDocSignature(node) || ast.IsJSDocTypeLiteral(node) {
			return true
		}

		return false
	}

	fullRange := l.converters.ToLSPRange(sourceFile, core.NewTextRange(sourceFile.Pos(), sourceFile.End()))
	result := &lsproto.SelectionRange{
		Range: fullRange,
	}

	var current *ast.Node
	for current = sourceFile.AsNode(); current != nil; {
		var next *ast.Node
		parent := current

		visit := func(node *ast.Node) *ast.Node {
			if node != nil && next == nil {
				var foundComment *ast.CommentRange
				for comment := range scanner.GetTrailingCommentRanges(factory, sourceFile.Text(), node.End()) {
					foundComment = &comment
					break
				}
				if foundComment != nil && foundComment.Kind == ast.KindSingleLineCommentTrivia {
					result = pushSelectionCommentRange(result, foundComment.Pos(), foundComment.End())
				}

				if nodeContainsPosition(node) {
					// Add range for multi-line function bodies before skipping the block
					if ast.IsBlock(node) && ast.IsFunctionLikeDeclaration(parent) {
						if !positionsAreOnSameLine(astnav.GetStartOfNode(node, sourceFile, false), node.End()) {
							start := astnav.GetStartOfNode(node, sourceFile, false)
							end := node.End()
							result = pushSelectionRange(result, start, end)
						}
					}

					// Synthesize a stop for '${ ... }' since '${' and '}' actually belong to siblings.
					if ast.IsTemplateSpan(parent) {
						templateSpan := parent.AsTemplateSpan()
						if templateSpan.Literal != nil {
							// Start from just before the '${' and end after the '}'
							// The '${' is 2 characters before the expression start
							spanStart := node.Pos() - 2
							// The '}' is the first character of the template literal (middle or tail)
							spanEnd := astnav.GetStartOfNode(templateSpan.Literal, sourceFile, false) + 1
							// Validate the positions are reasonable
							text := sourceFile.Text()
							if spanStart >= 0 && spanEnd <= len(text) && spanStart < spanEnd {
								result = pushSelectionRange(result, spanStart, spanEnd)
							}
						}
					}

					if !shouldSkipNode(node, parent) {
						start := astnav.GetStartOfNode(node, sourceFile, false)
						end := node.End()
						result = pushSelectionRange(result, start, end)

						// String literals should have a stop both inside and outside their quotes.
						if ast.IsStringLiteral(node) || node.Kind == ast.KindTemplateExpression || node.Kind == ast.KindNoSubstitutionTemplateLiteral {
							// Only add inner content range if there's actually content (handles unterminated literals)
							if start+1 < end-1 {
								result = pushSelectionRange(result, start+1, end-1)
							}
						}
					}

					next = node
				}
			}
			return node
		}

		visitNodes := func(nodes *ast.NodeList, v *ast.NodeVisitor) *ast.NodeList {
			if nodes != nil && len(nodes.Nodes) > 0 {
				shouldSkipList := parent != nil && (ast.IsVariableDeclarationList(parent) || ast.IsTemplateExpression(parent))

				if !shouldSkipList {
					start := astnav.GetStartOfNode(nodes.Nodes[0], sourceFile, false)
					end := nodes.Nodes[len(nodes.Nodes)-1].End()

					if start <= pos && pos < end {
						result = pushSelectionRange(result, start, end)
					}
				}
			}
			return v.VisitNodes(nodes)
		}

		// Visit JSDoc nodes first if they exist
		for _, jsdoc := range current.JSDoc(sourceFile) {
			visit(jsdoc)
		}

		tempVisitor := ast.NewNodeVisitor(visit, nil, ast.NodeVisitorHooks{
			VisitNodes: visitNodes,
		})

		current.VisitEachChild(tempVisitor)
		current = next
	}
	return result
}
