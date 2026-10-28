package tsfrontend

import (
	"context"
	"slices"
	"testing"

	"github.com/totoer/goya/ast"
)

func TestModuleResolutionCapturedInIR(t *testing.T) {
	files := map[string]string{
		"/app/main.ts":                   `import {add} from "../sources/math.js"; import {value} from "pkg/feature"; import type {Data} from "../types.js"; import {enabled} from "host:settings"; const data: Data = {value}; export const result = enabled ? add(data.value,1) : 0;`,
		"/sources/math.ts":               `export function add(a: number,b: number): number {return a+b;}`,
		"/node_modules/pkg/package.json": `{"name":"pkg","type":"module","exports":{"./feature":"./feature.ts"}}`,
		"/node_modules/pkg/feature.ts":   `export const value = 41;`,
		"/types.ts":                      `export interface Data {value: number}`,
	}
	analysis, ds, err := Analyze(context.Background(), Project{EntryPoints: []string{"/app/main.ts"}, Files: files}, &TypeEnvironment{Declarations: map[string]string{"/host.d.ts": `declare module "host:settings" {export const enabled: boolean}`}}, nil)
	if err != nil || analysis == nil {
		t.Fatalf("analyze: %v %+v", err, ds)
	}
	// Backend resolution must use the captured metadata, never this changed map.
	files["/node_modules/pkg/package.json"] = `{"exports":{"./feature":"./missing.ts"}}`
	lowered, ds, err := analysis.Lower(context.Background())
	if err != nil || lowered == nil {
		t.Fatalf("lower: %v %+v", err, ds)
	}
	visited := false
	if err := lowered.Visit(func(module *IRModule) error {
		if module.ID() != "/app/main.ts" {
			return nil
		}
		visited = true
		dependencies := module.Dependencies()
		ids := make([]string, len(dependencies))
		for i, d := range dependencies {
			ids[i] = d.ID
		}
		if !slices.Equal(ids, []string{"/sources/math.ts", "/node_modules/pkg/feature.ts", "host:host:settings"}) {
			t.Fatalf("wrong runtime resolution graph: %+v", dependencies)
		}
		if dependencies[0].Host || dependencies[1].Host || !dependencies[2].Host {
			t.Fatal("source and host dependencies were confused")
		}
		exports := module.Exports()
		if len(exports) != 1 || exports[0].Name != "result" || exports[0].From != -1 {
			t.Fatalf("exports not captured: %+v", exports)
		}
		return nil
	}); err != nil || !visited {
		t.Fatalf("entry module missing: %v", err)
	}
}

func TestModuleImportsUseReadOnlyLiveReferences(t *testing.T) {
	analysis, ds, err := Analyze(context.Background(), Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{
		"/main.ts":   `import {value, fn} from "./values.js"; export {value as renamed}; export const result = fn(value); export const object = {fn};`,
		"/values.ts": `export let value = 1; export function fn(n: number): number {return n;}`,
	}}, nil, nil)
	if err != nil || analysis == nil {
		t.Fatalf("analyze: %v %+v", err, ds)
	}
	lowered, ds, err := analysis.Lower(context.Background())
	if err != nil || lowered == nil {
		t.Fatalf("lower: %v %+v", err, ds)
	}
	if err := lowered.Visit(func(module *IRModule) error {
		if module.ID() != "/main.ts" {
			return nil
		}
		declaration := module.Tree().Body[0].(*ast.LexicalDeclaration)
		call := declaration.List[0].Initializer.(*ast.CallExpression)
		if _, ok := call.Callee.(*ast.SequenceExpression); !ok {
			t.Fatal("imported function call acquired a namespace receiver")
		}
		if _, ok := call.ArgumentList[0].(*ast.BracketExpression); !ok {
			t.Fatal("import was copied into a local snapshot")
		}
		for _, export := range module.Exports() {
			if export.Name == "renamed" && (export.From != 0 || export.Imported != "value") {
				t.Fatal("re-export lost its live alias")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
