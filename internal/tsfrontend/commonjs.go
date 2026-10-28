package tsfrontend

import "github.com/totoer/goya/internal/tsfrontend/upstream/ast"

func hasExportEquals(nodes []*ast.Node) bool {
	for _, n := range nodes {
		if n.Kind == ast.KindExportAssignment && n.AsExportAssignment().IsExportEquals {
			return true
		}
	}
	return false
}

func (l *lowerer) collectCommonJSExports() {
	seen := map[string]bool{}
	for _, symbol := range l.checker.GetExportsAndPropertiesOfModule(l.file.AsNode().Symbol()) {
		if symbol.Flags&(ast.SymbolFlagsValue|ast.SymbolFlagsAlias) == 0 || symbol.Name == "export=" {
			continue
		}
		if symbol.Flags&ast.SymbolFlagsAlias != 0 {
			target := l.checker.GetAliasedSymbol(symbol)
			if target == nil || target.Flags&ast.SymbolFlagsValue == 0 {
				continue
			}
			valueAlias := false
			for _, declaration := range symbol.Declarations {
				if l.checker.GetEmitResolver().IsValueAliasDeclaration(declaration) {
					valueAlias = true
					break
				}
			}
			if !valueAlias {
				continue
			}
		}
		if !seen[symbol.Name] {
			l.module.exports = append(l.module.exports, IRExport{Name: symbol.Name, From: -1})
			seen[symbol.Name] = true
		}
	}
	if !seen["default"] {
		l.module.exports = append(l.module.exports, IRExport{Name: "default", From: -1})
	}
}
