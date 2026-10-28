package protocol

import (
	"bytes"
	"encoding/json"
)

type CompletionTriggerKind uint32

const (
	CompletionInvoked              CompletionTriggerKind = 1
	CompletionTriggerCharacter     CompletionTriggerKind = 2
	CompletionTriggerForIncomplete CompletionTriggerKind = 3
)

type CompletionContext struct {
	TriggerKind      CompletionTriggerKind `json:"triggerKind"`
	TriggerCharacter *string               `json:"triggerCharacter,omitempty"`
}
type CompletionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      *CompletionContext     `json:"context,omitempty"`
}
type CompletionItemLabelDetails struct {
	Detail      *string `json:"detail,omitempty"`
	Description *string `json:"description,omitempty"`
}
type CompletionTextEdit struct {
	NewText string `json:"newText"`
	Range   *Range `json:"range,omitempty"`
	Insert  *Range `json:"insert,omitempty"`
	Replace *Range `json:"replace,omitempty"`
}
type StringOrMarkupContent struct {
	String        *string
	MarkupContent *MarkupContent
}

func (v StringOrMarkupContent) MarshalJSON() ([]byte, error) {
	if v.String != nil {
		return json.Marshal(v.String)
	}
	return json.Marshal(v.MarkupContent)
}
func (v *StringOrMarkupContent) UnmarshalJSON(data []byte) error {
	*v = StringOrMarkupContent{}
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		return json.Unmarshal(data, &v.String)
	}
	return json.Unmarshal(data, &v.MarkupContent)
}

type CompletionData struct {
	Handle string `json:"handle"`
	Index  int    `json:"index"`
}
type CompletionItem struct {
	Label               string                      `json:"label"`
	LabelDetails        *CompletionItemLabelDetails `json:"labelDetails,omitempty"`
	Kind                *uint32                     `json:"kind,omitempty"`
	Tags                []uint32                    `json:"tags,omitzero"`
	Detail              *string                     `json:"detail,omitempty"`
	Documentation       *StringOrMarkupContent      `json:"documentation,omitempty"`
	Deprecated          *bool                       `json:"deprecated,omitempty"`
	Preselect           *bool                       `json:"preselect,omitempty"`
	SortText            *string                     `json:"sortText,omitempty"`
	FilterText          *string                     `json:"filterText,omitempty"`
	InsertText          *string                     `json:"insertText,omitempty"`
	InsertTextFormat    *uint32                     `json:"insertTextFormat,omitempty"`
	InsertTextMode      *uint32                     `json:"insertTextMode,omitempty"`
	TextEdit            *CompletionTextEdit         `json:"textEdit,omitempty"`
	TextEditText        *string                     `json:"textEditText,omitempty"`
	AdditionalTextEdits []TextEdit                  `json:"additionalTextEdits,omitzero"`
	CommitCharacters    []string                    `json:"commitCharacters,omitzero"`
	Command             *Command                    `json:"command,omitempty"`
	Data                *CompletionData             `json:"data,omitempty"`
}
type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}
type CompletionOptions struct {
	ResolveProvider   bool     `json:"resolveProvider"`
	TriggerCharacters []string `json:"triggerCharacters"`
}

type Command struct {
	Title     string            `json:"title"`
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments,omitzero"`
}
