package tsfrontend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
)

func GlobalBindingKey(name string) string            { return "global:" + name }
func ModuleBindingKey(specifier, name string) string { return "module:" + specifier + "\x00" + name }

// BindingTypeDigests closes each registered type over the declarations it
// references. Alias/interface changes cannot hide behind an unchanged spelling
// or adapter ID. Unrelated registrations do not enter a required binding's hash.
func (a *Analysis) BindingTypeDigests(ctx context.Context, sourceName string) (map[string]string, error) {
	if a == nil || a.backend == nil || !a.backend.checked {
		return nil, fmt.Errorf("binding schemas require checked declarations")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.backend.mu.Lock()
	defer a.backend.mu.Unlock()
	checker, release := a.program.GetTypeChecker(ctx)
	defer release()
	var source *ast.SourceFile
	for _, file := range a.program.GetSourceFiles() {
		if file.FileName() == sourceName {
			source = file
			break
		}
	}
	if source == nil {
		return nil, fmt.Errorf("host declaration source is missing: %s", sourceName)
	}
	text := func(n *ast.Node) string {
		file := ast.GetSourceFileOfNode(n)
		if file == nil {
			return ""
		}
		start := scanner.SkipTrivia(file.Text(), n.Pos())
		return file.Text()[start:n.End()]
	}
	fingerprint := func(root *ast.Node) string {
		seen := make(map[*ast.Symbol]bool)
		declarations := make(map[string]bool)
		var walk func(*ast.Node)
		var follow func(*ast.Symbol)
		follow = func(symbol *ast.Symbol) {
			if symbol == nil || seen[symbol] {
				return
			}
			seen[symbol] = true
			if symbol.Flags&ast.SymbolFlagsAlias != 0 {
				follow(checker.GetAliasedSymbol(symbol))
			}
			for _, declaration := range symbol.Declarations {
				identity := ""
				if file := ast.GetSourceFileOfNode(declaration); file != nil {
					identity = file.FileName()
				}
				declarations[identity+":"+declaration.Kind.String()+":"+text(declaration)] = true
				walk(declaration)
			}
		}
		walk = func(n *ast.Node) {
			if n == nil {
				return
			}
			switch n.Kind {
			case ast.KindTypeReference:
				follow(checker.GetSymbolAtLocation(n.AsTypeReferenceNode().TypeName))
			case ast.KindTypeQuery:
				follow(checker.GetSymbolAtLocation(n.AsTypeQueryNode().ExprName))
			case ast.KindExpressionWithTypeArguments:
				follow(checker.GetSymbolAtLocation(n.Expression()))
			case ast.KindImportType:
				if qualifier := n.AsImportTypeNode().Qualifier; qualifier != nil {
					follow(checker.GetSymbolAtLocation(qualifier))
				} else if typ := checker.GetTypeAtLocation(n); typ != nil {
					follow(typ.Symbol())
				}
			}
			n.ForEachChild(func(child *ast.Node) bool { walk(child); return false })
		}
		walk(root)
		parts := make([]string, 0, len(declarations))
		for declaration := range declarations {
			parts = append(parts, declaration)
		}
		slices.Sort(parts)
		data, _ := json.Marshal(struct {
			Root         string
			Declarations []string
		}{text(root), parts})
		digest := sha256.Sum256(data)
		return hex.EncodeToString(digest[:])
	}
	result := make(map[string]string)
	for _, statement := range source.Statements.Nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch statement.Kind {
		case ast.KindVariableStatement:
			for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
				result[GlobalBindingKey(declaration.Name().Text())] = fingerprint(declaration.AsVariableDeclaration().Type)
			}
		case ast.KindModuleDeclaration:
			module := checker.GetSymbolAtLocation(statement.Name())
			if module == nil {
				return nil, fmt.Errorf("host module has no checked symbol: %s", statement.Name().Text())
			}
			for name, symbol := range module.Exports {
				if symbol.Flags&ast.SymbolFlagsAlias != 0 {
					symbol = checker.GetAliasedSymbol(symbol)
				}
				declaration := symbol.ValueDeclaration
				if declaration == nil || declaration.Kind != ast.KindVariableDeclaration {
					continue
				}
				result[ModuleBindingKey(statement.Name().Text(), name)] = fingerprint(declaration.AsVariableDeclaration().Type)
			}
		}
	}
	return result, nil
}
