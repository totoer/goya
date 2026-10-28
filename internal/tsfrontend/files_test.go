package tsfrontend

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/bundled"
	"github.com/totoer/goya/internal/tsfrontend/upstream/compiler"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tsoptions"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
)

func TestFrozenFilesDoNotReadHostFilesystem(t *testing.T) {
	files := map[string]string{"main.ts": "const value = 1;", "src/other.ts": "export {};"}
	snapshot, err := freezeFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	files["main.ts"] = "changed"
	if text, _ := snapshot.ReadFile("/main.ts"); text != "const value = 1;" {
		t.Fatal("source map mutation changed the snapshot")
	}
	if snapshot.FileExists("/etc/passwd") || snapshot.Stat("/etc/passwd") != nil {
		t.Fatal("virtual filesystem leaked the host filesystem")
	}
	entries := snapshot.GetAccessibleEntries("/")
	entries.Files[0] = "mutated"
	if snapshot.GetAccessibleEntries("/").Files[0] != "main.ts" {
		t.Fatal("directory listing leaked mutable backing storage")
	}
	for _, err := range []error{
		snapshot.WriteFile("/main.ts", "changed"), snapshot.AppendFile("/main.ts", "changed"),
		snapshot.Remove("/main.ts"), snapshot.Chtimes("/main.ts", time.Now(), time.Now()),
	} {
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("write was not rejected: %v", err)
		}
	}
}

func TestCanonicalFileCollisions(t *testing.T) {
	for _, files := range []map[string]string{
		{"main.ts": "", "./main.ts": ""},
		{"src": "", "src/main.ts": ""},
		{"bundled:///libs/lib.es5.d.ts": ""},
	} {
		if _, err := freezeFiles(files); err == nil {
			t.Fatalf("accepted invalid virtual files: %v", files)
		}
	}
}

func TestEmbeddedStandardLibraryChecking(t *testing.T) {
	if !bundled.Embedded {
		t.Fatal("the TypeScript frontend requires embedded compiler assets")
	}
	for _, test := range []struct {
		name   string
		source string
		code   int32
	}{
		{"standard-language-types", `const values: Array<number> = [1, 2]; const lookup = new Map<string, number>(); lookup.set("a", values[0]);`, 0},
		{"type-error", `const count: number = "hello";`, 2322},
		{"no-implicit-dom", `document.title = "hello";`, 2584},
		{"no-implicit-node", `process.exit(0);`, 2591},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot, err := freezeFiles(map[string]string{"main.ts": test.source})
			if err != nil {
				t.Fatal(err)
			}
			options := &core.CompilerOptions{
				Target: core.ScriptTargetES2025, Module: core.ModuleKindESNext,
				Strict: core.TSTrue, NoEmit: core.TSTrue, Lib: []string{"lib.es2025.d.ts"},
			}
			host := compiler.NewCompilerHost("/", bundled.WrapFS(snapshot), bundled.LibPath(), nil, nil)
			config := tsoptions.NewParsedCommandLine(options, []string{"/main.ts"}, tspath.ComparePathsOptions{UseCaseSensitiveFileNames: true, CurrentDirectory: "/"})
			program := compiler.NewProgram(compiler.ProgramOptions{Host: host, Config: config, SingleThreaded: core.TSTrue})
			ctx := context.Background()
			var diagnostics []*ast.Diagnostic
			diagnostics = append(diagnostics, program.GetProgramDiagnostics()...)
			diagnostics = append(diagnostics, program.GetSyntacticDiagnostics(ctx, nil)...)
			diagnostics = append(diagnostics, program.GetGlobalDiagnostics(ctx)...)
			diagnostics = append(diagnostics, program.GetSemanticDiagnostics(ctx, nil)...)
			if test.code == 0 {
				if len(diagnostics) != 0 {
					for _, diagnostic := range diagnostics {
						t.Errorf("unexpected diagnostic %d: %s", diagnostic.Code(), diagnostic.MessageKey())
					}
				}
				return
			}
			for _, diagnostic := range diagnostics {
				if diagnostic.Code() == test.code {
					return
				}
			}
			for _, diagnostic := range diagnostics {
				t.Logf("diagnostic %d: %s", diagnostic.Code(), diagnostic.MessageKey())
			}
			t.Fatalf("missing diagnostic %d", test.code)
		})
	}
}
