package refactor

import (
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/astnav"
	"slices"
)

// Statement groups intentionally retain the pinned provider's shared afterLast
// boundary, including selections interrupted by imports or prologue directives.
type moveStatementRange struct{ first, afterLast *ast.Node }
type moveSelection struct {
	all    []*ast.Node
	ranges []moveStatementRange
}

func moveRequire(node *ast.Node) bool {
	return node != nil && ast.IsCallExpression(node) && ast.IsIdentifier(node.Expression()) && node.Expression().Text() == "require" && len(node.Arguments()) == 1 && ast.IsStringLiteralLike(node.Arguments()[0])
}
func movePureImport(node *ast.Node) bool {
	if ast.IsImportDeclaration(node) {
		return true
	}
	if ast.IsImportEqualsDeclaration(node) {
		return !ast.HasSyntacticModifier(node, ast.ModifierFlagsExport)
	}
	if ast.IsVariableStatement(node) {
		for _, decl := range node.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
			if !moveRequire(decl.Initializer()) {
				return false
			}
		}
		return true
	}
	return false
}
func movePrologue(node *ast.Node) bool {
	return ast.IsExpressionStatement(node) && ast.IsStringLiteral(node.Expression())
}
func moveOverloadIndices(r *request, node *ast.Node) (int, int, bool) {
	if !ast.IsFunctionLikeDeclaration(node) {
		return 0, 0, false
	}
	r.types()
	symbol := node.Symbol()
	if symbol == nil || len(symbol.Declarations) <= 1 || !slices.Contains(symbol.Declarations, node) {
		return 0, 0, false
	}
	decls := symbol.Declarations
	first, last := decls[0], decls[len(decls)-1]
	start, end := -1, -1
	for index, statement := range r.file.Statements.Nodes {
		if start < 0 && statement.End() >= first.End() {
			start = index
		}
		if end < 0 && statement.End() >= last.End() {
			end = index
		}
	}
	return start, end, true
}
func selectMoveStatements(r *request) *moveSelection {
	statements := r.file.Statements.Nodes
	start := -1
	for i, statement := range statements {
		if statement.End() > r.span.Pos() {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	if first, _, ok := moveOverloadIndices(r, statements[start]); ok {
		start = first
	}
	if start < 0 {
		return nil
	}
	end := -1
	for i := start; i < len(statements); i++ {
		if statements[i].End() >= r.span.End() {
			end = i
			break
		}
	}
	if end >= 0 && r.span.End() <= tokenStart(r, statements[end]) {
		end--
	}
	var endNode *ast.Node
	if end >= 0 {
		endNode = statements[end]
	}
	if _, last, ok := moveOverloadIndices(r, endNode); ok {
		end = last
	}
	var afterLast *ast.Node
	if end >= 0 && end+1 < len(statements) {
		afterLast = statements[end+1]
	}
	afterEnd := len(statements)
	if end >= 0 {
		afterEnd = end + 1
	}
	selected := statements[start:afterEnd]
	result := &moveSelection{}
	for i := 0; i < len(selected); {
		if movePureImport(selected[i]) || movePrologue(selected[i]) {
			i++
			continue
		}
		begin := i
		for i < len(selected) && !movePureImport(selected[i]) && !movePrologue(selected[i]) {
			result.all = append(result.all, selected[i])
			i++
		}
		result.ranges = append(result.ranges, moveStatementRange{selected[begin], afterLast})
	}
	if len(result.all) == 0 {
		return nil
	}
	return result
}
func hideAutomaticMove(r *request) bool {
	if !r.options.Implicit {
		return false
	}
	start := ancestor(astnav.GetTokenAtPosition(r.file, r.span.Pos()), extractionBlockLike)
	end := ancestor(astnav.GetTokenAtPosition(r.file, r.span.End()), extractionBlockLike)
	return start != nil && !ast.IsSourceFile(start) && end != nil && !ast.IsSourceFile(end)
}
