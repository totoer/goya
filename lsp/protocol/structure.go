package protocol

type FoldingRangeKind string

const (
	FoldingComment FoldingRangeKind = "comment"
	FoldingImports FoldingRangeKind = "imports"
	FoldingRegion  FoldingRangeKind = "region"
)

type FoldingRange struct {
	StartLine uint32           `json:"startLine"`
	EndLine   uint32           `json:"endLine"`
	Kind      FoldingRangeKind `json:"kind,omitempty"`
}
type SelectionRange struct {
	Range  Range           `json:"range"`
	Parent *SelectionRange `json:"parent,omitempty"`
}
type LinkedEditingRanges struct {
	Ranges      []Range `json:"ranges"`
	WordPattern *string `json:"wordPattern,omitempty"`
}
type SelectionRangeParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Positions    []Position             `json:"positions"`
}
