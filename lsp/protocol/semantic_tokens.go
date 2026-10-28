package protocol

type SemanticTokens struct {
	Data []uint32 `json:"data"`
}
type SemanticTokensLegend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}
type SemanticTokensOptions struct {
	DocumentSelector any                  `json:"documentSelector"`
	Legend           SemanticTokensLegend `json:"legend"`
	Full             bool                 `json:"full"`
	Range            bool                 `json:"range"`
}
type SemanticTokensRangeParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
}
