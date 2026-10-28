package lspfixture

import (
	"encoding/json"
	"fmt"
	"slices"
)

// EOFClientRequestEdit identifies an independently recorded source edit in a
// workspace/applyEdit request. The raw expected client payload stays intact.
type EOFClientRequestEdit struct {
	PastEOFInsertion *EOFPastEOFInsertion `json:"past_eof_insertion,omitempty"`
	Request          int                  `json:"request"`
	Edit             int                  `json:"edit"`
	URI              string               `json:"uri"`
	Start            int                  `json:"start"`
	Length           int                  `json:"length"`
	SourceSHA256     string               `json:"source_sha256"`
}

func projectClientRequestEOF(f Fixture, n int, requests []ClientRequest) ([]ClientRequest, error) {
	policy := f.Steps[n].SourceEOF
	if policy == nil || len(policy.ClientRequestEdits) == 0 {
		return requests, nil
	}
	if f.Steps[n].Method != "workspace/executeCommand" {
		return nil, fmt.Errorf("client EOF spans require a command")
	}
	texts, err := fixtureTexts(f, n)
	if err != nil {
		return nil, err
	}
	result := slices.Clone(requests)
	seen := map[struct {
		request, edit int
		uri           string
	}]bool{}
	for _, span := range policy.ClientRequestEdits {
		key := struct {
			request, edit int
			uri           string
		}{span.Request, span.Edit, span.URI}
		if seen[key] || span.Request < 0 || span.Request >= len(result) || span.Edit < 0 {
			return nil, fmt.Errorf("invalid or duplicate EOF client edit index")
		}
		seen[key] = true
		request := result[span.Request]
		if request.Method != "workspace/applyEdit" {
			return nil, fmt.Errorf("EOF client edit requires workspace/applyEdit")
		}
		var params, workspace map[string]json.RawMessage
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(params["edit"], &workspace); err != nil {
			return nil, err
		}
		var changes map[string]json.RawMessage
		if err := json.Unmarshal(workspace["changes"], &changes); err != nil {
			return nil, err
		}
		edits, ok := changes[span.URI]
		if !ok {
			return nil, fmt.Errorf("EOF client target missing")
		}
		if span.PastEOFInsertion != nil {
			projected, err := projectStatementFreeEOF(f, n, span, edits, texts)
			if err != nil {
				return nil, err
			}
			changes[span.URI] = projected
			workspace["changes"], _ = json.Marshal(changes)
			params["edit"], _ = json.Marshal(workspace)
			result[span.Request].Params, _ = json.Marshal(params)
			continue
		}
		// Reuse the strict code-action projector, including pinned provenance,
		// source hashes, UTF-16 boundaries and exact legacy/canonical EOF checks.
		action, _ := json.Marshal(map[string]any{"edit": map[string]any{"documentChanges": []any{map[string]any{"textDocument": map[string]any{"uri": span.URI}, "edits": edits}}}})
		copy := f
		copy.Steps = slices.Clone(f.Steps)
		copy.Steps[n].Method = "codeAction/resolve"
		copy.Steps[n].SourceEOF = &SourceEOF{CodeActionEdits: []EOFCodeActionEdit{{Action: 0, DocumentChange: 0, Edit: span.Edit, URI: span.URI, Start: span.Start, Length: span.Length, SourceSHA256: span.SourceSHA256}}}
		projected, err := projectCodeActionEOF(copy, n, action, texts)
		if err != nil {
			return nil, err
		}
		var updated struct {
			Edit struct {
				DocumentChanges []struct {
					Edits json.RawMessage `json:"edits"`
				} `json:"documentChanges"`
			} `json:"edit"`
		}
		if err := json.Unmarshal(projected, &updated); err != nil {
			return nil, err
		}
		changes[span.URI] = updated.Edit.DocumentChanges[0].Edits
		workspace["changes"], _ = json.Marshal(changes)
		params["edit"], _ = json.Marshal(workspace)
		result[span.Request].Params, _ = json.Marshal(params)
	}
	return result, nil
}
