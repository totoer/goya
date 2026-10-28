package lsp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestIncompleteAutoImportCompletionsIsolateCacheAndResolveSkippedEntry(t *testing.T) {
	ctx := context.Background()
	text := "export {};\nappe();\n"
	files := map[string]string{"/main.ts": text}
	roots := []string{"/main.ts"}
	for i := 0; i < 105; i++ {
		name := fmt.Sprintf("/modules/item%04d.ts", i)
		files[name] = fmt.Sprintf("export function append%04d() { return %d; }\n", i, i)
		roots = append(roots, name)
	}
	project := goya.Project{Files: files, EntryPoints: roots}
	options := goya.CompileOptions{"moduleResolution": "node10", "module": "esnext", "strict": true, "target": "es2025", "lib": []string{"es2025"}, "types": []string{}, "noEmit": true}
	newSession := func() *Session {
		s, err := NewSession(ctx, SessionOptions{Project: project, CompilerOptions: options})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		if err := s.OpenDocument(ctx, "/main.ts", 1, text); err != nil {
			t.Fatal(err)
		}
		return s
	}
	s, other := newSession(), newSession()
	query := func(session *Session, wantIncomplete bool) CompletionResult {
		t.Helper()
		result, err := session.Completion(ctx, "/main.ts", protocol.Position{Line: 1, Character: 4}, nil)
		if err != nil || result.List == nil || result.List.IsIncomplete != wantIncomplete {
			t.Fatalf("completion budget: %+v %v", result.List, err)
		}
		return result
	}
	cold := query(s, true)
	query(s, false)
	query(other, true)
	var skipped protocol.CompletionItem
	found := false
	for _, item := range cold.List.Items {
		if item.Label == "append0104" {
			skipped = item
			found = true
			break
		}
	}
	if !found || skipped.Detail != nil {
		t.Fatalf("missing unresolved route: %+v", skipped)
	}
	details, err := s.ResolveCompletion(ctx, skipped)
	if err != nil || details.Item.Detail == nil || !strings.Contains(*details.Item.Detail, "./modules/item0104") || len(details.Item.AdditionalTextEdits) == 0 {
		t.Fatalf("resolve skipped route: %+v %v", details, err)
	}
	before, _ := s.Snapshot(ctx)
	if err := s.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Text: text + "\n"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveCompletion(ctx, skipped); !errors.Is(err, ErrContentModified) {
		t.Fatalf("stale cached completion: %v", err)
	}
	fresh := query(s, true)
	if fresh.Generation <= before.Generation {
		t.Fatal("source update retained the old cache")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Completion(cancelled, "/main.ts", protocol.Position{Line: 1, Character: 4}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled budget request: %v", err)
	}
	after, _ := s.Snapshot(ctx)
	if after.Project.Files["/main.ts"] != text+"\n" {
		t.Fatal("completion changed source")
	}
}
