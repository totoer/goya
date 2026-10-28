// Command inventory records the exact declarations shipped by the pinned
// TypeScript frontend. It does not claim those declarations are implemented.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/parser"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
)

type declaration struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Pos    int    `json:"pos"`
	End    int    `json:"end"`
	Family string `json:"family"`
	Symbol string `json:"symbol,omitempty"`
}

type library struct {
	Name         string        `json:"name"`
	SHA256       string        `json:"sha256"`
	External     bool          `json:"external"`
	References   []string      `json:"references"`
	Declarations []declaration `json:"declarations"`
}

func main() {
	directory := flag.String("libs", "internal/tsfrontend/upstream/bundled/libs", "pinned declaration directory")
	output := flag.String("out", "docs/typescript-library-inventory.json", "inventory path")
	check := flag.Bool("check", false, "verify inventory without writing")
	flag.Parse()
	if err := run(*directory, *output, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(directory, output string, check bool) error {
	paths, err := filepath.Glob(filepath.Join(directory, "*.d.ts"))
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no standard declarations in %s", directory)
	}
	slices.Sort(paths)
	var libraries []library
	for _, filename := range paths {
		text, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		name := filepath.Base(filename)
		text = []byte(tsfrontend.ExecutionProfileLibraryText(name, string(text)))
		digest := sha256.Sum256(text)
		lib := library{
			Name: name, SHA256: hex.EncodeToString(digest[:]),
			External:   strings.Contains(name, ".dom.") || strings.Contains(name, ".webworker.") || name == "lib.scripthost.d.ts",
			References: []string{}, Declarations: []declaration{},
		}
		for _, line := range strings.Split(string(text), "\n") {
			if reference, ok := strings.CutPrefix(strings.TrimSpace(line), `/// <reference lib="`); ok {
				reference, _, _ = strings.Cut(reference, `"`)
				lib.References = append(lib.References, reference)
			}
		}
		if !lib.External {
			file := parser.ParseSourceFile(ast.SourceFileParseOptions{FileName: "/" + name, Path: tspath.Path("/" + name)}, string(text), core.ScriptKindTS)
			if len(file.Diagnostics()) > 0 {
				return fmt.Errorf("cannot parse %s: %s", name, file.Diagnostics()[0].MessageKey())
			}
			collect(file.AsNode(), "", &lib.Declarations)
			for _, declaration := range lib.Declarations {
				if declaration.Family == "unclassified" {
					return fmt.Errorf("unclassified standard declaration %s in %s; extend the compatibility inventory", declaration.Name, name)
				}
			}
		}
		libraries = append(libraries, lib)
	}
	inventory := struct {
		Version        string                             `json:"typescript_version"`
		Status         string                             `json:"status"`
		SyntaxKinds    []string                           `json:"syntax_kinds"`
		SyntaxCoverage []tsfrontend.SyntaxCoverage        `json:"syntax_coverage"`
		SyntaxFeatures []tsfrontend.SyntaxFeatureCoverage `json:"syntax_features"`
		Libraries      []library                          `json:"libraries"`
	}{Version: core.Version(), Status: "inventory-only; runtime compatibility is not established", Libraries: libraries}
	inventory.SyntaxCoverage, err = tsfrontend.SyntaxInventory()
	inventory.SyntaxFeatures = tsfrontend.SyntaxFeatures()
	if err != nil {
		return err
	}
	for kind := ast.Kind(0); kind < ast.KindCount; kind++ {
		inventory.SyntaxKinds = append(inventory.SyntaxKinds, kind.String())
	}
	data, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if check {
		existing, err := os.ReadFile(output)
		if err != nil {
			return err
		}
		if string(existing) != string(data) {
			return fmt.Errorf("standard library inventory is stale: %s", output)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(output, data, 0644); err != nil {
			return err
		}
	}
	fmt.Printf("TypeScript %s: inventoried %d standard declaration files\n", core.Version(), len(libraries))
	return nil
}

func collect(node *ast.Node, scope string, result *[]declaration) {
	if ast.IsGlobalScopeAugmentation(node) {
		node.ForEachChild(func(child *ast.Node) bool {
			collect(child, scope, result)
			return false
		})
		return
	}
	name := ""
	switch node.Kind {
	case ast.KindInterfaceDeclaration, ast.KindClassDeclaration, ast.KindModuleDeclaration,
		ast.KindTypeAliasDeclaration, ast.KindVariableDeclaration, ast.KindFunctionDeclaration,
		ast.KindMethodSignature, ast.KindPropertySignature, ast.KindMethodDeclaration,
		ast.KindPropertyDeclaration, ast.KindGetAccessor, ast.KindSetAccessor:
		name = ast.GetDeclarationName(node)
	case ast.KindCallSignature:
		name = "<call>"
	case ast.KindConstructSignature, ast.KindConstructor:
		name = "<construct>"
	case ast.KindIndexSignature:
		name = "<index>"
	}
	if name != "" {
		symbol := ""
		if member := ast.GetNonAssignedNameOfDeclaration(node); member != nil && ast.IsComputedPropertyName(member) {
			expression := member.Expression()
			if ast.IsPropertyAccessExpression(expression) && ast.IsIdentifier(expression.Expression()) && expression.Expression().Text() == "Symbol" {
				symbol = expression.Name().Text()
			}
		}
		if scope != "" {
			name = scope + "." + name
		}
		*result = append(*result, declaration{Name: name, Kind: node.Kind.String(), Pos: node.Pos(), End: node.End(), Family: family(name), Symbol: symbol})
		scope = name
	}
	node.ForEachChild(func(child *ast.Node) bool {
		collect(child, scope, result)
		return false
	})
}

func family(name string) string {
	root, _, _ := strings.Cut(name, ".")
	for _, rule := range []struct{ prefix, family string }{
		{"AsyncDisposable", "disposal"}, {"Disposable", "disposal"},
		{"Intl", "intl"}, {"Temporal", "temporal"},
		{"SharedArrayBuffer", "shared-memory"}, {"Atomics", "atomics"},
		{"WeakRef", "weak-reference"}, {"FinalizationRegistry", "finalization"},
		{"Float16Array", "float16"},
		{"ArrayBuffer", "buffer"}, {"DataView", "buffer"},
		{"BigInt64Array", "typed-array"}, {"BigUint64Array", "typed-array"},
		{"Int8Array", "typed-array"}, {"Int16Array", "typed-array"}, {"Int32Array", "typed-array"},
		{"Uint8Array", "typed-array"}, {"Uint16Array", "typed-array"}, {"Uint32Array", "typed-array"},
		{"Uint8ClampedArray", "typed-array"}, {"Float32Array", "typed-array"}, {"Float64Array", "typed-array"},
		{"Array", "array"}, {"ReadonlyArray", "array"}, {"ConcatArray", "array"}, {"FlatArray", "array"},
		{"WeakMap", "collection"}, {"Map", "collection"}, {"ReadonlyMap", "collection"},
		{"WeakSet", "set"}, {"Set", "set"}, {"ReadonlySet", "set"},
		{"Promise", "promise"}, {"String", "string"}, {"TemplateStringsArray", "string"},
		{"Symbol", "symbol"}, {"Number", "number-math"}, {"Boolean", "number-math"}, {"Math", "number-math"},
		{"Date", "date"}, {"RegExp", "regexp"}, {"JSON", "json"}, {"BigInt", "bigint"},
		{"Reflect", "proxy-reflect"}, {"Proxy", "proxy-reflect"},
		{"Function", "closures"}, {"CallableFunction", "closures"}, {"NewableFunction", "closures"}, {"IArguments", "closures"},
		{"Generator", "generators"}, {"AsyncGenerator", "async"},
		{"AsyncIterator", "async"}, {"AsyncIterable", "async"}, {"Iterator", "iterator"}, {"Iterable", "iterator"},
		{"BuiltinIterator", "iterator"}, {"Object", "object"}, {"PropertyDescriptor", "object"}, {"TypedPropertyDescriptor", "object"},
		{"Import", "modules"},
	} {
		if strings.HasPrefix(root, rule.prefix) {
			return rule.family
		}
	}
	if strings.Contains(root, "Error") {
		return "error"
	}
	if strings.Contains(root, "Decorator") {
		return "standard-decorators"
	}
	switch root {
	case "eval":
		return "eval"
	case "Infinity", "NaN", "isFinite", "isNaN", "parseFloat", "parseInt":
		return "number-math"
	case "decodeURI", "decodeURIComponent", "encodeURI", "encodeURIComponent", "escape", "unescape":
		return "global-functions"
	case "Awaited", "Capitalize", "ConstructorParameters", "Exclude", "Extract", "InstanceType", "Lowercase",
		"NoInfer", "NonNullable", "Omit", "OmitThisParameter", "Parameters", "Partial", "Pick", "PropertyKey",
		"Readonly", "Record", "Required", "ReturnType", "ThisParameterType", "ThisType", "Uncapitalize", "Uppercase",
		"WeakKey", "WeakKeyTypes":
		return "type-system"
	}
	return "unclassified"
}
