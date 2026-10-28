package protocol

import (
	"encoding/json"
	"fmt"
)

type OptionalVersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version *int32      `json:"version"`
}
type TextDocumentEdit struct {
	TextDocument OptionalVersionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []TextEdit                              `json:"edits"`
}
type CreateFileOptions struct {
	Overwrite      bool `json:"overwrite,omitempty"`
	IgnoreIfExists bool `json:"ignoreIfExists,omitempty"`
}
type RenameFileOptions struct {
	Overwrite      bool `json:"overwrite,omitempty"`
	IgnoreIfExists bool `json:"ignoreIfExists,omitempty"`
}
type DeleteFileOptions struct {
	Recursive         bool `json:"recursive,omitempty"`
	IgnoreIfNotExists bool `json:"ignoreIfNotExists,omitempty"`
}
type CreateFile struct {
	Kind         string             `json:"kind"`
	URI          DocumentURI        `json:"uri"`
	Options      *CreateFileOptions `json:"options,omitempty"`
	AnnotationID string             `json:"annotationId,omitempty"`
}
type RenameFile struct {
	Kind         string             `json:"kind"`
	OldURI       DocumentURI        `json:"oldUri"`
	NewURI       DocumentURI        `json:"newUri"`
	Options      *RenameFileOptions `json:"options,omitempty"`
	AnnotationID string             `json:"annotationId,omitempty"`
}
type DeleteFile struct {
	Kind         string             `json:"kind"`
	URI          DocumentURI        `json:"uri"`
	Options      *DeleteFileOptions `json:"options,omitempty"`
	AnnotationID string             `json:"annotationId,omitempty"`
}
type ChangeAnnotation struct {
	Label             string `json:"label"`
	NeedsConfirmation bool   `json:"needsConfirmation,omitempty"`
	Description       string `json:"description,omitempty"`
}

// DocumentChange contains exactly one text edit or resource operation.
type DocumentChange struct {
	TextDocumentEdit *TextDocumentEdit
	CreateFile       *CreateFile
	RenameFile       *RenameFile
	DeleteFile       *DeleteFile
}

func (c DocumentChange) MarshalJSON() ([]byte, error) {
	count := 0
	var value any
	if c.TextDocumentEdit != nil {
		count++
		value = c.TextDocumentEdit
	}
	if c.CreateFile != nil {
		count++
		copy := *c.CreateFile
		copy.Kind = "create"
		value = copy
	}
	if c.RenameFile != nil {
		count++
		copy := *c.RenameFile
		copy.Kind = "rename"
		value = copy
	}
	if c.DeleteFile != nil {
		count++
		copy := *c.DeleteFile
		copy.Kind = "delete"
		value = copy
	}
	if count != 1 {
		return nil, fmt.Errorf("document change must contain exactly one operation")
	}
	return json.Marshal(value)
}
func (c *DocumentChange) UnmarshalJSON(data []byte) error {
	var header struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return err
	}
	*c = DocumentChange{}
	switch header.Kind {
	case "create":
		c.CreateFile = &CreateFile{}
		return json.Unmarshal(data, c.CreateFile)
	case "rename":
		c.RenameFile = &RenameFile{}
		return json.Unmarshal(data, c.RenameFile)
	case "delete":
		c.DeleteFile = &DeleteFile{}
		return json.Unmarshal(data, c.DeleteFile)
	case "":
		c.TextDocumentEdit = &TextDocumentEdit{}
		return json.Unmarshal(data, c.TextDocumentEdit)
	default:
		return fmt.Errorf("unknown document change kind %q", header.Kind)
	}
}

type WorkspaceEdit struct {
	Changes           map[DocumentURI][]TextEdit  `json:"changes,omitzero"`
	DocumentChanges   []DocumentChange            `json:"documentChanges,omitzero"`
	ChangeAnnotations map[string]ChangeAnnotation `json:"changeAnnotations,omitempty"`
}
