package embedded_test

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/internal/lspfixture"
	"github.com/totoer/goya/internal/lspfixture/embedded"
	"github.com/totoer/goya/lsp/protocol"
)

func TestRecordedRefactorEditsCheckDetachedCatalog(t *testing.T) {
	paths, err := filepath.Glob("../../../testdata/lsp/refactor-*-apply-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("missing recorded refactor edits")
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
			ctx := context.Background()
			root := "file:///refactor%20workspace"
			options, err := embedded.OptionsForFixture(fixture, root)
			if err != nil {
				t.Fatal(err)
			}
			initialization, err := lspfixture.ResolveInitialization(fixture.Initialization, root)
			if err != nil {
				t.Fatal(err)
			}
			endpoint, err := embedded.NewDirect(ctx, options, initialization)
			if err != nil {
				t.Fatal(err)
			}
			defer endpoint.Close()
			before, err := endpoint.Session().Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := lspfixture.Run(ctx, fixture, root, endpoint); err != nil {
				t.Fatal(err)
			}
			var proposal *protocol.WorkspaceEdit
			for _, request := range endpoint.ClientRequests() {
				if request.Method == "workspace/applyEdit" {
					var params protocol.ApplyWorkspaceEditParams
					if err := json.Unmarshal(request.Params, &params); err != nil {
						t.Fatal(err)
					}
					proposal = &params.Edit
				}
			}
			if proposal == nil {
				// Some applicable reference actions deliberately return no edits
				// after validating all references (for example, callback aliases).
				for _, request := range fixture.Steps[len(fixture.Steps)-1].ExpectedClientRequests {
					if request.Method == "workspace/applyEdit" {
						t.Fatal("missing recorded client proposal")
					}
				}
				after, err := endpoint.Session().Snapshot(ctx)
				if err != nil || !maps.Equal(before.Project.Files, after.Project.Files) {
					t.Fatalf("empty refactor mutated source: %v", err)
				}
				return
			}
			detached := before.Project
			detached.Files = maps.Clone(detached.Files)
			detached.EntryPoints = slices.Clone(detached.EntryPoints)
			ensureDocument := func(name string) {
				if _, exists := detached.Files[name]; !exists {
					detached.Files[name] = ""
					detached.EntryPoints = append(detached.EntryPoints, name)
				}
			}
			for uri, edits := range proposal.Changes {
				name, err := endpoint.Session().DocumentName(ctx, uri)
				if err != nil {
					t.Fatal(err)
				}
				ensureDocument(name)
				detached.Files[name] = applyQuickFixTextEdits(t, detached.Files[name], edits)
			}
			for _, change := range proposal.DocumentChanges {
				if change.CreateFile != nil {
					name, err := endpoint.Session().DocumentName(ctx, change.CreateFile.URI)
					if err != nil {
						t.Fatal(err)
					}
					if _, exists := detached.Files[name]; exists {
						t.Fatal("refactor unexpectedly overwrites an existing file")
					}
					ensureDocument(name)
					continue
				}
				if change.TextDocumentEdit == nil {
					t.Fatal("unexpected refactor resource operation")
				}
				document := change.TextDocumentEdit
				name, err := endpoint.Session().DocumentName(ctx, document.TextDocument.URI)
				if err != nil {
					t.Fatal(err)
				}
				ensureDocument(name)
				detached.Files[name] = applyQuickFixTextEdits(t, detached.Files[name], document.Edits)
			}
			diagnostics, err := goya.Check(ctx, detached, before.Environment, before.CompilerOptions)
			if err != nil {
				t.Fatal(err)
			}
			codes := []int32{}
			for _, item := range diagnostics {
				if item.Severity == "error" {
					codes = append(codes, item.Code)
				}
			}
			slices.Sort(codes)
			expected := slices.Clone(fixture.EditedCompilerErrorCodes)
			slices.Sort(expected)
			if !slices.Equal(codes, expected) {
				t.Fatalf("refactored catalog: want %v got %+v\n%s", expected, diagnostics, detached.Files["/main.ts"])
			}
			after, err := endpoint.Session().Snapshot(ctx)
			if err != nil || !maps.Equal(before.Project.Files, after.Project.Files) {
				t.Fatalf("refactor mutated source catalog: %v", err)
			}
		})
	}
}
