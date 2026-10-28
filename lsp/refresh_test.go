package lsp_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp"
	"github.com/totoer/goya/lsp/protocol"
)

func TestRefreshProjectRetainsDetachedDescriptor(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{"/main.ts": "export const value=1;"}
	calls := 0
	s, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: goya.Project{Files: files, Resolver: goya.ResolverFunc(func(context.Context) (map[string]string, error) {
		calls++
		return map[string]string{"/dependency.ts": "export {};"}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	files["/main.ts"] = "changed by caller"
	if err := s.RefreshProject(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Project.Files["/main.ts"] != "export const value=1;" || calls != 2 || snapshot.Project.Resolver != nil {
		t.Fatalf("refresh descriptor changed: %+v calls=%d", snapshot.Project, calls)
	}
}

func TestSlowRefreshCannotReplaceNewerBase(t *testing.T) {
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	s, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: goya.Project{Resolver: goya.ResolverFunc(func(context.Context) (map[string]string, error) {
		calls++
		if calls == 2 {
			close(entered)
			<-release
		}
		return map[string]string{"/main.ts": "export const oldValue=1;"}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	done := make(chan error, 1)
	go func() { done <- s.RefreshProject(ctx) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not call resolver")
	}
	if err := s.UpdateProject(ctx, goya.Project{Files: map[string]string{"/main.ts": "export const newValue=2;"}}); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, lsp.ErrContentModified) {
			t.Fatalf("stale refresh: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not finish")
	}
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Project.Files["/main.ts"] != "export const newValue=2;" {
		t.Fatal("stale refresh overwrote new base")
	}
	if err := s.NotifyFilesChanged(ctx, []protocol.FileEvent{{URI: "file:///main.ts", Type: protocol.FileChanged}}); err == nil {
		t.Fatal("file event silently read without a host resolver")
	}
}

func TestURIMapReplacementIsAtomicAndInvalidatesResults(t *testing.T) {
	ctx := context.Background()
	s, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "export {};"}}, DocumentURIs: map[string]protocol.DocumentURI{"/main.ts": "memory:/main.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDocumentURIs(ctx, map[string]protocol.DocumentURI{"/main.ts": "file:///C:/project/main.ts"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadDocument(ctx, "memory:/main.ts"); err == nil {
		t.Fatal("obsolete URI still resolved")
	}
	document, err := s.ReadDocument(ctx, "file:///C:/project/main.ts")
	if err != nil || document.Text != "export {};" || document.Generation <= before.Generation {
		t.Fatalf("new mapping: %+v %v", document, err)
	}
	if err := s.UpdateDocumentURIs(ctx, map[string]protocol.DocumentURI{"/main.ts": "goya-lib:/lib.d.ts"}); err == nil {
		t.Fatal("reserved scheme update accepted")
	}
	if _, err := s.ReadDocument(ctx, "file:///C:/project/main.ts"); err != nil {
		t.Fatal("failed mapping update changed state")
	}
}
