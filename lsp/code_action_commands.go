package lsp

import (
	"context"
	"encoding/json"
)

const applyCodeActionCommand = "_typescript.applyCodeActionCommand"
const applyFixAllCodeActionCommand = "_typescript.applyFixAllCodeAction"

// CodeActionCommands completes a quick-fix follow-up after the client applied
// its text edit. Applicable embedded providers have no external commands.
// The reference returns true for an empty list. Any external command rejects
// the complete operation; this method never applies the accompanying text edit.
func (s *Session) CodeActionCommands(ctx context.Context, commands []json.RawMessage) (bool, error) {
	if err := s.checkContext(ctx); err != nil {
		return false, err
	}
	if len(commands) > 0 {
		return false, &Error{Code: CodeInvalidOptions, Message: "external tsserver code-action commands are not supported"}
	}
	return true, nil
}
func (s *Session) prepareCodeActionCommand(ctx context.Context, command string, args []json.RawMessage) (requestWork, error) {
	if command == applyFixAllCodeActionCommand {
		// Every applicable embedded combined provider yields text edits only. The
		// client already applied the resolved edit; this follow-up has no commands.
		return func(ctx context.Context) (any, error) { _, err := s.CodeActionCommands(ctx, nil); return nil, err }, nil
	}
	if len(args) == 0 {
		return nil, &Error{Code: CodeInvalidOptions, Message: "code action command requires an action"}
	}
	var arg struct {
		Action *struct {
			Commands []json.RawMessage `json:"commands"`
		} `json:"action"`
	}
	if err := decodeObject(args[0], &arg); err != nil {
		return nil, err
	}
	if arg.Action == nil {
		return nil, &Error{Code: CodeInvalidOptions, Message: "code action command requires an action"}
	}
	commands := arg.Action.Commands
	return func(ctx context.Context) (any, error) { return s.CodeActionCommands(ctx, commands) }, nil
}
