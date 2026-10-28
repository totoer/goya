package embedded_test

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/internal/lspfixture"
	"github.com/totoer/goya/internal/lspfixture/embedded"
	"github.com/totoer/goya/lsp"
	"github.com/totoer/goya/lsp/protocol"
)

func TestRecordedFileRenameEditsCheckMovedCopies(t *testing.T) {
	paths, err := filepath.Glob("../../../testdata/lsp/file-rename-*.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			fixture, err := lspfixture.Load(file)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			verifyNativeBaseline(t, fixture)
			ctx := context.Background()
			options, err := embedded.OptionsForFixture(fixture, "file:///rename-copies")
			if err != nil {
				t.Fatal(err)
			}
			endpoint, err := embedded.NewDirect(ctx, options, fixture.Initialization)
			if err != nil {
				t.Fatal(err)
			}
			defer endpoint.Close()
			var edit protocol.WorkspaceEdit
			var renames []lsp.FileRename
			for _, step := range fixture.Steps {
				params := json.RawMessage(strings.ReplaceAll(string(step.Params), "{{ROOT_URI}}", "file:///rename-copies"))
				if step.Notification {
					if err := endpoint.Notify(ctx, step.Method, params); err != nil {
						t.Fatal(err)
					}
					continue
				}
				raw, err := endpoint.Call(ctx, step.Method, params)
				if err != nil {
					t.Fatal(err)
				}
				if step.Method != "workspace/willRenameFiles" {
					continue
				}
				if err := json.Unmarshal(raw, &edit); err != nil {
					t.Fatal(err)
				}
				var pairs protocol.RenameFilesParams
				if err := json.Unmarshal(params, &pairs); err != nil {
					t.Fatal(err)
				}
				for _, pair := range pairs.Files {
					oldName, err := endpoint.Session().DocumentName(ctx, pair.OldURI)
					if err != nil {
						t.Fatal(err)
					}
					newName, err := endpoint.Session().DocumentName(ctx, pair.NewURI)
					if err != nil {
						t.Fatal(err)
					}
					renames = append(renames, lsp.FileRename{OldName: oldName, NewName: newName})
				}
			}
			before, err := endpoint.Session().Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			copy := before.Project
			copy.Files = maps.Clone(copy.Files)
			if len(edit.DocumentChanges) != 0 {
				t.Fatal("file rename unexpectedly proposed a resource operation")
			}
			for uri, edits := range edit.Changes {
				name, err := endpoint.Session().DocumentName(ctx, uri)
				if err != nil {
					t.Fatal(err)
				}
				copy.Files[name] = applyQuickFixTextEdits(t, copy.Files[name], edits)
			}
			movedName := func(name string) string {
				for _, pair := range renames {
					if name == pair.OldName || strings.HasPrefix(name, pair.OldName+"/") {
						return pair.NewName + strings.TrimPrefix(name, pair.OldName)
					}
				}
				return name
			}
			moved := make(map[string]string, len(copy.Files))
			for name, text := range copy.Files {
				target := movedName(name)
				if _, exists := moved[target]; exists {
					t.Fatal("copy rename collision:", target)
				}
				moved[target] = text
			}
			copy.Files = moved
			copy.EntryPoints = slices.Clone(copy.EntryPoints)
			for i, name := range copy.EntryPoints {
				copy.EntryPoints[i] = movedName(name)
			}
			if copy.ConfigFile != "" {
				copy.ConfigFile = movedName(copy.ConfigFile)
			}
			ds, err := goya.Check(ctx, copy, before.Environment, before.CompilerOptions)
			if err != nil {
				t.Fatal(err)
			}
			codes := []int32{}
			for _, d := range ds {
				if d.Severity == "error" {
					codes = append(codes, d.Code)
				}
			}
			slices.Sort(codes)
			expectedCodes := slices.Clone(fixture.EditedCompilerErrorCodes)
			slices.Sort(expectedCodes)
			if !slices.Equal(codes, expectedCodes) {
				t.Fatalf("renamed copy: want error codes %v, got %+v", expectedCodes, ds)
			}
			after, _ := endpoint.Session().Snapshot(ctx)
			if !maps.Equal(before.Project.Files, after.Project.Files) || !slices.Equal(before.Project.EntryPoints, after.Project.EntryPoints) {
				t.Fatal("file rename computation mutated the live catalog")
			}
		})
	}
}
