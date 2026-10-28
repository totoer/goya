package lsp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func nextDiagnosticEvent(t *testing.T, w *DiagnosticStream) DiagnosticEvent {
	t.Helper()
	select {
	case event, ok := <-w.Events():
		if !ok {
			t.Fatal("diagnostic stream closed early")
		}
		if event.Error != nil {
			t.Fatal(event.Error)
		}
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("diagnostic event timed out")
		return DiagnosticEvent{}
	}
}

func expectDiagnosticKind(t *testing.T, w *DiagnosticStream, kind DiagnosticEventKind) DiagnosticEvent {
	t.Helper()
	event := nextDiagnosticEvent(t, w)
	if event.Kind != kind {
		t.Fatalf("event=%+v, want %s", event, kind)
	}
	return event
}

func TestDiagnosticStreamPublishesFiltersEagerClearAndClose(t *testing.T) {
	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": `export const value:number="wrong";`}}, EditorOptions: EditorOptions{Diagnostics: DiagnosticSettings{EagerClear: true}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/main.ts", 1, `export const value:number="wrong";`); err != nil {
		t.Fatal(err)
	}
	w, err := s.WatchDiagnostics(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if e := expectDiagnosticKind(t, w, DiagnosticEventProgress); e.Progress.Kind != "begin" {
		t.Fatal(e)
	}
	e := expectDiagnosticKind(t, w, DiagnosticEventResult)
	if e.Diagnostics.Version == nil || *e.Diagnostics.Version != 1 || len(e.Diagnostics.Items) != 1 || e.Diagnostics.Items[0].Code != 2322 {
		t.Fatalf("bad initial diagnostics: %+v", e.Diagnostics)
	}
	if e := expectDiagnosticKind(t, w, DiagnosticEventProgress); e.Progress.Kind != "end" || e.Progress.Cancelled {
		t.Fatal(e)
	}
	if err := s.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Text: `export const value:number=42;`}}); err != nil {
		t.Fatal(err)
	}
	if e := expectDiagnosticKind(t, w, DiagnosticEventClear); !e.Eager || len(e.Diagnostics.Items) != 0 {
		t.Fatal(e)
	}
	expectDiagnosticKind(t, w, DiagnosticEventProgress)
	e = expectDiagnosticKind(t, w, DiagnosticEventResult)
	if *e.Diagnostics.Version != 2 || len(e.Diagnostics.Items) != 0 {
		t.Fatal(e.Diagnostics)
	}
	expectDiagnosticKind(t, w, DiagnosticEventProgress)
	if err := s.Configure(ctx, WorkspaceConfiguration{Diagnostics: &DiagnosticConfiguration{IgnoredCodes: []int32{2322}}}); err != nil {
		t.Fatal(err)
	}
	expectDiagnosticKind(t, w, DiagnosticEventProgress)
	if e = expectDiagnosticKind(t, w, DiagnosticEventResult); len(e.Diagnostics.Items) != 0 {
		t.Fatal(e)
	}
	expectDiagnosticKind(t, w, DiagnosticEventProgress)
	if err := s.CloseDocument(ctx, "/main.ts"); err != nil {
		t.Fatal(err)
	}
	if e = expectDiagnosticKind(t, w, DiagnosticEventClear); e.Eager || len(e.Diagnostics.Items) != 0 || e.URI != "file:///main.ts" {
		t.Fatal(e)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-w.Events(); ok {
		t.Fatal("events left open")
	}
}

func slowDiagnosticAnalyzer(entered, release, finished chan struct{}) diagnosticAnalyzer {
	return func(ctx context.Context, q *querySnapshot, name string, _ *DiagnosticOptions) (DiagnosticResult, error) {
		result := DiagnosticResult{Generation: q.snapshot.Generation, URI: sourceURI(name, q.snapshot.Environment, q.mappings), Version: q.version(name), Items: []protocol.Diagnostic{}}
		if *result.Version == 1 {
			close(entered)
			<-release // deliberately finish after cancellation/newer analysis
			result.Items = append(result.Items, protocol.Diagnostic{Code: 2322, Message: "obsolete error"})
			close(finished)
		}
		return result, nil
	}
}

func TestDiagnosticStreamNeverPublishesLateOldAnalysis(t *testing.T) {
	ctx := context.Background()
	s := testSession(t, nil)
	if err := s.OpenDocument(ctx, "/main.ts", 1, "bad"); err != nil {
		t.Fatal(err)
	}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	w, err := s.watchDiagnostics(ctx, nil, slowDiagnosticAnalyzer(entered, release, finished))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	<-entered
	initial := expectDiagnosticKind(t, w, DiagnosticEventProgress)
	if err := s.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Text: "corrected"}}); err != nil {
		t.Fatal(err)
	}
	e := expectDiagnosticKind(t, w, DiagnosticEventProgress)
	if e.Generation != initial.Generation || e.Progress.Kind != "end" || !e.Progress.Cancelled {
		t.Fatal(e)
	}
	expectDiagnosticKind(t, w, DiagnosticEventProgress)
	e = expectDiagnosticKind(t, w, DiagnosticEventResult)
	if *e.Diagnostics.Version != 2 || len(e.Diagnostics.Items) != 0 {
		t.Fatal(e.Diagnostics)
	}
	expectDiagnosticKind(t, w, DiagnosticEventProgress)
	close(release)
	released = true
	<-finished
	// A subsequent generation acts as a barrier; no obsolete result may precede it.
	if err := s.ChangeDocument(ctx, "/main.ts", 3, []protocol.ContentChange{{Text: "still corrected"}}); err != nil {
		t.Fatal(err)
	}
	expectDiagnosticKind(t, w, DiagnosticEventProgress)
	e = expectDiagnosticKind(t, w, DiagnosticEventResult)
	if *e.Diagnostics.Version != 3 || len(e.Diagnostics.Items) != 0 {
		t.Fatal(e.Diagnostics)
	}
	expectDiagnosticKind(t, w, DiagnosticEventProgress)
}

func TestDiagnosticStreamBackpressureLimitsAndSessionCancellation(t *testing.T) {
	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{MaxDiagnosticSubscriptions: 1, Project: goya.Project{Files: map[string]string{"/main.ts": ""}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/main.ts", 1, ""); err != nil {
		t.Fatal(err)
	}
	w, err := s.WatchDiagnostics(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WatchDiagnostics(ctx, nil); err == nil {
		t.Fatal("subscription limit ignored")
	}
	// No consumer: updates and shutdown must not depend on draining events.
	for version := int32(2); version < 100; version++ {
		if err := s.ChangeDocument(ctx, "/main.ts", version, []protocol.ContentChange{{Text: "export const n=42;"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	w, err = s.WatchDiagnostics(ctx, nil)
	if err != nil {
		t.Fatal("subscription slot was not released", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-w.Events(); ok {
		t.Fatal("session close did not close stream")
	}
	if _, err := s.WatchDiagnostics(ctx, nil); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}
