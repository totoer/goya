package lsp

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/totoer/goya/lsp/protocol"
)

// BoundEdits are proposals only: the host/client owns their application. Validate
// immediately before applying, then report accepted changes as document events.
type BoundEdits struct {
	Handle ResultHandle           `json:"handle"`
	Edit   protocol.WorkspaceEdit `json:"edit"`
}

func cloneEdit(edit protocol.WorkspaceEdit) protocol.WorkspaceEdit {
	copy := edit
	copy.Changes = maps.Clone(edit.Changes)
	for uri, edits := range copy.Changes {
		copy.Changes[uri] = slices.Clone(edits)
	}
	copy.ChangeAnnotations = maps.Clone(edit.ChangeAnnotations)
	copy.DocumentChanges = slices.Clone(edit.DocumentChanges)
	for i, c := range copy.DocumentChanges {
		if c.TextDocumentEdit != nil {
			v := *c.TextDocumentEdit
			v.Edits = slices.Clone(v.Edits)
			if v.TextDocument.Version != nil {
				version := *v.TextDocument.Version
				v.TextDocument.Version = &version
			}
			copy.DocumentChanges[i].TextDocumentEdit = &v
		}
		if c.CreateFile != nil {
			v := *c.CreateFile
			if v.Options != nil {
				o := *v.Options
				v.Options = &o
			}
			copy.DocumentChanges[i].CreateFile = &v
		}
		if c.RenameFile != nil {
			v := *c.RenameFile
			if v.Options != nil {
				o := *v.Options
				v.Options = &o
			}
			copy.DocumentChanges[i].RenameFile = &v
		}
		if c.DeleteFile != nil {
			v := *c.DeleteFile
			if v.Options != nil {
				o := *v.Options
				v.Options = &o
			}
			copy.DocumentChanges[i].DeleteFile = &v
		}
	}
	return copy
}

func (s *Session) checkEditableLocked(uri protocol.DocumentURI) (string, error) {
	name, err := s.documentNameLocked(uri)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(name, libraryPrefix) {
		return "", ErrReadOnly
	}
	if _, ok := s.environment.TypeDeclarations()[name]; ok {
		return "", ErrReadOnly
	}
	return name, nil
}

func (s *Session) bindEditsLocked(edit protocol.WorkspaceEdit) (BoundEdits, error) {
	edit = cloneEdit(edit)
	checkAnnotation := func(id string) error {
		if id != "" {
			if _, ok := edit.ChangeAnnotations[id]; !ok {
				return &Error{Code: CodeInvalidOptions, Message: "edit references an unknown change annotation"}
			}
		}
		return nil
	}
	if len(edit.Changes) > 0 && len(edit.DocumentChanges) > 0 {
		return BoundEdits{}, &Error{Code: CodeInvalidOptions, Message: "workspace edit must use one change representation"}
	}
	size := 128
	for uri, edits := range edit.Changes {
		if _, err := s.checkEditableLocked(uri); err != nil {
			return BoundEdits{}, err
		}
		size += len(uri) + len(edits)*64
		for _, edit := range edits {
			if edit.AnnotationID != "" {
				return BoundEdits{}, &Error{Code: CodeInvalidOptions, Message: "annotated edits require documentChanges"}
			}
			size += len(edit.NewText)
		}
	}
	for _, change := range edit.DocumentChanges {
		count := 0
		if change.TextDocumentEdit != nil {
			count++
			d := change.TextDocumentEdit
			name, err := s.checkEditableLocked(d.TextDocument.URI)
			if err != nil {
				return BoundEdits{}, err
			}
			if doc, ok := s.documents[name]; ok {
				if d.TextDocument.Version != nil && *d.TextDocument.Version != doc.version {
					return BoundEdits{}, ErrContentModified
				}
				version := doc.version
				d.TextDocument.Version = &version
			} else if d.TextDocument.Version != nil {
				return BoundEdits{}, ErrContentModified
			}
			size += len(d.TextDocument.URI) + len(d.Edits)*64
			for _, textEdit := range d.Edits {
				if err := checkAnnotation(textEdit.AnnotationID); err != nil {
					return BoundEdits{}, err
				}
				size += len(textEdit.NewText) + len(textEdit.AnnotationID)
			}
		}
		if change.CreateFile != nil {
			count++
			change.CreateFile.Kind = "create"
			if err := checkAnnotation(change.CreateFile.AnnotationID); err != nil {
				return BoundEdits{}, err
			}
			if _, err := s.checkEditableLocked(change.CreateFile.URI); err != nil {
				return BoundEdits{}, err
			}
			size += len(change.CreateFile.URI) + len(change.CreateFile.AnnotationID) + 128
		}
		if change.RenameFile != nil {
			count++
			change.RenameFile.Kind = "rename"
			if err := checkAnnotation(change.RenameFile.AnnotationID); err != nil {
				return BoundEdits{}, err
			}
			size += len(change.RenameFile.AnnotationID)
			for _, uri := range []protocol.DocumentURI{change.RenameFile.OldURI, change.RenameFile.NewURI} {
				if _, err := s.checkEditableLocked(uri); err != nil {
					return BoundEdits{}, err
				}
				size += len(uri) + 128
			}
		}
		if change.DeleteFile != nil {
			count++
			change.DeleteFile.Kind = "delete"
			if err := checkAnnotation(change.DeleteFile.AnnotationID); err != nil {
				return BoundEdits{}, err
			}
			if _, err := s.checkEditableLocked(change.DeleteFile.URI); err != nil {
				return BoundEdits{}, err
			}
			size += len(change.DeleteFile.URI) + len(change.DeleteFile.AnnotationID) + 128
		}
		if count != 1 {
			return BoundEdits{}, &Error{Code: CodeInvalidOptions, Message: "document change requires exactly one operation"}
		}
	}
	for id, annotation := range edit.ChangeAnnotations {
		size += len(id) + len(annotation.Label) + len(annotation.Description) + 32
	}
	return BoundEdits{Handle: s.retainLocked(edit, size), Edit: cloneEdit(edit)}, nil
}

// ValidateEdits rejects stale, evicted, foreign or altered proposals. It never
// applies text edits, creates/renames files, or calls application code.
func (s *Session) ValidateEdits(ctx context.Context, edits BoundEdits) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLocked(ctx); err != nil {
		return err
	}
	value, err := s.resolveLocked(edits.Handle)
	if err != nil {
		return err
	}
	original, ok := value.(protocol.WorkspaceEdit)
	if !ok || !reflect.DeepEqual(original, edits.Edit) {
		return &Error{Code: CodeInvalidHandle, Message: "edit proposal differs from the retained result"}
	}
	return nil
}
