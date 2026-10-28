package lsp

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/lsp/protocol"
)

// FileRename names an intended file or directory move in the virtual catalog.
// It is a proposal input, never an instruction to modify the filesystem.
type FileRename struct{ OldName, NewName string }
type FileRenameEditsResult struct {
	Generation uint64
	Edits      *BoundEdits
}

func (s *Session) FileRenameEdits(ctx context.Context, renames []FileRename) (FileRenameEditsResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return FileRenameEditsResult{}, err
	}
	work, err := s.prepareFileRenameEdits(q, renames)
	if err != nil {
		return FileRenameEditsResult{}, err
	}
	return work(ctx)
}
func pathWithin(name, root string) bool { return name == root || strings.HasPrefix(name, root+"/") }
func (s *Session) prepareFileRenameEdits(q *querySnapshot, input []FileRename) (func(context.Context) (FileRenameEditsResult, error), error) {
	if len(input) > 1000 {
		return nil, ErrRequestLimit
	}
	renames := slices.Clone(input)
	for i, rename := range renames {
		if strings.HasPrefix(rename.OldName, libraryPrefix) || strings.HasPrefix(rename.NewName, libraryPrefix) {
			return nil, ErrReadOnly
		}
		oldName, err := documentName(rename.OldName)
		if err != nil {
			return nil, err
		}
		newName, err := documentName(rename.NewName)
		if err != nil {
			return nil, err
		}
		for name := range q.snapshot.Environment.TypeDeclarations() {
			if pathWithin(name, oldName) || pathWithin(name, newName) {
				return nil, ErrReadOnly
			}
		}
		renames[i] = FileRename{OldName: oldName, NewName: newName}
	}
	return func(ctx context.Context) (FileRenameEditsResult, error) {
		result := FileRenameEditsResult{Generation: q.snapshot.Generation}
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return result, err
		}
		defer lease.release()
		edit := protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{}}
		for _, rename := range renames {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			anchor := ""
			names := make([]string, 0, len(q.snapshot.Project.Files))
			for name := range q.snapshot.Project.Files {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, root := range []string{rename.OldName, rename.NewName} {
				for _, name := range names {
					if pathWithin(name, root) && isEditorSource(name) {
						anchor = name
						break
					}
				}
				if anchor != "" {
					break
				}
			}
			if anchor == "" {
				for _, name := range q.snapshot.Project.EntryPoints {
					if isEditorSource(name) {
						anchor = name
						break
					}
				}
			}
			if anchor == "" {
				for _, name := range names {
					if isEditorSource(name) {
						anchor = name
						break
					}
				}
			}
			if anchor == "" {
				continue
			}
			// TS6 retains the first project's edits for each file, rather than
			// concatenating duplicate views of shared sources across project graphs.
			seen := map[string]bool{}
			err = lease.state.native.WithFileRenameServices(lease.ctx, anchor, func(ctx context.Context, service *ls.LanguageService) error {
				changes, err := service.ReferenceFileRenameEdits(ctx, nativeURI(rename.OldName), nativeURI(rename.NewName))
				if err != nil {
					return err
				}
				for _, file := range changes {
					if seen[file.FileName] {
						continue
					}
					if !q.editable(file.FileName) {
						return ErrReadOnly
					}
					text, ok := q.text(file.FileName)
					if !ok {
						return ErrReadOnly
					}
					edits := []protocol.TextEdit{}
					for _, change := range file.TextChanges {
						start, err := OffsetAt(text, protocol.Position(change.Range.Start))
						if err != nil {
							return err
						}
						end, err := OffsetAt(text, protocol.Position(change.Range.End))
						if err != nil {
							return err
						}
						if start > end {
							return &Error{Code: CodeInvalidOptions, Message: "reversed file rename edit"}
						}
						if text[start:end] != change.NewText {
							edits = append(edits, protocol.TextEdit{Range: publicRange(change.Range), NewText: change.NewText})
						}
					}
					if len(edits) > 0 {
						seen[file.FileName] = true
						uri := sourceURI(file.FileName, q.snapshot.Environment, q.mappings)
						edit.Changes[uri] = append(edit.Changes[uri], edits...)
					}
				}
				return ctx.Err()
			})
			if err != nil {
				return result, publicServiceError(err)
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.checkLocked(ctx); err != nil {
			return result, err
		}
		if s.generation != q.snapshot.Generation {
			return result, ErrContentModified
		}
		if len(edit.Changes) > 0 {
			bound, err := s.bindEditsLocked(edit)
			if err != nil {
				return result, err
			}
			result.Edits = &bound
		}
		return result, nil
	}, nil
}
func isEditorSource(name string) bool {
	for _, suffix := range []string{".ts", ".tsx", ".mts", ".cts"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
func fileRenameRequestPreparers(s *Session) map[string]requestPreparer {
	return map[string]requestPreparer{"workspace/willRenameFiles": func(ctx context.Context, c *rpcConnection, raw json.RawMessage) (requestWork, error) {
		var params protocol.RenameFilesParams
		if err := decodeObject(raw, &params); err != nil {
			return nil, err
		}
		if params.Files == nil {
			return nil, &Error{Code: CodeInvalidOptions, Message: "willRenameFiles requires files"}
		}
		q, err := s.captureQuery(ctx)
		if err != nil {
			return nil, err
		}
		renames := make([]FileRename, 0, len(params.Files))
		for _, file := range params.Files {
			oldName, err := q.name(file.OldURI)
			if err != nil {
				return nil, err
			}
			newName, err := q.name(file.NewURI)
			if err != nil {
				return nil, err
			}
			canonicalOld, _ := uriKey(protocol.DocumentURI((&url.URL{Scheme: "file", Path: oldName}).String()))
			actualOld, _ := uriKey(file.OldURI)
			if canonicalOld != actualOld {
				explicit := false
				target, _ := uriKey(file.NewURI)
				for _, mapped := range q.mappings {
					key, _ := uriKey(mapped)
					if key == target {
						explicit = true
						break
					}
				}
				if _, exists := q.text(newName); !explicit && !exists {
					return nil, &Error{Code: CodeInvalidDocument, Message: "rename target requires an explicit URI mapping in this namespace"}
				}
			}

			renames = append(renames, FileRename{OldName: oldName, NewName: newName})
		}
		work, err := s.prepareFileRenameEdits(q, renames)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) (any, error) {
			result, err := work(ctx)
			if err != nil {
				return nil, err
			}
			if result.Edits == nil {
				return protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{}}, nil
			}
			return result.Edits.Edit, nil
		}, nil
	}}
}
