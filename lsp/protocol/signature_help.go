package protocol

// SignatureHelpTriggerKind identifies why signature help was requested.
type SignatureHelpTriggerKind uint32

const (
	SignatureHelpInvoked          SignatureHelpTriggerKind = 1
	SignatureHelpTriggerCharacter SignatureHelpTriggerKind = 2
	SignatureHelpContentChange    SignatureHelpTriggerKind = 3
)

type SignatureHelpContext struct {
	TriggerKind         SignatureHelpTriggerKind `json:"triggerKind"`
	TriggerCharacter    *string                  `json:"triggerCharacter,omitempty"`
	IsRetrigger         bool                     `json:"isRetrigger"`
	ActiveSignatureHelp *SignatureHelp           `json:"activeSignatureHelp,omitempty"`
}
type SignatureHelpParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      *SignatureHelpContext  `json:"context,omitempty"`
}
type ParameterInformation struct {
	Label         string                 `json:"label"`
	Documentation *StringOrMarkupContent `json:"documentation,omitempty"`
}
type SignatureInformation struct {
	Label         string                 `json:"label"`
	Documentation *StringOrMarkupContent `json:"documentation,omitempty"`
	Parameters    []ParameterInformation `json:"parameters"`
}
type SignatureHelp struct {
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature *uint32                `json:"activeSignature,omitempty"`
	ActiveParameter *uint32                `json:"activeParameter,omitempty"`
}
type SignatureHelpOptions struct {
	TriggerCharacters   []string `json:"triggerCharacters"`
	RetriggerCharacters []string `json:"retriggerCharacters"`
}
