package lspfixture

import (
	"encoding/json"
	"fmt"
)

// CodeActionSource forwards the actual issued action, including opaque data.
type CodeActionSource struct {
	Step  int `json:"step"`
	Index int `json:"index"`
}

func codeActionAt(raw json.RawMessage, index int) (json.RawMessage, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	if index < 0 || index >= len(items) {
		return nil, fmt.Errorf("code action index %d out of bounds", index)
	}
	var action struct {
		Title   *string         `json:"title"`
		Command json.RawMessage `json:"command"`
	}
	if json.Unmarshal(items[index], &action) != nil || action.Title == nil || len(action.Command) > 0 && action.Command[0] == '"' {
		return nil, fmt.Errorf("source is not a code action")
	}
	return items[index], nil
}
func canonicalCodeAction(method string, raw []byte) ([]byte, error) {
	var actions []map[string]json.RawMessage
	if method == "codeAction/resolve" {
		var action map[string]json.RawMessage
		if err := json.Unmarshal(raw, &action); err != nil {
			return nil, err
		}
		actions = []map[string]json.RawMessage{action}
	} else if err := json.Unmarshal(raw, &actions); err != nil {
		return nil, err
	}
	for _, action := range actions {
		if _, ok := action["data"]; ok {
			action["data"] = json.RawMessage(`{"opaque":true}`)
		}
	}
	var value any = actions
	if method == "codeAction/resolve" {
		value = actions[0]
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return canonical(encoded)
}
