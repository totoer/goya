package lsp

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/totoer/goya/lsp/protocol"
)

// ClientCapabilities describe operations accepted by this particular caller.
// They are per interaction, rather than shared mutable session configuration.
type ClientCapabilities struct {
	ApplyEdit          bool
	DocumentChanges    bool
	ResourceOperations []protocol.ResourceOperationKind
	ChangeAnnotations  bool
}

// ClientHooks bridge interactions to a host application or LSP connection.
// Callbacks must honor cancellation and own application of accepted edits.
// The language service invokes them outside state locks and checker leases.
type ClientHooks struct {
	Capabilities       ClientCapabilities
	ApplyEdit          func(context.Context, protocol.ApplyWorkspaceEditParams) (protocol.ApplyWorkspaceEditResult, error)
	ShowMessageRequest func(context.Context, protocol.ShowMessageRequestParams) (*protocol.MessageActionItem, error)
}

func unsupportedClient(message string) error {
	return &Error{Code: CodeUnsupportedClient, Message: message}
}

// ApplyEdits validates a proposal and delegates its application to the client.
// Accepted/rejected/cancelled interactions never mutate the session catalog.
// The host reports accepted contents/resource changes through normal events.
func (s *Session) ApplyEdits(ctx context.Context, edits BoundEdits, label string, client ClientHooks) (protocol.ApplyWorkspaceEditResult, error) {
	if err := s.ValidateEdits(ctx, edits); err != nil {
		return protocol.ApplyWorkspaceEditResult{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	defer cancel()
	if !client.Capabilities.ApplyEdit || client.ApplyEdit == nil {
		return protocol.ApplyWorkspaceEditResult{}, unsupportedClient("client does not support workspace/applyEdit")
	}
	prepared, err := editForClient(edits.Edit, client.Capabilities)
	if err != nil {
		return protocol.ApplyWorkspaceEditResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return protocol.ApplyWorkspaceEditResult{}, err
	}
	result, err := client.ApplyEdit(ctx, protocol.ApplyWorkspaceEditParams{Label: label, Edit: prepared})
	if err != nil {
		return protocol.ApplyWorkspaceEditResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return protocol.ApplyWorkspaceEditResult{}, err
	}
	return result, nil
}

func editForClient(edit protocol.WorkspaceEdit, caps ClientCapabilities) (protocol.WorkspaceEdit, error) {
	edit = cloneEdit(edit)
	if len(edit.ChangeAnnotations) > 0 && !caps.ChangeAnnotations {
		return protocol.WorkspaceEdit{}, unsupportedClient("client does not support edit change annotations")
	}
	for _, change := range edit.DocumentChanges {
		var kind protocol.ResourceOperationKind
		if change.CreateFile != nil {
			kind = protocol.ResourceCreate
		}
		if change.RenameFile != nil {
			kind = protocol.ResourceRename
		}
		if change.DeleteFile != nil {
			kind = protocol.ResourceDelete
		}
		if kind != "" && (!caps.DocumentChanges || !slices.Contains(caps.ResourceOperations, kind)) {
			return protocol.WorkspaceEdit{}, unsupportedClient("client does not support resource operation " + string(kind))
		}
	}
	if !caps.DocumentChanges && len(edit.DocumentChanges) > 0 {
		edit.Changes = map[protocol.DocumentURI][]protocol.TextEdit{}
		for _, change := range edit.DocumentChanges {
			d := change.TextDocumentEdit
			edit.Changes[d.TextDocument.URI] = append(edit.Changes[d.TextDocument.URI], d.Edits...)
		}
		edit.DocumentChanges = nil
	}
	return edit, nil
}

func (c ClientHooks) RequestMessage(ctx context.Context, params protocol.ShowMessageRequestParams) (*protocol.MessageActionItem, error) {
	if ctx == nil {
		return nil, &Error{Code: CodeInvalidOptions, Message: "a non-nil context is required"}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.ShowMessageRequest == nil {
		return nil, unsupportedClient("client does not support message requests")
	}
	params.Actions = slices.Clone(params.Actions)
	result, err := c.ShowMessageRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func decodeClientCapabilities(raw json.RawMessage) (ClientCapabilities, error) {
	var wire struct {
		Workspace *struct {
			ApplyEdit     bool `json:"applyEdit"`
			WorkspaceEdit *struct {
				DocumentChanges         bool                             `json:"documentChanges"`
				ResourceOperations      []protocol.ResourceOperationKind `json:"resourceOperations"`
				ChangeAnnotationSupport *struct{}                        `json:"changeAnnotationSupport"`
			} `json:"workspaceEdit"`
		} `json:"workspace"`
	}
	if err := decodeObject(raw, &wire); err != nil {
		return ClientCapabilities{}, err
	}
	var caps ClientCapabilities
	if wire.Workspace != nil {
		caps.ApplyEdit = wire.Workspace.ApplyEdit
		if w := wire.Workspace.WorkspaceEdit; w != nil {
			caps.DocumentChanges = w.DocumentChanges
			caps.ResourceOperations = slices.Clone(w.ResourceOperations)
			caps.ChangeAnnotations = w.ChangeAnnotationSupport != nil
		}
	}
	return caps, nil
}

func (c *rpcConnection) clientHooks() ClientHooks {
	caps := c.capabilities
	caps.ResourceOperations = slices.Clone(caps.ResourceOperations)
	return ClientHooks{Capabilities: caps, ApplyEdit: func(ctx context.Context, params protocol.ApplyWorkspaceEditParams) (protocol.ApplyWorkspaceEditResult, error) {
		var wire struct {
			Applied       *bool   `json:"applied"`
			FailureReason *string `json:"failureReason"`
			FailedChange  *uint32 `json:"failedChange"`
		}
		if err := c.requestClient(ctx, "workspace/applyEdit", params, &wire); err != nil {
			return protocol.ApplyWorkspaceEditResult{}, err
		}
		if wire.Applied == nil {
			return protocol.ApplyWorkspaceEditResult{}, &Error{Code: CodeInvalidOptions, Message: "applyEdit response requires applied"}
		}
		return protocol.ApplyWorkspaceEditResult{Applied: *wire.Applied, FailureReason: wire.FailureReason, FailedChange: wire.FailedChange}, nil
	}, ShowMessageRequest: func(ctx context.Context, params protocol.ShowMessageRequestParams) (*protocol.MessageActionItem, error) {
		var result *protocol.MessageActionItem
		err := c.requestClient(ctx, "window/showMessageRequest", params, &result)
		return result, err
	}}
}
