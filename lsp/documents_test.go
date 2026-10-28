package lsp_test

import (
	"context"
	"errors"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp"
	"github.com/totoer/goya/lsp/protocol"
)

func TestUnsavedDependencySurvivesBaseRefresh(t *testing.T) {
	ctx := context.Background()
	calls := 0
	value := "export const value = 1;"
	project := goya.Project{Files: map[string]string{"/main.ts": `import {value} from "./dep.js";const count:number=value;`}, Resolver: goya.ResolverFunc(func(context.Context) (map[string]string, error) {
		calls++
		return map[string]string{"/dep.ts": value}, nil
	})}
	s, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: project})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.OpenDocument(ctx, "/dep.ts", 1, `export const value = "unsaved";`); err != nil {
		t.Fatal(err)
	}
	value = "export const value = 2;"
	if err := s.UpdateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDocument(ctx, "/dep.ts"); err != nil {
		t.Fatal(err)
	}
	open, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if open.Project.Files["/dep.ts"] != `export const value = "unsaved";` {
		t.Fatal("base refresh or save overwrote overlay")
	}
	diagnostics, err := goya.Check(ctx, open.Project, open.Environment, open.CompilerOptions)
	if err != nil || !goya.HasTypeErrors(diagnostics) {
		t.Fatalf("importer did not see unsaved dependency: %v %+v", err, diagnostics)
	}
	if err := s.CloseDocument(ctx, "/dep.ts"); err != nil {
		t.Fatal(err)
	}
	closed, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Project.Files["/dep.ts"] != value {
		t.Fatal("close did not restore current base")
	}
	diagnostics, err = goya.Check(ctx, closed.Project, closed.Environment, closed.CompilerOptions)
	if err != nil || goya.HasTypeErrors(diagnostics) {
		t.Fatalf("base dependency: %v %+v", err, diagnostics)
	}
	if before.Project.Files["/dep.ts"] != "export const value = 1;" {
		t.Fatal("active snapshot was changed")
	}
	if calls != 2 {
		t.Fatalf("resolver called %d times; reads and document events must not recapture it", calls)
	}
}

func TestIncrementalChangesAreOrderedAndAtomic(t *testing.T) {
	ctx := context.Background()
	s, err := lsp.NewSession(ctx, lsp.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	text := "const emoji = \"😀\";\r\nconst count=1;\r\n"
	if err := s.OpenDocument(ctx, "/main.ts", 1, text); err != nil {
		t.Fatal(err)
	}
	first, _ := s.Snapshot(ctx)
	rangeAt := func(line, start, end uint32) *protocol.Range {
		return &protocol.Range{Start: protocol.Position{Line: line, Character: start}, End: protocol.Position{Line: line, Character: end}}
	}
	if err := s.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Range: rangeAt(1, 6, 11), Text: "total"}, {Range: rangeAt(1, 12, 13), Text: "2"}}); err != nil {
		t.Fatal(err)
	}
	second, _ := s.Snapshot(ctx)
	if second.Project.Files["/main.ts"] != "const emoji = \"😀\";\r\nconst total=2;\r\n" {
		t.Fatal("incorrect sequential range application")
	}
	if first.Project.Files["/main.ts"] != text {
		t.Fatal("prior snapshot changed")
	}
	err = s.ChangeDocument(ctx, "/main.ts", 3, []protocol.ContentChange{{Range: rangeAt(1, 6, 11), Text: "wrong"}, {Range: rangeAt(99, 0, 1), Text: "invalid"}})
	if err == nil {
		t.Fatal("accepted range outside document")
	}
	after, _ := s.Snapshot(ctx)
	if after.Generation != second.Generation || after.Project.Files["/main.ts"] != second.Project.Files["/main.ts"] {
		t.Fatal("invalid notification partially changed state")
	}
	if err := s.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Text: "outdated"}}); !errors.Is(err, lsp.ErrContentModified) {
		t.Fatalf("old version: %v", err)
	}
	if err := s.ChangeDocument(ctx, "/main.ts", 3, []protocol.ContentChange{{Text: "export {};"}}); err != nil {
		t.Fatalf("failed notification consumed version: %v", err)
	}
}

func TestOverlayAndBasePathConflictsAreRejected(t *testing.T) {
	ctx := context.Background()
	s, err := lsp.NewSession(ctx, lsp.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/dir/main.ts", 1, "export {};"); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Snapshot(ctx)
	if err := s.UpdateProject(ctx, goya.Project{Files: map[string]string{"/dir": "conflicting file"}}); err == nil {
		t.Fatal("accepted file/directory conflict with open overlay")
	}
	after, _ := s.Snapshot(ctx)
	if after.Generation != before.Generation || after.Project.Files["/dir/main.ts"] != "export {};" {
		t.Fatal("failed refresh changed state")
	}
	if err := s.OpenDocument(ctx, "/dir", 1, "conflicting overlay"); err == nil {
		t.Fatal("accepted conflicting overlay")
	}
}
