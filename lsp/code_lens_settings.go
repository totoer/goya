package lsp

import "encoding/json"

type ReferencesCodeLensSettings struct {
	Enabled            *bool `json:"enabled,omitempty"`
	ShowOnAllFunctions *bool `json:"showOnAllFunctions,omitempty"`
}
type ImplementationsCodeLensSettings struct {
	Enabled *bool `json:"enabled,omitempty"`
}

func cloneReferencesCodeLensSettings(value *ReferencesCodeLensSettings) *ReferencesCodeLensSettings {
	if value == nil {
		return nil
	}
	data, _ := json.Marshal(value)
	var result ReferencesCodeLensSettings
	_ = json.Unmarshal(data, &result)
	return &result
}
func cloneImplementationsCodeLensSettings(value *ImplementationsCodeLensSettings) *ImplementationsCodeLensSettings {
	if value == nil {
		return nil
	}
	data, _ := json.Marshal(value)
	var result ImplementationsCodeLensSettings
	_ = json.Unmarshal(data, &result)
	return &result
}
func codeLensCapabilities(raw json.RawMessage) (bool, bool, error) {
	var caps struct {
		TextDocument struct {
			CodeLens json.RawMessage `json:"codeLens"`
		} `json:"textDocument"`
		Workspace struct {
			CodeLens struct {
				RefreshSupport bool `json:"refreshSupport"`
			} `json:"codeLens"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &caps); err != nil {
		return false, false, &Error{Code: CodeInvalidOptions, Message: "invalid code lens capability", Cause: err}
	}
	return len(caps.TextDocument.CodeLens) > 0 && string(caps.TextDocument.CodeLens) != "null", caps.Workspace.CodeLens.RefreshSupport, nil
}
