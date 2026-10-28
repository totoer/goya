package lsp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func receiveError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("session operation did not finish")
		return nil
	}
}

func TestActiveSnapshotDoesNotBlockStateOrNewGeneration(t *testing.T) {
	s := testSession(t, nil)
	ctx := context.Background()
	lease, err := s.acquireWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release()
	originalGeneration := lease.state.snapshot.Generation
	updated := make(chan error, 1)
	go func() {
		updated <- s.UpdateProject(ctx, goya.Project{Files: map[string]string{"/main.ts": "export const newValue=2;"}})
	}()
	if err := receiveError(t, updated); err != nil {
		t.Fatal(err)
	}
	info, err := s.ProjectInfo(ctx, "/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	if info.Generation <= originalGeneration {
		t.Fatal("new request used old generation")
	}
	if lease.state.snapshot.Project.Files["/main.ts"] != "export const count=1;" {
		t.Fatal("active request snapshot changed")
	}
	if lease.state == s.workspace {
		t.Fatal("new request reused active old checker")
	}
}

func TestQueuedCheckerRequestCanCancelAndContinue(t *testing.T) {
	s := testSession(t, nil)
	ctx := context.Background()
	lease, err := s.acquireWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	request, cancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { _, err := s.ProjectInfo(request, "/main.ts"); result <- err }()
	cancel()
	if err := receiveError(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation: %v", err)
	}
	lease.release()
	if _, err := s.ProjectInfo(ctx, "/main.ts"); err != nil {
		t.Fatalf("query after cancellation: %v", err)
	}
}

func TestCloseCancelsAndWaitsForActiveAndQueuedRequests(t *testing.T) {
	s := testSession(t, nil)
	lease, err := s.acquireWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	queued := make(chan error, 1)
	go func() { _, err := s.ProjectInfo(context.Background(), "/main.ts"); queued <- err }()
	active := make(chan error, 1)
	go func() { <-lease.ctx.Done(); lease.release(); active <- nil }()
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	if err := receiveError(t, active); err != nil {
		t.Fatal(err)
	}
	if err := receiveError(t, queued); err == nil {
		t.Fatal("queued request survived Close")
	}
	if err := receiveError(t, closed); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for range s.Events() {
	}
}

func TestResolverCallbackCanReadAndUpdateSession(t *testing.T) {
	s := testSession(t, nil)
	ctx := context.Background()
	result := make(chan error, 1)
	go func() {
		result <- s.UpdateProject(ctx, goya.Project{Resolver: goya.ResolverFunc(func(ctx context.Context) (map[string]string, error) {
			if _, err := s.Snapshot(ctx); err != nil {
				return nil, err
			}
			if err := s.UpdateCompilerOptions(ctx, goya.CompileOptions{"strict": false}); err != nil {
				return nil, err
			}
			return map[string]string{"/main.ts": "export {};"}, nil
		})})
	}()
	if err := receiveError(t, result); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CompilerOptions["strict"] != false {
		t.Fatal("resolver callback update lost")
	}
}

func TestInvalidationEventsAreBoundedAndNeverBlockUpdates(t *testing.T) {
	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{EventBuffer: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	result := make(chan error, 1)
	go func() {
		for i := 0; i < 1000; i++ {
			if err := s.UpdateEnvironment(ctx, nil); err != nil {
				result <- err
				return
			}
		}
		result <- nil
	}()
	if err := receiveError(t, result); err != nil {
		t.Fatal(err)
	}
	if len(s.events) > 2 {
		t.Fatal("event queue exceeded capacity")
	}
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var latest uint64
	for len(s.events) > 0 {
		event := <-s.events
		if event.Kind != EventInvalidated {
			t.Fatalf("unexpected event: %+v", event)
		}
		latest = event.Generation
	}
	if latest != snapshot.Generation {
		t.Fatalf("coalescing lost current generation: %d != %d", latest, snapshot.Generation)
	}
}

func TestRequestSlotsAndSourceRetentionAreBounded(t *testing.T) {
	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "export {};"}}, MaxConcurrentRequests: 1, MaxSourceBytes: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lease, err := s.acquireWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	request, cancel := context.WithCancel(ctx)
	queued := make(chan error, 1)
	go func() { _, err := s.ProjectInfo(request, "/main.ts"); queued <- err }()
	cancel()
	if err := receiveError(t, queued); !errors.Is(err, context.Canceled) {
		t.Fatalf("slot cancellation: %v", err)
	}
	lease.release()
	if err := s.OpenDocument(ctx, "/main.ts", 1, "export {};"); err != nil {
		t.Fatal(err)
	}
	before, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Text: strings.Repeat(" ", 512)}}); err == nil {
		t.Fatal("oversized overlay retained")
	}
	after, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != before.Generation || after.Project.Files["/main.ts"] != before.Project.Files["/main.ts"] {
		t.Fatal("failed edit changed snapshot")
	}
	if err := s.UpdateProject(ctx, goya.Project{Files: map[string]string{"/large.ts": strings.Repeat(" ", 512)}}); err == nil {
		t.Fatal("oversized base catalog retained")
	}
}
