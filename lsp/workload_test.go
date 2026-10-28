package lsp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestRepeatedEditCancelCloseKeepsQueuesAndRetentionBounded(t *testing.T) {
	for cycle := 0; cycle < 3; cycle++ {
		ctx := context.Background()
		s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "export const value = 0;\nvalue;\n"}}, EventBuffer: 2, MaxConcurrentRequests: 2, ResultCacheLimits: &RetentionLimits{MaxEntries: 4, MaxBytes: 8192}})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.OpenDocument(ctx, "/main.ts", 1, "export const value = 0;\nvalue;\n"); err != nil {
			t.Fatal(err)
		}
		var previous protocol.CompletionItem
		for version := int32(2); version <= 25; version++ {
			text := fmt.Sprintf("export const value = %d;\nvalue;\n", version)
			if err := s.ChangeDocument(ctx, "/main.ts", version, []protocol.ContentChange{{Text: text}}); err != nil {
				t.Fatal(err)
			}
			if previous.Data != nil {
				if _, err := s.ResolveCompletion(ctx, previous); !errors.Is(err, ErrContentModified) {
					t.Fatalf("old completion survived edit: %v", err)
				}
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := s.Hover(cancelled, "/main.ts", protocol.Position{Line: 1, Character: 2}, nil); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel: %v", err)
			}
			if _, err := s.Hover(ctx, "/main.ts", protocol.Position{Line: 1, Character: 2}, nil); err != nil {
				t.Fatal(err)
			}
			result, err := s.Completion(ctx, "/main.ts", protocol.Position{Line: 1, Character: 3}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.List != nil && len(result.List.Items) > 0 {
				previous = result.List.Items[0]
			}
			s.mu.RLock()
			entries, retained := len(s.results.entries), s.results.bytes
			events := len(s.events)
			s.mu.RUnlock()
			if entries > 4 || retained > 8192 || events > 2 {
				t.Fatalf("unbounded workload: results=%d bytes=%d events=%d", entries, retained, events)
			}
		}
		var closing sync.WaitGroup
		for i := 0; i < 4; i++ {
			closing.Add(1)
			go func() {
				defer closing.Done()
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			}()
		}
		closing.Wait()
		if s.workspace != nil || len(s.results.entries) != 0 || s.results.bytes != 0 || len(s.requestSlots) != 0 || s.base != nil || s.environment != nil {
			t.Fatal("Close retained session work or source state")
		}
		if _, err := s.Snapshot(ctx); !errors.Is(err, ErrClosed) {
			t.Fatalf("query after close: %v", err)
		}
	}
}

// Each iteration rebuilds the editor generation, exercises successful and
// cancelled queries, and leaves invalidation events deliberately unread.
func BenchmarkSessionRepeatedEditCancel(b *testing.B) {
	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "export const value = 0;\nvalue;\n"}}, EventBuffer: 2, ResultCacheLimits: &RetentionLimits{MaxEntries: 4, MaxBytes: 8192}})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/main.ts", 1, "export const value = 0;\nvalue;\n"); err != nil {
		b.Fatal(err)
	}
	if _, err := s.Hover(ctx, "/main.ts", protocol.Position{Line: 1, Character: 2}, nil); err != nil {
		b.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		text := fmt.Sprintf("export const value = %d;\nvalue;\n", i)
		if err := s.ChangeDocument(ctx, "/main.ts", int32(i+2), []protocol.ContentChange{{Text: text}}); err != nil {
			b.Fatal(err)
		}
		if _, err := s.Hover(ctx, "/main.ts", protocol.Position{Line: 1, Character: 2}, nil); err != nil {
			b.Fatal(err)
		}
		if _, err := s.Hover(cancelled, "/main.ts", protocol.Position{Line: 1, Character: 2}, nil); !errors.Is(err, context.Canceled) {
			b.Fatalf("cancelled: %v", err)
		}
	}
	b.StopTimer()
	s.mu.RLock()
	b.ReportMetric(float64(len(s.events)), "queued-events")
	b.ReportMetric(float64(s.results.bytes), "retained-result-bytes")
	s.mu.RUnlock()
}
