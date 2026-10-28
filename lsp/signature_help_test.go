package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestSignatureHelpOwnsContextAndCapturedGeneration(t *testing.T) {
	ctx := context.Background()
	source := "declare function choose(value: number): number;\ndeclare function choose(value: string): string;\nchoose(\"a\");\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": source}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/main.ts", 1, source); err != nil {
		t.Fatal(err)
	}
	q, err := s.captureQuery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	options := &SignatureHelpRequestOptions{Context: &protocol.SignatureHelpContext{TriggerKind: protocol.SignatureHelpContentChange, IsRetrigger: true, ActiveSignatureHelp: &protocol.SignatureHelp{ActiveSignature: new(uint32(0)), Signatures: []protocol.SignatureInformation{{Label: "choose(value: number): number"}}}}}
	work, err := s.prepareSignatureHelp(q, "/main.ts", protocol.Position{Line: 2, Character: 7}, options)
	if err != nil {
		t.Fatal(err)
	}
	// Both the prior signature and the trigger pointer are caller owned.
	options.Context.ActiveSignatureHelp.Signatures[0].Label = "mutated"
	options.Context.IsRetrigger = false
	next := "declare function choose(flag: boolean): boolean;\nchoose(true);\n"
	if err := s.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Text: next}}); err != nil {
		t.Fatal(err)
	}
	old, err := work(ctx)
	if err != nil || old.Generation != q.snapshot.Generation || old.Version == nil || *old.Version != 1 || old.Help == nil || len(old.Help.Signatures) != 2 || old.Help.ActiveSignature == nil || *old.Help.ActiveSignature != 0 {
		t.Fatalf("captured overload/context: %+v %v", old, err)
	}
	current, err := s.SignatureHelp(ctx, "/main.ts", protocol.Position{Line: 1, Character: 7}, nil)
	if err != nil || current.Generation <= old.Generation || current.Version == nil || *current.Version != 2 || current.Help == nil || len(current.Help.Signatures) != 1 || current.Help.Signatures[0].Label != "choose(flag: boolean): boolean" {
		t.Fatalf("updated signature: %+v %v", current, err)
	}
	current.Help.Signatures[0].Label = "caller mutation"
	again, err := s.SignatureHelp(ctx, "/main.ts", protocol.Position{Line: 1, Character: 7}, nil)
	if err != nil || again.Help == nil || again.Help.Signatures[0].Label != "choose(flag: boolean): boolean" {
		t.Fatal("returned signature aliases checker state", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.SignatureHelp(cancelled, "/main.ts", protocol.Position{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.SignatureHelp(ctx, "/main.ts", protocol.Position{}, &SignatureHelpRequestOptions{Context: &protocol.SignatureHelpContext{TriggerKind: 4}}); !errors.Is(err, &Error{Code: CodeInvalidOptions}) {
		t.Fatal(err)
	}
}

func TestSignatureHelpMalformedWireRequestDoesNotBreakConnection(t *testing.T) {
	s, err := NewSession(context.Background(), SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "declare function id(value: number): number;\nid(1);\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s, capabilities: map[string]any{"textDocument": map[string]any{"signatureHelp": map[string]any{"signatureInformation": map[string]any{"activeParameterSupport": true, "parameterInformation": map[string]any{"labelOffsetSupport": true}, "documentationFormat": []string{"plaintext"}}}}}})
	for i, params := range []any{map[string]any{}, map[string]any{"textDocument": map[string]any{"uri": "file:///main.ts"}, "position": protocol.Position{}, "context": map[string]any{"triggerKind": 0, "isRetrigger": false}}} {
		c.send(t, i+10, "textDocument/signatureHelp", params)
		response := c.read(t)
		var e ResponseError
		if err := json.Unmarshal(response["error"], &e); err != nil || e.Code != -32602 {
			t.Fatalf("invalid request: %s %v", response["error"], err)
		}
	}
	p := protocol.SignatureHelpParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, Position: protocol.Position{Line: 1, Character: 3}}
	c.send(t, "valid", "textDocument/signatureHelp", p)
	response := c.read(t)
	var help protocol.SignatureHelp
	if err := json.Unmarshal(response["result"], &help); err != nil || len(help.Signatures) != 1 || help.Signatures[0].Parameters[0].Label != "value: number" {
		t.Fatalf("recovered response: %s %v", response["result"], err)
	}
	c.finish(t)
}

func TestDocumentationMapsVirtualLinksAndPreservesExternalLinks(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{"/types.ts": "/** Coordinates. */\nexport type Point = {x:number};\n", "/main.ts": "import type {Point} from './types';\n/** Return {@link Point}, see {@link https://example.com | docs}. */\nexport function point():Point{return {x:0};}\npoint();\n"}
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: files}, DocumentURIs: map[string]protocol.DocumentURI{"/types.ts": "mem://workspace/types%20source.ts", "/main.ts": "mem://workspace/main.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := s.Hover(ctx, "/main.ts", protocol.Position{Line: 3, Character: 2}, nil)
	if err != nil || h.Hover == nil {
		t.Fatal(err)
	}
	for _, link := range []string{"[Point](mem://workspace/types%20source.ts#L2%2C1)", "[docs](https://example.com)"} {
		if !strings.Contains(h.Hover.Contents.Value, link) {
			t.Fatalf("missing %s in %s", link, h.Hover.Contents.Value)
		}
	}
	sig, err := s.SignatureHelp(ctx, "/main.ts", protocol.Position{Line: 3, Character: 6}, nil)
	if err != nil || sig.Help == nil || sig.Help.Signatures[0].Documentation == nil {
		t.Fatal(err)
	}
	documentation := sig.Help.Signatures[0].Documentation.MarkupContent.Value
	if !strings.Contains(documentation, "mem://workspace/types%20source.ts#L2%2C1") {
		t.Fatal(documentation)
	}
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s})
	c.send(t, "links", "textDocument/signatureHelp", protocol.SignatureHelpParams{TextDocument: protocol.TextDocumentIdentifier{URI: "mem://workspace/main.ts"}, Position: protocol.Position{Line: 3, Character: 6}})
	response := c.read(t)
	var wire protocol.SignatureHelp
	if err := json.Unmarshal(response["result"], &wire); err != nil || !reflect.DeepEqual(&wire, sig.Help) {
		t.Fatalf("wire links differ: %s %v", response["result"], err)
	}
	c.finish(t)
}
