package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestRenameEditsAreDetachedMappedBoundAndCancellable(t *testing.T) {
	ctx := context.Background()
	main := "import {value} from './helper';\nconst record = {value};\nrecord.value;\n"
	helper := "export let value=1;\nvalue++;\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": main, "/helper.ts": helper}}, DocumentURIs: map[string]protocol.DocumentURI{"/helper.ts": "memory:///helper.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/helper.ts", 7, helper); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PrepareRename(ctx, "/helper.ts", protocol.Position{Character: 12})
	if err != nil || prepared.Range == nil || prepared.Version == nil || *prepared.Version != 7 {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	result, err := s.Rename(ctx, "/helper.ts", protocol.Position{Character: 12}, "next")
	if err != nil || result.Edits == nil || len(result.Edits.Edit.Changes) != 2 || result.Generation != prepared.Generation {
		t.Fatalf("rename: %+v %v", result, err)
	}
	if err := s.ValidateEdits(ctx, *result.Edits); err != nil {
		t.Fatal(err)
	}
	if len(result.Edits.Edit.Changes["memory:///helper.ts"]) != 2 {
		t.Fatal(result.Edits.Edit)
	}
	tampered := cloneEdit(result.Edits.Edit)
	tampered.Changes["memory:///helper.ts"][0].NewText = "tampered"
	if err := s.ValidateEdits(ctx, BoundEdits{Handle: result.Edits.Handle, Edit: tampered}); !errors.Is(err, &Error{Code: CodeInvalidHandle}) {
		t.Fatalf("accepted mutated edits: %v", err)
	}
	if err := s.ValidateEdits(ctx, *result.Edits); err != nil {
		t.Fatal("mutation reached retained proposal", err)
	}
	q, err := s.captureQuery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.prepareRename(q, "/helper.ts", protocol.Position{Character: 12}, "next", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeDocument(ctx, "/helper.ts", 8, []protocol.ContentChange{{Text: "\n" + helper}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateEdits(ctx, *result.Edits); !errors.Is(err, ErrContentModified) {
		t.Fatalf("stale proposal: %v", err)
	}
	if _, _, err := old(ctx); !errors.Is(err, ErrContentModified) {
		t.Fatalf("bound old request: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Rename(cancelled, "/helper.ts", protocol.Position{Line: 1, Character: 12}, "next"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	next, err := s.Rename(ctx, "/helper.ts", protocol.Position{Line: 1, Character: 12}, "next")
	if err != nil || next.Edits == nil || next.Version == nil || *next.Version != 8 {
		t.Fatalf("recovery: %+v %v", next, err)
	}
	doc, err := s.ReadDocument(ctx, "memory:///helper.ts")
	if err != nil || doc.Text != "\n"+helper {
		t.Fatalf("rename mutated catalog: %+v %v", doc, err)
	}
}

func TestRenameFrozenDeclarationsCannotProducePartialEdits(t *testing.T) {
	ctx := context.Background()
	calls := 0
	registry := goya.NewRegistry()
	if err := registry.Register("host", func() float64 { calls++; return 42 }); err != nil {
		t.Fatal(err)
	}
	env, err := registry.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	main := "const value=host();\nhost();\nMath.abs(value);\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": main}}, Environment: env})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/main.ts", 1, main); err != nil {
		t.Fatal(err)
	}
	for _, prepare := range []bool{true, false} {
		var err error
		if prepare {
			_, err = s.PrepareRename(ctx, "/main.ts", protocol.Position{Line: 1, Character: 2})
		} else {
			_, err = s.Rename(ctx, "/main.ts", protocol.Position{Line: 1, Character: 2}, "next")
		}
		if !errors.Is(err, ErrReadOnly) {
			t.Fatalf("frozen host prepare=%t: %v", prepare, err)
		}
	}
	const name = "/.goya/host-api.d.ts"
	pos, err := PositionAt(env.TypeDeclarations()[name], strings.Index(env.TypeDeclarations()[name], "host:"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareRename(ctx, name, pos); !errors.Is(err, ErrReadOnly) {
		t.Fatal(err)
	}
	if _, err := s.Rename(ctx, libraryPrefix+"lib.es5.d.ts", protocol.Position{}, "next"); !errors.Is(err, ErrReadOnly) {
		t.Fatal(err)
	}
	if _, err := s.PrepareRename(ctx, "/main.ts", protocol.Position{Line: 2, Character: 6}); err == nil {
		t.Fatal("library symbol was renameable")
	} else {
		var unavailable *RenameUnavailableError
		if !errors.As(err, &unavailable) {
			t.Fatal(err)
		}
	}
	if len(s.results.entries) != 0 || calls != 0 {
		t.Fatalf("denied operation retained partial edits or called Go: %d %d", len(s.results.entries), calls)
	}
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s})
	c.send(t, "host", "textDocument/rename", protocol.RenameParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, Position: protocol.Position{Line: 1, Character: 2}, NewName: "next"})
	message := c.read(t)
	var failure ResponseError
	if err := json.Unmarshal(message["error"], &failure); err != nil || failure.Code != -32602 || calls != 0 {
		t.Fatalf("wire frozen host: %+v %v", message, err)
	}
	c.send(t, "invalid", "textDocument/rename", map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, "position": protocol.Position{}})
	message = c.read(t)
	if err := json.Unmarshal(message["error"], &failure); err != nil || failure.Code != -32602 {
		t.Fatalf("missing name: %+v %v", message, err)
	}
	c.finish(t)
}
