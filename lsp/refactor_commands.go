package lsp

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/totoer/goya/internal/tsfrontend/refactor"
	"github.com/totoer/goya/lsp/protocol"
)

func (q *querySnapshot) refactorCommandName(file string) (string, error) {
	// Command paths are the caller-visible paths emitted by commandFileName, not
	// operating-system lookup targets. Custom URI mappings retain virtual names.
	for name := range q.snapshot.Project.Files {
		if q.commandFileName(name) == file {
			return name, nil
		}
	}
	for name := range q.snapshot.Environment.TypeDeclarations() {
		if q.commandFileName(name) == file {
			return name, nil
		}
	}
	return "", &Error{Code: CodeInvalidDocument, Message: "refactor command file is not in the captured catalog"}
}
func (s *Session) prepareApplyRefactorCommand(ctx context.Context, c *rpcConnection, arguments []json.RawMessage) (requestWork, error) {
	if len(arguments) == 0 {
		return nil, &Error{Code: CodeInvalidOptions, Message: "refactor command requires an operation"}
	}
	var arg applyRefactorArgument
	if err := decodeObject(arguments[0], &arg); err != nil {
		return nil, err
	}
	if arg.File == "" || arg.Refactor == "" || arg.Action == "" || arg.StartLine == 0 || arg.StartOffset == 0 || arg.EndLine == 0 || arg.EndOffset == 0 {
		return nil, &Error{Code: CodeInvalidOptions, Message: "refactor command requires file, names and one-based source range"}
	}
	if err := c.validateRefactorCommand(ctx, s, arg); err != nil {
		return nil, err
	}
	q, err := s.captureQuery(ctx)
	if err != nil {
		return nil, err
	}
	name, err := q.refactorCommandName(arg.File)
	if err != nil {
		return nil, err
	}
	span := protocol.Range{Start: protocol.Position{Line: arg.StartLine - 1, Character: arg.StartOffset - 1}, End: protocol.Position{Line: arg.EndLine - 1, Character: arg.EndOffset - 1}}
	name, span, err = refactorSpan(q, name, span)
	if err != nil {
		return nil, err
	}
	args := refactor.Arguments{}
	if arg.InteractiveArguments != nil {
		targetURI := protocol.DocumentURI((&url.URL{Scheme: "file", Path: arg.InteractiveArguments.TargetFile}).String())
		args.TargetFile, err = q.name(targetURI)
		if err != nil {
			return nil, err
		}
	}
	return func(ctx context.Context) (any, error) {
		if err := c.validateRefactorCommand(ctx, s, arg); err != nil {
			return nil, err
		}
		result, err := s.computeRefactor(ctx, q, name, span, refactor.Options{}, arg.Refactor, arg.Action, args)
		if err != nil {
			return nil, err
		}
		if result.NotApplicableReason != "" {
			return nil, &ResponseError{Code: -32603, Message: "Request workspace/executeCommand failed with message: " + result.NotApplicableReason}
		}
		if result.Edits == nil {
			return nil, nil
		}
		client := c.clientHooks()
		apply := client.ApplyEdit
		// TLS refactor commands use legacy text changes, including implicit
		// creation of new documents. The retained SDK plan still carries and
		// validates its explicit create operations before this wire projection.
		client.Capabilities.DocumentChanges = true
		client.Capabilities.ResourceOperations = []protocol.ResourceOperationKind{protocol.ResourceCreate}
		client.ApplyEdit = func(ctx context.Context, params protocol.ApplyWorkspaceEditParams) (protocol.ApplyWorkspaceEditResult, error) {
			projected, err := legacyRefactorEdit(params.Edit)
			if err != nil {
				return protocol.ApplyWorkspaceEditResult{}, err
			}
			params.Edit = projected
			return apply(ctx, params)
		}
		response, err := s.ApplyEdits(ctx, *result.Edits, "", client)
		if err != nil {
			return nil, err
		}
		if response.Applied && result.Rename != nil {
			var ignored json.RawMessage
			err = c.requestClient(ctx, "_typescript.rename", protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: sourceURI(name, q.snapshot.Environment, q.mappings)}, Position: result.Rename.Position}, &ignored)
		}
		return nil, err
	}, nil
}

func legacyRefactorEdit(edit protocol.WorkspaceEdit) (protocol.WorkspaceEdit, error) {
	result := protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{}, ChangeAnnotations: edit.ChangeAnnotations}
	for uri, changes := range edit.Changes {
		result.Changes[uri] = changes
	}
	creates := map[protocol.DocumentURI]bool{}
	for _, change := range edit.DocumentChanges {
		if change.TextDocumentEdit != nil {
			document := change.TextDocumentEdit
			result.Changes[document.TextDocument.URI] = append(result.Changes[document.TextDocument.URI], document.Edits...)
		} else if change.CreateFile != nil {
			creates[change.CreateFile.URI] = true
		} else {
			return protocol.WorkspaceEdit{}, &Error{Code: CodeInvalidEdit, Message: "refactor command cannot project this resource operation"}
		}
	}
	for uri := range creates {
		if _, ok := result.Changes[uri]; !ok {
			return protocol.WorkspaceEdit{}, &Error{Code: CodeInvalidEdit, Message: "refactor create operation lacks initial text"}
		}
	}
	return result, nil
}
