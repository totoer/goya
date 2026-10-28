package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

type CodeActionCapabilities struct {
	SupportsMoveToFileCodeAction bool `json:"-"`
	ResolveSupport               *struct {
		Properties []string `json:"properties"`
	} `json:"resolveSupport,omitempty"`
	DisabledSupport bool `json:"disabledSupport,omitempty"`
}
type CodeActionRequestOptions struct {
	Capabilities           *CodeActionCapabilities
	DiagnosticCapabilities *DiagnosticCapabilities
}
type CodeActionResult struct {
	// Refactors aligns with Actions; nil entries are not refactor choices.
	Refactors  []*RefactorChoice
	Generation uint64
	Version    *int32
	Actions    []protocol.CodeAction
	// Proposals aligns with Actions. Nil entries have no eager edit.
	Proposals []*BoundEdits
}
type CodeActionResolveResult struct {
	Generation uint64
	Version    *int32
	Action     protocol.CodeAction
	Edits      *BoundEdits
}
type codeActionTarget struct {
	refactor             *refactorActionTarget
	name, fixID, fixName string
	action               protocol.CodeAction
}
type codeActionMetadata struct {
	targets       []codeActionTarget
	epoch         uint64
	providerState *codeFixProviderState
}

// This state belongs to one enumeration. It models the pinned async provider's
// success flag across its fix-all resolves without sharing state across sessions.
type codeFixProviderState struct {
	async ls.ReferenceAsyncConversionState
	gate  chan struct{}
}

func newCodeFixProviderState() *codeFixProviderState {
	return &codeFixProviderState{async: ls.ReferenceAsyncConversionState{Succeeded: true}, gate: make(chan struct{}, 1)}
}
func (state *codeFixProviderState) withAsync(ctx context.Context, run func(context.Context) error) error {
	select {
	case state.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-state.gate }()
	return run(ls.WithReferenceAsyncConversionState(ctx, &state.async))
}

func cloneCodeAction(action protocol.CodeAction) protocol.CodeAction {
	raw, _ := json.Marshal(action)
	var result protocol.CodeAction
	_ = json.Unmarshal(raw, &result)
	return result
}
func codeActionCaps(options *CodeActionRequestOptions) CodeActionCapabilities {
	if options != nil && options.Capabilities != nil {
		result := *options.Capabilities
		if result.ResolveSupport != nil {
			copy := *result.ResolveSupport
			copy.Properties = slices.Clone(copy.Properties)
			result.ResolveSupport = &copy
		}
		return result
	}
	var result CodeActionCapabilities
	result.ResolveSupport = &struct {
		Properties []string `json:"properties"`
	}{Properties: []string{"edit"}}
	result.DisabledSupport = true
	return result
}

// CodeActions enumerates detached proposals against a captured catalog. It
// never applies edits, invokes code, or writes files.
func (s *Session) CodeActions(ctx context.Context, name string, span protocol.Range, actionContext protocol.CodeActionContext, options *CodeActionRequestOptions) (CodeActionResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return CodeActionResult{}, err
	}
	work, err := s.prepareCodeActions(q, name, span, actionContext, options)
	if err != nil {
		return CodeActionResult{}, err
	}
	return work(ctx)
}
func codeActionDiagnosticCode(d protocol.Diagnostic) (int32, bool) {
	if d.CodeString == nil {
		return d.Code, d.Code != 0
	}
	n, err := strconv.ParseInt(*d.CodeString, 10, 32)
	return int32(n), err == nil && strconv.FormatInt(n, 10) == *d.CodeString
}
func sameDiagnosticCode(a, b protocol.Diagnostic) bool {
	if (a.CodeString == nil) != (b.CodeString == nil) {
		return false
	}
	if a.CodeString != nil {
		return *a.CodeString == *b.CodeString
	}
	return a.Code == b.Code
}
func diagnosticIntersection(a, b protocol.Range) bool {
	less := func(a, b protocol.Position) bool {
		return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character
	}
	return !less(a.End, b.Start) && !less(b.End, a.Start)
}
func (s *Session) prepareCodeActions(q *querySnapshot, name string, span protocol.Range, actionContext protocol.CodeActionContext, options *CodeActionRequestOptions) (func(context.Context) (CodeActionResult, error), error) {
	name, start, err := q.position(name, span.Start)
	if err != nil {
		return nil, err
	}
	_, end, err := q.position(name, span.End)
	if err != nil {
		return nil, err
	}
	if end.Line < start.Line || end.Line == start.Line && end.Character < start.Character {
		return nil, &Error{Code: CodeInvalidOptions, Message: "code action range is reversed"}
	}
	span = protocol.Range{Start: start, End: end}
	if actionContext.TriggerKind != nil && *actionContext.TriggerKind != protocol.CodeActionInvoked && *actionContext.TriggerKind != protocol.CodeActionAutomatic {
		return nil, &Error{Code: CodeInvalidOptions, Message: "invalid code action triggerKind"}
	}
	raw, err := json.Marshal(actionContext)
	if err != nil {
		return nil, err
	}
	if len(raw) > 4<<20 || len(actionContext.Diagnostics) > 1001 {
		return nil, ErrRequestLimit
	}
	var owned protocol.CodeActionContext
	_ = json.Unmarshal(raw, &owned)
	caps := codeActionCaps(options)
	diagnosticCaps := diagnosticCapabilities(nil)
	if options != nil && options.DiagnosticCapabilities != nil {
		diagnosticCaps = diagnosticCapabilities(&DiagnosticOptions{Capabilities: options.DiagnosticCapabilities})
	}
	diagnosticCaps.RelatedInformation = true
	return func(ctx context.Context) (CodeActionResult, error) {
		result := CodeActionResult{Generation: q.snapshot.Generation, Version: q.version(name), Actions: []protocol.CodeAction{}, Proposals: []*BoundEdits{}}
		// TLS clears its resolve cache at the start of every code-action request,
		// including requests which produce no actions or fail during computation.
		s.mu.Lock()
		if err := s.checkLocked(ctx); err != nil {
			s.mu.Unlock()
			return result, err
		}
		s.codeActionEpoch++
		epoch := s.codeActionEpoch
		s.mu.Unlock()
		providerState := newCodeFixProviderState()
		ctx, err := s.withDocumentFormatting(ctx, q, name)
		if err != nil {
			return result, err
		}
		// This mirrors TLS's pending-diagnostics path without a timer: compute the
		// current generation before considering the client's possibly stale list.
		current := q.diagnosticsReady[name] != q.snapshot.Generation
		diagnostics := owned.Diagnostics
		work, err := s.prepareDiagnostics(q, name, &DiagnosticOptions{Capabilities: &diagnosticCaps})
		if err != nil {
			return result, err
		}
		full, err := work(ctx)
		if err != nil {
			return result, err
		}
		if current {
			diagnostics = []protocol.Diagnostic{}
			for _, d := range full.Items {
				if diagnosticIntersection(span, d.Range) {
					diagnostics = append(diagnostics, d)
					if len(diagnostics) > 1000 {
						break
					}
				}
			}
		}
		diagnostics = deduplicateActionDiagnostics(diagnostics)
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return result, err
		}
		defer lease.release()
		ctx = lsproto.WithClientCapabilities(lease.ctx, &lsproto.ResolvedClientCapabilities{})
		ctx = ls.WithReferenceAsyncConversionState(ctx, &providerState.async)
		set := quickFixActionSet{fixAll: map[string]*codeActionTarget{}}
		var sourceTargets, importTargets, refactorTargets []codeActionTarget
		err = lease.state.native.WithService(ctx, name, func(ctx context.Context, service *ls.LanguageService) error {
			sourceTargets, err = q.sourceCodeActions(ctx, service, name, owned.Only, full.Items, !current || q.sourceDiagnosticsReady[name] == q.snapshot.Generation)
			if err != nil {
				return err
			}
			for _, diagnostic := range diagnostics {
				if err := ctx.Err(); err != nil {
					return err
				}
				code, ok := codeActionDiagnosticCode(diagnostic)
				if !ok {
					continue
				}
				_, _, err := q.position(name, diagnostic.Range.Start)
				if err != nil {
					return err
				}
				_, _, err = q.position(name, diagnostic.Range.End)
				if err != nil {
					return err
				}
				native := &lsproto.Diagnostic{Range: lsproto.Range{Start: lsproto.Position(diagnostic.Range.Start), End: lsproto.Position(diagnostic.Range.End)}, Code: &lsproto.IntegerOrString{Integer: &code}, Message: lsproto.StringOrMarkupContent{String: &diagnostic.Message}}
				fixes, err := service.ProvideReferenceCodeFixes(ctx, nativeURI(name), native)
				if err != nil {
					return err
				}
				for _, fix := range fixes {
					edit, rawChanges, err := q.codeFixChanges(fix.Changes)
					if errors.Is(err, ErrReadOnly) {
						// A frozen declaration candidate must not hide another
						// fix which edits only the user's source document.
						continue
					}
					if err != nil {
						return err
					}
					rawFix := referenceCodeFix{FixName: fix.FixName, Description: fix.Description, Changes: rawChanges, FixID: fix.FixID, FixAllDescription: fix.FixAllDescription}
					arg, _ := json.Marshal(struct {
						Action     referenceCodeFix     `json:"action"`
						Diagnostic protocol.Diagnostic  `json:"diagnostic"`
						URI        protocol.DocumentURI `json:"documentUri"`
					}{rawFix, diagnostic, sourceURI(name, q.snapshot.Environment, q.mappings)})
					action := protocol.CodeAction{Title: fix.Description, Kind: protocol.CodeActionQuickFix, Diagnostics: []protocol.Diagnostic{diagnostic}, Edit: edit, Command: &protocol.Command{Title: "", Command: "_typescript.applyCodeActionCommand", Arguments: []json.RawMessage{arg}}}
					target := codeActionTarget{name: name, fixName: fix.FixName, action: action}
					set.addAction(&target, fix.FixID)
					if caps.ResolveSupport != nil && fix.FixID != "" && set.fixAll[fix.FixID] == nil {
						other := false
						for _, d := range full.Items {
							// TLS rematerializes diagnostics on every cache read;
							// its identity check does not exclude this DTO's code.
							if sameDiagnosticCode(d, diagnostic) || (d.Code == 2339 || d.Code == 2345) && (diagnostic.Code == 2339 || diagnostic.Code == 2345) {
								other = true
								break
							}
						}
						if other {
							title := fix.FixAllDescription
							if title == "" {
								title = fix.Description + " (Fix all in file)"
							}
							arg, _ := json.Marshal(struct {
								ID string `json:"tsActionId"`
							}{fix.FixName})
							lazy := protocol.CodeAction{Title: title, Kind: protocol.CodeActionQuickFix, Diagnostics: []protocol.Diagnostic{diagnostic}, Command: &protocol.Command{Command: "_typescript.applyFixAllCodeAction", Arguments: []json.RawMessage{arg}}}
							set.addFixAll(fix.FixID, &codeActionTarget{name: name, fixID: fix.FixID, fixName: fix.FixName, action: lazy})
						}
					}
				}
			}
			refactorTargets, err = q.refactorCodeActions(ctx, service, name, span, owned, caps)
			if err != nil {
				return err
			}
			importTargets, err = q.organizeImportActions(ctx, service, name, owned.Only)
			return err
		})
		if err != nil {
			return result, publicServiceError(err)
		}
		targets := make([]codeActionTarget, 0, len(sourceTargets)+len(set.actions))
		targets = append(targets, sourceTargets...)
		for _, target := range set.actions {
			targets = append(targets, *target)
		}

		targets = append(targets, refactorTargets...)
		targets = append(targets, importTargets...)
		for i := range targets {
			if targets[i].action.Kind != protocol.CodeActionQuickFix {
				continue
			}
			preferred := preferredQuickFix(targets, i)
			targets[i].action.IsPreferred = &preferred
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.checkLocked(ctx); err != nil {
			return result, err
		}
		if s.generation != q.snapshot.Generation {
			return result, ErrContentModified
		}
		if s.diagnosticsReady == nil {
			s.diagnosticsReady = map[string]uint64{}
		}
		s.diagnosticsReady[name] = q.snapshot.Generation
		// Retained metadata is private, detached, and bounded by the normal store.
		var handle ResultHandle
		if slices.ContainsFunc(targets, func(target codeActionTarget) bool { return target.fixID != "" }) {
			metadata := codeActionMetadata{targets: targets, epoch: epoch, providerState: providerState}
			encoded, _ := json.Marshal(targetsForSize(targets))
			extra := 0
			for _, target := range targets {
				if target.refactor != nil {
					raw, _ := json.Marshal(target.refactor.metadata.infos)
					extra += len(raw) + 128
				}
			}
			handle = s.retainLocked(metadata, len(encoded)+len(targets)*64+128+extra)
		}
		for i, t := range targets {
			action := cloneCodeAction(t.action)
			var bound *BoundEdits
			if action.Edit != nil {
				b, err := s.bindEditsLocked(*action.Edit)
				if err != nil {
					return result, err
				}
				bound = &b
			}
			if t.fixID != "" {
				action.Data, _ = json.Marshal(protocol.CodeActionData{Handle: string(handle), Index: i})
			}
			var choice *RefactorChoice
			if t.refactor != nil {
				retained := t.refactor.metadata
				retained.infos = cloneRefactorInfos(retained.infos)
				raw, _ := json.Marshal(retained.infos)
				handle := s.retainLocked(retained, len(raw)+len(retained.name)+128)
				choice = &RefactorChoice{Handle: handle, RefactorIndex: t.refactor.group, ActionIndex: t.refactor.action, Actions: cloneRefactorInfos(retained.infos)[t.refactor.group].Actions}
			}
			result.Refactors = append(result.Refactors, choice)
			result.Actions = append(result.Actions, action)
			result.Proposals = append(result.Proposals, bound)
		}
		return result, nil
	}, nil
}
func targetsForSize(targets []codeActionTarget) []any {
	values := make([]any, 0, len(targets))
	for _, t := range targets {
		values = append(values, struct {
			Name, FixID, FixName string
			Action               protocol.CodeAction
		}{t.name, t.fixID, t.fixName, t.action})
	}
	return values
}
func deduplicateActionDiagnostics(diagnostics []protocol.Diagnostic) []protocol.Diagnostic {
	result := []protocol.Diagnostic{}
	indices := map[string]int{}
	for _, d := range diagnostics {
		code := strconv.FormatInt(int64(d.Code), 10)
		if d.CodeString != nil {
			code = *d.CodeString
		}
		key := fmt.Sprintf("%s-%d,%d-%d,%d", code, d.Range.Start.Line, d.Range.Start.Character, d.Range.End.Line, d.Range.End.Character)
		if i, ok := indices[key]; ok {
			result[i] = d
		} else {
			indices[key] = len(result)
			result = append(result, d)
		}
	}
	return result
}

type quickFixActionSet struct {
	actions []*codeActionTarget
	fixAll  map[string]*codeActionTarget
}

func (set *quickFixActionSet) remove(target *codeActionTarget) {
	set.actions = slices.DeleteFunc(set.actions, func(existing *codeActionTarget) bool { return existing == target })
}
func (set *quickFixActionSet) addAction(target *codeActionTarget, fixID string) {
	// TLS's Set deduplicates all actions by fix name and edit, including deferred
	// actions with no edit. The fix-all map keeps identities even when removed.
	set.actions = slices.DeleteFunc(set.actions, func(existing *codeActionTarget) bool {
		return existing.fixName == target.fixName && reflect.DeepEqual(existing.action.Edit, target.action.Edit)
	})
	set.actions = append(set.actions, target)
	if fixID != "" {
		if existing := set.fixAll[fixID]; existing != nil {
			set.remove(existing)
			set.actions = append(set.actions, existing)
		}
	}
}
func (set *quickFixActionSet) addFixAll(fixID string, target *codeActionTarget) {
	if existing := set.fixAll[fixID]; existing != nil {
		set.remove(existing)
	}
	set.addAction(target, fixID)
	set.fixAll[fixID] = target
}

func preferredQuickFix(targets []codeActionTarget, index int) bool {
	target := targets[index]
	if target.fixID != "" {
		return false
	}
	priorities := map[string]int{
		"annotateWithTypeFromJSDoc": 2, "constructorForDerivedNeedSuperCall": 2, "extendsInterfaceBecomesImplements": 2,
		"fixAwaitInSyncFunction": 2, "removeUnnecessaryAwait": 2, "fixClassIncorrectlyImplementsInterface": 3,
		"fixClassDoesntImplementInheritedAbstractMember": 3, "fixUnreachableCode": 2, "unusedIdentifier": 2,
		"forgottenThisPropertyAccess": 2, "spelling": 0, "addMissingAwait": 2, "fixOverrideModifier": 2, "addMissingNewOperator": 2, "import": 1,
	}
	priority, ok := priorities[target.fixName]
	if !ok {
		return false
	}
	for i, other := range targets {
		if i == index || other.fixID != "" {
			continue
		}
		if p, ok := priorities[other.fixName]; ok && (p > priority || target.fixName == "import" && other.fixName == "import") {
			return false
		}
	}
	return true
}

// Reference protocol command metadata uses one-based line/offset positions.
type referencePosition struct {
	Line   uint32 `json:"line"`
	Offset uint32 `json:"offset"`
}
type referenceTextChange struct {
	Start   referencePosition `json:"start"`
	End     referencePosition `json:"end"`
	NewText string            `json:"newText"`
}
type referenceFileChanges struct {
	FileName    string                `json:"fileName"`
	TextChanges []referenceTextChange `json:"textChanges"`
	IsNewFile   bool                  `json:"isNewFile,omitempty"`
}
type referenceCodeFix struct {
	FixName           string                 `json:"fixName"`
	Description       string                 `json:"description"`
	Changes           []referenceFileChanges `json:"changes"`
	FixID             string                 `json:"fixId,omitempty"`
	FixAllDescription string                 `json:"fixAllDescription,omitempty"`
}

func (q *querySnapshot) commandFileName(name string) string {
	uri := sourceURI(name, q.snapshot.Environment, q.mappings)
	if parsed, err := url.Parse(string(uri)); err == nil && parsed.Scheme == "file" {
		if parsed.Host != "" {
			return "//" + parsed.Host + parsed.Path
		}
		if len(parsed.Path) > 3 && parsed.Path[0] == '/' && parsed.Path[2] == ':' {
			return parsed.Path[1:]
		}
		return parsed.Path
	}
	return name
}
func (q *querySnapshot) codeFixChanges(changes []ls.ReferenceFileCodeChanges) (*protocol.WorkspaceEdit, []referenceFileChanges, error) {
	rawChanges := []referenceFileChanges{}
	if len(changes) == 0 {
		return nil, rawChanges, nil
	}
	edit := &protocol.WorkspaceEdit{DocumentChanges: []protocol.DocumentChange{}}
	for _, file := range changes {
		if !q.editable(file.FileName) {
			return nil, nil, ErrReadOnly
		}
		uri := sourceURI(file.FileName, q.snapshot.Environment, q.mappings)
		if file.IsNewFile {
			edit.DocumentChanges = append(edit.DocumentChanges, protocol.DocumentChange{CreateFile: &protocol.CreateFile{URI: uri}})
		}
		document := &protocol.TextDocumentEdit{TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{URI: uri, Version: q.version(file.FileName)}, Edits: []protocol.TextEdit{}}
		rawFile := referenceFileChanges{FileName: q.commandFileName(file.FileName), TextChanges: []referenceTextChange{}, IsNewFile: file.IsNewFile}
		for _, e := range file.TextChanges {
			document.Edits = append(document.Edits, protocol.TextEdit{Range: publicRange(e.Range), NewText: e.NewText})
			rawFile.TextChanges = append(rawFile.TextChanges, referenceTextChange{Start: referencePosition{Line: e.Range.Start.Line + 1, Offset: e.Range.Start.Character + 1}, End: referencePosition{Line: e.Range.End.Line + 1, Offset: e.Range.End.Character + 1}, NewText: e.NewText})
		}
		edit.DocumentChanges = append(edit.DocumentChanges, protocol.DocumentChange{TextDocumentEdit: document})
		rawChanges = append(rawChanges, rawFile)
	}
	return edit, rawChanges, nil
}

// ResolveCodeAction returns the saved action, ignoring mutable presentation
// fields supplied by callers. Only handles issued by this session are accepted.
func (s *Session) ResolveCodeAction(ctx context.Context, action protocol.CodeAction) (CodeActionResolveResult, error) {
	work, err := s.prepareResolveCodeAction(ctx, action)
	if err != nil {
		return CodeActionResolveResult{}, err
	}
	return work(ctx)
}
func (s *Session) prepareResolveCodeAction(ctx context.Context, action protocol.CodeAction) (func(context.Context) (CodeActionResolveResult, error), error) {
	action = cloneCodeAction(action)
	if len(action.Data) == 0 || string(action.Data) == "null" {
		return func(ctx context.Context) (CodeActionResolveResult, error) {
			if err := s.checkContext(ctx); err != nil {
				return CodeActionResolveResult{}, err
			}
			return CodeActionResolveResult{Action: cloneCodeAction(action)}, nil
		}, nil
	}
	var data protocol.CodeActionData
	if json.Unmarshal(action.Data, &data) != nil || data.Handle == "" {
		return nil, &Error{Code: CodeInvalidHandle, Message: "code action lacks an issued handle"}
	}
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
	metadata, ok := value.(codeActionMetadata)
	if !ok || data.Index < 0 || data.Index >= len(metadata.targets) {
		s.mu.Unlock()
		return nil, &Error{Code: CodeInvalidHandle, Message: "invalid code action handle or index"}
	}
	target := metadata.targets[data.Index]
	if target.fixID == "" {
		s.mu.Unlock()
		return nil, &Error{Code: CodeInvalidHandle, Message: "code action selector was not issued for resolve"}
	}
	target.action = cloneCodeAction(target.action)
	s.mu.Unlock()
	q, err := s.captureQuery(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	_, err = s.resolveLocked(ResultHandle(data.Handle))
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (CodeActionResolveResult, error) {
		result := CodeActionResolveResult{Generation: q.snapshot.Generation, Version: q.version(target.name), Action: cloneCodeAction(target.action)}
		result.Action.Data, _ = json.Marshal(data)
		s.mu.Lock()
		_, epochErr := s.resolveLocked(ResultHandle(data.Handle))
		currentEpoch := s.codeActionEpoch
		s.mu.Unlock()
		if epochErr != nil {
			return result, epochErr
		}
		if metadata.epoch != currentEpoch {
			return result, nil
		}
		ctx, err := s.withDocumentFormatting(ctx, q, target.name)
		if err != nil {
			return result, err
		}
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return result, err
		}
		defer lease.release()
		ctx = lsproto.WithClientCapabilities(lease.ctx, &lsproto.ResolvedClientCapabilities{})
		if target.fixID != "" {
			compute := func(ctx context.Context) error {
				return lease.state.native.WithService(ctx, target.name, func(ctx context.Context, service *ls.LanguageService) error {
					changes, err := service.ProvideReferenceCombinedCodeFix(ctx, nativeURI(target.name), target.fixID)
					if err != nil {
						return err
					}
					edit, _, err := q.codeFixChanges(changes)
					if err != nil {
						return err
					}
					if edit == nil {
						edit = &protocol.WorkspaceEdit{DocumentChanges: []protocol.DocumentChange{}}
					}
					result.Action.Edit = edit
					return nil
				})
			}
			if target.fixID == "convertToAsyncFunction" {
				err = metadata.providerState.withAsync(ctx, compute)
			} else {
				err = compute(ctx)
			}
			if err != nil {
				return result, publicServiceError(err)
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.checkLocked(ctx); err != nil {
			return result, err
		}
		if _, err := s.resolveLocked(ResultHandle(data.Handle)); err != nil {
			return result, err
		}
		if result.Action.Edit != nil {
			bound, err := s.bindEditsLocked(*result.Action.Edit)
			if err != nil {
				return result, err
			}
			result.Edits = &bound
		}
		return result, nil
	}, nil
}
func resolveCodeActionProperties(original, resolved protocol.CodeAction, caps CodeActionCapabilities) protocol.CodeAction {
	original = cloneCodeAction(original)
	if caps.ResolveSupport == nil {
		return original
	}
	for _, property := range caps.ResolveSupport.Properties {
		switch property {
		case "title":
			original.Title = resolved.Title
		case "kind":
			original.Kind = resolved.Kind
		case "diagnostics":
			original.Diagnostics = resolved.Diagnostics
		case "isPreferred":
			original.IsPreferred = resolved.IsPreferred
		case "disabled":
			if resolved.Disabled != nil {
				original.Disabled = resolved.Disabled
			}
		case "edit":
			if resolved.Edit != nil {
				original.Edit = resolved.Edit
			}
		case "command":
			if resolved.Command != nil {
				original.Command = resolved.Command
			}
		}
	}
	return cloneCodeAction(original)
}
func actionKindContains(parent, child protocol.CodeActionKind) bool {
	return parent == "" || parent == child || strings.HasPrefix(string(child), string(parent)+".")
}
