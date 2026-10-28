package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/lsp/protocol"
)

const actionTestText = "interface Shape { run(input: number): string; }\nclass Box implements Shape {}\nclass Other implements Shape {}\n"

var actionTestRange = protocol.Range{Start: protocol.Position{Line: 1, Character: 6}, End: protocol.Position{Line: 1, Character: 9}}

func actionTestSession(t *testing.T, limits *RetentionLimits) *Session {
	t.Helper()
	s, err := NewSession(context.Background(), SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": actionTestText}, EntryPoints: []string{"/main.ts"}}, ResultCacheLimits: limits, DocumentURIs: map[string]protocol.DocumentURI{"/main.ts": "memory:///main.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.OpenDocument(context.Background(), "/main.ts", 3, actionTestText); err != nil {
		t.Fatal(err)
	}
	return s
}
func enumerateTestActions(t *testing.T, s *Session) CodeActionResult {
	t.Helper()
	kinds := []protocol.CodeActionKind{protocol.CodeActionQuickFix}
	// Force the current-diagnostics branch after each enumeration, including
	// tests which change only cache retention rather than source generations.
	s.mu.Lock()
	clear(s.diagnosticsReady)
	s.mu.Unlock()
	r, err := s.CodeActions(context.Background(), "/main.ts", actionTestRange, protocol.CodeActionContext{Only: &kinds, Diagnostics: []protocol.Diagnostic{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Actions) != 2 || r.Actions[0].IsPreferred == nil || !*r.Actions[0].IsPreferred || r.Actions[1].IsPreferred == nil || *r.Actions[1].IsPreferred || r.Actions[0].Edit == nil || r.Actions[1].Edit != nil || len(r.Actions[1].Data) == 0 {
		t.Fatalf("actions: %+v", r)
	}
	return r
}
func TestCodeActionsResolveOwnedProposalChecksCopyAndNeverMutates(t *testing.T) {
	ctx := context.Background()
	s := actionTestSession(t, nil)
	before, _ := s.Snapshot(ctx)
	r := enumerateTestActions(t, s)
	if err := s.ValidateEdits(ctx, *r.Proposals[0]); err != nil {
		t.Fatal(err)
	}
	r.Actions[0].Edit.DocumentChanges[0].TextDocumentEdit.Edits[0].NewText = "tampered"
	if err := s.ValidateEdits(ctx, *r.Proposals[0]); err != nil {
		t.Fatalf("action aliases proposal: %v", err)
	}
	lazy := r.Actions[1]
	lazy.Title = "tampered"
	lazy.Kind = "source.evil"
	lazy.Command = &protocol.Command{Title: "evil", Command: "evil"}
	resolved, err := s.ResolveCodeAction(ctx, lazy)
	if err != nil || resolved.Edits == nil || resolved.Action.Title == "tampered" || resolved.Action.Command.Command != "_typescript.applyFixAllCodeAction" {
		t.Fatalf("resolve: %+v %v", resolved, err)
	}
	if err = s.ValidateEdits(ctx, *resolved.Edits); err != nil {
		t.Fatal(err)
	}
	copy := before.Project
	copy.Files = maps.Clone(copy.Files)
	edits := resolved.Edits.Edit.DocumentChanges[0].TextDocumentEdit
	if edits.TextDocument.URI != "memory:///main.ts" || edits.TextDocument.Version == nil || *edits.TextDocument.Version != 3 {
		t.Fatalf("identity/version: %+v", edits.TextDocument)
	}
	copy.Files["/main.ts"] = applyFormattingCopy(t, copy.Files["/main.ts"], edits.Edits)
	if strings.Count(copy.Files["/main.ts"], "Method not implemented.") != 2 {
		t.Fatalf("combined fix: %s", copy.Files["/main.ts"])
	}
	if ds, checkErr := goya.Check(ctx, copy, before.Environment, before.CompilerOptions); checkErr != nil || goya.HasTypeErrors(ds) {
		t.Fatalf("fixed copy: %+v %v", ds, checkErr)
	}
	resolved.Action.Edit.DocumentChanges[0].TextDocumentEdit.Edits[0].NewText = "tampered"
	if err = s.ValidateEdits(ctx, *resolved.Edits); err != nil {
		t.Fatalf("resolved DTO aliases proposal: %v", err)
	}
	after, _ := s.Snapshot(ctx)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("code action mutated snapshot")
	}
	if err = s.ChangeDocument(ctx, "/main.ts", 4, []protocol.ContentChange{{Text: actionTestText}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveCodeAction(ctx, lazy); !errors.Is(err, ErrContentModified) {
		t.Fatalf("stale: %v", err)
	}
	if err = s.ValidateEdits(ctx, *resolved.Edits); !errors.Is(err, ErrContentModified) {
		t.Fatalf("stale proposal: %v", err)
	}
}
func TestCodeActionsResolveForeignExpiredDisabledAndCancellation(t *testing.T) {
	ctx := context.Background()
	s := actionTestSession(t, nil)
	r := enumerateTestActions(t, s)
	other := actionTestSession(t, nil)
	if _, err := other.ResolveCodeAction(ctx, r.Actions[1]); !errors.Is(err, &Error{Code: CodeInvalidHandle}) {
		t.Fatalf("foreign: %v", err)
	}

	var issued protocol.CodeActionData
	if err := json.Unmarshal(r.Actions[1].Data, &issued); err != nil {
		t.Fatal(err)
	}
	issued.Index = 0
	forged := cloneCodeAction(r.Actions[1])
	forged.Data, _ = json.Marshal(issued)
	if _, err := s.ResolveCodeAction(ctx, forged); !errors.Is(err, &Error{Code: CodeInvalidHandle}) {
		t.Fatalf("unissued eager selector: %v", err)
	}
	invalid := cloneCodeAction(r.Actions[1])
	invalid.Data = json.RawMessage(`{"handle":"invalid","index":0}`)
	if _, err := s.ResolveCodeAction(ctx, invalid); !errors.Is(err, &Error{Code: CodeInvalidHandle}) {
		t.Fatalf("invalid: %v", err)
	}
	limited := actionTestSession(t, &RetentionLimits{MaxEntries: 1, MaxBytes: 1 << 20})
	expired := enumerateTestActions(t, limited)
	// Eager edit retention evicts the older continuation batch at this limit.
	if _, err := limited.ResolveCodeAction(ctx, expired.Actions[1]); !errors.Is(err, ErrResultExpired) {
		t.Fatalf("eviction: %v", err)
	}
	disabled := protocol.CodeAction{Title: "Unavailable", Kind: "refactor.extract", Disabled: &protocol.CodeActionDisabled{Reason: "Selection is not applicable"}}
	resolved, err := s.ResolveCodeAction(ctx, disabled)
	if err != nil || !reflect.DeepEqual(resolved.Action, disabled) || resolved.Edits != nil {
		t.Fatalf("disabled: %+v %v", resolved, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ResolveCodeAction(cancelled, r.Actions[1]); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveCodeAction(ctx, r.Actions[1]); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed: %v", err)
	}
}
func TestCodeActionFormattingProviderRunsOutsideLocksAndLease(t *testing.T) {
	ctx := context.Background()
	var s *Session
	provider := func(ctx context.Context, uri protocol.DocumentURI) (*protocol.DocumentFormattingOptions, error) {
		if _, err := s.ReadDocument(ctx, uri); err != nil {
			return nil, err
		}
		if _, err := s.FoldingRanges(ctx, "/main.ts"); err != nil {
			return nil, err
		}
		size := uint32(2)
		spaces := true
		return &protocol.DocumentFormattingOptions{TabSize: &size, InsertSpaces: &spaces}, nil
	}
	var err error
	s, err = NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": actionTestText}}, FormattingOptionsProvider: provider})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := enumerateTestActions(t, s)
	if !strings.HasPrefix(r.Actions[0].Edit.DocumentChanges[0].TextDocumentEdit.Edits[0].NewText, "\n  run") {
		t.Fatalf("per-document formatting: %+v", r.Actions[0].Edit)
	}
	if _, err = s.ResolveCodeAction(ctx, r.Actions[1]); err != nil {
		t.Fatal(err)
	}
}
func TestCodeActionInvalidRangesAndOptions(t *testing.T) {
	ctx := context.Background()
	s := actionTestSession(t, nil)
	backwards := protocol.Range{Start: actionTestRange.End, End: actionTestRange.Start}
	if _, err := s.CodeActions(ctx, "/main.ts", backwards, protocol.CodeActionContext{}, nil); !errors.Is(err, &Error{Code: CodeInvalidOptions}) {
		t.Fatalf("range: %v", err)
	}
	invalid := protocol.CodeActionTriggerKind(5)
	if _, err := s.CodeActions(ctx, "/main.ts", actionTestRange, protocol.CodeActionContext{TriggerKind: &invalid}, nil); !errors.Is(err, &Error{Code: CodeInvalidOptions}) {
		t.Fatalf("trigger: %v", err)
	}
	enumerateTestActions(t, s)
	if _, err := s.ResolveCodeAction(ctx, protocol.CodeAction{Data: json.RawMessage(`{"handle":3}`)}); !errors.Is(err, &Error{Code: CodeInvalidHandle}) {
		t.Fatalf("data: %v", err)
	}
}

func TestCodeActionWireStaleInvalidAndRecovery(t *testing.T) {
	s := actionTestSession(t, nil)
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s, capabilities: map[string]any{"textDocument": map[string]any{"codeAction": map[string]any{"resolveSupport": map[string]any{"properties": []string{"edit"}}}}}})
	kinds := []protocol.CodeActionKind{protocol.CodeActionQuickFix}
	params := protocol.CodeActionParams{TextDocument: protocol.TextDocumentIdentifier{URI: "memory:///main.ts"}, Range: actionTestRange, Context: protocol.CodeActionContext{Only: &kinds, Diagnostics: []protocol.Diagnostic{}}}
	c.send(t, "actions", "textDocument/codeAction", params)
	var actions []protocol.CodeAction
	message := c.read(t)
	if err := json.Unmarshal(message["result"], &actions); err != nil || len(actions) != 2 {
		t.Fatalf("enumerate: %+v %v", message, err)
	}
	c.send(t, nil, "textDocument/didChange", protocol.DidChangeTextDocumentParams{TextDocument: protocol.VersionedTextDocumentIdentifier{URI: "memory:///main.ts", Version: 4}, ContentChanges: []protocol.ContentChange{{Text: actionTestText}}})
	c.send(t, "stale", "codeAction/resolve", actions[1])
	assertResponseCode(t, c.read(t), `"stale"`, -32801)
	invalid := cloneCodeAction(actions[1])
	invalid.Data = json.RawMessage(`{"handle":"foreign","index":0}`)
	c.send(t, "bad", "codeAction/resolve", invalid)
	assertResponseCode(t, c.read(t), `"bad"`, -32602)
	c.send(t, "missing", "textDocument/codeAction", map[string]any{})
	assertResponseCode(t, c.read(t), `"missing"`, -32602)
	c.send(t, "again", "textDocument/codeAction", params)
	message = c.read(t)
	if err := json.Unmarshal(message["result"], &actions); err != nil || len(actions) != 2 {
		t.Fatalf("recovery: %+v %v", message, err)
	}
	c.finish(t)
}
func TestCodeActionFrozenEditBoundaryAndPreferenceCompetition(t *testing.T) {
	ctx := context.Background()
	s := actionTestSession(t, nil)
	snapshot, _ := s.Snapshot(ctx)
	q := &querySnapshot{snapshot: snapshot}
	_, _, err := q.codeFixChanges([]ls.ReferenceFileCodeChanges{{FileName: libraryPrefix + "lib.es5.d.ts"}})
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("frozen library: %v", err)
	}
	a := []codeActionTarget{{fixName: "import"}, {fixName: "import"}}
	if preferredQuickFix(a, 0) || preferredQuickFix(a, 1) {
		t.Fatal("ambiguous imports preferred")
	}
	a = []codeActionTarget{{fixName: "import"}, {fixName: "fixClassIncorrectlyImplementsInterface"}, {fixName: "fixClassIncorrectlyImplementsInterface", fixID: "all"}}
	if preferredQuickFix(a, 0) || !preferredQuickFix(a, 1) || preferredQuickFix(a, 2) {
		t.Fatal("priority/fix-all preference")
	}
}

func TestCodeActionHandlesInvalidateWithConfigurationAndEnvironment(t *testing.T) {
	for _, update := range []struct {
		name string
		run  func(*Session) error
	}{
		{"configuration", func(s *Session) error { return s.Configure(context.Background(), WorkspaceConfiguration{}) }},
		{"environment", func(s *Session) error { return s.UpdateEnvironment(context.Background(), nil) }},
	} {
		t.Run(update.name, func(t *testing.T) {
			s := actionTestSession(t, nil)
			r := enumerateTestActions(t, s)
			if err := update.run(s); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ResolveCodeAction(context.Background(), r.Actions[1]); !errors.Is(err, ErrContentModified) {
				t.Fatalf("continuation: %v", err)
			}
			if err := s.ValidateEdits(context.Background(), *r.Proposals[0]); !errors.Is(err, ErrContentModified) {
				t.Fatalf("proposal: %v", err)
			}
		})
	}
}

// The reference scans foreign declaration lists with the caller's text and
// returns no actions for this catalog. The query must leave both sources intact.
func TestConvertConstToLetCrossFileNoResultPreservesCatalog(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{"/main.ts": "first = 2;\nsecond = 3;\n", "/first.d.ts": "declare const first: number;\n", "/second.d.ts": "declare const second: number;\n"}
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: files, EntryPoints: []string{"/main.ts", "/first.d.ts", "/second.d.ts"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []protocol.CodeActionKind{protocol.CodeActionQuickFix}
	result, err := s.CodeActions(ctx, "/main.ts", protocol.Range{End: protocol.Position{Line: 2}}, protocol.CodeActionContext{Only: &kinds, Diagnostics: []protocol.Diagnostic{}}, nil)
	if err != nil || len(result.Actions) != 0 {
		t.Fatalf("cross-file actions: %+v %v", result, err)
	}
	after, err := s.Snapshot(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("cross-file query changed catalog: %v", err)
	}
}

type spellingFrozenRecord struct{ Length float64 }

func TestSafeSpellingActionSurvivesFrozenMissingMemberCandidates(t *testing.T) {
	ctx := context.Background()
	calls := 0
	registry := goya.NewRegistry()
	if err := registry.Register("host", func() spellingFrozenRecord { calls++; return spellingFrozenRecord{Length: 1} }); err != nil {
		t.Fatal(err)
	}
	environment, err := registry.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	text := "export const value = host().Lenght;\n"
	session, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": text}}, Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.OpenDocument(ctx, "/main.ts", 1, text); err != nil {
		t.Fatal(err)
	}
	kinds := []protocol.CodeActionKind{protocol.CodeActionQuickFix}
	result, err := session.CodeActions(ctx, "/main.ts", protocol.Range{End: protocol.Position{Character: uint32(len(text) - 1)}}, protocol.CodeActionContext{Diagnostics: []protocol.Diagnostic{}, Only: &kinds}, nil)
	if err != nil || len(result.Actions) != 1 || result.Actions[0].Title != "Change spelling to 'Length'" {
		t.Fatalf("safe spelling: %+v %v", result, err)
	}
	if result.Proposals[0] == nil {
		t.Fatal("missing spelling edits")
	}
	for _, change := range result.Proposals[0].Edit.DocumentChanges {
		if change.TextDocumentEdit == nil || change.TextDocumentEdit.TextDocument.URI != "file:///main.ts" {
			t.Fatalf("frozen edit: %+v", change)
		}
	}
	if calls != 0 {
		t.Fatal("language service invoked the binding")
	}
}
