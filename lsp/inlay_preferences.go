package lsp

import (
	"encoding/json"
	"github.com/totoer/goya/lsp/protocol"
)

// InlayHintSettings are optional workspace overrides over initialization
// preferences. Nil fields retain those preferences; an absent block clears
// workspace overrides when Configure replaces dynamic settings.
type InlayHintSettings struct {
	IncludeInlayParameterNameHints                        string `json:"includeInlayParameterNameHints,omitempty"`
	IncludeInlayParameterNameHintsWhenArgumentMatchesName *bool  `json:"includeInlayParameterNameHintsWhenArgumentMatchesName,omitempty"`
	IncludeInlayFunctionParameterTypeHints                *bool  `json:"includeInlayFunctionParameterTypeHints,omitempty"`
	IncludeInlayVariableTypeHints                         *bool  `json:"includeInlayVariableTypeHints,omitempty"`
	IncludeInlayVariableTypeHintsWhenTypeMatchesName      *bool  `json:"includeInlayVariableTypeHintsWhenTypeMatchesName,omitempty"`
	IncludeInlayPropertyDeclarationTypeHints              *bool  `json:"includeInlayPropertyDeclarationTypeHints,omitempty"`
	IncludeInlayFunctionLikeReturnTypeHints               *bool  `json:"includeInlayFunctionLikeReturnTypeHints,omitempty"`
	IncludeInlayEnumMemberValueHints                      *bool  `json:"includeInlayEnumMemberValueHints,omitempty"`
}
type TypeScriptConfiguration struct {
	Format                  *protocol.FormatCodeSettings     `json:"format,omitempty"`
	ReferencesCodeLens      *ReferencesCodeLensSettings      `json:"referencesCodeLens,omitempty"`
	ImplementationsCodeLens *ImplementationsCodeLensSettings `json:"implementationsCodeLens,omitempty"`
	InlayHints              *InlayHintSettings               `json:"inlayHints,omitempty"`
}

func cloneInlaySettings(settings *InlayHintSettings) *InlayHintSettings {
	if settings == nil {
		return nil
	}
	data, _ := json.Marshal(settings)
	var result InlayHintSettings
	_ = json.Unmarshal(data, &result)
	return &result
}
func nativeEditorPreferences(options EditorOptions) map[string]any {
	data, _ := json.Marshal(options.Preferences)
	raw := map[string]any{}
	_ = json.Unmarshal(data, &raw)
	if options.Format != nil {
		data, _ := json.Marshal(options.Format)
		var formatting map[string]any
		_ = json.Unmarshal(data, &formatting)
		for key, value := range formatting {
			raw[key] = value
		}
	}
	if options.InlayHints != nil {
		data, _ := json.Marshal(options.InlayHints)
		var overrides map[string]any
		_ = json.Unmarshal(data, &overrides)
		for key, value := range overrides {
			raw[key] = value
		}
	}
	return raw
}
