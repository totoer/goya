package lsp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func findCompletion(t *testing.T, s *Session, name string, position protocol.Position, label string) (protocol.CompletionItem, bool) {
	t.Helper()
	r, err := s.Completion(context.Background(), name, position, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range r.List.Items {
		if item.Label == label {
			return item, true
		}
	}
	return protocol.CompletionItem{}, false
}

func applyCompletionForTest(t *testing.T, text string, item protocol.CompletionItem) string {
	t.Helper()
	edits := slices.Clone(item.AdditionalTextEdits)
	if item.TextEdit == nil {
		t.Fatal("completion requires a positioned edit")
	}
	rangeValue := item.TextEdit.Range
	if rangeValue == nil {
		rangeValue = item.TextEdit.Replace
	}
	if rangeValue == nil {
		t.Fatal("completion has no replacement range")
	}
	edits = append(edits, protocol.TextEdit{Range: *rangeValue, NewText: item.TextEdit.NewText})
	type change struct {
		start, end int
		text       string
	}
	var changes []change
	for _, edit := range edits {
		start, err := OffsetAt(text, edit.Range.Start)
		if err != nil {
			t.Fatal(err)
		}
		end, err := OffsetAt(text, edit.Range.End)
		if err != nil {
			t.Fatal(err)
		}
		changes = append(changes, change{start, end, edit.NewText})
	}
	slices.SortFunc(changes, func(a, b change) int {
		if a.start != b.start {
			return b.start - a.start
		}
		return b.end - a.end
	})
	previous := len(text) + 1
	for _, edit := range changes {
		if edit.end > previous || edit.start > edit.end {
			t.Fatal("overlapping/reversed completion edits")
		}
		text = text[:edit.start] + edit.text + text[edit.end:]
		previous = edit.start
	}
	return text
}

func TestAutoImportUsesUnsavedExportAndDetachedEditsPassCheck(t *testing.T) {
	ctx := context.Background()
	main := "export {};\nappe(42);\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{ConfigFile: "/tsconfig.json", Files: map[string]string{"/tsconfig.json": `{"files":["main.ts","helper.ts"]}`, "/main.ts": main, "/helper.ts": "export {};\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/main.ts", 1, main); err != nil {
		t.Fatal(err)
	}
	position := protocol.Position{Line: 1, Character: 4}
	if _, ok := findCompletion(t, s, "/main.ts", position, "append"); ok {
		t.Fatal("nonexistent export suggested")
	}
	if err := s.OpenDocument(ctx, "/helper.ts", 1, "/** Appends a value. */\nexport function append(value:number):number{return value;}\n"); err != nil {
		t.Fatal(err)
	}
	item, ok := findCompletion(t, s, "/main.ts", position, "append")
	if !ok {
		t.Fatal("unqueried unsaved export not indexed")
	}
	resolved, err := s.ResolveCompletion(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Item.AdditionalTextEdits) != 1 || resolved.Item.Detail == nil || !strings.Contains(*resolved.Item.Detail, "function append(value: number): number") || resolved.Item.Documentation == nil {
		t.Fatalf("auto-import details: %+v", resolved.Item)
	}
	updated := applyCompletionForTest(t, main, resolved.Item)
	if updated != "import { append } from \"./helper\";\n\nexport {};\nappend(42);\n" {
		t.Fatal(updated)
	}
	snapshot, err := s.ProjectSnapshot(ctx, "/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Project.Files["/main.ts"] = updated
	ds, err := goya.Check(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
	if err != nil || goya.HasTypeErrors(ds) {
		t.Fatalf("proposed edits failed Check: %v %+v", err, ds)
	}
	original, err := s.ReadDocument(ctx, "file:///main.ts")
	if err != nil || original.Text != main {
		t.Fatal("completion silently applied edits", err)
	}
	if err := s.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Text: updated}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveCompletion(ctx, item); !errors.Is(err, ErrContentModified) {
		t.Fatal("old export token survived accepted edit", err)
	}
	if err := s.CloseDocument(ctx, "/helper.ts"); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeDocument(ctx, "/main.ts", 3, []protocol.ContentChange{{Text: main}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := findCompletion(t, s, "/main.ts", position, "append"); ok {
		t.Fatal("closed overlay's export remained indexed")
	}
}

func TestAutoImportEnvironmentSwapDoesNotExecuteBindings(t *testing.T) {
	ctx := context.Background()
	calls := 0
	environment := func(name string) *goya.Environment {
		r := goya.NewRegistry()
		if err := r.RegisterModule("host-api", map[string]any{name: func(value float64) float64 { calls++; return value }}); err != nil {
			t.Fatal(err)
		}
		env, err := r.Freeze(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return env
	}
	one, two := environment("hostValue"), environment("otherValue")
	main := "export {};\nhostVa(42);\n"
	s, err := NewSession(ctx, SessionOptions{Environment: one, Project: goya.Project{Files: map[string]string{"/main.ts": main, "/tsconfig.json": `{"files":["main.ts"]}`}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	position := protocol.Position{Line: 1, Character: 6}
	item, ok := findCompletion(t, s, "/main.ts", position, "hostValue")
	if !ok {
		t.Fatal("host module export not indexed")
	}
	r, err := s.ResolveCompletion(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	updated := applyCompletionForTest(t, main, r.Item)
	snapshot, err := s.ProjectSnapshot(ctx, "/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Project.Files["/main.ts"] = updated
	program, ds, err := goya.Compile(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
	if err != nil || program == nil || goya.HasTypeErrors(ds) || calls != 0 {
		t.Fatalf("host auto import compile gate: %v %+v calls=%d", err, ds, calls)
	}
	if err := s.UpdateEnvironment(ctx, two); err != nil {
		t.Fatal(err)
	}
	if _, ok := findCompletion(t, s, "/main.ts", position, "hostValue"); ok {
		t.Fatal("old environment export remained indexed")
	}
	if _, err := s.ResolveCompletion(ctx, item); !errors.Is(err, ErrContentModified) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("editor executed a binding")
	}
}

func TestAutoImportPreparationKeepsExplicitConfiguredProject(t *testing.T) {
	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{ConfigFile: "/custom.json", Files: map[string]string{
		"/custom.json":   `{"files":["main.ts","desired.ts"]}`,
		"/tsconfig.json": `{"files":["main.ts","unrelated.ts"]}`,
		"/main.ts":       "export {};\ndesi\n",
		"/desired.ts":    "export function desired(value:number):number{return value;}",
		"/unrelated.ts":  "export function unrelated(value:string):string{return value;}",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	position := protocol.Position{Line: 1, Character: 4}
	item, ok := findCompletion(t, s, "/main.ts", position, "desired")
	if !ok {
		t.Fatal("auto-import preparation replaced the explicit project with the nearest config")
	}
	resolved, err := s.ResolveCompletion(ctx, item)
	if err != nil || resolved.Item.Detail == nil || !strings.Contains(*resolved.Item.Detail, "desired(value: number): number") {
		t.Fatalf("selected project details: %+v %v", resolved, err)
	}
	info, err := s.ProjectInfo(ctx, "/main.ts")
	if err != nil || info.ConfigFile != "/custom.json" {
		t.Fatalf("project changed during auto-import preparation: %+v %v", info, err)
	}
}

func TestInferredAutoImportSeesOtherUnqueriedOpenDocuments(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "default entries", true: "explicit executable entry"}[explicit], func(t *testing.T) {
			ctx := context.Background()
			project := goya.Project{Files: map[string]string{"/main.ts": "export {};\nappe(42);\n", "/helper.ts": "export {};\n"}}
			if explicit {
				project.EntryPoints = []string{"/main.ts"}
			}
			s, err := NewSession(ctx, SessionOptions{Project: project})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.OpenDocument(ctx, "/main.ts", 1, project.Files["/main.ts"]); err != nil {
				t.Fatal(err)
			}
			if err := s.OpenDocument(ctx, "/helper.ts", 1, "export function append(value:number):number{return value;}"); err != nil {
				t.Fatal(err)
			}
			item, ok := findCompletion(t, s, "/main.ts", protocol.Position{Line: 1, Character: 4}, "append")
			if !ok {
				t.Fatal("inferred index omitted unqueried overlay")
			}
			resolved, err := s.ResolveCompletion(ctx, item)
			if err != nil {
				t.Fatal(err)
			}
			updated := applyCompletionForTest(t, project.Files["/main.ts"], resolved.Item)
			snapshot, err := s.ProjectSnapshot(ctx, "/main.ts")
			if err != nil {
				t.Fatal(err)
			}
			snapshot.Project.Files["/main.ts"] = updated
			ds, err := goya.Check(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
			if err != nil || goya.HasTypeErrors(ds) {
				t.Fatalf("inferred auto-import edits failed Check: %v %+v", err, ds)
			}
		})
	}
}

func TestAutoImportExistingImportEditsProduceCheckedText(t *testing.T) {
	for _, tc := range []struct {
		name, main, helper, wanted, expected string
		position                             protocol.Position
	}{
		{"named", "import { first } from \"./helper\";\nappe(42);\n", "export const first=1;export function append(value:number):number{return value;}", "append", "import { append, first } from \"./helper\";\nappend(42);\n", protocol.Position{Line: 1, Character: 4}},
		{"namespace", "import * as helper from \"./helper\";\nappe(42);\n", "export function append(value:number):number{return value;}", "append", "import * as helper from \"./helper\";\nhelper.append(42);\n", protocol.Position{Line: 1, Character: 4}},
		{"type-only", "import type { Shape } from \"./helper\";\nnew Sha();\n", "export class Shape {value=42;}", "Shape", "import { Shape } from \"./helper\";\nnew Shape();\n", protocol.Position{Line: 1, Character: 7}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": tc.main, "/helper.ts": tc.helper, "/tsconfig.json": `{"files":["main.ts","helper.ts"]}`}}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			item, ok := findCompletion(t, s, "/main.ts", tc.position, tc.wanted)
			if !ok {
				t.Fatal("completion missing")
			}
			resolved, err := s.ResolveCompletion(ctx, item)
			if err != nil {
				t.Fatal(err)
			}
			updated := applyCompletionForTest(t, tc.main, resolved.Item)
			if updated != tc.expected {
				t.Fatalf("want %q, got %q", tc.expected, updated)
			}
			snapshot, err := s.ProjectSnapshot(ctx, "/main.ts")
			if err != nil {
				t.Fatal(err)
			}
			snapshot.Project.Files["/main.ts"] = updated
			ds, err := goya.Check(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
			if err != nil || goya.HasTypeErrors(ds) {
				t.Fatalf("completion edits failed Check: %v %+v", err, ds)
			}
		})
	}
}

func TestWireAutoImportObservesHostEnvironmentEvents(t *testing.T) {
	ctx := context.Background()
	calls := 0
	makeEnvironment := func(name string) *goya.Environment {
		r := goya.NewRegistry()
		if err := r.RegisterModule("host-api", map[string]any{name: func() float64 { calls++; return 42 }}); err != nil {
			t.Fatal(err)
		}
		env, err := r.Freeze(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return env
	}
	s, err := NewSession(ctx, SessionOptions{Environment: makeEnvironment("hostValue"), Project: goya.Project{Files: map[string]string{"/main.ts": "export {};\nhostVa();\n", "/tsconfig.json": `{"files":["main.ts"]}`}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := startTestConnection(t, nil, 8, 16, testClientSetup{session: s})
	request := protocol.CompletionParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, Position: protocol.Position{Line: 1, Character: 6}}
	c.send(t, 2, "textDocument/completion", request)
	var list protocol.CompletionList
	if err := decodeObject(c.read(t)["result"], &list); err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(list.Items, func(item protocol.CompletionItem) bool { return item.Label == "hostValue" })
	if index < 0 {
		t.Fatal("wire did not index frozen environment")
	}
	item := list.Items[index]
	c.send(t, 3, "completionItem/resolve", item)
	var resolved protocol.CompletionItem
	if err := decodeObject(c.read(t)["result"], &resolved); err != nil || len(resolved.AdditionalTextEdits) == 0 {
		t.Fatalf("wire host edits: %+v %v", resolved, err)
	}
	if err := s.UpdateEnvironment(ctx, makeEnvironment("otherValue")); err != nil {
		t.Fatal(err)
	}
	c.send(t, 4, "completionItem/resolve", item)
	assertResponseCode(t, c.read(t), "4", -32801)
	c.send(t, 5, "textDocument/completion", request)
	if err := decodeObject(c.read(t)["result"], &list); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(list.Items, func(item protocol.CompletionItem) bool { return item.Label == "hostValue" }) {
		t.Fatal("wire retained removed environment export")
	}
	if calls != 0 {
		t.Fatal("wire completion executed a host binding")
	}
	c.finish(t)
}
