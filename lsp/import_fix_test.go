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

func TestImportFixRepeatedConcurrentRequestsKeepSourceTree(t *testing.T) {
	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.cts": "second;\n", "/helper.d.cts": "declare class Box { static second: number; }\nexport = Box;\n"}, EntryPoints: []string{"/main.cts", "/helper.d.cts"}}, CompilerOptions: goya.CompileOptions{"strict": true, "target": "es2025", "module": "nodenext", "moduleResolution": "nodenext", "verbatimModuleSyntax": true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	before, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	diagnosticsBefore, err := s.Diagnostics(ctx, "/main.cts", nil)
	if err != nil {
		t.Fatal(err)
	}
	span := protocol.Range{End: protocol.Position{Character: 6}}
	kinds := []protocol.CodeActionKind{protocol.CodeActionQuickFix}
	actionContext := protocol.CodeActionContext{Only: &kinds, Diagnostics: []protocol.Diagnostic{{Range: span, Code: 2304, Severity: 1, Message: "stale diagnostic"}}}
	first, err := s.CodeActions(ctx, "/main.cts", span, actionContext, nil)
	if err != nil || len(first.Actions) != 1 || first.Actions[0].Edit == nil {
		t.Fatalf("initial import action: %+v, %v", first, err)
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
				result, err := s.CodeActions(ctx, "/main.cts", span, actionContext, nil)
				if err != nil {
					errors <- err
					return
				}
				got, err := json.Marshal(result.Actions)
				if err != nil || !bytes.Equal(got, want) {
					errors <- fmt.Errorf("import action changed after indexing: %s; error: %v", got, err)
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
	diagnosticsAfter, err := s.Diagnostics(ctx, "/main.cts", nil)
	if err != nil || !reflect.DeepEqual(diagnosticsBefore, diagnosticsAfter) {
		t.Fatalf("import action changed diagnostics: %v", err)
	}
	after, err := s.Snapshot(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("import action mutated the source snapshot: %v", err)
	}
}
