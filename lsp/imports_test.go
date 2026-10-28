package lsp

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestOrganizeImportsModesAreBoundDetachedAndCheckOnCopies(t *testing.T) {
	ctx := context.Background()
	text := "import { Beta } from \"./b\";\nimport { apple, Alpha } from \"./a\";\nexport const result = Alpha + Beta;\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": text, "/a.ts": "export const Alpha=1, apple=2;\n", "/b.ts": "export const Beta=1;\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/main.ts", 3, text); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Snapshot(ctx)
	var last *BoundEdits
	for _, test := range []struct {
		mode OrganizeImportsMode
		want string
	}{
		{OrganizeImportsAll, "import { Alpha } from \"./a\";\nimport { Beta } from \"./b\";\nexport const result = Alpha + Beta;\n"},
		{OrganizeImportsSortAndCombine, "import { Alpha, apple } from \"./a\";\nimport { Beta } from \"./b\";\nexport const result = Alpha + Beta;\n"},
		{OrganizeImportsRemoveUnused, "import { Beta } from \"./b\";\nimport { Alpha } from \"./a\";\nexport const result = Alpha + Beta;\n"},
	} {
		t.Run(string(test.mode), func(t *testing.T) {
			result, err := s.OrganizeImports(ctx, "/main.ts", test.mode)
			if err != nil || result.Edits == nil || result.Version == nil || *result.Version != 3 || result.Generation != before.Generation {
				t.Fatalf("proposal: %+v %v", result, err)
			}
			if err := s.ValidateEdits(ctx, *result.Edits); err != nil {
				t.Fatal(err)
			}
			copy := before.Project
			copy.Files = maps.Clone(copy.Files)
			for _, change := range result.Edits.Edit.DocumentChanges {
				document := change.TextDocumentEdit
				if document == nil || document.TextDocument.URI != "file:///main.ts" {
					t.Fatalf("unexpected operation: %+v", change)
				}
				copy.Files["/main.ts"] = applyFormattingCopy(t, copy.Files["/main.ts"], document.Edits)
			}
			if copy.Files["/main.ts"] != test.want {
				t.Fatalf("want %q, got %q", test.want, copy.Files["/main.ts"])
			}
			if ds, err := goya.Check(ctx, copy, before.Environment, before.CompilerOptions); err != nil || goya.HasTypeErrors(ds) {
				t.Fatalf("copy: %+v %v", ds, err)
			}
			after, _ := s.Snapshot(ctx)
			if !maps.Equal(before.Project.Files, after.Project.Files) {
				t.Fatal("organize imports changed the live catalog")
			}
			last = result.Edits
		})
	}
	if _, err := s.OrganizeImports(ctx, "/main.ts", "invalid"); !errors.Is(err, &Error{Code: CodeInvalidOptions}) {
		t.Fatalf("mode: %v", err)
	}
	if _, err := s.OrganizeImports(ctx, libraryPrefix+"lib.es5.d.ts", OrganizeImportsAll); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("read-only: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.OrganizeImports(cancelled, "/main.ts", OrganizeImportsAll); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if err := s.ChangeDocument(ctx, "/main.ts", 4, []protocol.ContentChange{{Text: text}}); err != nil {
		t.Fatal(err)
	}
	if last == nil {
		t.Fatal("missing proposal")
	}
	if err := s.ValidateEdits(ctx, *last); !errors.Is(err, ErrContentModified) {
		t.Fatalf("stale import proposal: %v", err)
	}
}

func TestAddMissingImportsComputesCurrentEditsWithoutPriorDiagnostics(t *testing.T) {
	ctx := context.Background()
	text := "export const value: Shape = { value: 1 };\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts", "/lib.ts"}, Files: map[string]string{"/main.ts": text, "/lib.ts": "export interface Shape { value: number; }\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, _ := s.Snapshot(ctx)
	result, err := s.AddMissingImports(ctx, "/main.ts")
	if err != nil || result.Edits == nil || result.Version != nil {
		t.Fatalf("closed catalog imports: %+v %v", result, err)
	}
	copy := before.Project
	copy.Files = maps.Clone(copy.Files)
	for _, change := range result.Edits.Edit.DocumentChanges {
		copy.Files["/main.ts"] = applyFormattingCopy(t, copy.Files["/main.ts"], change.TextDocumentEdit.Edits)
	}
	want := "import { Shape } from \"./lib\";\n\n" + text
	if copy.Files["/main.ts"] != want {
		t.Fatalf("want %q got %q", want, copy.Files["/main.ts"])
	}
	if ds, err := goya.Check(ctx, copy, before.Environment, before.CompilerOptions); err != nil || goya.HasTypeErrors(ds) {
		t.Fatalf("copy: %+v %v", ds, err)
	}
	after, _ := s.Snapshot(ctx)
	if !maps.Equal(before.Project.Files, after.Project.Files) {
		t.Fatal("add imports changed the live catalog")
	}
}
