package lsp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestAsyncConversionFailureStateAndResolveCacheAreSessionOwned(t *testing.T) {
	ctx := context.Background()
	valid := "export function first() { return Promise.resolve(1).then(value => value + 1); }\n"
	invalid := "export function second() { return Promise.resolve(1).then(value => { if (value > 1) return value; return 0; }).then(value => value + 1); }\n"
	create := func(text string) *Session {
		t.Helper()
		s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": text}, EntryPoints: []string{"/main.ts"}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	first := create(valid + invalid)
	second := create(valid + strings.ReplaceAll(valid, "first", "second"))
	before, err := first.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	enumerate := func(s *Session) protocol.CodeAction {
		t.Helper()
		report, err := s.Diagnostics(ctx, "/main.ts", nil)
		if err != nil {
			t.Fatal(err)
		}
		kinds := []protocol.CodeActionKind{protocol.CodeActionQuickFix}
		result, err := s.CodeActions(ctx, "/main.ts", protocol.Range{End: protocol.Position{Line: 2}}, protocol.CodeActionContext{Only: &kinds, Diagnostics: report.Items}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, action := range result.Actions {
			if action.Title == "Convert all to async functions" {
				return action
			}
		}
		t.Fatalf("missing async fix-all: %+v", result.Actions)
		return protocol.CodeAction{}
	}
	failedAction := enumerate(first)
	validAction := enumerate(second)
	validResult, err := second.ResolveCodeAction(ctx, validAction)
	if err != nil || validResult.Edits == nil {
		t.Fatalf("second session resolve: %+v, %v", validResult, err)
	}
	validJSON, _ := json.Marshal(validResult.Action.Edit)
	if !strings.Contains(string(validJSON), "await Promise.resolve") {
		t.Fatalf("the second session inherited failure state: %s", validJSON)
	}
	for range 2 {
		result, err := first.ResolveCodeAction(ctx, failedAction)
		if err != nil || result.Edits == nil {
			t.Fatalf("first session resolve: %+v, %v", result, err)
		}
		encoded, _ := json.Marshal(result.Action.Edit)
		if strings.Contains(string(encoded), "await Promise.resolve") {
			t.Fatalf("the other session reset the failure flag: %s", encoded)
		}
	}
	after, err := first.Snapshot(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("resolve mutated the source snapshot: %v", err)
	}
}
