package lspfixture

import (
	"encoding/json"
	"fmt"
)

// HierarchyItemSource selects an actual item issued by a preceding request.
// The source method determines whether the item is direct, from, or to.
type HierarchyItemSource struct {
	Step  int `json:"step"`
	Index int `json:"index"`
}

func hierarchyItemAt(method string, result json.RawMessage, index int) (json.RawMessage, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(result, &entries); err != nil {
		return nil, err
	}
	if index < 0 || index >= len(entries) {
		return nil, fmt.Errorf("call hierarchy item index %d out of bounds", index)
	}
	item := entries[index]
	switch method {
	case "textDocument/prepareCallHierarchy":
	case "callHierarchy/incomingCalls", "callHierarchy/outgoingCalls":
		var call map[string]json.RawMessage
		if err := json.Unmarshal(item, &call); err != nil {
			return nil, err
		}
		key := "from"
		if method == "callHierarchy/outgoingCalls" {
			key = "to"
		}
		item = call[key]
	default:
		return nil, fmt.Errorf("unsupported hierarchy source %s", method)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(item, &record); err != nil || len(record["uri"]) == 0 || len(record["selectionRange"]) == 0 {
		return nil, fmt.Errorf("invalid call hierarchy item")
	}
	return json.Marshal(map[string]json.RawMessage{"item": item})
}
func canonicalHierarchy(method string, raw []byte) ([]byte, error) {
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if method == "textDocument/prepareCallHierarchy" {
			delete(entry, "data")
			continue
		}
		key := "from"
		if method == "callHierarchy/outgoingCalls" {
			key = "to"
		}
		var item map[string]json.RawMessage
		if err := json.Unmarshal(entry[key], &item); err != nil {
			return nil, err
		}
		delete(item, "data")
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		entry[key] = encoded
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	return canonical(encoded)
}
