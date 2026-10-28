package protocol

type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}
type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      ReferenceContext       `json:"context"`
}
type DocumentHighlightKind uint32

const (
	HighlightText  DocumentHighlightKind = 1
	HighlightRead  DocumentHighlightKind = 2
	HighlightWrite DocumentHighlightKind = 3
)

type DocumentHighlight struct {
	Range Range                  `json:"range"`
	Kind  *DocumentHighlightKind `json:"kind,omitempty"`
}
