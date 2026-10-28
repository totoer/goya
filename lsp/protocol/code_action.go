package protocol

import (
	"encoding/json"
	"fmt"
)

type CodeActionKind string

const (
	CodeActionQuickFix                    CodeActionKind = "quickfix"
	CodeActionRefactor                    CodeActionKind = "refactor"
	CodeActionSource                      CodeActionKind = "source"
	CodeActionSourceFixAllTS              CodeActionKind = "source.fixAll.ts"
	CodeActionSourceRemoveUnusedTS        CodeActionKind = "source.removeUnused.ts"
	CodeActionSourceAddMissingImportsTS   CodeActionKind = "source.addMissingImports.ts"
	CodeActionSourceOrganizeImportsTS     CodeActionKind = "source.organizeImports.ts"
	CodeActionSourceSortImportsTS         CodeActionKind = "source.sortImports.ts"
	CodeActionSourceRemoveUnusedImportsTS CodeActionKind = "source.removeUnusedImports.ts"
)

type CodeActionTriggerKind uint32

const (
	CodeActionInvoked   CodeActionTriggerKind = 1
	CodeActionAutomatic CodeActionTriggerKind = 2
)

type CodeActionContext struct {
	Diagnostics []Diagnostic           `json:"diagnostics"`
	Only        *[]CodeActionKind      `json:"only,omitempty"`
	TriggerKind *CodeActionTriggerKind `json:"triggerKind,omitempty"`
}
type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      CodeActionContext      `json:"context"`
}
type CodeActionDisabled struct {
	Reason string `json:"reason"`
}

// Data identifies a bounded, generation-bound continuation. Preserve it verbatim.
type CodeActionData struct {
	Handle string `json:"handle"`
	Index  int    `json:"index"`
}
type CodeAction struct {
	Title       string              `json:"title"`
	Kind        CodeActionKind      `json:"kind,omitempty"`
	Diagnostics []Diagnostic        `json:"diagnostics,omitempty"`
	IsPreferred *bool               `json:"isPreferred,omitempty"`
	Disabled    *CodeActionDisabled `json:"disabled,omitempty"`
	Edit        *WorkspaceEdit      `json:"edit,omitempty"`
	Command     *Command            `json:"command,omitempty"`
	Data        json.RawMessage     `json:"data,omitempty"`
}
type CodeActionOptions struct {
	CodeActionKinds []CodeActionKind `json:"codeActionKinds,omitempty"`
	ResolveProvider bool             `json:"resolveProvider"`
}

// CommandOrCodeAction contains exactly one alternative, preserving legacy commands.
type CommandOrCodeAction struct {
	Command    *Command
	CodeAction *CodeAction
}

func (v CommandOrCodeAction) MarshalJSON() ([]byte, error) {
	if v.Command != nil && v.CodeAction == nil {
		return json.Marshal(v.Command)
	}
	if v.CodeAction != nil && v.Command == nil {
		return json.Marshal(v.CodeAction)
	}
	return nil, fmt.Errorf("code action must contain exactly one alternative")
}
func (v *CommandOrCodeAction) UnmarshalJSON(raw []byte) error {
	var shape struct {
		Command json.RawMessage `json:"command"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		return err
	}
	*v = CommandOrCodeAction{}
	if len(shape.Command) > 0 && shape.Command[0] == '"' {
		v.Command = &Command{}
		return json.Unmarshal(raw, v.Command)
	}
	v.CodeAction = &CodeAction{}
	return json.Unmarshal(raw, v.CodeAction)
}
