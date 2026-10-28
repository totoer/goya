package protocol

type FileOperationPattern struct {
	Glob    string `json:"glob"`
	Matches string `json:"matches,omitempty"`
}
type FileOperationFilter struct {
	Scheme  string               `json:"scheme,omitempty"`
	Pattern FileOperationPattern `json:"pattern"`
}
type FileOperationRegistrationOptions struct {
	Filters []FileOperationFilter `json:"filters"`
}
type WorkspaceFileOperationsServerCapabilities struct {
	WillRename *FileOperationRegistrationOptions `json:"willRename,omitempty"`
}
type WorkspaceServerCapabilities struct {
	FileOperations *WorkspaceFileOperationsServerCapabilities `json:"fileOperations,omitempty"`
}
