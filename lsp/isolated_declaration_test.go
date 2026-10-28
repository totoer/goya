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

func TestIsolatedDeclarationRepeatedConcurrentRequestsKeepSourceTree(t *testing.T) {
	ctx := context.Background()
	text := "export function run() { return Math.random(); }\n"

	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": text}, EntryPoints: []string{"/main.ts"}}, CompilerOptions: goya.CompileOptions{"strict": true, "isolatedDeclarations": true, "declaration": true, "target": "es2025", "module": "esnext", "moduleResolution": "bundler"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	diagnosticsBefore, err := s.Diagnostics(ctx, "/main.ts", nil)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	span := protocol.Range{Start: protocol.Position{Character: 16}, End: protocol.Position{Character: 19}}
	kinds := []protocol.CodeActionKind{protocol.CodeActionQuickFix}
	actionContext := protocol.CodeActionContext{Only: &kinds, Diagnostics: []protocol.Diagnostic{{Range: span, Code: 9007, Severity: 4, Message: "stale diagnostic"}}}
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
	diagnosticsAfter, err := s.Diagnostics(ctx, "/main.ts", nil)
	if err != nil || !reflect.DeepEqual(diagnosticsBefore, diagnosticsAfter) {
		t.Fatalf("isolated declaration action changed diagnostics: %v", err)
	}
	after, err := s.Snapshot(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("conversion mutated the source snapshot: %v", err)
	}
}

func TestIsolatedDeclarationQuotedBindingReferenceErrorRejectsWholeProposalAndRecovers(t *testing.T) {
	testReferenceInvalidEditFailures(t, "fix-isolated-destructure-quoted-error.json", 1, "Token end is child end", "isolated destructuring property cannot be formatted")
}
