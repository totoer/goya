package lsp

import (
	"context"
	"encoding/json"

	"github.com/totoer/goya/lsp/protocol"
)

const (
	organizeImportsCommand           = "_typescript.organizeImports"
	applyRenameFileCommand           = "_typescript.applyRenameFile"
	applyCompletionCodeActionCommand = "_typescript.applyCompletionCodeAction"
)

func (s *Session) prepareCompatibilityCommand(ctx context.Context, c *rpcConnection, params protocol.ExecuteCommandParams) (requestWork, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return nil, err
	}
	empty := func(context.Context) (any, error) { return nil, nil }
	apply := func(ctx context.Context, edits *BoundEdits) (any, error) {
		if edits == nil {
			return nil, nil
		}
		client := c.clientHooks()
		client.Capabilities.DocumentChanges = false
		_, err := s.ApplyEdits(ctx, *edits, "", client)
		return nil, err
	}
	switch params.Command {
	case organizeImportsCommand:
		if len(params.Arguments) == 0 {
			return nil, &Error{Code: CodeInvalidOptions, Message: "organize imports requires a file"}
		}
		var file string
		if err := json.Unmarshal(params.Arguments[0], &file); err != nil {
			return nil, err
		}
		name, err := q.refactorCommandName(file)
		if err != nil {
			return empty, nil
		}
		if _, open := q.versions[name]; !open {
			return empty, nil
		}
		var options struct {
			Mode                       OrganizeImportsMode `json:"mode"`
			SkipDestructiveCodeActions bool                `json:"skipDestructiveCodeActions"`
		}
		if len(params.Arguments) > 1 && string(params.Arguments[1]) != "null" {
			if err := decodeObject(params.Arguments[1], &options); err != nil {
				return nil, err
			}
		}
		if options.Mode == "" {
			options.Mode = OrganizeImportsAll
			if options.SkipDestructiveCodeActions {
				options.Mode = OrganizeImportsSortAndCombine
			}
		}
		work, err := s.prepareImportEdits(q, name, options.Mode, false)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) (any, error) {
			result, err := work(ctx)
			if err != nil {
				return nil, err
			}
			return apply(ctx, result.Edits)
		}, nil
	case applyRenameFileCommand:
		if len(params.Arguments) == 0 {
			return nil, &Error{Code: CodeInvalidOptions, Message: "file rename requires source and target URIs"}
		}
		var arg struct {
			SourceURI protocol.DocumentURI `json:"sourceUri"`
			TargetURI protocol.DocumentURI `json:"targetUri"`
		}
		if err := decodeObject(params.Arguments[0], &arg); err != nil {
			return nil, err
		}
		if arg.SourceURI == "" || arg.TargetURI == "" {
			return nil, &Error{Code: CodeInvalidOptions, Message: "file rename requires source and target URIs"}
		}
		oldName, err := q.name(arg.SourceURI)
		if err != nil {
			return empty, nil
		}
		newName, err := q.name(arg.TargetURI)
		if err != nil {
			return empty, nil
		}
		work, err := s.prepareFileRenameEdits(q, []FileRename{{OldName: oldName, NewName: newName}})
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) (any, error) {
			result, err := work(ctx)
			if err != nil {
				return nil, err
			}
			return apply(ctx, result.Edits)
		}, nil
	case applyCompletionCodeActionCommand:
		if len(params.Arguments) < 2 {
			return nil, &Error{Code: CodeInvalidOptions, Message: "completion command requires code actions"}
		}
		var actions []CompletionCodeAction
		if err := json.Unmarshal(params.Arguments[1], &actions); err != nil {
			return nil, err
		}
		return func(ctx context.Context) (any, error) {
			result, err := s.completionCodeActionEdits(ctx, q, actions)
			if err != nil {
				return nil, err
			}
			return apply(ctx, result.Edits)
		}, nil
	}
	return nil, &ResponseError{Code: -32601, Message: "unsupported editor command: " + params.Command}
}
