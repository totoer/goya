package lsp

import (
	"context"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestHoverUsesCapturedGenerationAndCurrentOverlays(t *testing.T) {
	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "const count: number = 1;\ncount;\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	q, err := s.captureQuery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	work, err := s.prepareHover(q, "/main.ts", protocol.Position{Line: 1, Character: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.OpenDocument(ctx, "/main.ts", 1, "const count: string = 'one';\ncount;\n"); err != nil {
		t.Fatal(err)
	}
	old, err := work(ctx)
	if err != nil || old.Generation != 1 || old.Version != nil || old.Hover == nil || old.Hover.Contents.Value != "\n```typescript\nconst count: number\n```\n" {
		t.Fatalf("captured hover: %+v %v", old, err)
	}
	current, err := s.Hover(ctx, "/main.ts", protocol.Position{Line: 1, Character: 2}, nil)
	if err != nil || current.Generation != 2 || current.Version == nil || *current.Version != 1 || current.Hover == nil || current.Hover.Contents.Value != "\n```typescript\nconst count: string\n```\n" {
		t.Fatalf("current hover: %+v %v", current, err)
	}
	if current.Hover.Range == nil || current.Hover.Range.Start.Character != 0 || current.Hover.Range.End.Character != 5 {
		t.Fatalf("hover lost original range: %+v", current.Hover)
	}
}

func TestHoverHostTypesChangeWithoutCallingGo(t *testing.T) {
	ctx := context.Background()
	calls := 0
	stringRegistry := goya.NewRegistry()
	if err := stringRegistry.Register("host", func() string { calls++; return "value" }); err != nil {
		t.Fatal(err)
	}
	stringEnv, err := stringRegistry.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	numberRegistry := goya.NewRegistry()
	if err := numberRegistry.Register("host", func() float64 { calls++; return 42 }); err != nil {
		t.Fatal(err)
	}
	numberEnv, err := numberRegistry.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(ctx, SessionOptions{Environment: stringEnv, Project: goya.Project{Files: map[string]string{"/main.ts": "const count=host();\ncount;"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		env  *goya.Environment
		want string
	}{{stringEnv, "string"}, {numberEnv, "number"}} {
		if err := s.UpdateEnvironment(ctx, tc.env); err != nil {
			t.Fatal(err)
		}
		result, err := s.Hover(ctx, "/main.ts", protocol.Position{Line: 1, Character: 2}, nil)
		if err != nil || result.Hover == nil || !strings.Contains(result.Hover.Contents.Value, "count: "+tc.want) || calls != 0 {
			t.Fatalf("host hover: %+v %v calls=%d", result, err, calls)
		}
	}
}
