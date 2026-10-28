package lspfixture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf16"

	"github.com/totoer/goya/lsp/protocol"
)

// EOFCodeActionEdit identifies an independently recorded TS6 edit ending at source EOF.
// The raw oracle response remains intact; only its exact EOF coordinates project.
type EOFCodeActionEdit struct {
	Action         int    `json:"action"`
	DocumentChange int    `json:"document_change"`
	Edit           int    `json:"edit"`
	CommandFile    *int   `json:"command_file,omitempty"`
	URI            string `json:"uri"`
	Start          int    `json:"start"`
	Length         int    `json:"length"`
	SourceSHA256   string `json:"source_sha256"`
}

func projectCodeActionEOF(f Fixture, n int, raw json.RawMessage, texts map[string]string) (json.RawMessage, error) {
	if f.Oracle.ServerVersion != "6.0.2" || f.Oracle.TypeScriptVersion != "6.0.3" {
		return nil, fmt.Errorf("code action EOF evidence requires the pinned oracle")
	}
	spans := f.Steps[n].SourceEOF.CodeActionEdits
	if len(spans) == 0 {
		return nil, fmt.Errorf("code action EOF projection requires recorded source spans")
	}
	single := f.Steps[n].Method == "codeAction/resolve"
	var actions []map[string]json.RawMessage
	if single {
		var action map[string]json.RawMessage
		if err := json.Unmarshal(raw, &action); err != nil {
			return nil, err
		}
		actions = []map[string]json.RawMessage{action}
	} else if err := json.Unmarshal(raw, &actions); err != nil {
		return nil, err
	}
	seen := map[[3]int]bool{}
	for _, span := range spans {
		key := [3]int{span.Action, span.DocumentChange, span.Edit}
		if seen[key] || span.Action < 0 || span.Action >= len(actions) || span.DocumentChange < 0 || span.Edit < 0 {
			return nil, fmt.Errorf("invalid or duplicate EOF code action index")
		}
		seen[key] = true
		text, ok := texts[span.URI]
		if !ok {
			return nil, fmt.Errorf("EOF code action source missing")
		}
		digest := sha256.Sum256([]byte(text))
		units := utf16.Encode([]rune(text))
		if span.SourceSHA256 != hex.EncodeToString(digest[:]) || span.Start < 0 || span.Start > len(units) || span.Length < 0 || span.Length != len(units)-span.Start {
			return nil, fmt.Errorf("EOF code action source span/hash mismatch")
		}
		if span.Start > 0 && span.Start < len(units) && units[span.Start] >= 0xdc00 && units[span.Start] <= 0xdfff && units[span.Start-1] >= 0xd800 && units[span.Start-1] <= 0xdbff {
			return nil, fmt.Errorf("EOF source span splits a surrogate pair")
		}
		legacy, canonical, hasLegacy := legacyEOF(text)
		canonicalStart := sourceUnitPosition(units, span.Start)
		action := actions[span.Action]
		var workspace map[string]json.RawMessage
		if err := json.Unmarshal(action["edit"], &workspace); err != nil {
			return nil, err
		}
		var documents []map[string]json.RawMessage
		if err := json.Unmarshal(workspace["documentChanges"], &documents); err != nil {
			return nil, err
		}
		if span.DocumentChange >= len(documents) {
			return nil, fmt.Errorf("EOF code action document index out of bounds")
		}
		document := documents[span.DocumentChange]
		var identifier struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(document["textDocument"], &identifier); err != nil {
			return nil, err
		}
		if identifier.URI != span.URI {
			return nil, fmt.Errorf("EOF code action target differs from recorded source")
		}
		var edits []map[string]json.RawMessage
		if err := json.Unmarshal(document["edits"], &edits); err != nil {
			return nil, err
		}
		if span.Edit >= len(edits) {
			return nil, fmt.Errorf("EOF code action edit index out of bounds")
		}
		var sourceRange protocol.Range
		if err := json.Unmarshal(edits[span.Edit]["range"], &sourceRange); err != nil {
			return nil, err
		}
		if sourceRange.End != canonical && (!hasLegacy || sourceRange.End != legacy) {
			return nil, fmt.Errorf("EOF code action end differs from recorded source end")
		}
		if sourceRange.Start != canonicalStart && (span.Length != 0 || !hasLegacy || sourceRange.Start != legacy) {
			return nil, fmt.Errorf("EOF code action start differs from recorded source span")
		}
		sourceRange.Start = canonicalStart
		sourceRange.End = canonical
		edits[span.Edit]["range"], _ = json.Marshal(sourceRange)
		document["edits"], _ = json.Marshal(edits)
		documents[span.DocumentChange] = document
		workspace["documentChanges"], _ = json.Marshal(documents)
		action["edit"], _ = json.Marshal(workspace)
		if span.CommandFile != nil {
			var command map[string]json.RawMessage
			if err := json.Unmarshal(action["command"], &command); err != nil {
				return nil, err
			}
			var commandID string
			_ = json.Unmarshal(command["command"], &commandID)
			if commandID != "_typescript.applyCodeActionCommand" {
				return nil, fmt.Errorf("EOF code action command is not a quick-fix command")
			}
			var arguments []map[string]json.RawMessage
			if err := json.Unmarshal(command["arguments"], &arguments); err != nil {
				return nil, err
			}
			if len(arguments) != 1 {
				return nil, fmt.Errorf("EOF code action command argument count")
			}
			argument := arguments[0]
			var documentURI string
			_ = json.Unmarshal(argument["documentUri"], &documentURI)
			if documentURI != span.URI {
				return nil, fmt.Errorf("EOF code action command source mismatch")
			}
			var fix map[string]json.RawMessage
			if err := json.Unmarshal(argument["action"], &fix); err != nil {
				return nil, err
			}
			var files []map[string]json.RawMessage
			if err := json.Unmarshal(fix["changes"], &files); err != nil {
				return nil, err
			}
			index := *span.CommandFile
			if index < 0 || index >= len(files) {
				return nil, fmt.Errorf("EOF command file index out of bounds")
			}
			file := files[index]
			var name string
			_ = json.Unmarshal(file["fileName"], &name)
			if !strings.HasPrefix(span.URI, "{{ROOT_URI}}/") {
				return nil, fmt.Errorf("EOF command URI must use the explicit fixture root")
			}
			decodedPath, err := url.PathUnescape(strings.TrimPrefix(span.URI, "{{ROOT_URI}}"))
			if err != nil {
				return nil, err
			}
			expectedName := "{{ROOT_PATH}}" + decodedPath
			if name != expectedName {
				return nil, fmt.Errorf("EOF command filename does not match the recorded document")
			}
			var changes []map[string]json.RawMessage
			if err := json.Unmarshal(file["textChanges"], &changes); err != nil {
				return nil, err
			}
			if span.Edit >= len(changes) {
				return nil, fmt.Errorf("EOF command edit index out of bounds")
			}
			change := changes[span.Edit]
			type point struct {
				Line   uint32 `json:"line"`
				Offset uint32 `json:"offset"`
			}
			var start, end point
			if err := json.Unmarshal(change["start"], &start); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(change["end"], &end); err != nil {
				return nil, err
			}
			canonicalPoint := point{canonical.Line + 1, canonical.Character + 1}
			canonicalStartPoint := point{canonicalStart.Line + 1, canonicalStart.Character + 1}
			legacyPoint := point{legacy.Line + 1, legacy.Character + 1}
			if end != canonicalPoint && (!hasLegacy || end != legacyPoint) || start != canonicalStartPoint && (span.Length != 0 || !hasLegacy || start != legacyPoint) {
				return nil, fmt.Errorf("EOF command position differs from recorded source end")
			}
			// The two edit representations must still describe the same text.
			var publicText, commandText string
			if json.Unmarshal(edits[span.Edit]["newText"], &publicText) != nil || json.Unmarshal(change["newText"], &commandText) != nil || publicText != commandText {
				return nil, fmt.Errorf("EOF command and workspace edit text disagree")
			}
			change["start"], _ = json.Marshal(canonicalStartPoint)
			change["end"], _ = json.Marshal(canonicalPoint)
			changes[span.Edit] = change
			file["textChanges"], _ = json.Marshal(changes)
			files[index] = file
			fix["changes"], _ = json.Marshal(files)
			argument["action"], _ = json.Marshal(fix)
			arguments[0] = argument
			command["arguments"], _ = json.Marshal(arguments)
			action["command"], _ = json.Marshal(command)
		} else if len(action["command"]) > 0 && string(action["command"]) != "null" {
			// Resolved fix-all commands contain only the action identity. Their
			// source ranges live exclusively in the independently recorded edit.
			var command struct {
				Command   string                       `json:"command"`
				Arguments []map[string]json.RawMessage `json:"arguments"`
			}
			if json.Unmarshal(action["command"], &command) != nil || command.Command != "_typescript.applyFixAllCodeAction" || len(command.Arguments) != 1 || len(command.Arguments[0]) != 1 {
				return nil, fmt.Errorf("EOF code action command requires explicit recorded file mapping")
			}
			var identity string
			if json.Unmarshal(command.Arguments[0]["tsActionId"], &identity) != nil || identity == "" {
				return nil, fmt.Errorf("EOF fix-all command requires an action identity")
			}
		}
		actions[span.Action] = action
	}
	if single {
		return json.Marshal(actions[0])
	}
	return json.Marshal(actions)
}
