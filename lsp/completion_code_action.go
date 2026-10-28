package lsp

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/totoer/goya/lsp/protocol"
)

// CompletionCodeAction is the source-compatible completion action payload.
// Only the first action is selected, as in typescript-language-server. External
// tsserver commands are rejected before any proposal or client interaction.
type CompletionCodeAction struct {
	Description string                    `json:"description"`
	Changes     []CompletionFileCodeEdits `json:"changes"`
	Commands    []json.RawMessage         `json:"commands,omitempty"`
}

// CompletionFileCodeEdits uses caller-visible command paths and one-based UTF-16
// source locations, matching the completion command arguments of the reference.
type CompletionFileCodeEdits struct {
	FileName    string               `json:"fileName"`
	TextChanges []CompletionCodeEdit `json:"textChanges"`
}
type CompletionCodeEdit struct {
	Start   CompletionSourceLocation `json:"start"`
	End     CompletionSourceLocation `json:"end"`
	NewText string                   `json:"newText"`
}
type CompletionSourceLocation struct {
	Line   uint32 `json:"line"`
	Offset uint32 `json:"offset"`
}
type CompletionCodeActionEditsResult struct {
	Generation uint64
	Edits      *BoundEdits
}

// CompletionCodeActionEdits validates the first supplied action against the
// current snapshot and returns an owned generation-bound proposal. It never
// invokes external commands or applies changes to the catalog.
func (s *Session) CompletionCodeActionEdits(ctx context.Context, actions []CompletionCodeAction) (CompletionCodeActionEditsResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return CompletionCodeActionEditsResult{}, err
	}
	return s.completionCodeActionEdits(ctx, q, actions)
}
func (s *Session) completionCodeActionEdits(ctx context.Context, q *querySnapshot, actions []CompletionCodeAction) (CompletionCodeActionEditsResult, error) {
	result := CompletionCodeActionEditsResult{Generation: q.snapshot.Generation}
	if len(actions) == 0 {
		return result, nil
	}
	action := actions[0]
	if len(action.Commands) > 0 {
		return result, &Error{Code: CodeInvalidOptions, Message: "external tsserver completion commands are not supported"}
	}
	edit := protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{}}
	for _, file := range action.Changes {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		name, err := q.refactorCommandName(file.FileName)
		if err != nil {
			return result, err
		}
		if !q.editable(name) {
			return result, ErrReadOnly
		}
		text, ok := q.text(name)
		if !ok {
			return result, ErrReadOnly
		}
		changes := make([]protocol.TextEdit, 0, len(file.TextChanges))
		type interval struct{ start, end int }
		intervals := make([]interval, 0, len(file.TextChanges))
		for _, change := range file.TextChanges {
			if change.Start.Line == 0 || change.Start.Offset == 0 || change.End.Line == 0 || change.End.Offset == 0 {
				return result, &Error{Code: CodeInvalidEdit, Message: "completion edit requires one-based source locations"}
			}
			span := protocol.Range{Start: protocol.Position{Line: change.Start.Line - 1, Character: change.Start.Offset - 1}, End: protocol.Position{Line: change.End.Line - 1, Character: change.End.Offset - 1}}
			start, err := OffsetAt(text, span.Start)
			if err != nil {
				return result, err
			}
			end, err := OffsetAt(text, span.End)
			if err != nil {
				return result, err
			}
			actualStart, _ := PositionAt(text, start)
			actualEnd, _ := PositionAt(text, end)
			if actualStart != span.Start || actualEnd != span.End {
				return result, &Error{Code: CodeInvalidEdit, Message: "completion edit range is outside the captured source"}
			}
			if start > end {
				return result, &Error{Code: CodeInvalidEdit, Message: "reversed completion edit range"}
			}
			intervals = append(intervals, interval{start, end})
			changes = append(changes, protocol.TextEdit{Range: span, NewText: change.NewText})
		}
		slices.SortStableFunc(intervals, func(a, b interval) int {
			if a.start < b.start {
				return -1
			}
			if a.start > b.start {
				return 1
			}
			return 0
		})
		for i := 1; i < len(intervals); i++ {
			if intervals[i].start < intervals[i-1].end {
				return result, &Error{Code: CodeInvalidEdit, Message: "overlapping completion edits"}
			}
		}
		// The reference assigns each file entry, retaining the last duplicate file.
		edit.Changes[sourceURI(name, q.snapshot.Environment, q.mappings)] = changes
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
}
