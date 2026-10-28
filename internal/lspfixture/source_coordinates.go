package lspfixture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/totoer/goya/lsp/protocol"
)

// UnicodeSourceCoordinates is the explicit, agreed exception for TLS's mixed
// compiler/document line codecs. Spans are recorded from pinned TS6 navtree,
// in original source UTF-16 offsets, never taken from an implementation response.
// Resolve opts in with an empty object and uses its issued enumeration's span.
type UnicodeSourceCoordinates struct {
	SourceSHA256 string               `json:"source_sha256,omitempty"`
	CodeLenses   []OracleCodeLensSpan `json:"code_lenses,omitempty"`
}
type OracleTextSpan struct {
	Start  int `json:"start"`
	Length int `json:"length"`
}
type OracleCodeLensSpan struct {
	Text          string          `json:"text"`
	Kind          string          `json:"kind"`
	KindModifiers string          `json:"kind_modifiers"`
	Span          OracleTextSpan  `json:"span"`
	NameSpan      *OracleTextSpan `json:"name_span,omitempty"`
}

func sourceUnitPosition(units []uint16, offset int) protocol.Position {
	offset = min(max(offset, 0), len(units))
	var p protocol.Position
	for i := 0; i < offset; {
		switch units[i] {
		case '\r':
			if i+1 < len(units) && units[i+1] == '\n' {
				if i+1 == offset {
					return p
				}
				i++
			}
			p.Line++
			p.Character = 0
		case '\n':
			p.Line++
			p.Character = 0
		default:
			p.Character++
		}
		i++
	}
	return p
}
func validOracleSpan(span OracleTextSpan, size int) bool {
	return span.Start >= 0 && span.Length >= 0 && span.Start <= size && span.Length <= size-span.Start
}
func oracleCodeLensRange(text string, node OracleCodeLensSpan) (protocol.Range, error) {
	units := utf16.Encode([]rune(text))
	if node.Text == "" || node.Kind == "" || !validOracleSpan(node.Span, len(units)) {
		return protocol.Range{}, fmt.Errorf("invalid oracle navigation span")
	}
	start, end := 0, 0
	if node.NameSpan != nil {
		span := *node.NameSpan
		if !validOracleSpan(span, len(units)) || span.Start < node.Span.Start || span.Start+span.Length > node.Span.Start+node.Span.Length {
			return protocol.Range{}, fmt.Errorf("oracle name span outside declaration")
		}
		start, end = span.Start, span.Start+span.Length
	} else {
		start = node.Span.Start
		fragment := string(utf16.Decode(units[start : start+node.Span.Length]))
		// Literal getSymbolRange regex from TLS, with JS multiline boundaries.
		pattern := regexp.MustCompile(`^([^\n\r\x{2028}\x{2029}]*?(\b|\W))` + regexp.QuoteMeta(node.Text) + `(\b|\W)`)
		for from := 0; from <= len(fragment); {
			if match := pattern.FindStringSubmatchIndex(fragment[from:]); match != nil {
				start += len(utf16.Encode([]rune(fragment[:from+match[3]])))
				break
			}
			next := strings.IndexAny(fragment[from:], "\r\n\u2028\u2029")
			if next < 0 {
				break
			}
			from += next
			r, size := utf8.DecodeRuneInString(fragment[from:])
			from += size
			if r == '\r' && from < len(fragment) && fragment[from] == '\n' {
				from++
			}
		}
		end = start + len(utf16.Encode([]rune(node.Text)))
	}
	return protocol.Range{Start: sourceUnitPosition(units, start), End: sourceUnitPosition(units, end)}, nil
}

func projectUnicodeSourceCoordinates(f Fixture, index int, raw json.RawMessage) (json.RawMessage, error) {
	step := f.Steps[index]
	policy := step.UnicodeSourceCoordinates
	if policy == nil {
		return raw, nil
	}
	if step.Notification || len(step.ExpectedError) > 0 || f.Oracle.TypeScriptVersion != "6.0.3" || f.Oracle.ServerVersion != "6.0.2" {
		return nil, fmt.Errorf("invalid Unicode coordinate projection")
	}
	enumeration := index
	selected := -1
	if step.Method == "codeLens/resolve" {
		source := step.ParamsFromCodeLens
		if source == nil || source.Step < 0 || source.Step >= index || policy.SourceSHA256 != "" || len(policy.CodeLenses) > 0 {
			return nil, fmt.Errorf("Unicode lens resolve requires its recorded enumeration")
		}
		enumeration, selected = source.Step, source.Index
		policy = f.Steps[enumeration].UnicodeSourceCoordinates
	} else if step.Method != "textDocument/codeLens" {
		return nil, fmt.Errorf("Unicode projection not allowed for %s", step.Method)
	}
	if policy == nil || len(policy.CodeLenses) == 0 || f.Steps[enumeration].Method != "textDocument/codeLens" {
		return nil, fmt.Errorf("Unicode projection lacks oracle navigation spans")
	}
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(f.Steps[enumeration].Params, &params); err != nil {
		return nil, err
	}
	texts, err := fixtureTexts(f, index)
	if err != nil {
		return nil, err
	}
	text, ok := texts[params.TextDocument.URI]
	digest := sha256.Sum256([]byte(text))
	if !ok || !strings.ContainsAny(text, "\u2028\u2029") || policy.SourceSHA256 != hex.EncodeToString(digest[:]) {
		return nil, fmt.Errorf("Unicode projection source/hash mismatch")
	}
	var recorded []json.RawMessage
	if err = json.Unmarshal(f.Steps[enumeration].Expected, &recorded); err != nil || len(recorded) != len(policy.CodeLenses) {
		return nil, fmt.Errorf("Unicode projection changed oracle lens count")
	}
	var entries []map[string]json.RawMessage
	if selected >= 0 {
		if selected >= len(policy.CodeLenses) {
			return nil, fmt.Errorf("Unicode lens index out of bounds")
		}
		var item map[string]json.RawMessage
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		entries = []map[string]json.RawMessage{item}
	} else if err = json.Unmarshal(raw, &entries); err != nil || len(entries) != len(policy.CodeLenses) {
		return nil, fmt.Errorf("Unicode projection changed result count")
	}
	for i, item := range entries {
		n := i
		if selected >= 0 {
			n = selected
		}
		var oldRange map[string]json.RawMessage
		if json.Unmarshal(item["range"], &oldRange) != nil || len(oldRange["start"]) == 0 || len(oldRange["end"]) == 0 {
			return nil, fmt.Errorf("missing raw oracle range")
		}
		var data struct {
			URI string `json:"uri"`
		}
		if json.Unmarshal(item["data"], &data) != nil || data.URI != params.TextDocument.URI {
			return nil, fmt.Errorf("Unicode lens URI mismatch")
		}
		r, err := oracleCodeLensRange(text, policy.CodeLenses[n])
		if err != nil {
			return nil, err
		}
		item["range"], err = json.Marshal(r)
		if err != nil {
			return nil, err
		}
		if commandRaw := item["command"]; len(commandRaw) > 0 {
			var command map[string]json.RawMessage
			if err = json.Unmarshal(commandRaw, &command); err != nil {
				return nil, err
			}
			var args []json.RawMessage
			if json.Unmarshal(command["arguments"], &args) != nil || len(args) != 3 {
				return nil, fmt.Errorf("Unicode lens command lacks exact arguments")
			}
			var targets []json.RawMessage
			if json.Unmarshal(args[2], &targets) != nil || len(targets) != 0 {
				return nil, fmt.Errorf("Unicode target ranges require separate recorded source spans")
			}
			args[1], err = json.Marshal(r.Start)
			if err != nil {
				return nil, err
			}
			command["arguments"], err = json.Marshal(args)
			if err != nil {
				return nil, err
			}
			item["command"], err = json.Marshal(command)
			if err != nil {
				return nil, err
			}
		}
	}
	if selected >= 0 {
		return json.Marshal(entries[0])
	}
	return json.Marshal(entries)
}
