package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func functionRefactorSession(t *testing.T, limits *RetentionLimits) (*Session, string, protocol.Range) {
	t.Helper()
	ctx := context.Background()
	text := "export const run = (value: number) => value + 1;\nrun(1);\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": text}, EntryPoints: []string{"/main.ts"}}, CompilerOptions: goya.CompileOptions{"strict": true, "target": "es2025", "module": "esnext", "moduleResolution": "bundler"}, ResultCacheLimits: limits})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.OpenDocument(ctx, "/main.ts", 1, text); err != nil {
		t.Fatal(err)
	}
	position, err := PositionAt(text, strings.Index(text, "value"))
	if err != nil {
		t.Fatal(err)
	}
	return s, text, protocol.Range{Start: position, End: position}
}
func TestRefactorSelectionOwnsChoicesAndChecksDetachedCatalogs(t *testing.T) {
	ctx := context.Background()
	s, _, span := functionRefactorSession(t, nil)
	before, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kind := protocol.CodeActionKind("refactor.rewrite.function")
	invoked := protocol.CodeActionInvoked
	result, err := s.ApplicableRefactors(ctx, "/main.ts", span, &RefactorRequestOptions{Kind: &kind, TriggerKind: &invoked})
	if err != nil {
		t.Fatal(err)
	}
	group := -1
	for index, info := range result.Refactors {
		if info.Name == "Convert arrow function or function expression" {
			group = index
			break
		}
	}
	if group < 0 || len(result.Refactors[group].Actions) != 2 {
		t.Fatalf("function choices: %+v", result)
	}
	for actionIndex := range result.Refactors[group].Actions {
		// Returned labels cannot redirect the private issued choice.
		result.Refactors[group].Name = "caller changed group"
		result.Refactors[group].Actions[actionIndex].Name = "caller changed action"
		applied, err := s.ApplyRefactor(ctx, RefactorSelection{Handle: result.Handle, RefactorIndex: group, ActionIndex: actionIndex}, nil)
		if err != nil || applied.Edits == nil {
			t.Fatalf("apply choice %d: %+v %v", actionIndex, applied, err)
		}
		if applied.Version == nil || *applied.Version != 1 {
			t.Fatalf("missing captured document version: %+v", applied)
		}
		if err := s.ValidateEdits(ctx, *applied.Edits); err != nil {
			t.Fatal(err)
		}
		detached := before.Project
		detached.Files = maps.Clone(detached.Files)
		for _, change := range applied.Edits.Edit.DocumentChanges {
			if change.TextDocumentEdit == nil {
				t.Fatal("unexpected resource operation")
			}
			document := change.TextDocumentEdit
			name, err := s.DocumentName(ctx, document.TextDocument.URI)
			if err != nil {
				t.Fatal(err)
			}
			detached.Files[name] = applyFormattingCopy(t, detached.Files[name], document.Edits)
		}
		ds, err := goya.Check(ctx, detached, before.Environment, before.CompilerOptions)
		if err != nil || goya.HasTypeErrors(ds) {
			t.Fatalf("choice %d produced invalid catalog: %+v %v\n%s", actionIndex, ds, err, detached.Files["/main.ts"])
		}
	}
	after, err := s.Snapshot(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("refactor changed session: %v", err)
	}
}
func TestCodeActionsExposeOwnedRefactorSelections(t *testing.T) {
	ctx := context.Background()
	s, _, span := functionRefactorSession(t, nil)
	kinds := []protocol.CodeActionKind{"refactor.rewrite.function.named", "refactor.rewrite.function.anonymous"}
	actions, err := s.CodeActions(ctx, "/main.ts", span, protocol.CodeActionContext{Only: &kinds, Diagnostics: []protocol.Diagnostic{}}, nil)
	if err != nil || len(actions.Actions) != 2 || len(actions.Refactors) != 2 {
		t.Fatalf("refactor actions: %+v %v", actions, err)
	}
	for index, choice := range actions.Refactors {
		if choice == nil || choice.ActionIndex < 0 || actions.Proposals[index] != nil {
			t.Fatalf("missing deferred choice: %+v", choice)
		}
		applied, err := s.ApplyRefactor(ctx, RefactorSelection{Handle: choice.Handle, RefactorIndex: choice.RefactorIndex, ActionIndex: choice.ActionIndex}, nil)
		if err != nil || applied.Edits == nil {
			t.Fatalf("apply action %d: %+v %v", index, applied, err)
		}
	}
}
func TestRefactorSelectionsRejectStaleEvictedAndDisabledActions(t *testing.T) {
	ctx := context.Background()
	kind := protocol.CodeActionKind("refactor.rewrite.function.named")
	t.Run("stale", func(t *testing.T) {
		s, text, span := functionRefactorSession(t, nil)
		result, err := s.ApplicableRefactors(ctx, "/main.ts", span, &RefactorRequestOptions{Kind: &kind})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Text: text + "\n"}}); err != nil {
			t.Fatal(err)
		}
		_, err = s.ApplyRefactor(ctx, RefactorSelection{Handle: result.Handle}, nil)
		if !errors.Is(err, ErrContentModified) {
			t.Fatalf("stale choice: %v", err)
		}
	})
	t.Run("evicted", func(t *testing.T) {
		s, _, span := functionRefactorSession(t, &RetentionLimits{})
		result, err := s.ApplicableRefactors(ctx, "/main.ts", span, &RefactorRequestOptions{Kind: &kind})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.ApplyRefactor(ctx, RefactorSelection{Handle: result.Handle}, nil)
		if !errors.Is(err, ErrResultExpired) {
			t.Fatalf("evicted choice: %v", err)
		}
	})
	t.Run("disabled", func(t *testing.T) {
		s, _, _ := functionRefactorSession(t, nil)
		kind := protocol.CodeActionKind("refactor.rewrite.arrow.braces.remove")
		result, err := s.ApplicableRefactors(ctx, "/main.ts", protocol.Range{}, &RefactorRequestOptions{Kind: &kind})
		if err != nil || len(result.Refactors) != 1 || len(result.Refactors[0].Actions) != 2 {
			t.Fatalf("disabled choices: %+v %v", result, err)
		}
		reason := result.Refactors[0].Actions[0].NotApplicableReason
		_, err = s.ApplyRefactor(ctx, RefactorSelection{Handle: result.Handle}, nil)
		var failure *Error
		if !errors.As(err, &failure) || failure.Code != CodeInvalidEdit || failure.Message != reason {
			t.Fatalf("disabled action: %v", err)
		}
	})
}

func TestRefactorConcurrentRequestsKeepSourceAndPrintingStable(t *testing.T) {
	ctx := context.Background()
	s, _, span := functionRefactorSession(t, nil)
	kind := protocol.CodeActionKind("refactor.rewrite.function")
	choices, err := s.ApplicableRefactors(ctx, "/main.ts", span, &RefactorRequestOptions{Kind: &kind})
	if err != nil {
		t.Fatal(err)
	}
	group := -1
	for index, info := range choices.Refactors {
		if info.Name == "Convert arrow function or function expression" {
			group = index
			break
		}
	}
	if group < 0 {
		t.Fatal("missing function refactor")
	}
	before, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expected := [][]byte{}
	for action := 0; action < 2; action++ {
		result, err := s.ApplyRefactor(ctx, RefactorSelection{Handle: choices.Handle, RefactorIndex: group, ActionIndex: action}, nil)
		if err != nil || result.Edits == nil {
			t.Fatalf("initial choice: %+v %v", result, err)
		}
		raw, err := json.Marshal(result.Edits.Edit)
		if err != nil {
			t.Fatal(err)
		}
		expected = append(expected, raw)
	}
	failures := make(chan error, 4)
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for repeat := 0; repeat < 3; repeat++ {
				action := repeat % 2
				result, err := s.ApplyRefactor(ctx, RefactorSelection{Handle: choices.Handle, RefactorIndex: group, ActionIndex: action}, nil)
				if err != nil || result.Edits == nil {
					failures <- fmt.Errorf("repeated refactor: %v", err)
					return
				}
				raw, err := json.Marshal(result.Edits.Edit)
				if err != nil || string(raw) != string(expected[action]) {
					failures <- fmt.Errorf("refactor printing changed: %s, %v", raw, err)
					return
				}
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	after, err := s.Snapshot(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("concurrent refactors changed snapshot: %v", err)
	}
}

func TestRefactorProvidersKeepBorrowedTreesUnchanged(t *testing.T) {
	for _, item := range []struct{ name, text, needle, kind string }{
		{"inline", "const value = function(input: number) { /* body */ return input + 1; };\nconst result = value(1);\n", "value", "refactor.inline.variable"},
		{"string", "const value = \"a\" /* one */ + /* two */ \"b\" /* three */;\n", "\"a\"", "refactor.rewrite.string"},
		{"accessor", "class Box { constructor(public value: number) {} }\n", "value", "refactor.rewrite.property.generateAccessors"},
		{"predicate", "function isString(value: unknown) { return typeof value === \"string\"; }\n", "isString", "refactor.rewrite.function.returnType"},
		{"chain", "declare const object: { name: string } | undefined;\nconst value = object ? object.name : \"missing\";\n", "object ?", "refactor.rewrite.expression.optionalChain"},
		{"parameters", "function run(/* one */ first: number, second = \"value\") { return first + second.length; }\nrun(/* arg */ 1, \"text\");\n", "first", "refactor.rewrite.parameters.toDestructured"},

		{"extract-function", "function run<T extends { value: number }>(input: T) { return input.value + 1; }\n", "+ 1", "refactor.extract.function"},
		{"extract-constant", "function run(input: number) { return input + 1; }\n", "input +", "refactor.extract.constant"},

		{"move-new", "const value = 1;\nexport const result = value + 1;\n", "value", "refactor.move.newFile"},
	} {
		t.Run(item.name, func(t *testing.T) {
			ctx := context.Background()
			s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": item.text}, EntryPoints: []string{"/main.ts"}}, CompilerOptions: goya.CompileOptions{"strict": true, "target": "es2025", "module": "esnext", "moduleResolution": "bundler"}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			position, _ := PositionAt(item.text, strings.Index(item.text, item.needle))
			kind := protocol.CodeActionKind(item.kind)
			invoked := protocol.CodeActionInvoked
			choices, err := s.ApplicableRefactors(ctx, "/main.ts", protocol.Range{Start: position, End: position}, &RefactorRequestOptions{Kind: &kind, TriggerKind: &invoked})
			if err != nil || len(choices.Refactors) != 1 || len(choices.Refactors[0].Actions) < 1 || choices.Refactors[0].Actions[0].NotApplicableReason != "" {
				t.Fatalf("choices: %+v %v", choices, err)
			}
			selection := RefactorSelection{Handle: choices.Handle}
			first, err := s.ApplyRefactor(ctx, selection, nil)
			if err != nil || first.Edits == nil {
				t.Fatalf("first proposal: %+v %v", first, err)
			}
			want, _ := json.Marshal(first.Edits.Edit)
			before, err := s.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			failures := make(chan error, 3)
			var workers sync.WaitGroup
			for range 3 {
				workers.Add(1)
				go func() {
					defer workers.Done()
					for range 3 {
						next, err := s.ApplyRefactor(ctx, selection, nil)
						if err != nil || next.Edits == nil {
							failures <- fmt.Errorf("repeated proposal: %+v %v", next, err)
							return
						}
						got, err := json.Marshal(next.Edits.Edit)
						if err != nil || string(got) != string(want) || !reflect.DeepEqual(first.Rename, next.Rename) {
							failures <- fmt.Errorf("printing or continuation changed: %s %v", got, err)
							return
						}
					}
				}()
			}
			workers.Wait()
			close(failures)
			for err := range failures {
				t.Error(err)
			}
			after, err := s.Snapshot(ctx)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("provider changed source snapshot: %v", err)
			}
		})
	}
}
