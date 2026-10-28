package refactor

import (
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/printer"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
)

func copyComment(r *request, t *change.Tracker, comment ast.CommentRange, to *ast.Node, trailing bool, kind ast.Kind, newLine *bool) {
	if kind != ast.KindUnknown && kind != comment.Kind {
		return
	}
	start, end := comment.Pos()+2, comment.End()
	if comment.Kind == ast.KindMultiLineCommentTrivia {
		end -= 2
	}
	text := r.file.Text()
	if start < 0 || start > end || end > len(text) {
		return
	}
	line := comment.HasTrailingNewLine
	if newLine != nil {
		line = *newLine
	}
	if trailing {
		t.AddSyntheticTrailingComment(to, comment.Kind, text[start:end], line)
	} else {
		t.AddSyntheticLeadingComment(to, comment.Kind, text[start:end], line)
	}
}
func copyLeading(r *request, t *change.Tracker, from, to *ast.Node, kind ast.Kind, newLine *bool) {
	if from == nil || from.Pos() < 0 {
		return
	}
	for comment := range scanner.GetLeadingCommentRanges(t.NodeFactory, r.file.Text(), from.Pos()) {
		copyComment(r, t, comment, to, false, kind, newLine)
	}
}
func copyTrailing(r *request, t *change.Tracker, from, to *ast.Node, kind ast.Kind, newLine *bool) {
	if from == nil || from.End() < 0 {
		return
	}
	for comment := range scanner.GetTrailingCommentRanges(t.NodeFactory, r.file.Text(), from.End()) {
		copyComment(r, t, comment, to, true, kind, newLine)
	}
}
func copyTrailingAsLeading(r *request, t *change.Tracker, from, to *ast.Node, kind ast.Kind, newLine *bool) {
	if from == nil || from.Pos() < 0 {
		return
	}
	for comment := range scanner.GetTrailingCommentRanges(t.NodeFactory, r.file.Text(), from.Pos()) {
		copyComment(r, t, comment, to, false, kind, newLine)
	}
}
func copyComments(r *request, t *change.Tracker, from, to *ast.Node) {
	if file := ast.GetSourceFileOfNode(from); file != nil && file != r.file {
		view := *r
		view.file = file
		r = &view
	}
	start, end := from.Pos(), scanner.GetTokenPosOfNode(from, r.file, false)
	if start >= 0 && end >= start && strings.Contains(r.file.Text()[start:end], "\n") {
		copyLeading(r, t, from, to, ast.KindUnknown, nil)
	} else {
		copyTrailingAsLeading(r, t, from, to, ast.KindUnknown, nil)
	}
	copyTrailing(r, t, from, to, ast.KindUnknown, nil)
}

// Keep recursive suppression in the request-owned emit context. Source nodes
// and their parents remain available to other operations on the same snapshot.
func suppressTrivia(t *change.Tracker, node *ast.Node, leading bool) {
	for node != nil {
		flag := printer.EFNoTrailingComments
		if leading {
			flag = printer.EFNoLeadingComments
		}
		t.AddEmitFlags(node, flag)
		var child *ast.Node
		node.ForEachChild(func(next *ast.Node) bool { child = next; return leading })
		node = child
	}
}
