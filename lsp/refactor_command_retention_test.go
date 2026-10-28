package lsp

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/totoer/goya/lsp/protocol"
)

func TestIssuedRefactorCommandsRejectStaleAndEvictedSelections(t *testing.T) {
	for _, test := range []string{"source-before-prepare", "source-after-prepare", "settings", "evicted", "disabled-retention"} {
		t.Run(test, func(t *testing.T) {
			ctx := context.Background()
			var limits *RetentionLimits
			if test == "disabled-retention" {
				limits = &RetentionLimits{}
			}
			s, text, span := functionRefactorSession(t, limits)
			c := newRPCConnection(ctx, io.Discard, nil, 8, 8)
			defer c.cancel()
			only := []protocol.CodeActionKind{"refactor.rewrite.function.named"}
			result, err := s.CodeActions(ctx, "/main.ts", span, protocol.CodeActionContext{Diagnostics: []protocol.Diagnostic{}, Only: &only}, nil)
			if err != nil || len(result.Actions) != 1 || result.Actions[0].Command == nil {
				t.Fatalf("choices: %+v %v", result, err)
			}
			c.rememberRefactorCommands(s, result)
			if len(c.issuedRefactors) > s.results.limits.MaxEntries {
				t.Fatal("unbounded command cache")
			}
			arguments := result.Actions[0].Command.Arguments
			var work requestWork
			if test == "source-after-prepare" {
				work, err = s.prepareApplyRefactorCommand(ctx, c, arguments)
				if err != nil {
					t.Fatal(err)
				}
			}
			want := ErrContentModified
			switch test {
			case "source-before-prepare", "source-after-prepare":
				err = s.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Text: text + "\n"}})
			case "settings":
				err = s.UpdateEditorOptions(ctx, EditorOptions{})
			case "evicted":
				s.mu.Lock()
				s.results.clear()
				s.mu.Unlock()
				want = ErrResultExpired
			case "disabled-retention":
				want = ErrResultExpired
			}
			if err != nil {
				t.Fatal(err)
			}
			if work == nil {
				_, err = s.prepareApplyRefactorCommand(ctx, c, arguments)
			} else {
				_, err = work(ctx)
			}
			if !errors.Is(err, want) {
				t.Fatalf("stale command: want %v, got %v", want, err)
			}
		})
	}
}
