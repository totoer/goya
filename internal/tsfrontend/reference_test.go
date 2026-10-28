package tsfrontend

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/bundled"
	"github.com/totoer/goya/internal/tsfrontend/upstream/compiler"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tsoptions"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
)

// The reference path calls the pinned, mechanically relocated upstream API
// directly. It does not use Analyze, its JSON option conversion, or its
// diagnostic conversion. files.lock.json and the importer verify the upstream
// source bodies against the official source archive.
func referenceDiagnostics(t *testing.T, files map[string]string, explicitRoots ...[]string) []*ast.Diagnostic {
	t.Helper()
	snapshot, err := freezeFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	roots := make([]string, 0, len(files))
	for name := range snapshot.files {
		roots = append(roots, name)
	}
	slices.Sort(roots)
	if len(explicitRoots) > 0 {
		roots = slices.Clone(explicitRoots[0])
	}
	options := &core.CompilerOptions{
		Target: core.ScriptTargetES2025, Module: core.ModuleKindESNext,
		ModuleResolution: core.ModuleResolutionKindBundler,
		Strict:           core.TSTrue, NoEmit: core.TSTrue, NoEmitOnError: core.TSTrue,
		Lib: []string{"lib.es2025.d.ts"}, Types: []string{},
	}
	host := compiler.NewCompilerHost("/", bundled.WrapFS(snapshot), bundled.LibPath(), nil, nil)
	config := tsoptions.NewParsedCommandLine(options, roots, tspath.ComparePathsOptions{UseCaseSensitiveFileNames: true, CurrentDirectory: "/"})
	program := compiler.NewProgram(compiler.ProgramOptions{Host: host, Config: config, SingleThreaded: core.TSTrue})
	return compiler.GetDiagnosticsOfAnyProgram(context.Background(), program, nil, false, program.GetBindDiagnostics, program.GetSemanticDiagnostics)
}

func TestExplicitRootOrderMatchesUpstream(t *testing.T) {
	files := map[string]string{
		"a.d.ts":  `declare function choose(value: "same"): "first";`,
		"b.d.ts":  `declare function choose(value: "same"): "second";`,
		"main.ts": `const result: "first" = choose("same");`,
	}
	counts := make(map[int]bool)
	for _, roots := range [][]string{{"/a.d.ts", "/b.d.ts", "/main.ts"}, {"/b.d.ts", "/a.d.ts", "/main.ts"}} {
		reference := referenceDiagnostics(t, files, roots)
		actual, err := Check(context.Background(), Project{EntryPoints: roots, Files: files}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var expectedCodes, actualCodes []int32
		for _, d := range reference {
			if d.Category().Name() == "error" {
				expectedCodes = append(expectedCodes, d.Code())
			}
		}
		for _, d := range actual {
			if d.Severity == "error" {
				actualCodes = append(actualCodes, d.Code)
			}
		}
		slices.Sort(expectedCodes)
		slices.Sort(actualCodes)
		if !slices.Equal(expectedCodes, actualCodes) {
			t.Fatalf("root order %v: actual=%v, upstream=%v", roots, actualCodes, expectedCodes)
		}
		counts[len(expectedCodes)] = true
	}
	if len(counts) < 2 {
		t.Fatal("fixture no longer distinguishes TypeScript overload ordering; revise it for the new baseline")
	}
}

func TestTypeFixtureDiagnosticsMatchPinnedUpstream(t *testing.T) {
	for _, test := range fixtures(t, "types.json") {
		t.Run(test.ID, func(t *testing.T) {
			reference := referenceDiagnostics(t, test.Files)
			actual, err := Check(context.Background(), Project{Files: test.Files}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var expectedPositions, actualPositions []string
			for _, diagnostic := range reference {
				file := ""
				if diagnostic.File() != nil {
					file = diagnostic.File().FileName()
				}
				expectedPositions = append(expectedPositions, fmt.Sprintf("%s:%d:%d:%d:%s", file, diagnostic.Pos(), diagnostic.End(), diagnostic.Code(), diagnostic.Category().Name()))
			}
			for _, diagnostic := range actual {
				if diagnostic.Namespace != "typescript" {
					t.Fatalf("unexpected integration diagnostic: %+v", diagnostic)
				}
				start, end := -1, -1
				if diagnostic.Range != nil {
					start, end = diagnostic.Range.Start.Offset, diagnostic.Range.End.Offset
				}
				actualPositions = append(actualPositions, fmt.Sprintf("%s:%d:%d:%d:%s", diagnostic.File, start, end, diagnostic.Code, diagnostic.Severity))
			}
			slices.Sort(expectedPositions)
			slices.Sort(actualPositions)
			expectedPositions = slices.Compact(expectedPositions)
			actualPositions = slices.Compact(actualPositions)
			if !slices.Equal(actualPositions, expectedPositions) {
				t.Fatalf("diagnostics differ from pinned upstream\nactual: %v\nreference: %v", actualPositions, expectedPositions)
			}
		})
	}
}
