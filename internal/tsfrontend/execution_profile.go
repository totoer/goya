package tsfrontend

import (
	"context"
	"path"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/bundled"
	"github.com/totoer/goya/internal/tsfrontend/upstream/compiler"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"github.com/totoer/goya/internal/tsfrontend/upstream/vfs"
)

type executionProfileFS struct{ vfs.FS }

func profileFS(files vfs.FS) vfs.FS { return executionProfileFS{bundled.WrapFS(files)} }

func (fs executionProfileFS) ReadFile(name string) (string, bool) {
	text, ok := fs.FS.ReadFile(name)
	if ok && name == bundled.LibPath()+"/lib.es5.d.ts" {
		text = ExecutionProfileLibraryText(name, text)
	}
	return text, ok
}

// ExecutionProfileLibraryText applies the owned execution overlay without
// changing pinned upstream assets. Declaration positions remain stable.
func ExecutionProfileLibraryText(name, text string) string {
	if path.Base(name) == "lib.es5.d.ts" {
		const declaration = "declare function eval(x: string): any;"
		if index := strings.Index(text, declaration); index >= 0 {
			start := index
			if comment := strings.LastIndex(text[:index], "/**"); comment >= 0 && strings.HasSuffix(strings.TrimSpace(text[comment:index]), "*/") {
				start = comment
			}
			masked := []byte(text[start : index+len(declaration)])
			for i, b := range masked {
				if b != '\n' && b != '\r' {
					masked[i] = ' '
				}
			}
			return text[:start] + string(masked) + text[index+len(declaration):]
		}
	}
	return text
}

// ExecutionProfileDiagnostics is shared by compiler and editor programs. The
// intrinsic guard is authoritative for calls whose target is not statically known.
func ExecutionProfileDiagnostics(ctx context.Context, program *compiler.Program, file string) []Diagnostic {
	if ctx.Err() != nil {
		return nil
	}
	checker, release := program.GetTypeChecker(ctx)
	defer release()
	var diagnostics []Diagnostic
	for _, source := range program.GetSourceFiles() {
		if source.IsDeclarationFile || (file != "" && source.FileName() != file) {
			continue
		}
		var visit func(*ast.Node)
		visit = func(n *ast.Node) {
			var callee *ast.Node
			if n.Kind == ast.KindCallExpression {
				callee = n.AsCallExpression().Expression
			} else if n.Kind == ast.KindNewExpression {
				callee = n.AsNewExpression().Expression
			}
			if callee != nil {
				callee = ast.SkipParentheses(callee)
				if callee.Kind == ast.KindIdentifier || callee.Kind == ast.KindPropertyAccessExpression || callee.Kind == ast.KindElementAccessExpression {
					if symbol := checker.GetSymbolAtLocation(callee); symbol != nil && symbol.Name == "Function" {
						for _, declaration := range symbol.Declarations {
							if origin := ast.GetSourceFileOfNode(declaration); origin != nil && strings.HasPrefix(origin.FileName(), bundled.LibPath()+"/") {
								r := Range{Start: sourcePosition(source.Text(), scanner.SkipTrivia(source.Text(), callee.Pos())), End: sourcePosition(source.Text(), callee.End())}
								diagnostics = append(diagnostics, Diagnostic{Namespace: "goya", Code: 1114, Severity: "error", Message: "compilation of source strings is not supported by this execution profile", File: source.FileName(), Range: &r})
								break
							}
						}
					}
				}
			}
			n.ForEachChild(func(child *ast.Node) bool { visit(child); return false })
		}
		visit(source.AsNode())
	}
	return diagnostics
}
