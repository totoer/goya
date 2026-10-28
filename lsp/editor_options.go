package lsp

import (
	"context"
	"encoding/json"
	"github.com/totoer/goya/lsp/protocol"
	"slices"
)

// DiagnosticSettings affect editor presentation only, never Check/Compile.
type DiagnosticSettings struct {
	IgnoredCodes       []int32
	DisableSuggestions bool
	DisableValidation  bool
	EagerClear         bool
}
type EditorOptions struct {
	ImplicitProjectConfiguration *ImplicitProjectConfiguration
	Locale                       string
	HostInfo                     string
	CompletionDisableFilterText  bool
	Format                       *protocol.FormatCodeSettings
	ReferencesCodeLens           *ReferencesCodeLensSettings
	ImplementationsCodeLens      *ImplementationsCodeLensSettings
	InlayHints                   *InlayHintSettings
	Diagnostics                  DiagnosticSettings
	Preferences                  EditorPreferences
	Completions                  CompletionSettings
}
type DiagnosticConfiguration struct {
	IgnoredCodes []int32 `json:"ignoredCodes"`
	EagerClear   bool    `json:"eagerClear"`
}
type WorkspaceConfiguration struct {
	ImplicitProjectConfiguration *ImplicitProjectConfiguration `json:"implicitProjectConfiguration,omitempty"`
	TypeScript                   *TypeScriptConfiguration      `json:"typescript,omitempty"`
	Diagnostics                  *DiagnosticConfiguration      `json:"diagnostics,omitempty"`
	Completions                  *CompletionSettings           `json:"completions,omitempty"`
}

func cloneEditorOptions(options EditorOptions) EditorOptions {
	// All fields are owned JSON DTOs; this also detaches optional scalar pointers.
	data, _ := json.Marshal(options)
	var copy EditorOptions
	_ = json.Unmarshal(data, &copy)
	return copy
}
func (s *Session) UpdateEditorOptions(ctx context.Context, options EditorOptions) error {
	if err := validateImplicitConfiguration(options.ImplicitProjectConfiguration); err != nil {
		return err
	}
	if err := validateFormatSettings(options.Format); err != nil {
		return err
	}
	copy := cloneEditorOptions(options)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLocked(ctx); err != nil {
		return err
	}
	s.editorOptions = copy
	s.advanceGenerationLocked()
	return nil
}

// Configure replaces dynamic workspace settings over their defaults, preserving
// initialization/host settings outside that configuration surface.
func (s *Session) Configure(ctx context.Context, configuration WorkspaceConfiguration) error {
	if err := validateImplicitConfiguration(configuration.ImplicitProjectConfiguration); err != nil {
		return err
	}
	implicit := cloneImplicitConfiguration(configuration.ImplicitProjectConfiguration)
	if implicit == nil {
		implicit = &ImplicitProjectConfiguration{}
	}
	var inlay *InlayHintSettings
	var format *protocol.FormatCodeSettings
	var references *ReferencesCodeLensSettings
	var implementations *ImplementationsCodeLensSettings
	if configuration.TypeScript != nil {
		inlay = cloneInlaySettings(configuration.TypeScript.InlayHints)
		format = cloneFormatSettings(configuration.TypeScript.Format)
		references = cloneReferencesCodeLensSettings(configuration.TypeScript.ReferencesCodeLens)
		implementations = cloneImplementationsCodeLensSettings(configuration.TypeScript.ImplementationsCodeLens)
	}
	if err := validateFormatSettings(format); err != nil {
		return err
	}
	var ignored []int32
	var eagerClear bool
	if configuration.Diagnostics != nil {
		ignored = slices.Clone(configuration.Diagnostics.IgnoredCodes)
		eagerClear = configuration.Diagnostics.EagerClear
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLocked(ctx); err != nil {
		return err
	}
	s.editorOptions.ImplicitProjectConfiguration = implicit
	s.editorOptions.InlayHints = inlay
	s.editorOptions.Format = format
	s.editorOptions.ReferencesCodeLens = references
	s.editorOptions.ImplementationsCodeLens = implementations
	s.editorOptions.Diagnostics.IgnoredCodes = ignored
	s.editorOptions.Diagnostics.EagerClear = eagerClear
	s.editorOptions.Completions = CompletionSettings{}
	if configuration.Completions != nil {
		s.editorOptions.Completions = *configuration.Completions
	}
	s.advanceGenerationLocked()
	return nil
}
