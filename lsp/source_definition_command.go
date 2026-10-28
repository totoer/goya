package lsp

import (
	"context"
	"encoding/json"

	"github.com/totoer/goya/lsp/protocol"
)

const sourceDefinitionCommand = "_typescript.goToSourceDefinition"

func sourceDefinitionCommandPreparers(s *Session) map[string]requestPreparer {
	return map[string]requestPreparer{"workspace/executeCommand": func(ctx context.Context, c *rpcConnection, raw json.RawMessage) (requestWork, error) {
		var params protocol.ExecuteCommandParams
		if err := decodeObject(raw, &params); err != nil {
			return nil, err
		}
		switch params.Command {
		case applyCodeActionCommand, applyFixAllCodeActionCommand:
			return s.prepareCodeActionCommand(ctx, params.Command, params.Arguments)
		case applyRefactoringCommand:
			return s.prepareApplyRefactorCommand(ctx, c, params.Arguments)
		case organizeImportsCommand, applyRenameFileCommand, applyCompletionCodeActionCommand:
			return s.prepareCompatibilityCommand(ctx, c, params)
		}
		if params.Command != sourceDefinitionCommand {
			return nil, &ResponseError{Code: -32601, Message: "unsupported editor command: " + params.Command}
		}
		fail := func(message string) (requestWork, error) {
			return func(ctx context.Context) (any, error) {
				return nil, c.send(ctx, notification("window/showMessage", map[string]any{"type": 1, "message": message}))
			}, nil
		}
		var position struct {
			Line      *uint32 `json:"line"`
			Character *uint32 `json:"character"`
		}
		if len(params.Arguments) < 2 || decodeObject(params.Arguments[1], &position) != nil || position.Line == nil || position.Character == nil {
			return fail("Go to Source Definition failed. Invalid position.")
		}
		var uri protocol.DocumentURI
		if len(params.Arguments) < 1 || json.Unmarshal(params.Arguments[0], &uri) != nil || uri == "" {
			return fail("Go to Source Definition failed. No resource provided.")
		}
		q, err := s.captureQuery(ctx)
		if err != nil {
			return nil, err
		}
		name, err := q.name(uri)
		if err != nil {
			return fail("Go to Source Definition failed. No resource provided.")
		}
		if _, open := q.versions[name]; !open {
			return fail("Go to Source Definition failed. File not opened in the editor.")
		}
		work, err := s.prepareNavigation(q, name, protocol.Position{Line: *position.Line, Character: *position.Character}, navigationSourceDefinition)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) (any, error) {
			result, err := work(ctx)
			if err != nil {
				return nil, err
			}
			if result.Targets == nil {
				return nil, c.send(ctx, notification("window/showMessage", map[string]any{"type": 1, "message": "No source definitions found."}))
			}
			return result.Locations(), nil
		}, nil
	}}
}
