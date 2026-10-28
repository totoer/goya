package lsp

import (
	"context"
	"encoding/json"

	"github.com/totoer/goya/lsp/protocol"
)

// NotifyFilesChanged validates host/client file events and refreshes the same
// host resolver used by UpdateProject. It never reads a file from a client URI.
func (s *Session) NotifyFilesChanged(ctx context.Context, events []protocol.FileEvent) error {
	s.mu.RLock()
	if err := s.checkLocked(ctx); err != nil {
		s.mu.RUnlock()
		return err
	}
	for _, event := range events {
		if event.Type < protocol.FileCreated || event.Type > protocol.FileDeleted {
			s.mu.RUnlock()
			return &Error{Code: CodeInvalidOptions, Message: "unknown file event type"}
		}
		if _, err := s.checkEditableLocked(event.URI); err != nil {
			s.mu.RUnlock()
			return err
		}
	}
	s.mu.RUnlock()
	if len(events) == 0 {
		return s.checkContext(ctx)
	}
	return s.RefreshProject(ctx)
}

func decodeObject(data json.RawMessage, destination any) error {
	if !isJSONObject(data) {
		return &Error{Code: CodeInvalidOptions, Message: "parameters must be a JSON object"}
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return &Error{Code: CodeInvalidOptions, Message: "invalid parameters", Cause: err}
	}
	return nil
}

func isSynchronizationMethod(method string) bool {
	switch method {
	case "textDocument/didOpen", "textDocument/didChange", "textDocument/didSave", "textDocument/didClose", "workspace/didChangeWatchedFiles", "workspace/didCreateFiles", "workspace/didRenameFiles", "workspace/didDeleteFiles":
		return true
	}
	return false
}

// Synchronization calls exactly the public session operations. It performs no
// separate source caching/checking and no application callback under state lock.
func synchronize(ctx context.Context, s *Session, method string, params json.RawMessage) (bool, error) {
	switch method {
	case "textDocument/didOpen":
		var p struct {
			TextDocument *struct {
				URI        protocol.DocumentURI `json:"uri"`
				LanguageID string               `json:"languageId"`
				Version    *int32               `json:"version"`
				Text       *string              `json:"text"`
			} `json:"textDocument"`
		}
		if err := decodeObject(params, &p); err != nil {
			return true, err
		}
		if p.TextDocument == nil || p.TextDocument.Version == nil || p.TextDocument.Text == nil {
			return true, &Error{Code: CodeInvalidOptions, Message: "didOpen requires document, version and text"}
		}
		d := p.TextDocument
		if d.LanguageID != "typescript" && d.LanguageID != "typescriptreact" {
			return true, &Error{Code: CodeInvalidDocument, Message: "only TypeScript/TSX documents are supported"}
		}
		name, err := s.DocumentName(ctx, d.URI)
		if err != nil {
			return true, err
		}
		return true, s.OpenDocument(ctx, name, *d.Version, *d.Text)
	case "textDocument/didChange":
		var p struct {
			TextDocument *struct {
				URI     protocol.DocumentURI `json:"uri"`
				Version *int32               `json:"version"`
			} `json:"textDocument"`
			Changes *[]protocol.ContentChange `json:"contentChanges"`
		}
		if err := decodeObject(params, &p); err != nil {
			return true, err
		}
		if p.TextDocument == nil || p.TextDocument.Version == nil || p.Changes == nil {
			return true, &Error{Code: CodeInvalidOptions, Message: "didChange requires document, version and changes"}
		}
		name, err := s.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return true, err
		}
		return true, s.ChangeDocument(ctx, name, *p.TextDocument.Version, *p.Changes)
	case "textDocument/didSave", "textDocument/didClose":
		var p struct {
			TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
		}
		if err := decodeObject(params, &p); err != nil {
			return true, err
		}
		if p.TextDocument == nil {
			return true, &Error{Code: CodeInvalidOptions, Message: "missing textDocument"}
		}
		name, err := s.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return true, err
		}
		if method == "textDocument/didSave" {
			return true, s.SaveDocument(ctx, name)
		}
		return true, s.CloseDocument(ctx, name)
	case "workspace/didChangeWatchedFiles":
		var p protocol.DidChangeWatchedFilesParams
		if err := decodeObject(params, &p); err != nil {
			return true, err
		}
		if p.Changes == nil {
			return true, &Error{Code: CodeInvalidOptions, Message: "file notification requires changes"}
		}
		return true, s.NotifyFilesChanged(ctx, p.Changes)
	case "workspace/didCreateFiles":
		var p protocol.CreateFilesParams
		if err := decodeObject(params, &p); err != nil {
			return true, err
		}
		if p.Files == nil {
			return true, &Error{Code: CodeInvalidOptions, Message: "file notification requires files"}
		}
		events := make([]protocol.FileEvent, 0, len(p.Files))
		for _, f := range p.Files {
			events = append(events, protocol.FileEvent{URI: f.URI, Type: protocol.FileCreated})
		}
		return true, s.NotifyFilesChanged(ctx, events)
	case "workspace/didDeleteFiles":
		var p protocol.DeleteFilesParams
		if err := decodeObject(params, &p); err != nil {
			return true, err
		}
		if p.Files == nil {
			return true, &Error{Code: CodeInvalidOptions, Message: "file notification requires files"}
		}
		events := make([]protocol.FileEvent, 0, len(p.Files))
		for _, f := range p.Files {
			events = append(events, protocol.FileEvent{URI: f.URI, Type: protocol.FileDeleted})
		}
		return true, s.NotifyFilesChanged(ctx, events)
	case "workspace/didRenameFiles":
		var p protocol.RenameFilesParams
		if err := decodeObject(params, &p); err != nil {
			return true, err
		}
		if p.Files == nil {
			return true, &Error{Code: CodeInvalidOptions, Message: "file notification requires files"}
		}
		events := make([]protocol.FileEvent, 0, len(p.Files)*2)
		for _, f := range p.Files {
			events = append(events, protocol.FileEvent{URI: f.OldURI, Type: protocol.FileDeleted}, protocol.FileEvent{URI: f.NewURI, Type: protocol.FileCreated})
		}
		return true, s.NotifyFilesChanged(ctx, events)
	default:
		return false, nil
	}
}
