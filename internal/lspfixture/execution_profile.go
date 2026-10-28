package lspfixture

import (
	"encoding/json"
	"fmt"
)

func sourceStringsEnabled(e Endpoint) bool {
	profile, ok := e.(interface{ SourceStringCompilationEnabled() bool })
	return !ok || profile.SourceStringCompilationEnabled()
}

// Only reviewed, explicitly annotated reference completion steps are projected.
// The raw oracle response is immutable, and actual endpoint results are never
// filtered: an accidentally reintroduced eval remains a comparison failure.
func withoutGlobalEvalCompletion(raw json.RawMessage) (json.RawMessage, int, error) {
	var report map[string]json.RawMessage
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, 0, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(report["items"], &items); err != nil {
		return nil, 0, err
	}
	index := -1
	for i, raw := range items {
		var item struct {
			Label    string `json:"label"`
			Kind     int    `json:"kind"`
			SortText string `json:"sortText"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, 0, err
		}
		if item.Label == "eval" && item.Kind == 3 && item.SortText == "15" {
			if index != -1 {
				return nil, 0, fmt.Errorf("ambiguous reference eval completion")
			}
			index = i
		}
	}
	if index < 0 {
		return nil, 0, fmt.Errorf("profile projection requires one standard global eval completion")
	}
	items = append(items[:index:index], items[index+1:]...)
	report["items"], _ = json.Marshal(items)
	data, err := json.Marshal(report)
	return data, index, err
}
