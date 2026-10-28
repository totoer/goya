package lsp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/editor"
	"github.com/totoer/goya/internal/tsfrontend/upstream/format"
	nativejson "github.com/totoer/goya/internal/tsfrontend/upstream/json"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

type CompletionCapabilities struct {
	SnippetSupport          bool                  `json:"snippetSupport"`
	CommitCharactersSupport bool                  `json:"commitCharactersSupport"`
	LabelDetailsSupport     bool                  `json:"labelDetailsSupport"`
	InsertReplaceSupport    bool                  `json:"insertReplaceSupport"`
	DocumentationFormat     []protocol.MarkupKind `json:"documentationFormat,omitempty"`
}
type CompletionRequestOptions struct {
	Context      *protocol.CompletionContext
	Capabilities *CompletionCapabilities
}
type CompletionResult struct {
	Generation uint64
	Version    *int32
	List       *protocol.CompletionList
}
type CompletionResolveResult struct {
	Generation uint64
	Version    *int32
	Item       protocol.CompletionItem
}

type completionMetadata struct {
	name         string
	items        [][]byte
	capabilities CompletionCapabilities
	presentation completionPresentation
}

func completionCapabilities(options *CompletionRequestOptions) CompletionCapabilities {
	if options == nil || options.Capabilities == nil {
		return CompletionCapabilities{SnippetSupport: true, CommitCharactersSupport: true, LabelDetailsSupport: true, InsertReplaceSupport: true, DocumentationFormat: []protocol.MarkupKind{protocol.MarkupMarkdown}}
	}
	caps := *options.Capabilities
	caps.DocumentationFormat = slices.Clone(caps.DocumentationFormat)
	return caps
}
func nativeCompletionCapabilities(caps CompletionCapabilities, preferences EditorPreferences) *lsproto.ResolvedClientCapabilities {
	value := &lsproto.ResolvedClientCapabilities{}
	item := &value.TextDocument.Completion.CompletionItem
	// Keep engine preferences independent of client rendering. TLS filters
	// snippet entries for clients without snippet support after computation.
	item.SnippetSupport = preferenceEnabled(preferences.IncludeCompletionsWithSnippetText)
	item.CommitCharactersSupport = caps.CommitCharactersSupport
	item.LabelDetailsSupport = caps.LabelDetailsSupport && preferenceEnabled(preferences.UseLabelDetailsInCompletionEntries)
	item.InsertReplaceSupport = caps.InsertReplaceSupport
	for _, kind := range caps.DocumentationFormat {
		item.DocumentationFormat = append(item.DocumentationFormat, lsproto.MarkupKind(kind))
	}
	if len(item.DocumentationFormat) == 0 {
		item.DocumentationFormat = []lsproto.MarkupKind{lsproto.MarkupKindMarkdown}
	}
	return value
}

func (s *Session) Completion(ctx context.Context, name string, position protocol.Position, options *CompletionRequestOptions) (CompletionResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return CompletionResult{}, publicServiceError(err)
	}
	work, err := s.prepareCompletion(q, name, position, options)
	if err != nil {
		return CompletionResult{}, publicServiceError(err)
	}
	return work(ctx)
}

func (s *Session) prepareCompletion(q *querySnapshot, name string, position protocol.Position, options *CompletionRequestOptions) (func(context.Context) (CompletionResult, error), error) {
	name, position, err := q.position(name, position)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(name, libraryPrefix) {
		return nil, ErrReadOnly
	}
	if _, readonly := q.snapshot.Environment.TypeDeclarations()[name]; readonly {
		return nil, ErrReadOnly
	}
	caps := completionCapabilities(options)
	var trigger *lsproto.CompletionContext
	if options != nil && options.Context != nil {
		value := options.Context
		if value.TriggerKind < protocol.CompletionInvoked || value.TriggerKind > protocol.CompletionTriggerForIncomplete || value.TriggerKind == protocol.CompletionTriggerCharacter && (value.TriggerCharacter == nil || len(*value.TriggerCharacter) == 0) {
			return nil, &Error{Code: CodeInvalidOptions, Message: "invalid completion trigger"}
		}
		trigger = &lsproto.CompletionContext{TriggerKind: lsproto.CompletionTriggerKind(value.TriggerKind)}
		if value.TriggerCharacter != nil {
			text := *value.TriggerCharacter
			trigger.TriggerCharacter = &text
		}
	}
	return func(ctx context.Context) (CompletionResult, error) {
		ctx, formatErr := s.withDocumentFormatting(ctx, q, name)
		if formatErr != nil {
			return CompletionResult{}, formatErr
		}
		result := CompletionResult{Generation: q.snapshot.Generation, Version: q.version(name), List: &protocol.CompletionList{Items: []protocol.CompletionItem{}}}
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return CompletionResult{}, publicServiceError(err)
		}
		defer lease.release()
		ctx = lsproto.WithClientCapabilities(lease.ctx, nativeCompletionCapabilities(caps, q.snapshot.EditorOptions.Preferences))
		metadata := completionMetadata{name: name, capabilities: caps}
		size := len(name) + 256 + len(caps.DocumentationFormat)*16
		for _, kind := range caps.DocumentationFormat {
			size += len(kind)
		}
		err = lease.state.native.WithService(ctx, name, func(ctx context.Context, service *ls.LanguageService) error {
			file := service.GetProgram().GetSourceFile(name)
			offset, err := OffsetAt(file.Text(), position)
			if err != nil {
				return err
			}
			var character *string
			if trigger != nil {
				character = trigger.TriggerCharacter
			}
			ctx = format.WithFormatCodeSettings(ctx, service.FormatOptions(), service.FormatOptions().NewLineCharacter)
			list, err := service.GetReferenceCompletionsAtPosition(ctx, file, offset, character, true)
			if err != nil {
				return err
			}
			if list == nil {
				return nil
			}
			if err := editor.CompleteMembers(ctx, service, file, offset, list, preferenceEnabled(q.snapshot.EditorOptions.Preferences.IncludeCompletionsWithInsertText), preferenceEnabled(q.snapshot.EditorOptions.Preferences.IncludeCompletionsWithSnippetText)); err != nil {
				return err
			}
			metadata.presentation = completionPresentation{Position: position, Member: list.IsMemberCompletion, NewIdentifier: list.IsNewIdentifierLocation, StringLiteral: list.IsStringLiteral, JSDoc: list.JSDoc}
			result.List = &protocol.CompletionList{IsIncomplete: list.IsIncomplete, Items: make([]protocol.CompletionItem, 0, len(list.Items))}
			metadata.items = nil
			for _, entry := range list.Items {
				native := entry.CompletionItem
				if native == nil {
					continue
				}
				if native.InsertTextFormat != nil && *native.InsertTextFormat == lsproto.InsertTextFormatSnippet && !caps.SnippetSupport {
					continue
				}
				if !preferenceEnabled(q.snapshot.EditorOptions.Preferences.IncludeCompletionsWithInsertText) {
					name := strings.TrimSuffix(native.Label, "?")
					if native.InsertText != nil && *native.InsertText != name {
						continue
					}
					if native.TextEdit != nil {
						if edit := native.TextEdit.TextEdit; edit != nil && edit.NewText != name {
							continue
						}
						if edit := native.TextEdit.InsertReplaceEdit; edit != nil && edit.NewText != name {
							continue
						}
					}
				}
				if native.Data == nil {
					native.Data = &lsproto.CompletionItemData{FileName: name, Position: int32(offset), Name: native.Label}
				}
				data, err := nativejson.Marshal(native)
				if err != nil {
					return err
				}
				var item protocol.CompletionItem
				// Native completion data stays private; the public facade carries only
				// a bounded, session/generation-bound handle and an item index.
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil {
					return err
				}
				delete(fields, "data")
				public, err := json.Marshal(fields)
				if err != nil {
					return err
				}
				if err := json.Unmarshal(public, &item); err != nil {
					return err
				}
				adaptCompletionPresentation(&item, q, name, caps, metadata.presentation)
				metadata.items = append(metadata.items, data)
				size += len(data) + 32
				result.List.Items = append(result.List.Items, item)
			}
			return nil
		})
		if err != nil {
			return CompletionResult{}, publicServiceError(err)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.checkLocked(ctx); err != nil {
			return CompletionResult{}, publicServiceError(err)
		}
		if s.generation != q.snapshot.Generation {
			return CompletionResult{}, ErrContentModified
		}
		if result.List != nil {
			handle := s.retainLocked(metadata, size)
			for index := range result.List.Items {
				result.List.Items[index].Data = &protocol.CompletionData{Handle: string(handle), Index: index}
			}
		}
		return result, nil
	}, nil
}

// ResolveCompletion resolves the originally issued item, ignoring mutable client
// presentation fields. It never trusts a client's native file names or edits.
func (s *Session) ResolveCompletion(ctx context.Context, item protocol.CompletionItem) (CompletionResolveResult, error) {
	work, err := s.prepareResolveCompletion(ctx, item)
	if err != nil {
		return CompletionResolveResult{}, publicServiceError(err)
	}
	return work(ctx)
}
func (s *Session) prepareResolveCompletion(ctx context.Context, item protocol.CompletionItem) (func(context.Context) (CompletionResolveResult, error), error) {
	if item.Data == nil {
		return nil, &Error{Code: CodeInvalidHandle, Message: "completion item has no result handle"}
	}
	data := *item.Data
	s.mu.Lock()
	if err := s.checkLocked(ctx); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	value, err := s.resolveLocked(ResultHandle(data.Handle))
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	metadata, ok := value.(completionMetadata)
	if !ok || data.Index < 0 || data.Index >= len(metadata.items) {
		s.mu.Unlock()
		return nil, &Error{Code: CodeInvalidHandle, Message: "invalid completion item index or handle type"}
	}
	encoded := metadata.items[data.Index]
	s.mu.Unlock()
	q, err := s.captureQuery(ctx)
	if err != nil {
		return nil, err
	}
	// A generation change between handle lookup and snapshot capture invalidates
	// the handle rather than resolving its old position against a new program.
	s.mu.Lock()
	_, err = s.resolveLocked(ResultHandle(data.Handle))
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (CompletionResolveResult, error) {
		ctx, formatErr := s.withDocumentFormatting(ctx, q, metadata.name)
		if formatErr != nil {
			return CompletionResolveResult{}, formatErr
		}
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return CompletionResolveResult{}, publicServiceError(err)
		}
		defer lease.release()
		ctx = lsproto.WithClientCapabilities(lease.ctx, nativeCompletionCapabilities(metadata.capabilities, q.snapshot.EditorOptions.Preferences))
		var native lsproto.CompletionItem
		if err := nativejson.Unmarshal(encoded, &native); err != nil {
			return CompletionResolveResult{}, publicServiceError(err)
		}
		var resolved *lsproto.CompletionItem
		var parameters []string
		var optional, validCall bool
		err = lease.state.native.WithService(ctx, metadata.name, func(ctx context.Context, service *ls.LanguageService) error {
			var err error
			resolved, err = service.ResolveCompletionItem(ctx, &native, native.Data)
			if err == nil && native.Data != nil && native.Data.Source == ls.SourceTypeOnlyAlias {
				err = service.EmbeddedTypeOnlyAliasEdits(ctx, resolved)
			}
			if err == nil && native.Data != nil && native.Data.AutoImport != nil {
				err = service.EmbeddedAutoImportDetails(ctx, resolved)
			}
			if err == nil && native.Kind != nil && (*native.Kind == lsproto.CompletionItemKindMethod || *native.Kind == lsproto.CompletionItemKindFunction) {
				parameters, optional, validCall, err = service.EmbeddedCompletionCallParameters(ctx, resolved)
				validCall = validCall && metadata.capabilities.SnippetSupport && q.snapshot.EditorOptions.Completions.CompleteFunctionCalls
			}
			return err
		})
		if err != nil {
			return CompletionResolveResult{}, publicServiceError(err)
		}
		if resolved == nil {
			return CompletionResolveResult{}, &Error{Code: CodeInvalidProject, Message: "native completion resolve returned no item"}
		}
		resolved.Data = nil
		encoded, err := nativejson.Marshal(resolved)
		if err != nil {
			return CompletionResolveResult{}, publicServiceError(err)
		}
		var public protocol.CompletionItem
		if err := json.Unmarshal(encoded, &public); err != nil {
			return CompletionResolveResult{}, publicServiceError(err)
		}
		adaptCompletionPresentation(&public, q, metadata.name, metadata.capabilities, metadata.presentation)
		if validCall {
			completeFunctionCall(&public, q, metadata.name, metadata.presentation.Position, parameters, optional)
		}
		public.Data = &data
		if err := s.checkContext(ctx); err != nil {
			return CompletionResolveResult{}, publicServiceError(err)
		}
		s.mu.RLock()
		current := s.generation == q.snapshot.Generation
		s.mu.RUnlock()
		if !current {
			return CompletionResolveResult{}, ErrContentModified
		}
		return CompletionResolveResult{Generation: q.snapshot.Generation, Version: q.version(metadata.name), Item: public}, nil
	}, nil
}
