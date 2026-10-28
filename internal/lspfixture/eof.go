package lspfixture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/totoer/goya/lsp/protocol"
)

// SourceEOF opts into the agreed exception for TLS's history-dependent EOF
// presentation and explicit EOF+1 insertions into statement-free move targets.
// Expected remains the unmodified oracle response. Folding spans
// are recorded from the pinned oracle's OutliningElementsCollector.collectElements.
type SourceEOF struct {
	ClientRequestEdits []EOFClientRequestEdit `json:"client_request_edits,omitempty"`
	CodeActionEdits    []EOFCodeActionEdit    `json:"code_action_edits,omitempty"`
	FoldingSpans       []EOFFoldingSpan       `json:"folding_spans,omitempty"`
}
type EOFFoldingSpan struct {
	Index        int    `json:"index"`
	Start        int    `json:"start"`
	Length       int    `json:"length"`
	SourceSHA256 string `json:"source_sha256"`
}

// fixtureTexts replays only supplied inputs, never implementation responses.
func fixtureTexts(f Fixture, through int) (map[string]string, error) {
	base := make(map[string]string)
	for name, text := range f.Project.Files {
		base["{{ROOT_URI}}/"+strings.TrimPrefix(name, "/")] = text
	}
	for name, text := range f.Project.Declarations {
		base["{{ROOT_URI}}/"+strings.TrimPrefix(name, "/")] = text
	}
	overlays := make(map[string]string)
	for _, step := range f.Steps[:through+1] {
		switch step.Method {
		case "textDocument/didOpen":
			var p struct {
				TextDocument struct {
					URI  string `json:"uri"`
					Text string `json:"text"`
				} `json:"textDocument"`
			}
			if err := json.Unmarshal(step.Params, &p); err != nil {
				return nil, err
			}
			overlays[p.TextDocument.URI] = p.TextDocument.Text
		case "textDocument/didChange":
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
				Changes []struct {
					Range *protocol.Range `json:"range"`
					Text  string          `json:"text"`
				} `json:"contentChanges"`
			}
			if err := json.Unmarshal(step.Params, &p); err != nil {
				return nil, err
			}
			text, ok := overlays[p.TextDocument.URI]
			if !ok {
				return nil, fmt.Errorf("EOF replay change without open document")
			}
			for _, change := range p.Changes {
				if change.Range == nil {
					text = change.Text
					continue
				}
				start, err := fixtureOffset(text, change.Range.Start)
				if err != nil {
					return nil, err
				}
				end, err := fixtureOffset(text, change.Range.End)
				if err != nil || start > end {
					return nil, fmt.Errorf("invalid EOF replay edit")
				}
				text = text[:start] + change.Text + text[end:]
			}
			overlays[p.TextDocument.URI] = text
		case "textDocument/didClose":
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			if err := json.Unmarshal(step.Params, &p); err != nil {
				return nil, err
			}
			delete(overlays, p.TextDocument.URI)
		}
	}
	for uri, text := range overlays {
		base[uri] = text
	}
	return base, nil
}

func fixturePosition(text string) protocol.Position {
	var p protocol.Position
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		i += size
		switch r {
		case '\r':
			if i < len(text) && text[i] == '\n' {
				i++
			}
			p.Line++
			p.Character = 0
		case '\n':
			p.Line++
			p.Character = 0
		default:
			p.Character += uint32(utf16.RuneLen(r))
		}
	}
	return p
}
func fixtureOffset(text string, p protocol.Position) (int, error) {
	// Compare rune boundaries without accepting a split surrogate or an interior
	// CRLF byte. Input replay must not silently change the recorded source.
	var current protocol.Position
	for i := 0; i <= len(text); {
		if current == p {
			return i, nil
		}
		if i == len(text) {
			break
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		i += size
		switch r {
		case '\r':
			if i < len(text) && text[i] == '\n' {
				i++
			}
			current.Line++
			current.Character = 0
		case '\n':
			current.Line++
			current.Character = 0
		default:
			current.Character += uint32(utf16.RuneLen(r))
		}
	}

	return 0, fmt.Errorf("invalid fixture UTF-16 position %+v", p)
}
func legacyEOF(text string) (protocol.Position, protocol.Position, bool) {
	canonical := fixturePosition(text)
	newline := 0
	if strings.HasSuffix(text, "\r\n") {
		newline = 2
	} else if strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\r") {
		newline = 1
	}
	if newline == 0 {
		return protocol.Position{}, canonical, false
	}
	legacy := fixturePosition(text[:len(text)-newline])
	legacy.Character += uint32(newline)
	return legacy, canonical, true
}

func projectSourceEOF(f Fixture, n int, raw json.RawMessage) (json.RawMessage, error) {
	step := f.Steps[n]
	policy := step.SourceEOF
	if policy == nil {
		return raw, nil
	}
	if step.Notification || len(step.ExpectedError) > 0 {
		return nil, fmt.Errorf("EOF projection requires a successful request")
	}
	switch step.Method {
	case "workspace/executeCommand":
		if len(policy.ClientRequestEdits) == 0 || len(policy.FoldingSpans) != 0 || len(policy.CodeActionEdits) != 0 {
			return nil, fmt.Errorf("command EOF projection requires explicit client edit spans")
		}
		if _, err := projectClientRequestEOF(f, n, step.ExpectedClientRequests); err != nil {
			return nil, err
		}
		return raw, nil
	case "textDocument/selectionRange", "textDocument/foldingRange", "textDocument/prepareCallHierarchy", "callHierarchy/incomingCalls", "callHierarchy/outgoingCalls", "textDocument/codeAction", "codeAction/resolve":
	default:
		return nil, fmt.Errorf("EOF projection not allowed for %s", step.Method)
	}
	if len(policy.ClientRequestEdits) > 0 {
		return nil, fmt.Errorf("client EOF spans require a command request")
	}
	if len(policy.FoldingSpans) > 0 && step.Method != "textDocument/foldingRange" {
		return nil, fmt.Errorf("EOF folding spans on another method")
	}
	if len(policy.CodeActionEdits) > 0 && step.Method != "textDocument/codeAction" && step.Method != "codeAction/resolve" {
		return nil, fmt.Errorf("EOF action spans on another method")
	}

	texts, err := fixtureTexts(f, n)
	if err != nil {
		return nil, err
	}
	if step.Method == "textDocument/codeAction" || step.Method == "codeAction/resolve" {
		return projectCodeActionEOF(f, n, raw, texts)
	}

	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Item struct {
			URI string `json:"uri"`
		} `json:"item"`
	}
	paramData := step.Params
	if source := step.ParamsFromCallHierarchy; source != nil {
		if source.Step < 0 || source.Step >= n {
			return nil, fmt.Errorf("invalid EOF hierarchy source")
		}
		paramData, err = hierarchyItemAt(f.Steps[source.Step].Method, f.Steps[source.Step].Expected, source.Index)
		if err != nil {
			return nil, err
		}
	}
	if err = json.Unmarshal(paramData, &params); err != nil {
		return nil, err
	}
	var entries []map[string]json.RawMessage
	if err = json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	projectRange := func(uri string, data json.RawMessage) (json.RawMessage, error) {
		text, ok := texts[uri]
		if !ok {
			return nil, fmt.Errorf("EOF source missing: %s", uri)
		}
		var r protocol.Range
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		legacy, canonical, ok := legacyEOF(text)
		if ok && r.End == legacy {
			r.End = canonical
			return json.Marshal(r)
		}
		return data, nil
	}
	projectItem := func(item map[string]json.RawMessage) error {
		var uri string
		if err := json.Unmarshal(item["uri"], &uri); err != nil {
			return err
		}
		for _, key := range []string{"range", "selectionRange"} {
			value, err := projectRange(uri, item[key])
			if err != nil {
				return err
			}
			item[key] = value
		}
		return nil
	}
	switch step.Method {
	case "textDocument/selectionRange":
		var visit func(map[string]json.RawMessage) error
		visit = func(item map[string]json.RawMessage) error {
			value, err := projectRange(params.TextDocument.URI, item["range"])
			if err != nil {
				return err
			}
			item["range"] = value
			if len(item["parent"]) > 0 && string(item["parent"]) != "null" {
				var parent map[string]json.RawMessage
				if err = json.Unmarshal(item["parent"], &parent); err != nil {
					return err
				}
				if err = visit(parent); err != nil {
					return err
				}
				item["parent"], err = json.Marshal(parent)
			}
			return err
		}
		for _, item := range entries {
			if err = visit(item); err != nil {
				return nil, err
			}
		}
	case "textDocument/foldingRange":
		if len(policy.FoldingSpans) == 0 {
			return nil, fmt.Errorf("EOF folding projection requires recorded spans")
		}
		text, ok := texts[params.TextDocument.URI]
		if !ok {
			return nil, fmt.Errorf("EOF folding source missing")
		}
		digest := sha256.Sum256([]byte(text))
		legacy, canonical, hasEOF := legacyEOF(text)
		seen := make(map[int]bool)
		for _, span := range policy.FoldingSpans {
			if seen[span.Index] || span.Index < 0 || span.Index >= len(entries) || span.Start < 0 || span.Length <= 0 || span.Start+span.Length != len(utf16.Encode([]rune(text))) || span.SourceSHA256 != hex.EncodeToString(digest[:]) || !hasEOF {
				return nil, fmt.Errorf("invalid recorded EOF folding span")
			}
			seen[span.Index] = true
			// Check the recorded span's start as well as its exact EOF endpoint.
			units := utf16.Encode([]rune(text))
			start := fixturePosition(string(utf16.Decode(units[:span.Start])))
			var first, last uint32
			if json.Unmarshal(entries[span.Index]["startLine"], &first) != nil || json.Unmarshal(entries[span.Index]["endLine"], &last) != nil || first != start.Line || last != legacy.Line {
				return nil, fmt.Errorf("recorded EOF folding span disagrees with raw oracle")
			}
			entries[span.Index]["endLine"], _ = json.Marshal(canonical.Line)
		}
	case "textDocument/prepareCallHierarchy":
		for _, item := range entries {
			if err = projectItem(item); err != nil {
				return nil, err
			}
		}
	default:
		key := "from"
		if step.Method == "callHierarchy/outgoingCalls" {
			key = "to"
		}
		for _, call := range entries {
			var item map[string]json.RawMessage
			if err = json.Unmarshal(call[key], &item); err != nil {
				return nil, err
			}
			if err = projectItem(item); err != nil {
				return nil, err
			}
			call[key], err = json.Marshal(item)
			if err != nil {
				return nil, err
			}
			uri := params.Item.URI
			if key == "from" {
				if err = json.Unmarshal(item["uri"], &uri); err != nil {
					return nil, err
				}
			}
			var ranges []json.RawMessage
			if err = json.Unmarshal(call["fromRanges"], &ranges); err != nil {
				return nil, err
			}
			for i, r := range ranges {
				ranges[i], err = projectRange(uri, r)
				if err != nil {
					return nil, err
				}
			}
			call["fromRanges"], err = json.Marshal(ranges)
			if err != nil {
				return nil, err
			}
		}
	}
	return json.Marshal(entries)
}
