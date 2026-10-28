package protocol

type ResourceOperationKind string

const (
	ResourceCreate ResourceOperationKind = "create"
	ResourceRename ResourceOperationKind = "rename"
	ResourceDelete ResourceOperationKind = "delete"
)

type ApplyWorkspaceEditParams struct {
	Label string        `json:"label,omitempty"`
	Edit  WorkspaceEdit `json:"edit"`
}
type ApplyWorkspaceEditResult struct {
	Applied       bool    `json:"applied"`
	FailureReason *string `json:"failureReason,omitempty"`
	FailedChange  *uint32 `json:"failedChange,omitempty"`
}
type MessageType uint32

const (
	MessageError   MessageType = 1
	MessageWarning MessageType = 2
	MessageInfo    MessageType = 3
	MessageLog     MessageType = 4
)

type MessageActionItem struct {
	Title string `json:"title"`
}
type ShowMessageRequestParams struct {
	Type    MessageType         `json:"type"`
	Message string              `json:"message"`
	Actions []MessageActionItem `json:"actions,omitempty"`
}
