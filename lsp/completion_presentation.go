package lsp

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/totoer/goya/lsp/protocol"
)

type completionPresentation struct {
	Position              protocol.Position
	Member, NewIdentifier bool
	StringLiteral         bool
	JSDoc                 bool
}

var completionDotAccessor = regexp.MustCompile(`\??\.[\t\v\f ]*$`)

// TLS includes an accessor typed immediately before invoking member completion
// in the replacement. The native service uses editor-independent insertions.
func adaptCompletionPresentation(item *protocol.CompletionItem, q *querySnapshot, name string, caps CompletionCapabilities, info completionPresentation) {
	settings := q.snapshot.EditorOptions
	if settings.CompletionDisableFilterText {
		defer func() {
			item.FilterText = nil
			// The reference still installs the unadorned label for optional
			// entries after its ordinary filter-text branch has been skipped.
			if strings.HasSuffix(item.Label, "?") {
				value := strings.TrimSuffix(item.Label, "?")
				item.FilterText = &value
			}
		}()
	}
	if info.JSDoc && item.InsertText != nil && item.FilterText == nil {
		value := *item.InsertText
		item.FilterText = &value
	}
	originalSnippet := item.InsertTextFormat != nil && *item.InsertTextFormat == 2
	if info.StringLiteral && caps.CommitCharactersSupport {
		item.CommitCharacters = []string{}
	}
	if caps.CommitCharactersSupport && !settings.Completions.CompleteFunctionCalls && !info.NewIdentifier && item.Kind != nil && *item.Kind != 1 && *item.Kind != 12 && len(item.CommitCharacters) > 0 && !slices.Contains(item.CommitCharacters, "(") {
		item.CommitCharacters = append(item.CommitCharacters, "(")
	}
	if caps.SnippetSupport && settings.Completions.CompleteFunctionCalls && item.Kind != nil && (*item.Kind == 2 || *item.Kind == 3) {
		format := uint32(2)
		item.InsertTextFormat = &format
	}
	insertText := item.InsertText
	if item.TextEdit != nil {
		item.InsertText = nil
	}
	if !info.Member || originalSnippet {
		return
	}
	text, ok := q.text(name)
	if !ok {
		return
	}
	offset, err := OffsetAt(text, info.Position)
	if err != nil {
		return
	}
	lineStart, err := OffsetAt(text, protocol.Position{Line: info.Position.Line})
	if err != nil {
		return
	}
	match := completionDotAccessor.FindStringIndex(text[lineStart:offset])
	if match == nil {
		return
	}
	start, err := PositionAt(text, lineStart+match[0])
	if err != nil {
		return
	}
	value := item.Label
	if insertText != nil {
		value = *insertText
	}
	insert := text[lineStart+match[0]:offset] + value
	item.FilterText = &insert
	if item.TextEdit != nil {
		return
	}
	item.TextEdit = &protocol.CompletionTextEdit{NewText: insert, Range: &protocol.Range{Start: start, End: info.Position}}
	item.InsertText = nil
}

var existingCompletionCall = regexp.MustCompile(`(?i)^[a-z_$0-9]*\s*\(`)
var snippetTextEscaper = strings.NewReplacer(`\`, `\\`, `$`, `\$`, `}`, `\}`)

func completeFunctionCall(item *protocol.CompletionItem, q *querySnapshot, name string, position protocol.Position, parameters []string, optional bool) {
	text, ok := q.text(name)
	if !ok {
		return
	}
	offset, err := OffsetAt(text, position)
	if err != nil {
		return
	}
	after := strings.SplitN(text[offset:], "\n", 2)[0]
	if existingCompletionCall.MatchString(after) {
		return
	}
	base := item.Label
	if item.InsertText != nil {
		base = *item.InsertText
	} else if item.TextEdit != nil {
		base = item.TextEdit.NewText
	}
	snippet := snippetTextEscaper.Replace(base) + "("
	for index, param := range parameters {
		if index > 0 {
			snippet += ", "
		}
		snippet += "${" + strconv.Itoa(index+1) + ":" + snippetTextEscaper.Replace(param) + "}"
	}
	if optional {
		snippet += "$" + strconv.Itoa(len(parameters)+1)
	}
	snippet += ")$0"
	item.InsertText = &snippet
	format := uint32(2)
	item.InsertTextFormat = &format
	if item.TextEdit != nil {
		item.TextEdit.NewText = snippet
	}
}
