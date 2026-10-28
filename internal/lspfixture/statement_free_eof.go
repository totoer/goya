package lspfixture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/totoer/goya/lsp/protocol"
)

// EOFPastEOFInsertion records the pinned oracle SourceFile shape independently
// of its edit span. Only move-to-existing-file can opt into this exception.
type EOFPastEOFInsertion struct {
	SourceFileEnd  int `json:"source_file_end"`
	StatementCount int `json:"statement_count"`
}

func (proof *EOFPastEOFInsertion) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if len(fields) != 2 {
		return fmt.Errorf("past-EOF source shape requires end and statement count")
	}
	var end, count *int
	if json.Unmarshal(fields["source_file_end"], &end) != nil || json.Unmarshal(fields["statement_count"], &count) != nil || end == nil || count == nil {
		return fmt.Errorf("past-EOF source shape requires recorded integer fields")
	}
	proof.SourceFileEnd = *end
	proof.StatementCount = *count
	return nil
}

func projectStatementFreeEOF(f Fixture, n int, span EOFClientRequestEdit, raw json.RawMessage, texts map[string]string) (json.RawMessage, error) {
	if f.Oracle.ServerVersion != "6.0.2" || f.Oracle.TypeScriptVersion != "6.0.3" {
		return nil, fmt.Errorf("past-EOF evidence requires the pinned oracle")
	}
	text, ok := texts[span.URI]
	if !ok {
		return nil, fmt.Errorf("past-EOF source missing")
	}
	units := utf16.Encode([]rune(text))
	digest := sha256.Sum256([]byte(text))
	proof := span.PastEOFInsertion
	if proof == nil || proof.SourceFileEnd != len(units) || proof.StatementCount != 0 || span.Start != len(units)+1 || span.Length != 0 || span.SourceSHA256 != hex.EncodeToString(digest[:]) || !statementFreeText(text) {
		return nil, fmt.Errorf("past-EOF source shape/span/hash mismatch")
	}
	var command struct {
		Command   string `json:"command"`
		Arguments []struct {
			Refactor    string `json:"refactor"`
			Action      string `json:"action"`
			Interactive struct {
				TargetFile string `json:"targetFile"`
			} `json:"interactiveRefactorArguments"`
		} `json:"arguments"`
	}
	if json.Unmarshal(f.Steps[n].Params, &command) != nil || command.Command != "_typescript.applyRefactoring" || len(command.Arguments) != 1 || command.Arguments[0].Refactor != "Move to file" || command.Arguments[0].Action != "Move to file" || !strings.HasPrefix(span.URI, "{{ROOT_URI}}/") {
		return nil, fmt.Errorf("past-EOF insertion requires an existing-file move")
	}
	target, err := url.PathUnescape(strings.TrimPrefix(span.URI, "{{ROOT_URI}}"))
	if err != nil {
		return nil, err
	}
	if command.Arguments[0].Interactive.TargetFile != "{{ROOT_PATH}}"+target {
		return nil, fmt.Errorf("past-EOF target differs from move target")
	}
	var edits []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &edits); err != nil {
		return nil, err
	}
	if span.Edit < 0 || span.Edit >= len(edits) {
		return nil, fmt.Errorf("past-EOF edit index out of bounds")
	}
	var sourceRange protocol.Range
	if err := json.Unmarshal(edits[span.Edit]["range"], &sourceRange); err != nil {
		return nil, err
	}
	// Compiler coordinates count Unicode separators. LSP coordinates keep those
	// units on their source line. The raw oracle range must match EOF+1 exactly.
	oracle := compilerEOFPosition(units)
	oracle.Character++
	if sourceRange.Start != oracle || sourceRange.End != oracle {
		return nil, fmt.Errorf("past-EOF oracle range differs from recorded span")
	}
	canonical := sourceUnitPosition(units, len(units))
	edits[span.Edit]["range"], _ = json.Marshal(protocol.Range{Start: canonical, End: canonical})
	return json.Marshal(edits)
}
func compilerEOFPosition(units []uint16) protocol.Position {
	var p protocol.Position
	for i := 0; i < len(units); i++ {
		switch units[i] {
		case '\r':
			if i+1 < len(units) && units[i+1] == '\n' {
				i++
			}
			p.Line++
			p.Character = 0
		case '\n', 0x2028, 0x2029:
			p.Line++
			p.Character = 0
		default:
			p.Character++
		}
	}
	return p
}

// Validate the recorded statement-free shape without consulting implementation
// ASTs or outputs. Only whitespace, comments and a leading shebang are allowed.
func statementFreeText(text string) bool {
	if strings.HasPrefix(text, "#!") {
		if i := strings.IndexAny(text, "\r\n\u2028\u2029"); i >= 0 {
			text = text[i:]
		} else {
			return true
		}
	}
	for {
		text = strings.TrimLeftFunc(text, func(r rune) bool { return unicode.IsSpace(r) || r == 0xfeff })
		if text == "" {
			return true
		}
		if strings.HasPrefix(text, "//") {
			if i := strings.IndexAny(text, "\r\n\u2028\u2029"); i >= 0 {
				text = text[i:]
				continue
			}
			return true
		}
		if strings.HasPrefix(text, "/*") {
			if i := strings.Index(text[2:], "*/"); i >= 0 {
				text = text[i+4:]
				continue
			}
			return true
		}
		return false
	}
}
