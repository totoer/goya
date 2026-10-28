package protocol

type SymbolTag uint32

const SymbolDeprecated SymbolTag = 1

type CallHierarchyData struct {
	Handle string `json:"handle"`
	Index  int    `json:"index"`
}
type CallHierarchyItem struct {
	Name           string             `json:"name"`
	Kind           SymbolKind         `json:"kind"`
	Tags           []SymbolTag        `json:"tags,omitempty"`
	Detail         string             `json:"detail"`
	URI            DocumentURI        `json:"uri"`
	Range          Range              `json:"range"`
	SelectionRange Range              `json:"selectionRange"`
	Data           *CallHierarchyData `json:"data,omitempty"`
}
type CallHierarchyIncomingCall struct {
	From       CallHierarchyItem `json:"from"`
	FromRanges []Range           `json:"fromRanges"`
}
type CallHierarchyOutgoingCall struct {
	To         CallHierarchyItem `json:"to"`
	FromRanges []Range           `json:"fromRanges"`
}
type CallHierarchyCallsParams struct {
	Item CallHierarchyItem `json:"item"`
}
