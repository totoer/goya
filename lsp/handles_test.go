package lsp

import (
	"context"
	"errors"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func testSession(t *testing.T, limits *RetentionLimits) *Session {
	t.Helper()
	s, err := NewSession(context.Background(), SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "export const count=1;", "/tsconfig.json": `{"files":["main.ts"]}`}}, ResultCacheLimits: limits})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func retainTestEdits(t *testing.T, s *Session) BoundEdits {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	edits, err := s.bindEditsLocked(protocol.WorkspaceEdit{DocumentChanges: []protocol.DocumentChange{{TextDocumentEdit: &protocol.TextDocumentEdit{TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{URI: "file:///main.ts"}, Edits: []protocol.TextEdit{{Range: protocol.Range{Start: protocol.Position{Character: 13}, End: protocol.Position{Character: 18}}, NewText: "value"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return edits
}

func TestDeferredResultsAndEditsAreGenerationBound(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		update func(*Session) error
	}{
		{"source", func(s *Session) error { return s.OpenDocument(ctx, "/main.ts", 1, "export const count=2;") }},
		{"config", func(s *Session) error {
			return s.OpenDocument(ctx, "/tsconfig.json", 1, `{"compilerOptions":{"strict":false},"files":["main.ts"]}`)
		}},
		{"environment", func(s *Session) error { return s.UpdateEnvironment(ctx, nil) }},
		{"options", func(s *Session) error { return s.UpdateCompilerOptions(ctx, goya.CompileOptions{"strict": false}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testSession(t, nil)
			edits := retainTestEdits(t, s)
			if err := s.ValidateEdits(ctx, edits); err != nil {
				t.Fatal(err)
			}
			if err := tc.update(s); err != nil {
				t.Fatal(err)
			}
			if err := s.ValidateEdits(ctx, edits); !errors.Is(err, ErrContentModified) {
				t.Fatalf("stale result: %v", err)
			}
			if len(s.results.entries) != 0 || s.results.bytes != 0 {
				t.Fatal("generation retained old result metadata")
			}
		})
	}
}

func TestDeferredResultRetentionLimitsAndSessionIsolation(t *testing.T) {
	ctx := context.Background()
	s := testSession(t, &RetentionLimits{MaxEntries: 2, MaxBytes: 1024})
	a := retainTestEdits(t, s)
	b := retainTestEdits(t, s)
	if err := s.ValidateEdits(ctx, a); err != nil {
		t.Fatal(err)
	}
	c := retainTestEdits(t, s)
	if err := s.ValidateEdits(ctx, b); !errors.Is(err, ErrResultExpired) {
		t.Fatalf("LRU result not evicted: %v", err)
	}
	if err := s.ValidateEdits(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateEdits(ctx, c); err != nil {
		t.Fatal(err)
	}
	other := testSession(t, nil)
	var typed *Error
	if err := other.ValidateEdits(ctx, a); !errors.As(err, &typed) || typed.Code != CodeInvalidHandle {
		t.Fatalf("foreign handle: %v", err)
	}
	for _, limits := range []RetentionLimits{{MaxEntries: 8, MaxBytes: 1}, {MaxEntries: 0, MaxBytes: 1024}} {
		bounded := testSession(t, &limits)
		oversized := retainTestEdits(t, bounded)
		if err := bounded.ValidateEdits(ctx, oversized); !errors.Is(err, ErrResultExpired) {
			t.Fatalf("disabled/oversized retention: %v", err)
		}
		if bounded.results.bytes != 0 {
			t.Fatal("oversized result retained")
		}
	}
}

func TestEditVersionsDetachmentAndReadOnlyBoundary(t *testing.T) {
	ctx := context.Background()
	s := testSession(t, nil)
	if err := s.OpenDocument(ctx, "/main.ts", 7, "export const count=1;"); err != nil {
		t.Fatal(err)
	}
	a := retainTestEdits(t, s)
	if v := a.Edit.DocumentChanges[0].TextDocumentEdit.TextDocument.Version; v == nil || *v != 7 {
		t.Fatalf("edit version not bound: %v", v)
	}
	*a.Edit.DocumentChanges[0].TextDocumentEdit.TextDocument.Version = 8
	if err := s.ValidateEdits(ctx, a); err == nil {
		t.Fatal("caller mutation changed retained edit")
	}
	registry := goya.NewRegistry()
	if err := registry.Register("host", func() {}); err != nil {
		t.Fatal(err)
	}
	env, err := registry.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateEnvironment(ctx, env); err != nil {
		t.Fatal(err)
	}
	for _, uri := range []protocol.DocumentURI{"goya-host:/.goya/host-api.d.ts", "goya-lib:/lib.es2025.d.ts"} {
		for _, edit := range []protocol.WorkspaceEdit{
			{Changes: map[protocol.DocumentURI][]protocol.TextEdit{uri: {{NewText: "changed"}}}},
			{DocumentChanges: []protocol.DocumentChange{{DeleteFile: &protocol.DeleteFile{URI: uri}}}},
			{DocumentChanges: []protocol.DocumentChange{{RenameFile: &protocol.RenameFile{OldURI: uri, NewURI: "file:///new.ts"}}}},
		} {
			s.mu.Lock()
			_, err := s.bindEditsLocked(edit)
			s.mu.Unlock()
			if !errors.Is(err, ErrReadOnly) {
				t.Fatalf("frozen edit %s: %v", uri, err)
			}
		}
	}
	if err := s.OpenDocument(ctx, "/.goya/host-api.d.ts", 1, "changed"); err == nil {
		t.Fatal("frozen host overlay accepted")
	}
}
