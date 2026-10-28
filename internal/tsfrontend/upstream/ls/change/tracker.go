// Modified for Goya: internal import paths relocated by cmd/importupstream.

// Modified for Goya: embedded-language-service.

package change

import (
	"context"
	"slices"
	"unicode/utf8"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"github.com/totoer/goya/internal/tsfrontend/upstream/collections"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/format"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsconv"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsutil"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/internal/tsfrontend/upstream/printer"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"github.com/totoer/goya/internal/tsfrontend/upstream/stringutil"
)

// InvalidEdits identifies a rejected proposal, never a partial applied edit.
type InvalidEdits struct{ Reason string }

func (e *InvalidEdits) Error() string { return e.Reason }

type NodeOptions struct {
	// Text to be inserted before the new node
	Prefix string

	// Text to be inserted after the new node
	Suffix string

	// Text of inserted node will be formatted with this indentation, otherwise indentation will be inferred from the old node
	indentation *int

	// Text of inserted node will be formatted with this delta, otherwise delta will be inferred from the new node kind
	delta *int

	LeadingTriviaOption
	TrailingTriviaOption
	joiner string
}

type LeadingTriviaOption int

const (
	LeadingTriviaOptionNone       LeadingTriviaOption = 0
	LeadingTriviaOptionExclude    LeadingTriviaOption = 1
	LeadingTriviaOptionIncludeAll LeadingTriviaOption = 2
	LeadingTriviaOptionJSDoc      LeadingTriviaOption = 3
	LeadingTriviaOptionStartLine  LeadingTriviaOption = 4
)

type TrailingTriviaOption int

const (
	TrailingTriviaOptionNone              TrailingTriviaOption = 0
	TrailingTriviaOptionExclude           TrailingTriviaOption = 1
	TrailingTriviaOptionExcludeWhitespace TrailingTriviaOption = 2
	TrailingTriviaOptionInclude           TrailingTriviaOption = 3
)

type trackerEditKind int

const (
	trackerEditKindText                     trackerEditKind = 1
	trackerEditKindRemove                   trackerEditKind = 2
	trackerEditKindReplaceWithSingleNode    trackerEditKind = 3
	trackerEditKindReplaceWithMultipleNodes trackerEditKind = 4
)

type trackerEdit struct {
	kind trackerEditKind
	lsproto.Range

	NewText string // kind == text

	*ast.Node             // single
	nodes     []*ast.Node // multiple
	options   NodeOptions
}

type nodesInsertedAtStartState struct {
	node       *ast.Node
	sourceFile *ast.SourceFile
}

type Tracker struct {
	ReferenceDeletionTrivia bool
	ReferenceSourceContexts bool
	referenceSourceOrder    []*ast.SourceFile
	referenceSourceSeen     map[*ast.SourceFile]bool
	// initialized with
	formatSettings lsutil.FormatCodeSettings
	newLine        string
	converters     *lsconv.Converters
	ctx            context.Context
	*printer.EmitContext

	*ast.NodeFactory

	changes                    *collections.MultiMap[*ast.SourceFile, *trackerEdit]
	deletedNodes               []deletedNode
	nodesWithInsertionsAtStart map[*ast.Node]*nodesInsertedAtStartState

	// created during call to getChanges
	writer *printer.ChangeTrackerWriter
	// printer
}

type deletedNode struct {
	sourceFile *ast.SourceFile
	node       *ast.Node
}

func NewTracker(ctx context.Context, compilerOptions *core.CompilerOptions, formatOptions lsutil.FormatCodeSettings, converters *lsconv.Converters) *Tracker {
	emitContext := printer.NewEmitContext()
	newLine := compilerOptions.NewLine.GetNewLineCharacter()
	if formatOptions.NewLineCharacter != "" {
		newLine = formatOptions.NewLineCharacter
	}
	ctx = format.WithFormatCodeSettings(ctx, formatOptions, newLine) // !!! formatSettings in context?
	return &Tracker{
		EmitContext:                emitContext,
		NodeFactory:                &emitContext.Factory.NodeFactory,
		changes:                    &collections.MultiMap[*ast.SourceFile, *trackerEdit]{},
		ctx:                        ctx,
		converters:                 converters,
		formatSettings:             formatOptions,
		newLine:                    newLine,
		nodesWithInsertionsAtStart: make(map[*ast.Node]*nodesInsertedAtStartState),
	}
}

type ReferenceFileChanges struct {
	FileName    string
	TextChanges []*lsproto.TextEdit
}

func (t *Tracker) referenceRecordSource(file *ast.SourceFile) {
	if t.referenceSourceSeen == nil {
		t.referenceSourceSeen = map[*ast.SourceFile]bool{}
	}
	if !t.referenceSourceSeen[file] {
		t.referenceSourceSeen[file] = true
		t.referenceSourceOrder = append(t.referenceSourceOrder, file)
	}
}

// GetReferenceOrderedChanges preserves first mutation order across files.
// As with GetChanges, discard the tracker after consuming this result.
func (t *Tracker) GetReferenceOrderedChanges() []ReferenceFileChanges {
	changes := t.GetChanges()
	result := []ReferenceFileChanges{}
	for _, file := range t.referenceSourceOrder {
		if edits := changes[file.FileName()]; len(edits) > 0 {
			result = append(result, ReferenceFileChanges{FileName: file.FileName(), TextChanges: edits})
		}
	}
	return result
}

// InsertReferenceStatementAtEnd follows TypeScript's insertion-before-EOF
// trivia policy, retaining trailing comments and the editor newline setting.
func (t *Tracker) InsertReferenceStatementAtEnd(file *ast.SourceFile, node *ast.Node) {
	end := file.EndOfFileToken
	pos := t.getAdjustedStartPosition(file, end, LeadingTriviaOptionNone, false)
	prefix := t.newLine + t.newLine
	text := file.Text()
	if end.Pos() < len(text) {
		character, _ := utf8.DecodeRuneInString(text[end.Pos():])
		if stringutil.IsLineBreak(character) {
			prefix = t.newLine
		}
	}
	t.InsertNodeAt(file, core.TextPos(pos), node, NodeOptions{Prefix: prefix, Suffix: t.newLine})
}
func (t *Tracker) InsertReferenceNodeAtConstructorStart(file *ast.SourceFile, constructor, newStatement *ast.Node) {
	body := constructor.Body().AsBlock()
	if body.Statements == nil || len(body.Statements.Nodes) == 0 || !body.MultiLine {
		statements := []*ast.Node{newStatement}
		if body.Statements != nil {
			statements = append(statements, body.Statements.Nodes...)
		}
		replacement := t.NewBlock(t.NewNodeList(statements), true)
		t.ReplaceNode(file, constructor.Body(), replacement, nil)
	} else {
		t.InsertNodeBefore(file, body.Statements.Nodes[0], newStatement, false, LeadingTriviaOptionNone)
	}
}
func (t *Tracker) InsertReferenceNodeAtEndOfScope(file *ast.SourceFile, scope, node *ast.Node) {
	if ast.IsSourceFile(scope) {
		t.InsertReferenceStatementAtEnd(file, node)
		return
	}
	end := astnav.GetTokenAtPosition(file, scope.End()-1)
	pos := t.getAdjustedStartPosition(file, end, LeadingTriviaOptionNone, false)
	prefix := t.newLine + t.newLine
	if end.Pos() < len(file.Text()) {
		character, _ := utf8.DecodeRuneInString(file.Text()[end.Pos():])
		if stringutil.IsLineBreak(character) {
			prefix = t.newLine
		}
	}
	t.InsertNodeAt(file, core.TextPos(pos), node, NodeOptions{Prefix: prefix, Suffix: t.newLine})
}
func (t *Tracker) ReplaceReferenceObjectLiteral(file *ast.SourceFile, old, node *ast.Node, zeroIndent bool) {
	options := &NodeOptions{LeadingTriviaOption: LeadingTriviaOptionExclude, TrailingTriviaOption: TrailingTriviaOptionExclude}
	if zeroIndent {
		indentation := 0
		options.indentation = &indentation
	}
	t.ReplaceNode(file, old, node, options)
}

// ReplaceReferenceParameters preserves the reference's original parameter-list
// trivia and separate insertions for an originally empty list.
func (t *Tracker) ReplaceReferenceParameters(file *ast.SourceFile, declaration *ast.Node, parameters []*ast.Node) {
	original := declaration.ParameterList()
	if len(original.Nodes) > 0 {
		zero := 0
		options := NodeOptions{joiner: ", ", indentation: &zero, LeadingTriviaOption: LeadingTriviaOptionIncludeAll, TrailingTriviaOption: TrailingTriviaOptionInclude}
		span := t.GetAdjustedRange(file, original.Nodes[0], original.Nodes[len(original.Nodes)-1], options.LeadingTriviaOption, options.TrailingTriviaOption)
		t.ReplaceRangeWithNodes(file, span, parameters, options)
	} else {
		for index, parameter := range parameters {
			options := NodeOptions{}
			if index > 0 {
				options.Prefix = ", "
			}
			t.InsertNodeAt(file, core.TextPos(original.End()), parameter, options)
		}
	}
}

func (t *Tracker) InsertReferenceTypeParameters(file *ast.SourceFile, node *ast.Node, parameters []*ast.Node) {
	target := astnav.FindChildOfKind(node, ast.KindOpenParenToken, file)
	if target == nil {
		if len(node.Parameters()) == 0 {
			return
		}
		target = node.Parameters()[0]
	}
	t.InsertNodesAt(file, core.TextPos(scanner.GetTokenPosOfNode(target, file, false)), parameters, NodeOptions{Prefix: "<", Suffix: ">", joiner: ", "})
}

// ReplaceReferenceJSDocComment keeps the reference's separate deletion and
// insertion edits, including comments attached to an arrow host.
func (t *Tracker) ReplaceReferenceJSDocComment(file *ast.SourceFile, host *ast.Node, tags []*ast.Node) {
	docs := host.JSDoc(file)
	comments := []*ast.Node{}
	for _, doc := range docs {
		if doc.AsJSDoc().Comment != nil {
			for _, comment := range doc.AsJSDoc().Comment.Nodes {
				if len(comments) > 0 {
					comments = append(comments, t.NewJSDocText([]string{"\n"}))
				}
				comments = append(comments, comment)
			}
		}
	}
	var commentList *ast.NodeList
	if !(len(docs) == 1 && scanner.GetECMALineOfPosition(file, docs[0].Pos()) == scanner.GetECMALineOfPosition(file, docs[0].End()) && len(comments) == 0) {
		commentList = t.NewNodeList(comments)
	}
	target := host
	if ast.IsArrowFunction(host) && host.Parent != nil {
		if ast.IsPropertyDeclaration(host.Parent) {
			target = host.Parent
		} else if host.Parent.Parent != nil {
			target = host.Parent.Parent
		}
	}
	for _, doc := range docs {
		start := format.GetLineStartPositionForPosition(scanner.GetTokenPosOfNode(doc, file, false), file)
		end := t.getAdjustedEndPosition(file, doc, TrailingTriviaOptionNone)
		t.DeleteRange(file, core.NewTextRange(start, end))
	}
	start := scanner.GetTokenPosOfNode(target, file, false)
	text := file.Text()
	indentStart := start
	for indentStart > 0 {
		value, size := utf8.DecodeLastRuneInString(text[:indentStart])
		if !stringutil.IsWhiteSpaceSingleLine(value) {
			break
		}
		indentStart -= size
	}
	t.InsertNodeAt(file, core.TextPos(start), t.NewJSDoc(commentList, t.NewNodeList(tags)), NodeOptions{Suffix: t.newLine + text[indentStart:start]})
}

func (t *Tracker) ReplaceReferencePropertyAssignment(file *ast.SourceFile, oldNode, newNode *ast.Node) {
	next := astnav.FindNextToken(oldNode, oldNode.Parent, file)
	suffix := ""
	if next == nil || next.Kind != ast.KindCommaToken {
		suffix = "," + t.newLine
	}
	t.ReplaceNode(file, oldNode, newNode, &NodeOptions{Suffix: suffix})
}
func (t *Tracker) InsertReferenceNodeAfterComma(file *ast.SourceFile, after, newNode *ast.Node) {
	token := astnav.FindNextToken(after, after.Parent, file)
	target := after
	if token != nil && token.Kind == ast.KindCommaToken {
		target = token
	}
	t.InsertNodeAt(file, t.endPosForInsertNodeAfter(file, target, newNode), newNode, t.getInsertNodeAfterOptions(file, after))
}

// GetChanges returns the accumulated text edits.
// Note: after calling this, the Tracker object must be discarded!
func (t *Tracker) GetChanges() map[string][]*lsproto.TextEdit {
	t.finishDeleteDeclarations()
	t.finishNodesWithInsertionsAtStart()
	changes := t.getTextChangesFromChanges()
	// !!! changes for new files
	return changes
}

func (t *Tracker) ReplaceNode(sourceFile *ast.SourceFile, oldNode *ast.Node, newNode *ast.Node, options *NodeOptions) {
	if options == nil {
		// defaults to `useNonAdjustedPositions`
		options = &NodeOptions{
			LeadingTriviaOption:  LeadingTriviaOptionExclude,
			TrailingTriviaOption: TrailingTriviaOptionExclude,
		}
	}
	t.ReplaceRange(sourceFile, t.GetAdjustedRange(sourceFile, oldNode, oldNode, options.LeadingTriviaOption, options.TrailingTriviaOption), newNode, *options)
}

func (t *Tracker) ReplaceNodeWithNodes(sourceFile *ast.SourceFile, oldNode *ast.Node, newNodes []*ast.Node, options *NodeOptions) {
	if options == nil {
		options = &NodeOptions{
			LeadingTriviaOption:  LeadingTriviaOptionExclude,
			TrailingTriviaOption: TrailingTriviaOptionExclude,
		}
	}
	t.ReplaceRangeWithNodes(sourceFile, t.GetAdjustedRange(sourceFile, oldNode, oldNode, options.LeadingTriviaOption, options.TrailingTriviaOption), newNodes, *options)
}

func (t *Tracker) ReplaceRange(sourceFile *ast.SourceFile, lsprotoRange lsproto.Range, newNode *ast.Node, options NodeOptions) {
	t.referenceRecordSource(sourceFile)
	t.changes.Add(sourceFile, &trackerEdit{kind: trackerEditKindReplaceWithSingleNode, Range: lsprotoRange, options: options, Node: newNode})
}

func (t *Tracker) ReplaceRangeWithText(sourceFile *ast.SourceFile, lsprotoRange lsproto.Range, text string) {
	t.referenceRecordSource(sourceFile)
	t.changes.Add(sourceFile, &trackerEdit{kind: trackerEditKindText, Range: lsprotoRange, NewText: text})
}

func (t *Tracker) ReplaceRangeWithNodes(sourceFile *ast.SourceFile, lsprotoRange lsproto.Range, newNodes []*ast.Node, options NodeOptions) {
	if len(newNodes) == 1 {
		t.ReplaceRange(sourceFile, lsprotoRange, newNodes[0], options)
		return
	}
	t.referenceRecordSource(sourceFile)
	t.changes.Add(sourceFile, &trackerEdit{kind: trackerEditKindReplaceWithMultipleNodes, Range: lsprotoRange, nodes: newNodes, options: options})
}

func (t *Tracker) InsertText(sourceFile *ast.SourceFile, pos lsproto.Position, text string) {
	t.ReplaceRangeWithText(sourceFile, lsproto.Range{Start: pos, End: pos}, text)
}

func (t *Tracker) InsertNodeAt(sourceFile *ast.SourceFile, pos core.TextPos, newNode *ast.Node, options NodeOptions) {
	lsPos := t.converters.PositionToLineAndCharacter(sourceFile, pos)
	t.ReplaceRange(sourceFile, lsproto.Range{Start: lsPos, End: lsPos}, newNode, options)
}

func (t *Tracker) InsertNodesAt(sourceFile *ast.SourceFile, pos core.TextPos, newNodes []*ast.Node, options NodeOptions) {
	lsPos := t.converters.PositionToLineAndCharacter(sourceFile, pos)
	t.ReplaceRangeWithNodes(sourceFile, lsproto.Range{Start: lsPos, End: lsPos}, newNodes, options)
}

func (t *Tracker) InsertNodeAfter(sourceFile *ast.SourceFile, after *ast.Node, newNode *ast.Node) {
	endPosition := t.endPosForInsertNodeAfter(sourceFile, after, newNode)
	t.InsertNodeAt(sourceFile, endPosition, newNode, t.getInsertNodeAfterOptions(sourceFile, after))
}

func (t *Tracker) InsertNodesAfter(sourceFile *ast.SourceFile, after *ast.Node, newNodes []*ast.Node) {
	endPosition := t.endPosForInsertNodeAfter(sourceFile, after, newNodes[0])
	t.InsertNodesAt(sourceFile, endPosition, newNodes, t.getInsertNodeAfterOptions(sourceFile, after))
}

func (t *Tracker) InsertNodeBefore(sourceFile *ast.SourceFile, before *ast.Node, newNode *ast.Node, blankLineBetween bool, leadingTriviaOption LeadingTriviaOption) {
	t.InsertNodeAt(sourceFile, core.TextPos(t.getAdjustedStartPosition(sourceFile, before, leadingTriviaOption, false)), newNode, t.getOptionsForInsertNodeBefore(before, newNode, blankLineBetween))
}

// TryInsertTypeAnnotation inserts a type annotation after the appropriate position on a node
// (after the close paren for function-like, after the name/exclamation/question for variable-like).
// Returns true if successful.
func (t *Tracker) TryInsertTypeAnnotation(sourceFile *ast.SourceFile, node *ast.Node, typeNode *ast.Node) bool {
	var endNode *ast.Node
	if ast.IsFunctionLike(node) {
		endNode = astnav.FindChildOfKind(node, ast.KindCloseParenToken, sourceFile)
		if endNode == nil {
			if !ast.IsArrowFunction(node) {
				return false
			}
			// If no `)`, is an arrow function `x => x`, so use the end of the first parameter
			params := node.Parameters()
			if len(params) == 0 {
				return false
			}
			endNode = params[0]
		}
	} else {
		switch node.Kind {
		case ast.KindVariableDeclaration:
			endNode = node.AsVariableDeclaration().ExclamationToken
		case ast.KindPropertySignature:
			endNode = node.AsPropertySignatureDeclaration().PostfixToken
		case ast.KindPropertyDeclaration:
			endNode = node.AsPropertyDeclaration().PostfixToken
		case ast.KindParameter:
			endNode = node.AsParameterDeclaration().QuestionToken
		}
		if endNode == nil {
			endNode = node.Name()
		}
	}
	if endNode == nil {
		return false
	}
	t.InsertNodeAt(sourceFile, core.TextPos(endNode.End()), typeNode, NodeOptions{Prefix: ": "})
	return true
}

// ParenthesizeArrowParameters wraps the parameters of a paren-less arrow function in `(` and `)`.
// This is a no-op if the arrow function already has parens.
func (t *Tracker) ParenthesizeArrowParameters(sourceFile *ast.SourceFile, arrowFunc *ast.Node) {
	if astnav.FindChildOfKind(arrowFunc, ast.KindCloseParenToken, sourceFile) != nil {
		return
	}
	params := arrowFunc.Parameters()
	if len(params) == 0 {
		return
	}
	firstParam := params[0]
	lastParam := params[len(params)-1]
	startPos := astnav.GetStartOfNode(firstParam, sourceFile, false)
	t.InsertText(sourceFile, t.converters.PositionToLineAndCharacter(sourceFile, core.TextPos(startPos)), "(")
	t.InsertText(sourceFile, t.converters.PositionToLineAndCharacter(sourceFile, core.TextPos(lastParam.End())), ")")
}

// InsertModifierBefore inserts a modifier token (like 'type') before a node with a trailing space.
func (t *Tracker) InsertModifierBefore(sourceFile *ast.SourceFile, modifier ast.Kind, before *ast.Node) {
	pos := astnav.GetStartOfNode(before, sourceFile, false)
	token := t.NewToken(modifier)
	token.Loc = core.NewTextRange(pos, pos)
	token.Parent = before.Parent
	t.InsertNodeAt(sourceFile, core.TextPos(pos), token, NodeOptions{Suffix: " "})
}

// Delete queues a node for deletion with smart handling of list items, imports, etc.
// The actual deletion happens in finishDeleteDeclarations during GetChanges.
func (t *Tracker) Delete(sourceFile *ast.SourceFile, node *ast.Node) {
	t.deletedNodes = append(t.deletedNodes, deletedNode{sourceFile: sourceFile, node: node})
}

// DeleteRange deletes a text range from the source file.
func (t *Tracker) DeleteRange(sourceFile *ast.SourceFile, textRange core.TextRange) {
	lspRange := t.converters.ToLSPRange(sourceFile, textRange)
	t.ReplaceRangeWithText(sourceFile, lspRange, "")
}

// DeleteNode deletes a node immediately with specified trivia options.
// Stop! Consider using Delete instead, which has logic for deleting nodes from delimited lists.
func (t *Tracker) DeleteNode(sourceFile *ast.SourceFile, node *ast.Node, leadingTrivia LeadingTriviaOption, trailingTrivia TrailingTriviaOption) {
	rng := t.GetAdjustedRange(sourceFile, node, node, leadingTrivia, trailingTrivia)
	t.ReplaceRangeWithText(sourceFile, rng, "")
}

// DeleteNodeRange deletes a range of nodes with specified trivia options.
func (t *Tracker) DeleteReferenceNodeRangeExcludingEnd(file *ast.SourceFile, first, afterLast *ast.Node) {
	start := t.getAdjustedStartPosition(file, first, LeadingTriviaOptionIncludeAll, false)
	end := len(file.Text())
	if afterLast != nil {
		end = t.getAdjustedStartPosition(file, afterLast, LeadingTriviaOptionIncludeAll, false)
	}
	t.DeleteRange(file, core.NewTextRange(start, end))
}
func (t *Tracker) InsertReferenceNodesBefore(file *ast.SourceFile, before *ast.Node, nodes []*ast.Node, blank bool) {
	if len(nodes) == 0 {
		return
	}
	t.InsertNodesAt(file, core.TextPos(t.getAdjustedStartPosition(file, before, LeadingTriviaOptionNone, false)), nodes, t.getOptionsForInsertNodeBefore(before, nodes[0], blank))
}
func (t *Tracker) InsertReferenceNodesAtEndOfFile(file *ast.SourceFile, nodes []*ast.Node) {
	t.InsertNodesAt(file, core.TextPos(file.End()), nodes, NodeOptions{Prefix: t.newLine, Suffix: t.newLine})
}

func (t *Tracker) DeleteNodeRange(sourceFile *ast.SourceFile, startNode *ast.Node, endNode *ast.Node, leadingTrivia LeadingTriviaOption, trailingTrivia TrailingTriviaOption) {
	startPosition := t.getAdjustedStartPosition(sourceFile, startNode, leadingTrivia, false)
	endPosition := t.getAdjustedEndPosition(sourceFile, endNode, trailingTrivia)
	startPos := t.converters.PositionToLineAndCharacter(sourceFile, core.TextPos(startPosition))
	endPos := t.converters.PositionToLineAndCharacter(sourceFile, core.TextPos(endPosition))
	t.ReplaceRangeWithText(sourceFile, lsproto.Range{Start: startPos, End: endPos}, "")
}

// finishDeleteDeclarations processes all queued deletions with smart handling for lists and trailing commas.
func (t *Tracker) finishDeleteDeclarations() {
	deletedNodesInLists := make(map[*ast.Node]bool)

	for _, deleted := range t.deletedNodes {
		// Skip if this node is contained within another deleted node
		isContained := false
		for _, other := range t.deletedNodes {
			if other.sourceFile == deleted.sourceFile && other.node != deleted.node &&
				rangeContainsRangeExclusive(other.node, deleted.node) {
				isContained = true
				break
			}
		}
		if isContained {
			continue
		}

		deleteDeclaration(t, deletedNodesInLists, deleted.sourceFile, deleted.node)
	}

	// Handle trailing commas for last elements in lists
	for node := range deletedNodesInLists {
		sourceFile := ast.GetSourceFileOfNode(node)
		list := format.GetContainingList(node, sourceFile)
		if list == nil || node != list.Nodes[len(list.Nodes)-1] {
			continue
		}

		lastNonDeletedIndex := -1
		for i := len(list.Nodes) - 2; i >= 0; i-- {
			if !deletedNodesInLists[list.Nodes[i]] {
				lastNonDeletedIndex = i
				break
			}
		}

		if lastNonDeletedIndex != -1 {
			startPos := t.converters.PositionToLineAndCharacter(sourceFile, core.TextPos(list.Nodes[lastNonDeletedIndex].End()))
			endPos := t.converters.PositionToLineAndCharacter(sourceFile, core.TextPos(t.startPositionToDeleteNodeInList(sourceFile, list.Nodes[lastNonDeletedIndex+1])))
			t.ReplaceRangeWithText(sourceFile, lsproto.Range{Start: startPos, End: endPos}, "")
		}
	}
}

func (t *Tracker) endPosForInsertNodeAfter(sourceFile *ast.SourceFile, after *ast.Node, newNode *ast.Node) core.TextPos {
	if needSemicolonBetween(after, newNode) && (rune(sourceFile.Text()[after.End()-1]) != ';') {
		// check if previous statement ends with semicolon
		// if not - insert semicolon to preserve the code from changing the meaning due to ASI
		endPos := t.converters.PositionToLineAndCharacter(sourceFile, core.TextPos(after.End()))
		semicolon := t.NewToken(ast.KindSemicolonToken)
		semicolon.Loc = core.NewTextRange(after.End(), after.End())
		semicolon.Parent = after.Parent
		t.ReplaceRange(
			sourceFile,
			lsproto.Range{Start: endPos, End: endPos},
			semicolon,
			NodeOptions{},
		)
	}
	return core.TextPos(t.getAdjustedEndPosition(sourceFile, after, TrailingTriviaOptionNone))
}

/**
* This function should be used to insert nodes in lists when nodes don't carry separators as the part of the node range,
* i.e. arguments in arguments lists, parameters in parameter lists etc.
* Note that separators are part of the node in statements and class elements.
 */
func (t *Tracker) InsertNodeInListAfter(sourceFile *ast.SourceFile, after *ast.Node, newNode *ast.Node, containingList *ast.NodeList) {
	if containingList == nil {
		containingList = format.GetContainingList(after, sourceFile)
	}
	if containingList == nil {
		// Debug.fail("node is not a list element")
		return
	}
	index := slices.Index(containingList.Nodes, after)
	if index < 0 {
		return
	}
	end := after.End()
	if index != len(containingList.Nodes)-1 {
		// any element except the last one
		// use next sibling as an anchor
		if nextToken := astnav.GetTokenAtPosition(sourceFile, after.End()); nextToken != nil && isSeparator(after, nextToken) {
			// for list
			// a, b, c
			// create change for adding 'e' after 'a' as
			// - find start of next element after a (it is b)
			// - use next element start as start and end position in final change
			// - build text of change by formatting the text of node + whitespace trivia of b

			// in multiline case it will work as
			//   a,
			//   b,
			//   c,
			// result - '*' denotes leading trivia that will be inserted after new text (displayed as '#')
			//   a,
			//   insertedtext<separator>#
			// ###b,
			//   c,
			nextNode := containingList.Nodes[index+1]
			startPos := scanner.SkipTriviaEx(sourceFile.Text(), nextNode.Pos(), &scanner.SkipTriviaOptions{StopAfterLineBreak: false, StopAtComments: true})

			// write separator and leading trivia of the next element as suffix
			suffix := scanner.TokenToString(nextToken.Kind) + sourceFile.Text()[nextToken.End():startPos]
			t.InsertNodesAt(sourceFile, core.TextPos(startPos), []*ast.Node{newNode}, NodeOptions{Suffix: suffix})
		}
		return
	}

	afterStart := astnav.GetStartOfNode(after, sourceFile, false)
	afterStartLinePosition := format.GetLineStartPositionForPosition(afterStart, sourceFile)

	// insert element after the last element in the list that has more than one item
	// pick the element preceding the after element to:
	// - pick the separator
	// - determine if list is a multiline
	multilineList := false

	// if list has only one element then we'll format is as multiline if node has comment in trailing trivia, or as singleline otherwise
	// i.e. var x = 1 // this is x
	//     | new element will be inserted at this position
	separator := ast.KindCommaToken // SyntaxKind.CommaToken | SyntaxKind.SemicolonToken
	if len(containingList.Nodes) != 1 {
		// otherwise, if list has more than one element, pick separator from the list
		tokenBeforeInsertPosition := astnav.FindPrecedingToken(sourceFile, after.Pos())
		separator = core.IfElse(isSeparator(after, tokenBeforeInsertPosition), tokenBeforeInsertPosition.Kind, ast.KindCommaToken)
		// determine if list is multiline by checking lines of after element and element that precedes it.
		afterMinusOneStartLinePosition := format.GetLineStartPositionForPosition(astnav.GetStartOfNode(containingList.Nodes[index-1], sourceFile, false), sourceFile)
		multilineList = afterMinusOneStartLinePosition != afterStartLinePosition
	}
	if hasCommentsBeforeLineBreak(sourceFile.Text(), after.End()) || !positionsAreOnSameLine(containingList.Pos(), containingList.End(), sourceFile) {
		// in this case we'll always treat containing list as multiline
		multilineList = true
	}
	if multilineList {
		// insert separator immediately following the 'after' node to preserve comments in trailing trivia
		separatorToken := t.NewToken(separator)
		endPos := t.converters.PositionToLineAndCharacter(sourceFile, core.TextPos(end))
		t.ReplaceRange(sourceFile, lsproto.Range{Start: endPos, End: endPos}, separatorToken, NodeOptions{})
		// use the same indentation as 'after' item
		indentation := format.FindFirstNonWhitespaceColumn(afterStartLinePosition, afterStart, sourceFile, t.formatSettings)
		// insert element before the line break on the line that contains 'after' element
		insertPos := scanner.SkipTriviaEx(sourceFile.Text(), end, &scanner.SkipTriviaOptions{StopAfterLineBreak: true, StopAtComments: false})
		// find position before "\n" or "\r\n"
		for insertPos != end && stringutil.IsLineBreak(rune(sourceFile.Text()[insertPos-1])) {
			insertPos--
		}
		insertLSPos := t.converters.PositionToLineAndCharacter(sourceFile, core.TextPos(insertPos))
		t.ReplaceRange(
			sourceFile,
			lsproto.Range{Start: insertLSPos, End: insertLSPos},
			newNode,
			NodeOptions{
				indentation: &indentation,
				Prefix:      t.newLine,
			},
		)
	} else {
		separatorString := scanner.TokenToString(separator)
		endPos := t.converters.PositionToLineAndCharacter(sourceFile, core.TextPos(end))
		t.ReplaceRange(sourceFile, lsproto.Range{Start: endPos, End: endPos}, newNode, NodeOptions{Prefix: separatorString + " "})
	}
}

// InsertImportSpecifierAtIndex inserts a new import specifier at the specified index in a NamedImports list
func (t *Tracker) InsertImportSpecifierAtIndex(sourceFile *ast.SourceFile, newSpecifier *ast.Node, namedImports *ast.Node, index int) {
	namedImportsNode := namedImports.AsNamedImports()
	elements := namedImportsNode.Elements.Nodes

	var prevSpecifier *ast.Node
	if index > 0 && index-1 < len(elements) {
		prevSpecifier = elements[index-1]
	}
	if prevSpecifier != nil {
		t.InsertNodeInListAfter(sourceFile, prevSpecifier, newSpecifier, nil)
	} else {
		t.InsertNodeBefore(
			sourceFile,
			elements[0],
			newSpecifier,
			!positionsAreOnSameLine(astnav.GetStartOfNode(elements[0], sourceFile, false), astnav.GetStartOfNode(namedImports.Parent.Parent, sourceFile, false), sourceFile),
			LeadingTriviaOptionNone,
		)
	}
}

func (t *Tracker) InsertAtTopOfFile(sourceFile *ast.SourceFile, insert []*ast.Statement, blankLineBetween bool) {
	if len(insert) == 0 {
		return
	}

	pos := t.getInsertionPositionAtSourceFileTop(sourceFile)
	options := NodeOptions{}
	if pos != 0 {
		options.Prefix = t.newLine
	}
	if len(sourceFile.Text()) == 0 || !stringutil.IsLineBreak(rune(sourceFile.Text()[pos])) {
		options.Suffix = t.newLine
	}
	if blankLineBetween {
		options.Suffix += t.newLine
	}

	if len(insert) == 1 {
		t.InsertNodeAt(sourceFile, core.TextPos(pos), insert[0], options)
	} else {
		t.InsertNodesAt(sourceFile, core.TextPos(pos), insert, options)
	}
}

func (t *Tracker) InsertMemberAtStart(sourceFile *ast.SourceFile, node *ast.Node, newElement *ast.Node) {
	t.insertNodeAtStartWorker(sourceFile, node, newElement)
}

func (t *Tracker) insertNodeAtStartWorker(sourceFile *ast.SourceFile, node *ast.Node, newElement *ast.Node) {
	indentation := t.tryComputeIndentationFromExistingMembers(sourceFile, node)
	if indentation < 0 {
		indentation = t.tryComputeIndentationForNewMember(sourceFile, node)
	}

	members := getMembersOrProperties(node)
	if members == nil {
		return
	}

	t.InsertNodeAt(sourceFile, core.TextPos(members.Pos()), newElement, t.getInsertNodeAtStartInsertOptions(sourceFile, node, indentation))
}

func (t *Tracker) tryComputeIndentationForNewMember(sourceFile *ast.SourceFile, node *ast.Node) int {
	nodeStart := astnav.GetStartOfNode(node, sourceFile, false)
	lineStart := format.GetLineStartPositionForPosition(nodeStart, sourceFile)

	tabSize := t.formatSettings.TabSize
	if tabSize <= 0 {
		tabSize = 4
	}

	indentSize := t.formatSettings.IndentSize
	if indentSize <= 0 {
		indentSize = 4
	}
	return max(findIndentationColumn(sourceFile.Text(), lineStart, nodeStart, tabSize), 0) + indentSize
}

func (t *Tracker) tryComputeIndentationFromExistingMembers(sourceFile *ast.SourceFile, node *ast.Node) int {
	members := getMembersOrProperties(node)
	if members == nil {
		return -1
	}

	indentation := -1
	text := sourceFile.Text()
	tabSize := t.formatSettings.TabSize
	last := node

	if tabSize <= 0 {
		tabSize = 4
	}

	for _, member := range members.Nodes {
		if member == nil {
			continue
		}
		if printer.RangeStartPositionsAreOnSameLine(last.Loc, member.Loc, sourceFile) {
			return -1
		}

		memberStart := astnav.GetStartOfNode(member, sourceFile, false)
		lineStart := format.GetLineStartPositionForPosition(memberStart, sourceFile)
		column := findIndentationColumn(text, lineStart, memberStart, tabSize)
		if column < 0 {
			return -1
		}

		if indentation >= 0 {
			if indentation != column {
				return -1
			}
			last = member
			continue
		}

		indentation = column
		last = member
	}

	return indentation
}

func (t *Tracker) getInsertNodeAfterOptions(sourceFile *ast.SourceFile, node *ast.Node) NodeOptions {
	newLineChar := t.newLine
	var options NodeOptions
	switch node.Kind {
	case ast.KindParameter:
		// default opts
		options = NodeOptions{}
	case ast.KindClassDeclaration, ast.KindModuleDeclaration:
		options = NodeOptions{Prefix: newLineChar, Suffix: newLineChar}

	case ast.KindVariableDeclaration, ast.KindStringLiteral, ast.KindIdentifier:
		options = NodeOptions{Prefix: ", "}

	case ast.KindPropertyAssignment:
		options = NodeOptions{Suffix: "," + newLineChar}

	case ast.KindExportKeyword:
		options = NodeOptions{Prefix: " "}

	default:
		if !(ast.IsStatement(node) || ast.IsClassOrTypeElement(node)) {
			// Else we haven't handled this kind of node yet -- add it
			panic("unimplemented node type " + node.Kind.String() + " in changeTracker.getInsertNodeAfterOptions")
		}
		options = NodeOptions{Suffix: newLineChar}
	}
	if node.End() == sourceFile.End() && ast.IsStatement(node) {
		options.Prefix = t.newLine + options.Prefix
	}

	return options
}

func (t *Tracker) getOptionsForInsertNodeBefore(before *ast.Node, inserted *ast.Node, blankLineBetween bool) NodeOptions {
	if ast.IsStatement(before) || ast.IsClassOrTypeElement(before) {
		if blankLineBetween {
			return NodeOptions{Suffix: t.newLine + t.newLine}
		}
		return NodeOptions{Suffix: t.newLine}
	} else if before.Kind == ast.KindVariableDeclaration {
		// insert `x = 1, ` into `const x = 1, y = 2;
		return NodeOptions{Suffix: ", "}
	} else if before.Kind == ast.KindParameter {
		if inserted.Kind == ast.KindParameter {
			return NodeOptions{Suffix: ", "}
		}
		return NodeOptions{}
	} else if (before.Kind == ast.KindStringLiteral && before.Parent != nil && before.Parent.Kind == ast.KindImportDeclaration) || before.Kind == ast.KindNamedImports {
		return NodeOptions{Suffix: ", "}
	} else if before.Kind == ast.KindImportSpecifier {
		suffix := ","
		if blankLineBetween {
			suffix += t.newLine
		} else {
			suffix += " "
		}
		return NodeOptions{Suffix: suffix}
	}
	// We haven't handled this kind of node yet -- add it
	panic("unimplemented node type " + before.Kind.String() + " in changeTracker.getOptionsForInsertNodeBefore")
}

func (t *Tracker) getInsertNodeAtStartInsertOptions(sourceFile *ast.SourceFile, node *ast.Node, indentation int) NodeOptions {
	state := t.nodesWithInsertionsAtStart[node]
	hasPreviousInsertion := state != nil
	if state == nil {
		state = &nodesInsertedAtStartState{
			node:       node,
			sourceFile: sourceFile,
		}
		t.nodesWithInsertionsAtStart[node] = state
	}

	members := getMembersOrProperties(node)
	isObjectLiteral := ast.IsObjectLiteralExpression(node)
	isJSON := ast.IsJsonSourceFile(sourceFile)

	hasMembers := members != nil && len(members.Nodes) > 0

	insertTrailingComma := isObjectLiteral && (hasMembers || !isJSON)
	insertLeadingComma := isObjectLiteral && isJSON && !hasMembers && hasPreviousInsertion

	suffix := ""
	if insertTrailingComma {
		suffix = ","
	} else if ast.IsInterfaceDeclaration(node) && !hasMembers {
		suffix = ";"
	}

	prefix := t.newLine
	if insertLeadingComma {
		prefix = "," + prefix
	}

	return NodeOptions{indentation: &indentation, Prefix: prefix, Suffix: suffix}
}

func (t *Tracker) finishNodesWithInsertionsAtStart() {
	for _, state := range t.nodesWithInsertionsAtStart {
		if state == nil {
			continue
		}

		openBrace := astnav.FindChildOfKind(state.node, ast.KindOpenBraceToken, state.sourceFile)
		if openBrace == nil {
			continue
		}

		closeBrace := astnav.FindChildOfKind(state.node, ast.KindCloseBraceToken, state.sourceFile)
		if closeBrace == nil {
			continue
		}

		members := getMembersOrProperties(state.node)
		isEmpty := members == nil || len(members.Nodes) == 0
		isSingleLine := positionsAreOnSameLine(openBrace.End(), closeBrace.End(), state.sourceFile)

		if isEmpty && isSingleLine && openBrace.End() != closeBrace.End()-1 {
			t.DeleteRange(state.sourceFile, core.NewTextRange(openBrace.End(), closeBrace.End()-1))
		}

		if isSingleLine {
			t.InsertText(state.sourceFile, t.converters.PositionToLineAndCharacter(state.sourceFile, core.TextPos(closeBrace.End()-1)), t.newLine)
		}
	}
}

func getMembersOrProperties(node *ast.Node) *ast.NodeList {
	if ast.IsObjectLiteralExpression(node) {
		return node.PropertyList()
	}
	return node.MemberList()
}

func rangeContainsRangeExclusive(outer *ast.Node, inner *ast.Node) bool {
	return outer.Pos() < inner.Pos() && inner.End() < outer.End()
}

func isSeparator(node *ast.Node, candidate *ast.Node) bool {
	return candidate != nil && node.Parent != nil && (candidate.Kind == ast.KindCommaToken || (candidate.Kind == ast.KindSemicolonToken && node.Parent.Kind == ast.KindObjectLiteralExpression))
}

func findIndentationColumn(text string, lineStart, memberStart, tabSize int) int {
	column := 0

	for i := lineStart; i < memberStart && i < len(text); i++ {
		ch := rune(text[i])

		if stringutil.IsLineBreak(ch) {
			return -1
		}
		if stringutil.IsWhiteSpaceSingleLine(ch) {
			column = advanceIndentationColumn(column, ch, tabSize)
			continue
		}
		return column
	}

	return column
}

func advanceIndentationColumn(column int, ch rune, tabSize int) int {
	if ch == '\t' {
		return column + tabSize - (column % tabSize)
	}
	return column + 1
}
