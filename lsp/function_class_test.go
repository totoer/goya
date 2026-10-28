package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestConstructorConversionRepeatedConcurrentRequestsKeepSourceTree(t *testing.T) {
	ctx := context.Background()
	text := "/** Constructor. */\nexport function Box() {}\n// Static method.\nBox.run = function(value: number) { return value + 1; };\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": text}, EntryPoints: []string{"/main.ts"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err = s.Diagnostics(ctx, "/main.ts", nil); err != nil {
		t.Fatal(err)
	}
	before, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	span := protocol.Range{Start: protocol.Position{Line: 1, Character: 16}, End: protocol.Position{Line: 1, Character: 19}}
	kinds := []protocol.CodeActionKind{protocol.CodeActionQuickFix}
	actionContext := protocol.CodeActionContext{Only: &kinds, Diagnostics: []protocol.Diagnostic{{Range: span, Code: 80002, Severity: 4, Message: "stale diagnostic"}}}
	first, err := s.CodeActions(ctx, "/main.ts", span, actionContext, nil)
	if err != nil || len(first.Actions) != 1 || first.Actions[0].Edit == nil {
		t.Fatalf("initial conversion: %+v, %v", first, err)
	}
	want, err := json.Marshal(first.Actions)
	if err != nil {
		t.Fatal(err)
	}
	errors := make(chan error, 4)
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 2 {
				result, err := s.CodeActions(ctx, "/main.ts", span, actionContext, nil)
				if err != nil {
					errors <- err
					return
				}
				got, err := json.Marshal(result.Actions)
				if err != nil || !bytes.Equal(got, want) {
					errors <- fmt.Errorf("conversion changed after printing: %s; error: %v", got, err)
					return
				}
			}
		}()
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	after, err := s.Snapshot(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("conversion mutated the source snapshot: %v", err)
	}
}
