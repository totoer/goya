package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestHierarchyItemsAreOpaqueOwnedAndGenerationBound(t *testing.T) {
	ctx := context.Background()
	main := "import {target} from './helper';\nexport function caller() { target(); }\ncaller();\n"
	helper := "/** @deprecated */\nexport function target() {}\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": main, "/helper.ts": helper}}, DocumentURIs: map[string]protocol.DocumentURI{"/helper.ts": "memory:///helper.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/main.ts", 1, main); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PrepareCallHierarchy(ctx, "/main.ts", protocol.Position{Line: 1, Character: 28})
	if err != nil || len(prepared.Items) != 1 || prepared.Version == nil || *prepared.Version != 1 {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	item := prepared.Items[0]
	if item.URI != "memory:///helper.ts" || len(item.Tags) != 1 || item.Data == nil {
		t.Fatal(item)
	}
	// Reusable data controls the original target. Presentation fields are owned
	// by the caller and cannot redirect the server to another document/position.
	item.Name = "tampered"
	item.URI = "file:///untrusted.ts"
	item.SelectionRange.Start.Line = 99
	item.Tags[0] = 99
	incoming, err := s.IncomingCalls(ctx, item)
	if err != nil || len(incoming.Calls) != 1 || incoming.Calls[0].From.Name != "caller" || incoming.Calls[0].From.URI != "file:///main.ts" {
		t.Fatalf("incoming: %+v %v", incoming, err)
	}
	nested := incoming.Calls[0].From
	outgoing, err := s.OutgoingCalls(ctx, nested)
	if err != nil || len(outgoing.Calls) != 1 || outgoing.Calls[0].To.Name != "target" || outgoing.Calls[0].To.URI != "memory:///helper.ts" || outgoing.Calls[0].To.Tags[0] != protocol.SymbolDeprecated {
		t.Fatalf("nested outgoing: %+v %v", outgoing, err)
	}
	outgoing.Calls[0].FromRanges[0].Start.Line = 99
	outgoing.Calls[0].To.Name = "tampered"
	again, err := s.OutgoingCalls(ctx, nested)
	if err != nil || again.Calls[0].FromRanges[0].Start.Line == 99 || again.Calls[0].To.Name != "target" {
		t.Fatalf("DTO mutation: %+v %v", again, err)
	}
	work, err := s.prepareHierarchyCalls(ctx, nested, false)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.OutgoingCalls(cancelled, nested); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	foreign, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": main, "/helper.ts": helper}}})
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	if _, err := foreign.IncomingCalls(ctx, item); !errors.Is(err, &Error{Code: CodeInvalidHandle}) {
		t.Fatalf("foreign item: %v", err)
	}
	if err := s.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Text: "\n" + main}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OutgoingCalls(ctx, nested); !errors.Is(err, ErrContentModified) {
		t.Fatalf("stale item: %v", err)
	}
	if _, _, err := work(ctx); !errors.Is(err, ErrContentModified) {
		t.Fatalf("request captured before update: %v", err)
	}
	current, err := s.PrepareCallHierarchy(ctx, "/main.ts", protocol.Position{Line: 2, Character: 28})
	if err != nil || len(current.Items) != 1 || current.Generation <= prepared.Generation {
		t.Fatalf("fresh item: %+v %v", current, err)
	}
}

func TestHierarchyBatchesStayWithinRetentionLimits(t *testing.T) {
	ctx := context.Background()
	text := "export function target() {}\n"
	for i := 0; i < 100; i++ {
		text += fmt.Sprintf("function caller%d() { target(); }\n", i)
	}
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": text}}, ResultCacheLimits: &RetentionLimits{MaxEntries: 2, MaxBytes: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	prepared, err := s.PrepareCallHierarchy(ctx, "/main.ts", protocol.Position{Character: 18})
	if err != nil || len(prepared.Items) != 1 {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	result, err := s.IncomingCalls(ctx, prepared.Items[0])
	if err != nil || len(result.Calls) != 100 {
		t.Fatalf("batch: %d %v", len(result.Calls), err)
	}
	if len(s.results.entries) != 2 {
		t.Fatalf("each item consumed an independent cache entry: %d", len(s.results.entries))
	}
	for _, index := range []int{0, 99} {
		item := result.Calls[index].From
		if item.Data.Handle != result.Calls[0].From.Data.Handle || item.Data.Index != index {
			t.Fatal("batch handle/index", item)
		}
		empty, err := s.IncomingCalls(ctx, item)
		if err != nil || empty.Calls == nil || len(empty.Calls) != 0 {
			t.Fatalf("batch item %d: %+v %v", index, empty, err)
		}
	}
	if _, err := s.IncomingCalls(ctx, protocol.CallHierarchyItem{}); !errors.Is(err, &Error{Code: CodeInvalidHandle}) {
		t.Fatalf("missing handle: %v", err)
	}
	// A subsequent result evicts the oldest batch at the configured entry cap.
	if _, err := s.PrepareCallHierarchy(ctx, "/main.ts", protocol.Position{Line: 1, Character: 12}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IncomingCalls(ctx, prepared.Items[0]); !errors.Is(err, ErrResultExpired) {
		t.Fatalf("evicted item: %v", err)
	}
}

func TestHierarchyWireStaleAndMalformedItemsRemainRecoverable(t *testing.T) {
	ctx := context.Background()
	text := "export function target() {}\nfunction caller() { target(); }\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": text}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/main.ts", 1, text); err != nil {
		t.Fatal(err)
	}
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s})
	c.send(t, "prepare", "textDocument/prepareCallHierarchy", protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, Position: protocol.Position{Character: 18}})
	message := c.read(t)
	var items []protocol.CallHierarchyItem
	if err := json.Unmarshal(message["result"], &items); err != nil || len(items) != 1 {
		t.Fatalf("wire prepare: %+v %v", message, err)
	}
	if err := s.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Text: text + "target();\n"}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, method string
		params     any
		code       int32
	}{
		{"stale", "callHierarchy/incomingCalls", protocol.CallHierarchyCallsParams{Item: items[0]}, -32801},
		{"missing", "callHierarchy/outgoingCalls", map[string]any{}, -32602},
		{"forged", "callHierarchy/outgoingCalls", protocol.CallHierarchyCallsParams{Item: protocol.CallHierarchyItem{}}, -32602},
	} {
		c.send(t, tc.id, tc.method, tc.params)
		message = c.read(t)
		var failure ResponseError
		if err := json.Unmarshal(message["error"], &failure); err != nil || failure.Code != tc.code {
			t.Fatalf("%s: %+v %v", tc.id, message, err)
		}
	}
	c.send(t, "again", "textDocument/prepareCallHierarchy", protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, Position: protocol.Position{Character: 18}})
	message = c.read(t)
	if err := json.Unmarshal(message["result"], &items); err != nil || len(items) != 1 {
		t.Fatalf("wire recovery: %+v %v", message, err)
	}
	c.finish(t)
}
