package protocol

type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier `json:"textDocument"`
	ContentChanges []ContentChange                 `json:"contentChanges"`
}
type DidSaveTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"`
}
type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}
type FileChangeType uint32

const (
	FileCreated FileChangeType = 1
	FileChanged FileChangeType = 2
	FileDeleted FileChangeType = 3
)

type FileEvent struct {
	URI  DocumentURI    `json:"uri"`
	Type FileChangeType `json:"type"`
}
type DidChangeWatchedFilesParams struct {
	Changes []FileEvent `json:"changes"`
}
type FileCreate struct {
	URI DocumentURI `json:"uri"`
}
type FileRename struct {
	OldURI DocumentURI `json:"oldUri"`
	NewURI DocumentURI `json:"newUri"`
}
type FileDelete struct {
	URI DocumentURI `json:"uri"`
}
type CreateFilesParams struct {
	Files []FileCreate `json:"files"`
}
type RenameFilesParams struct {
	Files []FileRename `json:"files"`
}
type DeleteFilesParams struct {
	Files []FileDelete `json:"files"`
}
type ReadDocumentParams struct {
	URI DocumentURI `json:"uri"`
}
