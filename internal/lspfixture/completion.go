package lspfixture

import (
	"encoding/json"
	"fmt"
)

func completionItemAt(result json.RawMessage, index int) (json.RawMessage, error) {
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(result, &list); err != nil {
		return nil, err
	}
	if index < 0 || index >= len(list.Items) {
		return nil, fmt.Errorf("completion item index %d out of bounds", index)
	}
	return list.Items[index], nil
}

// Completion data is server-private transport metadata. Only that exact field
// is normalized; count/order, labels, snippets, documentation and every edit
// remain exact. Resolve uses the actual returned token, never the oracle token.
func canonicalEditorResult(method string, raw []byte) ([]byte, error) {
	if method == "textDocument/codeAction" || method == "codeAction/resolve" {
		return canonicalCodeAction(method, raw)
	}
	if method == "textDocument/codeLens" || method == "codeLens/resolve" {
		return canonicalCodeLens(method, raw)
	}
	if method == "textDocument/prepareCallHierarchy" || method == "callHierarchy/incomingCalls" || method == "callHierarchy/outgoingCalls" {
		return canonicalHierarchy(method, raw)
	}
	if method != "textDocument/completion" && method != "completionItem/resolve" {
		return canonical(raw)
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if method == "completionItem/resolve" {
		if _, ok := value["data"]; ok {
			value["data"] = json.RawMessage(`{"opaque":true}`)
		}
	} else if items, ok := value["items"]; ok {
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(items, &entries); err != nil {
			return nil, err
		}
		for _, item := range entries {
			if _, ok := item["data"]; ok {
				item["data"] = json.RawMessage(`{"opaque":true}`)
			}
		}
		encoded, err := json.Marshal(entries)
		if err != nil {
			return nil, err
		}
		value["items"] = encoded
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return canonical(encoded)
}
