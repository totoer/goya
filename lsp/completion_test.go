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

func completionSession(t *testing.T, limits *RetentionLimits) *Session {
	t.Helper()
	s, err := NewSession(context.Background(), SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "const object={count:1,title:\"hello\"};\nobject.\n"}}, ResultCacheLimits: limits})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCompletionResolveOwnsTokensAndOriginalPresentation(t *testing.T) {
	ctx := context.Background()
	s := completionSession(t, nil)
	result, err := s.Completion(ctx, "/main.ts", protocol.Position{Line: 1, Character: 7}, nil)
	if err != nil || result.List == nil || len(result.List.Items) != 2 {
		t.Fatalf("completion: %+v %v", result, err)
	}
	item := result.List.Items[0]
	encoded, _ := json.Marshal(item)
	if strings.Contains(string(encoded), "fileName") || strings.Contains(string(encoded), "/main.ts") {
		t.Fatal("native coordinates escaped through item data", string(encoded))
	}
	item.Label = "client mutation"
	item.TextEdit.NewText = "untrusted edit"
	resolved, err := s.ResolveCompletion(ctx, item)
	if err != nil || resolved.Item.Label != "count" || resolved.Item.Detail == nil || *resolved.Item.Detail != "(property) count: number" || resolved.Item.TextEdit.NewText != ".count" {
		t.Fatalf("resolve trusted mutable client fields: %+v %v", resolved, err)
	}
	other := completionSession(t, nil)
	if _, err := other.ResolveCompletion(ctx, item); !errors.Is(err, &Error{Code: CodeInvalidHandle}) {
		t.Fatalf("foreign item: %v", err)
	}
	if err := s.OpenDocument(ctx, "/main.ts", 1, "const object={value:42};\nobject.\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveCompletion(ctx, item); !errors.Is(err, ErrContentModified) {
		t.Fatalf("stale item: %v", err)
	}
	c := startTestConnection(t, nil, 8, 16, testClientSetup{session: s})
	c.send(t, 2, "completionItem/resolve", item)
	assertResponseCode(t, c.read(t), "2", -32801)
	c.send(t, 3, "textDocument/completion", protocol.CompletionParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, Position: protocol.Position{Line: 1, Character: 7}})
	var list protocol.CompletionList
	if err := json.Unmarshal(c.read(t)["result"], &list); err != nil || len(list.Items) != 1 || list.Items[0].Label != "value" {
		t.Fatalf("completion after stale resolve: %+v %v", list, err)
	}
	c.finish(t)
}

func TestCompletionRetentionIsBoundedPerListAndCancelable(t *testing.T) {
	ctx := context.Background()
	s := completionSession(t, &RetentionLimits{MaxEntries: 1, MaxBytes: 16 << 10})
	first, err := s.Completion(ctx, "/main.ts", protocol.Position{Line: 1, Character: 7}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Both items share one retained batch, so a small entry limit does not
	// silently expire every item except the last one in a long completion list.
	for _, item := range first.List.Items {
		if _, err := s.ResolveCompletion(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Completion(ctx, "/main.ts", protocol.Position{Line: 1, Character: 7}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveCompletion(ctx, first.List.Items[0]); !errors.Is(err, ErrResultExpired) {
		t.Fatalf("eviction ignored: %v", err)
	}
	request, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Completion(request, "/main.ts", protocol.Position{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.Completion(ctx, "/main.ts", protocol.Position{}, &CompletionRequestOptions{Context: &protocol.CompletionContext{TriggerKind: protocol.CompletionTriggerCharacter}}); !errors.Is(err, &Error{Code: CodeInvalidOptions}) {
		t.Fatal(err)
	}
}

func TestCompletionPreferencesAreDetachedAndNeverExecuteHostCalls(t *testing.T) {
	ctx := context.Background()
	calls := 0
	r := goya.NewRegistry()
	if err := r.Register("touch", func() { calls++ }); err != nil {
		t.Fatal(err)
	}
	env, err := r.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	s, err := NewSession(ctx, SessionOptions{Environment: env, Project: goya.Project{Files: map[string]string{"/main.ts": "touch();const object={value:42};object."}}, EditorOptions: EditorOptions{Preferences: EditorPreferences{UseLabelDetailsInCompletionEntries: &enabled}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	enabled = false
	snapshot, err := s.Snapshot(ctx)
	if err != nil || !*snapshot.EditorOptions.Preferences.UseLabelDetailsInCompletionEntries {
		t.Fatal("preference aliases caller", err)
	}
	*snapshot.EditorOptions.Preferences.UseLabelDetailsInCompletionEntries = false
	result, err := s.Completion(ctx, "/main.ts", protocol.Position{Character: 37}, nil)
	if err != nil || len(result.List.Items) == 0 {
		t.Fatalf("host-aware completion: %+v %v", result, err)
	}
	if _, err := s.ResolveCompletion(ctx, result.List.Items[0]); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("editor completion executed host API")
	}
	after, _ := s.Snapshot(ctx)
	if !*after.EditorOptions.Preferences.UseLabelDetailsInCompletionEntries {
		t.Fatal("preference aliases snapshot")
	}
	for name := range env.TypeDeclarations() {
		if _, err := s.Completion(ctx, name, protocol.Position{}, nil); !errors.Is(err, ErrReadOnly) {
			t.Fatalf("completion offered to edit frozen declaration: %v", err)
		}
	}
}
