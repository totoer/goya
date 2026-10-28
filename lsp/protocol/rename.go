package protocol

type RenameParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	NewName      string                 `json:"newName"`
}

type RenameOptions struct {
	PrepareProvider bool `json:"prepareProvider"`
}
