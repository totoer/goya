package tsfrontend

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/totoer/goya/ast"
	nativeast "github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/printer"
)

func TestExecutionIRRequiresCheckedAnalysis(t *testing.T) {
	for _, analysis := range []*Analysis{nil, {}, {program: nil}} {
		ir, diagnostics, err := analysis.Lower(context.Background())
		if err != nil || ir != nil || len(diagnostics) != 1 || diagnostics[0].Code != 1100 {
			t.Fatalf("unchecked analysis accepted: %v %+v", err, diagnostics)
		}
	}
	var empty ExecutionIR
	called := false
	if err := empty.Visit(func(*IRModule) error { called = true; return nil }); err == nil || called {
		t.Fatal("unchecked execution tree reached backend")
	}
	analysis, diagnostics, err := Analyze(context.Background(), Project{Files: map[string]string{"/main.ts": `const n: number = "wrong";`}}, nil, nil)
	if err != nil || analysis != nil || !HasErrors(diagnostics) {
		t.Fatal("type error created analysis")
	}
}

func TestExecutionIRSnapshotAndRanges(t *testing.T) {
	source := "// 😀 comment\nconst message: string = \"😀\";\nmessage;"
	files := map[string]string{"/main.ts": source}
	analysis, diagnostics, err := Analyze(context.Background(), Project{Files: files}, nil, nil)
	if err != nil || analysis == nil {
		t.Fatalf("analysis: %v %+v", err, diagnostics)
	}
	files["/main.ts"] = "broken"
	ir, diagnostics, err := analysis.Lower(context.Background())
	if err != nil || ir == nil {
		t.Fatalf("lower: %v %+v", err, diagnostics)
	}
	if ir.Key() != analysis.Key() {
		t.Fatal("lowering lost checked identity")
	}
	err = ir.Visit(func(module *IRModule) error {
		if module.Tree().File.Source() != source || module.ID() != "/main.ts" {
			t.Fatal("lowering reread caller sources")
		}
		last := module.Tree().Body[len(module.Tree().Body)-1].(*ast.ExpressionStatement).Expression
		r, ok := module.SourceRange(last)
		if !ok || r.Start.Offset != strings.LastIndex(source, "message;") || r.Start.Line != 3 || r.Start.Column != 1 || r.End.Offset-r.Start.Offset != len("message") {
			t.Fatalf("lost source range: %+v", r)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 3 {
		group.Go(func() {
			lowered, ds, err := analysis.Lower(context.Background())
			if err != nil || lowered == nil || HasErrors(ds) {
				t.Errorf("repeat lowering: %v %+v", err, ds)
			}
		})
	}
	group.Wait()
}

func TestExecutionIRUnsupportedNodeIsDiagnostic(t *testing.T) {
	analysis, ds, err := Analyze(context.Background(), Project{Files: map[string]string{"/main.ts": `1;`}}, nil, nil)
	if err != nil || analysis == nil {
		t.Fatalf("analysis: %v %+v", err, ds)
	}
	var source *nativeast.SourceFile
	for _, file := range analysis.program.GetSourceFiles() {
		if file.FileName() == "/main.ts" {
			source = file
		}
	}
	l := &lowerer{ctx: context.Background(), file: source, emit: printer.NewEmitContext(), module: &IRModule{ranges: make(map[ast.Node]Range)}}
	defer func() {
		failure, ok := recover().(loweringFailure)
		if !ok || failure.diagnostic.Code != 1101 || failure.diagnostic.Namespace != "goya" || failure.diagnostic.Range.Start.Offset != 0 {
			t.Fatalf("unknown operation did not produce a source diagnostic: %+v", failure)
		}
	}()
	l.expression(&nativeast.Node{Kind: nativeast.KindUnknown, Loc: core.NewTextRange(0, 1)})
	t.Fatal("unknown operation was accepted")
}

func TestTypeOnlySyntaxHasNoExecutionOperations(t *testing.T) {
	analysis, ds, err := Analyze(context.Background(), Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{
		"/main.ts":  `import type { Data } from "./types.js"; interface Local {value: number} type Alias = Data & Local; const value: Alias = {value: 1}; value.value;`,
		"/types.ts": `export interface Data {value: number}`,
	}}, nil, nil)
	if err != nil || analysis == nil {
		t.Fatalf("analysis: %v %+v", err, ds)
	}
	ir, ds, err := analysis.Lower(context.Background())
	if err != nil || ir == nil {
		t.Fatalf("lowering: %v %+v", err, ds)
	}
	if err := ir.Visit(func(module *IRModule) error {
		if module.ID() == "/main.ts" && len(module.Tree().Body) != 2 {
			t.Fatalf("type syntax left operations: %+v", module.Tree().Body)
		}
		if module.ID() == "/types.ts" && len(module.Tree().Body) != 0 {
			t.Fatal("type-only module has executable statements")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
