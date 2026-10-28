package lsp

import (
	"context"
	"encoding/json"
	"slices"
	"sort"

	"github.com/totoer/goya/internal/tsfrontend/refactor"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

// RefactorRequestOptions follows the reference kind and invocation filters.
// SDK callers can enumerate interactive actions and supply their arguments.
type RefactorRequestOptions struct {
	Kind                      *protocol.CodeActionKind
	TriggerKind               *protocol.CodeActionTriggerKind
	IncludeInteractiveActions *bool
}
type RefactorResult struct {
	Generation uint64
	Version    *int32
	Handle     ResultHandle
	Refactors  []protocol.ApplicableRefactorInfo
}

// RefactorSelection indexes one issued, generation-bound enumeration.
type RefactorSelection struct {
	Handle                     ResultHandle
	RefactorIndex, ActionIndex int
}
type RefactorArguments struct{ TargetFile string }

// RenameContinuation describes a position after the proposed edits are applied.
// The host reports accepted changes before starting a subsequent rename query.
type RenameContinuation struct {
	URI      protocol.DocumentURI `json:"uri"`
	Position protocol.Position    `json:"position"`
}
type RefactorEditResult struct {
	Generation          uint64
	Version             *int32
	Edits               *BoundEdits
	Rename              *RenameContinuation
	NotApplicableReason string
}

type refactorMetadata struct {
	name    string
	span    protocol.Range
	options refactor.Options
	infos   []protocol.ApplicableRefactorInfo
}

func cloneRefactorInfos(infos []protocol.ApplicableRefactorInfo) []protocol.ApplicableRefactorInfo {
	result := slices.Clone(infos)
	for i := range result {
		result[i].Actions = slices.Clone(result[i].Actions)
		for j := range result[i].Actions {
			if result[i].Actions[j].Range != nil {
				span := *result[i].Actions[j].Range
				result[i].Actions[j].Range = &span
			}
		}
		if result[i].Inlineable != nil {
			value := *result[i].Inlineable
			result[i].Inlineable = &value
		}
	}
	return result
}
func refactorOptions(options *RefactorRequestOptions) (refactor.Options, error) {
	owned := refactor.Options{IncludeInteractive: true}
	if options == nil {
		return owned, nil
	}
	if options.Kind != nil {
		owned.Kind = string(*options.Kind)
	}
	if options.IncludeInteractiveActions != nil {
		owned.IncludeInteractive = *options.IncludeInteractiveActions
	}
	if options.TriggerKind != nil {
		if *options.TriggerKind != protocol.CodeActionInvoked && *options.TriggerKind != protocol.CodeActionAutomatic {
			return owned, &Error{Code: CodeInvalidOptions, Message: "invalid refactor triggerKind"}
		}
		owned.Invoked = *options.TriggerKind == protocol.CodeActionInvoked
	}
	return owned, nil
}
func refactorSpan(q *querySnapshot, name string, span protocol.Range) (string, protocol.Range, error) {
	name, start, err := q.position(name, span.Start)
	if err != nil {
		return "", span, err
	}
	_, end, err := q.position(name, span.End)
	if err != nil {
		return "", span, err
	}
	if end.Line < start.Line || end.Line == start.Line && end.Character < start.Character {
		return "", span, &Error{Code: CodeInvalidOptions, Message: "refactor range is reversed"}
	}
	return name, protocol.Range{Start: start, End: end}, nil
}

// ApplicableRefactors returns owned action groups without applying changes.
func (s *Session) ApplicableRefactors(ctx context.Context, name string, span protocol.Range, options *RefactorRequestOptions) (RefactorResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return RefactorResult{}, err
	}
	name, span, err = refactorSpan(q, name, span)
	if err != nil {
		return RefactorResult{}, err
	}
	owned, err := refactorOptions(options)
	if err != nil {
		return RefactorResult{}, err
	}
	result := RefactorResult{Generation: q.snapshot.Generation, Version: q.version(name), Refactors: []protocol.ApplicableRefactorInfo{}}
	ctx, err = s.withDocumentFormatting(ctx, q, name)
	if err != nil {
		return result, err
	}
	lease, err := s.acquireWorkspaceSnapshot(ctx, q)
	if err != nil {
		return result, err
	}
	defer lease.release()
	err = lease.state.native.WithService(lease.ctx, name, func(ctx context.Context, service *ls.LanguageService) error {
		var err error
		result.Refactors, err = refactor.Applicable(ctx, service, name, lsproto.Range{Start: lsproto.Position(span.Start), End: lsproto.Position(span.End)}, owned)
		return err
	})
	if err != nil {
		return result, publicServiceError(err)
	}
	result.Refactors = cloneRefactorInfos(result.Refactors)
	metadata := refactorMetadata{name: name, span: span, options: owned, infos: cloneRefactorInfos(result.Refactors)}
	raw, _ := json.Marshal(result.Refactors)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLocked(ctx); err != nil {
		return result, err
	}
	if s.generation != q.snapshot.Generation {
		return result, ErrContentModified
	}
	result.Handle = s.retainLocked(metadata, len(raw)+len(name)+128)
	return result, nil
}

// ApplyRefactor computes a proposal for one issued action. Presentation changes
// cannot redirect the retained target; source/configuration changes invalidate it.
func (s *Session) ApplyRefactor(ctx context.Context, selection RefactorSelection, arguments *RefactorArguments) (RefactorEditResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return RefactorEditResult{}, err
	}
	s.mu.Lock()
	if err := s.checkLocked(ctx); err != nil {
		s.mu.Unlock()
		return RefactorEditResult{}, err
	}
	value, err := s.resolveLocked(selection.Handle)
	if err != nil {
		s.mu.Unlock()
		return RefactorEditResult{}, err
	}
	metadata, ok := value.(refactorMetadata)
	if !ok || selection.RefactorIndex < 0 || selection.RefactorIndex >= len(metadata.infos) {
		s.mu.Unlock()
		return RefactorEditResult{}, &Error{Code: CodeInvalidHandle, Message: "invalid refactor selection"}
	}
	info := metadata.infos[selection.RefactorIndex]
	if selection.ActionIndex < 0 || selection.ActionIndex >= len(info.Actions) {
		s.mu.Unlock()
		return RefactorEditResult{}, &Error{Code: CodeInvalidHandle, Message: "invalid refactor action selection"}
	}
	action := info.Actions[selection.ActionIndex]
	generation := s.generation
	s.mu.Unlock()
	if generation != q.snapshot.Generation {
		return RefactorEditResult{}, ErrContentModified
	}
	if action.NotApplicableReason != "" {
		return RefactorEditResult{}, &Error{Code: CodeInvalidEdit, Message: action.NotApplicableReason}
	}
	args := refactor.Arguments{}
	if arguments != nil {
		args.TargetFile = arguments.TargetFile
	}
	if args.TargetFile != "" && !action.IsInteractive && !(info.Name == "Move to file" && action.Name == "Move to file") {
		return RefactorEditResult{}, &Error{Code: CodeInvalidOptions, Message: "this refactor does not accept a target file"}
	}
	return s.computeRefactor(ctx, q, metadata.name, metadata.span, metadata.options, info.Name, action.Name, args)
}
func (s *Session) computeRefactor(ctx context.Context, q *querySnapshot, name string, span protocol.Range, options refactor.Options, refactorName, actionName string, args refactor.Arguments) (RefactorEditResult, error) {
	result := RefactorEditResult{Generation: q.snapshot.Generation, Version: q.version(name)}
	if !q.editable(name) {
		return result, ErrReadOnly
	}
	if args.TargetFile != "" {
		if !q.editable(args.TargetFile) {
			return result, ErrReadOnly
		}
		target, err := documentName(args.TargetFile)
		if err != nil {
			return result, err
		}
		args.TargetFile = target
		if !q.editable(target) {
			return result, ErrReadOnly
		}
	}
	ctx, err := s.withDocumentFormatting(ctx, q, name)
	if err != nil {
		return result, err
	}
	lease, err := s.acquireWorkspaceSnapshot(ctx, q)
	if err != nil {
		return result, err
	}
	defer lease.release()
	var plan *refactor.EditInfo
	err = lease.state.native.WithService(lease.ctx, name, func(ctx context.Context, service *ls.LanguageService) error {
		var err error
		plan, err = refactor.Apply(ctx, service, name, lsproto.Range{Start: lsproto.Position(span.Start), End: lsproto.Position(span.End)}, options, refactorName, actionName, args)
		return err
	})
	if err != nil {
		return result, publicServiceError(err)
	}
	if plan == nil {
		return result, nil
	}
	result.NotApplicableReason = plan.NotApplicableReason
	edit, _, err := q.codeFixChanges(plan.Changes)
	if err != nil {
		return result, err
	}
	var rename *RenameContinuation
	if plan.RenamePosition != nil {
		target := plan.RenameFilename
		if target == "" {
			target = name
		}
		text, _ := q.text(target)
		for _, file := range plan.Changes {
			if file.FileName == target {
				text, err = prospectiveRefactorText(text, file.TextChanges)
				if err != nil {
					return result, err
				}
				break
			}
		}
		position, err := PositionAt(text, *plan.RenamePosition)
		if err != nil {
			return result, err
		}
		rename = &RenameContinuation{URI: sourceURI(target, q.snapshot.Environment, q.mappings), Position: position}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLocked(ctx); err != nil {
		return result, err
	}
	if s.generation != q.snapshot.Generation {
		return result, ErrContentModified
	}
	if edit != nil {
		bound, err := s.bindEditsLocked(*edit)
		if err != nil {
			return result, err
		}
		result.Edits = &bound
	}
	result.Rename = rename
	return result, nil
}
func prospectiveRefactorText(text string, edits []*lsproto.TextEdit) (string, error) {
	type mutation struct {
		start, end, index int
		text              string
	}
	changes := make([]mutation, len(edits))
	for index, edit := range edits {
		start, err := OffsetAt(text, protocol.Position(edit.Range.Start))
		if err != nil {
			return "", err
		}
		end, err := OffsetAt(text, protocol.Position(edit.Range.End))
		if err != nil {
			return "", err
		}
		if end < start {
			return "", &Error{Code: CodeInvalidEdit, Message: "refactor edit range is reversed"}
		}
		changes[index] = mutation{start: start, end: end, index: index, text: edit.NewText}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].start == changes[j].start {
			return changes[i].index > changes[j].index
		}
		return changes[i].start > changes[j].start
	})
	for _, change := range changes {
		text = text[:change.start] + change.text + text[change.end:]
	}
	return text, nil
}

// RefactorEdits computes an explicitly named operation on the current snapshot.
// Use ApplyRefactor with an issued selection when retaining an earlier choice.
func (s *Session) RefactorEdits(ctx context.Context, name string, span protocol.Range, refactorName, actionName string, arguments *RefactorArguments) (RefactorEditResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return RefactorEditResult{}, err
	}
	name, span, err = refactorSpan(q, name, span)
	if err != nil {
		return RefactorEditResult{}, err
	}
	if refactorName == "" || actionName == "" {
		return RefactorEditResult{}, &Error{Code: CodeInvalidOptions, Message: "refactor and action names are required"}
	}
	args := refactor.Arguments{}
	if arguments != nil {
		args.TargetFile = arguments.TargetFile
	}
	return s.computeRefactor(ctx, q, name, span, refactor.Options{}, refactorName, actionName, args)
}
