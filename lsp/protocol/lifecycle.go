package protocol

// Lifecycle DTOs are owned by the public facade. Capabilities grow only when
// their corresponding wire operations have behavioral coverage.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}
type ServerCapabilities struct {
	Workspace                       *WorkspaceServerCapabilities `json:"workspace,omitempty"`
	CodeActionProvider              *CodeActionOptions           `json:"codeActionProvider,omitempty"`
	DocumentFormattingProvider      bool                         `json:"documentFormattingProvider,omitempty"`
	DocumentRangeFormattingProvider bool                         `json:"documentRangeFormattingProvider,omitempty"`
	CodeLensProvider                *CodeLensOptions             `json:"codeLensProvider,omitempty"`
	InlayHintProvider               bool                         `json:"inlayHintProvider,omitempty"`
	SemanticTokensProvider          *SemanticTokensOptions       `json:"semanticTokensProvider,omitempty"`
	CallHierarchyProvider           bool                         `json:"callHierarchyProvider,omitempty"`
	FoldingRangeProvider            bool                         `json:"foldingRangeProvider,omitempty"`
	SelectionRangeProvider          bool                         `json:"selectionRangeProvider,omitempty"`
	LinkedEditingRangeProvider      bool                         `json:"linkedEditingRangeProvider,omitempty"`
	DocumentSymbolProvider          bool                         `json:"documentSymbolProvider,omitempty"`
	WorkspaceSymbolProvider         bool                         `json:"workspaceSymbolProvider,omitempty"`
	RenameProvider                  *RenameOptions               `json:"renameProvider,omitempty"`
	ReferencesProvider              bool                         `json:"referencesProvider,omitempty"`
	DocumentHighlightProvider       bool                         `json:"documentHighlightProvider,omitempty"`
	ExecuteCommandProvider          *ExecuteCommandOptions       `json:"executeCommandProvider,omitempty"`
	DefinitionProvider              bool                         `json:"definitionProvider,omitempty"`
	TypeDefinitionProvider          bool                         `json:"typeDefinitionProvider,omitempty"`
	ImplementationProvider          bool                         `json:"implementationProvider,omitempty"`
	PositionEncoding                string                       `json:"positionEncoding,omitempty"`
	TextDocumentSync                *TextDocumentSyncOptions     `json:"textDocumentSync,omitempty"`
	SignatureHelpProvider           *SignatureHelpOptions        `json:"signatureHelpProvider,omitempty"`
	HoverProvider                   bool                         `json:"hoverProvider,omitempty"`
	CompletionProvider              *CompletionOptions           `json:"completionProvider,omitempty"`
	DiagnosticProvider              *DiagnosticProviderOptions   `json:"diagnosticProvider,omitempty"`
}
type TextDocumentSyncOptions struct {
	OpenClose bool         `json:"openClose"`
	Change    uint32       `json:"change"`
	Save      *SaveOptions `json:"save,omitempty"`
}
type SaveOptions struct {
	IncludeText bool `json:"includeText"`
}
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   *ServerInfo        `json:"serverInfo,omitempty"`
}

// TypeScriptVersionParams identifies the embedded compiler; no external
// tsserver selection or installation is performed.
type TypeScriptVersionParams struct {
	Version string `json:"version"`
	Source  string `json:"source"`
}
