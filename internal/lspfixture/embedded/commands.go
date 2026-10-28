package embedded

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/totoer/goya/internal/lspfixture"
	"github.com/totoer/goya/lsp"
	"github.com/totoer/goya/lsp/protocol"
)

func (d *Direct) compatibilityCommand(ctx context.Context, p protocol.ExecuteCommandParams) (json.RawMessage, error) {
	var edits *lsp.BoundEdits
	nameForPath := func(path string) (string, error) {
		return d.session.DocumentName(ctx, protocol.DocumentURI((&url.URL{Scheme: "file", Path: path}).String()))
	}
	switch p.Command {
	case "_typescript.organizeImports":
		var path string
		if len(p.Arguments) == 0 {
			return nil, fmt.Errorf("missing command file")
		}
		if err := json.Unmarshal(p.Arguments[0], &path); err != nil {
			return nil, err
		}
		name, err := nameForPath(path)
		if err != nil {
			return json.RawMessage("null"), nil
		}
		var options struct {
			Mode lsp.OrganizeImportsMode `json:"mode"`
			Skip bool                    `json:"skipDestructiveCodeActions"`
		}
		if len(p.Arguments) > 1 {
			if err := json.Unmarshal(p.Arguments[1], &options); err != nil {
				return nil, err
			}
		}
		if options.Mode == "" {
			options.Mode = lsp.OrganizeImportsAll
			if options.Skip {
				options.Mode = lsp.OrganizeImportsSortAndCombine
			}
		}
		result, err := d.session.OrganizeImports(ctx, name, options.Mode)
		if err != nil {
			return nil, err
		}
		if result.Version == nil {
			return json.RawMessage("null"), nil
		}
		edits = result.Edits
	case "_typescript.applyRenameFile":
		var arg struct {
			SourceURI protocol.DocumentURI `json:"sourceUri"`
			TargetURI protocol.DocumentURI `json:"targetUri"`
		}
		if len(p.Arguments) == 0 {
			return nil, fmt.Errorf("missing rename arguments")
		}
		if err := json.Unmarshal(p.Arguments[0], &arg); err != nil {
			return nil, err
		}
		oldName, err := d.session.DocumentName(ctx, arg.SourceURI)
		if err != nil {
			return json.RawMessage("null"), nil
		}
		newName, err := d.session.DocumentName(ctx, arg.TargetURI)
		if err != nil {
			return json.RawMessage("null"), nil
		}
		result, err := d.session.FileRenameEdits(ctx, []lsp.FileRename{{OldName: oldName, NewName: newName}})
		if err != nil {
			return nil, err
		}
		edits = result.Edits
	case "_typescript.applyCompletionCodeAction":
		var actions []lsp.CompletionCodeAction
		if len(p.Arguments) < 2 {
			return nil, fmt.Errorf("missing completion actions")
		}
		if err := json.Unmarshal(p.Arguments[1], &actions); err != nil {
			return nil, err
		}
		result, err := d.session.CompletionCodeActionEdits(ctx, actions)
		if err != nil {
			return nil, err
		}
		edits = result.Edits
	}
	if edits != nil {
		_, err := d.session.ApplyEdits(ctx, *edits, "", lsp.ClientHooks{Capabilities: lsp.ClientCapabilities{ApplyEdit: true}, ApplyEdit: func(ctx context.Context, params protocol.ApplyWorkspaceEditParams) (protocol.ApplyWorkspaceEditResult, error) {
			raw, err := json.Marshal(params)
			if err != nil {
				return protocol.ApplyWorkspaceEditResult{}, err
			}
			d.clientMu.Lock()
			d.clientRequests = append(d.clientRequests, lspfixture.ClientRequest{Method: "workspace/applyEdit", Params: raw})
			d.clientMu.Unlock()
			if response, ok := d.clientResponses["workspace/applyEdit"]; ok {
				var value protocol.ApplyWorkspaceEditResult
				err := json.Unmarshal(response, &value)
				return value, err
			}
			reason := "capture does not apply client edits"
			return protocol.ApplyWorkspaceEditResult{Applied: false, FailureReason: &reason}, ctx.Err()
		}})
		if err != nil {
			return nil, err
		}
	}
	return json.RawMessage("null"), nil
}
