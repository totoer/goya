package editor

import "github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsutil"

// ConfigurePreferences applies detached editor DTOs before the generation's
// first request. It cannot change compiler options or access external tools.
func (w *Workspace) ConfigurePreferences(raw map[string]any) {
	defaults := map[string]any{
		"allowTextChangesInNewFiles":                        true,
		"reportStyleChecksAsWarnings":                       false,
		"includeAutomaticOptionalChainCompletions":          true,
		"includeCompletionsWithClassMemberSnippets":         true,
		"includeCompletionsWithObjectLiteralMethodSnippets": true,
		"jsxAttributeCompletionStyle":                       "auto",
	}
	for key, value := range raw {
		defaults[key] = value
	}
	w.session.InitializeWithUserConfig(lsutil.ParseUserPreferences(map[string]any{"typescript": map[string]any{"unstable": defaults}}))
}
