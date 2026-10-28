package lsp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/refactor"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

const applyRefactoringCommand = "_typescript.applyRefactoring"
const selectRefactoringCommand = "_typescript.selectRefactoring"

// RefactorChoice accompanies a CodeActions entry. ActionIndex is -1 for a
// grouped menu; choose an index from Actions before calling ApplyRefactor.
type RefactorChoice struct {
	Handle                     ResultHandle
	RefactorIndex, ActionIndex int
	Actions                    []protocol.RefactorActionInfo
}
type refactorActionTarget struct {
	metadata      refactorMetadata
	group, action int
}
type refactorFileRange struct {
	File        string `json:"file"`
	StartLine   uint32 `json:"startLine"`
	StartOffset uint32 `json:"startOffset"`
	EndLine     uint32 `json:"endLine"`
	EndOffset   uint32 `json:"endOffset"`
}
type applyRefactorArgument struct {
	refactorFileRange
	Refactor             string `json:"refactor"`
	Action               string `json:"action"`
	InteractiveArguments *struct {
		TargetFile string `json:"targetFile"`
	} `json:"interactiveRefactorArguments,omitempty"`
}

func refactorActionKind(action protocol.RefactorActionInfo) protocol.CodeActionKind {
	if action.Kind != "" {
		return action.Kind
	}
	for _, entry := range []struct{ prefix, kind string }{
		{"function_", "refactor.extract.function"}, {"constant_", "refactor.extract.constant"},
		{"Extract to type alias", "refactor.extract.type"}, {"Extract to interface", "refactor.extract.interface"},
		{"Move to file", "refactor.move.file"}, {"Move to a new file", "refactor.move.newFile"},
		{"Convert namespace import", "refactor.rewrite.import"}, {"Convert named imports", "refactor.rewrite.import"},
		{"Convert default export", "refactor.rewrite.export"}, {"Convert named export", "refactor.rewrite.export"},
		{"Convert parameters to destructured object", "refactor.rewrite.parameters.toDestructured"},
		{"Generate 'get' and 'set' accessors", "refactor.rewrite.property.generateAccessors"},
	} {
		if strings.HasPrefix(action.Name, entry.prefix) {
			return protocol.CodeActionKind(entry.kind)
		}
	}
	return protocol.CodeActionRefactor
}
func (q *querySnapshot) refactorCodeActions(ctx context.Context, service *ls.LanguageService, name string, span protocol.Range, actionContext protocol.CodeActionContext, caps CodeActionCapabilities) ([]codeActionTarget, error) {
	kinds := []string{}
	if actionContext.Only == nil {
		kinds = append(kinds, "")
	} else {
		for _, kind := range *actionContext.Only {
			if kind == protocol.CodeActionRefactor || strings.HasPrefix(string(kind), "refactor.") {
				kinds = append(kinds, string(kind))
			}
		}
	}
	targets := []codeActionTarget{}
	args := refactorFileRange{File: q.commandFileName(name), StartLine: span.Start.Line + 1, StartOffset: span.Start.Character + 1, EndLine: span.End.Line + 1, EndOffset: span.End.Character + 1}
	nativeSpan := lsproto.Range{Start: lsproto.Position(span.Start), End: lsproto.Position(span.End)}
	for _, kind := range kinds {
		options := refactor.Options{Kind: kind, IncludeInteractive: caps.SupportsMoveToFileCodeAction, Invoked: actionContext.TriggerKind != nil && *actionContext.TriggerKind == protocol.CodeActionInvoked}
		infos, err := refactor.Applicable(ctx, service, name, nativeSpan, options)
		if err != nil {
			return nil, err
		}
		metadata := refactorMetadata{name: name, span: span, options: options, infos: cloneRefactorInfos(infos)}
		for groupIndex, info := range infos {
			if info.Inlineable != nil && !*info.Inlineable {
				rawInfo, _ := json.Marshal(info)
				rawArgs, _ := json.Marshal(args)
				action := protocol.CodeAction{Title: info.Description, Kind: protocol.CodeActionRefactor, Command: &protocol.Command{Title: info.Description, Command: selectRefactoringCommand, Arguments: []json.RawMessage{rawInfo, rawArgs}}}
				targets = append(targets, codeActionTarget{name: name, action: action, refactor: &refactorActionTarget{metadata: metadata, group: groupIndex, action: -1}})
				continue
			}
			for actionIndex, item := range info.Actions {
				if item.NotApplicableReason != "" && !caps.DisabledSupport {
					continue
				}
				if item.IsInteractive && (!caps.SupportsMoveToFileCodeAction || item.Name != "Move to file") {
					continue
				}
				action := protocol.CodeAction{Title: item.Description, Kind: refactorActionKind(item)}
				if item.NotApplicableReason != "" {
					action.Disabled = &protocol.CodeActionDisabled{Reason: item.NotApplicableReason}
				} else {
					argument := applyRefactorArgument{refactorFileRange: args, Refactor: info.Name, Action: item.Name}
					raw, _ := json.Marshal(argument)
					action.Command = &protocol.Command{Title: item.Description, Command: applyRefactoringCommand, Arguments: []json.RawMessage{raw}}
				}
				targets = append(targets, codeActionTarget{name: name, action: action, refactor: &refactorActionTarget{metadata: metadata, group: groupIndex, action: actionIndex}})
			}
		}
	}
	return targets, ctx.Err()
}
