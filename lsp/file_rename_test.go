package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestFileRenameEditsCheckMovedCopyWithoutRenamingCatalog(t *testing.T) {
	ctx := context.Background()
	main := "import { value } from \"./helper\";\nexport const result = value;\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": main, "/helper.ts": "export const value = 1;\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/main.ts", 3, main); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Snapshot(ctx)
	result, err := s.FileRenameEdits(ctx, []FileRename{{OldName: "/helper.ts", NewName: "/renamed.ts"}})
	if err != nil || result.Edits == nil || result.Generation != before.Generation {
		t.Fatalf("proposal: %+v %v", result, err)
	}
	if err := s.ValidateEdits(ctx, *result.Edits); err != nil {
		t.Fatal(err)
	}
	if len(result.Edits.Edit.DocumentChanges) != 0 || len(result.Edits.Edit.Changes["file:///main.ts"]) != 1 {
		t.Fatalf("text-only file rename: %+v", result.Edits.Edit)
	}
	copy := before.Project
	copy.Files = maps.Clone(copy.Files)
	for uri, edits := range result.Edits.Edit.Changes {
		name, err := s.DocumentName(ctx, uri)
		if err != nil {
			t.Fatal(err)
		}
		copy.Files[name] = applyFormattingCopy(t, copy.Files[name], edits)
	}
	copy.Files["/renamed.ts"] = copy.Files["/helper.ts"]
	delete(copy.Files, "/helper.ts")
	if !strings.Contains(copy.Files["/main.ts"], `"./renamed"`) {
		t.Fatal(copy.Files["/main.ts"])
	}
	if ds, err := goya.Check(ctx, copy, before.Environment, before.CompilerOptions); err != nil || goya.HasTypeErrors(ds) {
		t.Fatalf("moved copy: %+v %v", ds, err)
	}
	after, _ := s.Snapshot(ctx)
	if !maps.Equal(before.Project.Files, after.Project.Files) {
		t.Fatal("file rename computation mutated the catalog")
	}
	if _, err := s.FileRenameEdits(ctx, []FileRename{{OldName: libraryPrefix + "lib.es5.d.ts", NewName: "/next.d.ts"}}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("frozen source: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.FileRenameEdits(cancelled, []FileRename{{OldName: "/helper.ts", NewName: "/next.ts"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if err := s.ChangeDocument(ctx, "/main.ts", 4, []protocol.ContentChange{{Text: main}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateEdits(ctx, *result.Edits); !errors.Is(err, ErrContentModified) {
		t.Fatalf("stale rename: %v", err)
	}
}

func TestWireFileRenameRequiresKnownDestinationInForeignNamespace(t *testing.T) {
	ctx := context.Background()
	text := "import { value } from \"./helper\";\nexport const result = value;\n"
	mappings := map[string]protocol.DocumentURI{"/main.ts": "file:///host/main.ts", "/helper.ts": "file:///host/helper.ts"}
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": text, "/helper.ts": "export const value = 1;\n"}}, DocumentURIs: mappings})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s})
	params := protocol.RenameFilesParams{Files: []protocol.FileRename{{OldURI: "file:///host/helper.ts", NewURI: "file:///host/future.ts"}}}
	c.send(t, "unmapped", "workspace/willRenameFiles", params)
	assertResponseCode(t, c.read(t), `"unmapped"`, -32602)
	mappings["/future.ts"] = "file:///host/future.ts"
	if err := s.UpdateDocumentURIs(ctx, mappings); err != nil {
		t.Fatal(err)
	}
	c.send(t, "mapped", "workspace/willRenameFiles", params)
	message := c.read(t)
	var edit protocol.WorkspaceEdit
	if err := json.Unmarshal(message["result"], &edit); err != nil {
		t.Fatalf("mapped proposal: %+v %v", message, err)
	}
	changes := edit.Changes["file:///host/main.ts"]
	if len(changes) != 1 || changes[0].NewText != "./future" {
		t.Fatalf("mapped proposal: %+v", edit)
	}
	snapshot, _ := s.Snapshot(ctx)
	if snapshot.Project.Files["/helper.ts"] == "" || snapshot.Project.Files["/future.ts"] != "" {
		t.Fatal("wire proposal moved a source")
	}
	c.finish(t)
}
