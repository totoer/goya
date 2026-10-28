package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func resourceEdits(t *testing.T, s *Session) BoundEdits {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	edits, err := s.bindEditsLocked(protocol.WorkspaceEdit{DocumentChanges: []protocol.DocumentChange{
		{CreateFile: &protocol.CreateFile{URI: "file:///created.ts"}},
		{TextDocumentEdit: &protocol.TextDocumentEdit{TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{URI: "file:///created.ts"}, Edits: []protocol.TextEdit{{NewText: "export const value=42;"}}}},
		{RenameFile: &protocol.RenameFile{OldURI: "file:///main.ts", NewURI: "file:///moved.ts"}},
		{DeleteFile: &protocol.DeleteFile{URI: "file:///unused.ts", Options: &protocol.DeleteFileOptions{IgnoreIfNotExists: true}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return edits
}
func completeClientCapabilities() ClientCapabilities {
	return ClientCapabilities{ApplyEdit: true, DocumentChanges: true, ResourceOperations: []protocol.ResourceOperationKind{protocol.ResourceCreate, protocol.ResourceRename, protocol.ResourceDelete}}
}

func TestDirectClientEditsAreProposalsUntilHostEvents(t *testing.T) {
	ctx := context.Background()
	for _, accepted := range []bool{false, true} {
		s := testSession(t, nil)
		edits := resourceEdits(t, s)
		before, err := s.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result, err := s.ApplyEdits(ctx, edits, "Move", ClientHooks{Capabilities: completeClientCapabilities(), ApplyEdit: func(ctx context.Context, params protocol.ApplyWorkspaceEditParams) (protocol.ApplyWorkspaceEditResult, error) {
			inside, err := s.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, inside) {
				t.Fatal("client callback ran after unconfirmed mutation")
			}
			if params.Label != "Move" || len(params.Edit.DocumentChanges) != 4 || params.Edit.DocumentChanges[0].CreateFile.Kind != "create" {
				t.Fatalf("client proposal: %+v", params)
			}
			params.Edit.DocumentChanges[0].CreateFile.URI = "file:///caller-mutated.ts"
			return protocol.ApplyWorkspaceEditResult{Applied: accepted}, nil
		}})
		if err != nil || result.Applied != accepted {
			t.Fatalf("client decision: %+v %v", result, err)
		}
		after, err := s.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("applyEdit changed session without document events")
		}
		if err := s.ValidateEdits(ctx, edits); err != nil {
			t.Fatalf("callback mutation escaped DTO: %v", err)
		}
		if accepted {
			if err := s.UpdateProject(ctx, goya.Project{Files: map[string]string{"/created.ts": "export const value=42;", "/moved.ts": before.Project.Files["/main.ts"]}}); err != nil {
				t.Fatal(err)
			}
			after, err = s.Snapshot(ctx)
			if err != nil || after.Generation <= before.Generation || after.Project.Files["/created.ts"] == "" {
				t.Fatalf("accepted host event not visible: %+v %v", after, err)
			}
		}
	}
}

func TestClientCancellationAndCapabilitiesDoNotMutate(t *testing.T) {
	ctx := context.Background()
	s := testSession(t, nil)
	resources := resourceEdits(t, s)
	before, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = s.ApplyEdits(ctx, resources, "Move", ClientHooks{Capabilities: ClientCapabilities{ApplyEdit: true}, ApplyEdit: func(context.Context, protocol.ApplyWorkspaceEditParams) (protocol.ApplyWorkspaceEditResult, error) {
		called = true
		return protocol.ApplyWorkspaceEditResult{}, nil
	}})
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != CodeUnsupportedClient || called {
		t.Fatalf("unsupported resource operation: %v called=%v", err, called)
	}
	request, cancel := context.WithCancel(ctx)
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := s.ApplyEdits(request, resources, "Move", ClientHooks{Capabilities: completeClientCapabilities(), ApplyEdit: func(ctx context.Context, _ protocol.ApplyWorkspaceEditParams) (protocol.ApplyWorkspaceEditResult, error) {
			close(entered)
			<-ctx.Done()
			return protocol.ApplyWorkspaceEditResult{}, ctx.Err()
		}})
		done <- err
	}()
	<-entered
	cancel()
	if err := receiveError(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled client: %v", err)
	}
	after, err := s.Snapshot(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("cancelled action changed state: %+v %v", after, err)
	}
}

func TestSessionCloseCancelsDirectClientInteraction(t *testing.T) {
	s := testSession(t, nil)
	edits := retainTestEdits(t, s)
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := s.ApplyEdits(context.Background(), edits, "Rename", ClientHooks{Capabilities: ClientCapabilities{ApplyEdit: true}, ApplyEdit: func(ctx context.Context, _ protocol.ApplyWorkspaceEditParams) (protocol.ApplyWorkspaceEditResult, error) {
			close(entered)
			<-ctx.Done()
			return protocol.ApplyWorkspaceEditResult{}, ctx.Err()
		}})
		done <- err
	}()
	<-entered
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := receiveError(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestTextEditCapabilityFallbackAndReentrantHostEvents(t *testing.T) {
	ctx := context.Background()
	s := testSession(t, nil)
	edits := retainTestEdits(t, s)
	result, err := s.ApplyEdits(ctx, edits, "Rename", ClientHooks{Capabilities: ClientCapabilities{ApplyEdit: true}, ApplyEdit: func(ctx context.Context, params protocol.ApplyWorkspaceEditParams) (protocol.ApplyWorkspaceEditResult, error) {
		if params.Edit.DocumentChanges != nil || len(params.Edit.Changes["file:///main.ts"]) != 1 {
			t.Fatalf("text fallback: %+v", params.Edit)
		}
		// The host may report confirmed contents from this callback.
		err := s.OpenDocument(ctx, "/main.ts", 1, "export const value=1;")
		return protocol.ApplyWorkspaceEditResult{Applied: err == nil}, err
	}})
	if err != nil || !result.Applied {
		t.Fatalf("reentrant host callback: %+v %v", result, err)
	}
	snapshot, err := s.Snapshot(ctx)
	if err != nil || snapshot.Generation != 2 || snapshot.Project.Files["/main.ts"] != "export const value=1;" {
		t.Fatalf("confirmed event applied incorrectly: %+v %v", snapshot, err)
	}
}

func TestWireClientApplyEditHonorsDecisionAndResourceCapabilities(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		s := testSession(t, nil)
		edits := resourceEdits(t, s)
		before, err := s.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		handlers := map[string]requestPreparer{"test/apply": func(_ context.Context, c *rpcConnection, _ json.RawMessage) (requestWork, error) {
			return func(ctx context.Context) (any, error) { return s.ApplyEdits(ctx, edits, "Move", c.clientHooks()) }, nil
		}}
		caps := map[string]any{"workspace": map[string]any{"applyEdit": true, "workspaceEdit": map[string]any{"documentChanges": true, "resourceOperations": []string{"create", "rename", "delete"}}}}
		c := startTestConnection(t, handlers, 2, 2, testClientSetup{session: s, capabilities: caps})
		c.send(t, 2, "test/apply", map[string]any{})
		clientRequest := c.read(t)
		if string(clientRequest["method"]) != `"workspace/applyEdit"` {
			t.Fatalf("applyEdit request: %+v", clientRequest)
		}
		var params protocol.ApplyWorkspaceEditParams
		if err := json.Unmarshal(clientRequest["params"], &params); err != nil {
			t.Fatal(err)
		}
		if params.Label != "Move" || len(params.Edit.DocumentChanges) != 4 || params.Edit.DocumentChanges[0].CreateFile == nil || params.Edit.DocumentChanges[2].RenameFile == nil || params.Edit.DocumentChanges[3].DeleteFile == nil {
			t.Fatalf("wire resource operations changed: %+v", params)
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": clientRequest["id"], "result": map[string]any{"applied": accepted}})
		if err := writeFrame(context.Background(), c.client, body); err != nil {
			t.Fatal(err)
		}
		response := c.read(t)
		var result protocol.ApplyWorkspaceEditResult
		if err := json.Unmarshal(response["result"], &result); err != nil || result.Applied != accepted {
			t.Fatalf("wire decision: %+v %v", response, err)
		}
		after, err := s.Snapshot(context.Background())
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("wire applyEdit mutated source state")
		}
		c.finish(t)
	}
}

func TestWireCancelledApplyEditLeavesCatalogAndAcceptsLaterRequest(t *testing.T) {
	s := testSession(t, nil)
	edits := retainTestEdits(t, s)
	handlers := map[string]requestPreparer{"test/apply": func(_ context.Context, c *rpcConnection, _ json.RawMessage) (requestWork, error) {
		return func(ctx context.Context) (any, error) { return s.ApplyEdits(ctx, edits, "Rename", c.clientHooks()) }, nil
	}}
	c := startTestConnection(t, handlers, 1, 1, testClientSetup{session: s, capabilities: map[string]any{"workspace": map[string]any{"applyEdit": true}}})
	c.send(t, 2, "test/apply", map[string]any{})
	request := c.read(t)
	var params protocol.ApplyWorkspaceEditParams
	if err := json.Unmarshal(request["params"], &params); err != nil {
		t.Fatal(err)
	}
	if len(params.Edit.Changes) != 1 || params.Edit.DocumentChanges != nil {
		t.Fatal("wire text capability fallback failed")
	}
	c.send(t, nil, "$/cancelRequest", map[string]any{"id": 2})
	assertResponseCode(t, c.read(t), "2", -32800)
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"applied": true}})
	if err := writeFrame(context.Background(), c.client, body); err != nil {
		t.Fatal(err)
	}
	c.send(t, 3, "goya/readDocument", map[string]any{"uri": "file:///main.ts"})
	var document VirtualDocument
	if err := json.Unmarshal(c.read(t)["result"], &document); err != nil || document.Text != "export const count=1;" {
		t.Fatalf("cancelled apply changed catalog: %+v %v", document, err)
	}
	c.finish(t)
}

func TestTypedMessageRequestAndClientDecline(t *testing.T) {
	handlers := map[string]requestPreparer{"test/message": func(_ context.Context, c *rpcConnection, _ json.RawMessage) (requestWork, error) {
		return func(ctx context.Context) (any, error) {
			return c.clientHooks().RequestMessage(ctx, protocol.ShowMessageRequestParams{Type: protocol.MessageInfo, Message: "Choose an action", Actions: []protocol.MessageActionItem{{Title: "Continue"}}})
		}, nil
	}}
	c := startTestConnection(t, handlers, 2, 2)
	for _, selection := range []any{map[string]any{"title": "Continue"}, nil} {
		c.send(t, 2, "test/message", map[string]any{})
		request := c.read(t)
		if string(request["method"]) != `"window/showMessageRequest"` {
			t.Fatalf("typed message request: %+v", request)
		}
		var params protocol.ShowMessageRequestParams
		if err := json.Unmarshal(request["params"], &params); err != nil || len(params.Actions) != 1 || params.Type != protocol.MessageInfo {
			t.Fatalf("message parameters: %+v %v", params, err)
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": selection})
		if err := writeFrame(context.Background(), c.client, body); err != nil {
			t.Fatal(err)
		}
		response := c.read(t)
		if response["error"] != nil {
			t.Fatalf("message request failed: %+v", response)
		}
		if selection == nil && string(response["result"]) != "null" {
			t.Fatalf("client decline was not preserved: %+v", response)
		}
	}
	c.finish(t)
}
