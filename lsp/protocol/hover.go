package protocol

type MarkupKind string

const (
	MarkupMarkdown  MarkupKind = "markdown"
	MarkupPlainText MarkupKind = "plaintext"
)

type MarkupContent struct {
	Kind  MarkupKind `json:"kind"`
	Value string     `json:"value"`
}
type Hover struct {
	Contents                  MarkupContent `json:"contents"`
	Range                     *Range        `json:"range,omitempty"`
	CanIncreaseVerbosityLevel *bool         `json:"canIncreaseVerbosityLevel,omitempty"`
}
type HoverParams struct {
	TextDocument   TextDocumentIdentifier `json:"textDocument"`
	Position       Position               `json:"position"`
	VerbosityLevel *int32                 `json:"verbosityLevel,omitempty"`
}
