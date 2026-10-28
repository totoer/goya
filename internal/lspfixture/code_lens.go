package lspfixture

import (
	"encoding/json"
	"fmt"
)

// CodeLensSource selects an item actually issued by a previous enumeration.
type CodeLensSource struct {
	Step  int `json:"step"`
	Index int `json:"index"`
}

func codeLensAt(raw json.RawMessage, index int) (json.RawMessage, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	if index < 0 || index >= len(items) {
		return nil, fmt.Errorf("code lens index %d out of bounds", index)
	}
	var item map[string]json.RawMessage
	if err := json.Unmarshal(items[index], &item); err != nil {
		return nil, err
	}
	if len(item["range"]) == 0 || len(item["data"]) == 0 {
		return nil, fmt.Errorf("invalid code lens source")
	}
	return items[index], nil
}
func canonicalCodeLens(method string, raw []byte) ([]byte, error) {
	var items []map[string]json.RawMessage
	if method == "codeLens/resolve" {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		items = []map[string]json.RawMessage{item}
	} else if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	for _, item := range items {
		if len(item["data"]) == 0 {
			continue
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(item["data"], &data); err != nil {
			return nil, err
		}
		// URI/type are TLS presentation data and remain exact. Only opaque batch
		// identity differs between implementations; resolve always reuses actual data.
		delete(data, "handle")
		delete(data, "index")
		encoded, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		item["data"] = encoded
	}
	var encoded []byte
	var err error
	if method == "codeLens/resolve" {
		encoded, err = json.Marshal(items[0])
	} else {
		encoded, err = json.Marshal(items)
	}
	if err != nil {
		return nil, err
	}
	return canonical(encoded)
}
