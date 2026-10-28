package protocol

// RefactorActionInfo describes an individual TypeScript refactor choice.
type RefactorActionInfo struct {
	Name                string         `json:"name"`
	Description         string         `json:"description"`
	Kind                CodeActionKind `json:"kind,omitempty"`
	NotApplicableReason string         `json:"notApplicableReason,omitempty"`
	IsInteractive       bool           `json:"isInteractive,omitempty"`
	// Range uses stable zero-based LSP source coordinates.
	Range *Range `json:"range,omitempty"`
}

// ApplicableRefactorInfo groups actions under the reference provider name.
type ApplicableRefactorInfo struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Inlineable  *bool                `json:"inlineable,omitempty"`
	Actions     []RefactorActionInfo `json:"actions"`
}
