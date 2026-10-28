package protocol

type SymbolKind uint32

const (
	SymbolFile SymbolKind = iota + 1
	SymbolModule
	SymbolNamespace
	SymbolPackage
	SymbolClass
	SymbolMethod
	SymbolProperty
	SymbolField
	SymbolConstructor
	SymbolEnum
	SymbolInterface
	SymbolFunction
	SymbolVariable
	SymbolConstant
	SymbolString
	SymbolNumber
	SymbolBoolean
	SymbolArray
	SymbolObject
	SymbolKey
	SymbolNull
	SymbolEnumMember
	SymbolStruct
	SymbolEvent
	SymbolOperator
	SymbolTypeParameter
)

type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail"`
	Kind           SymbolKind       `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children"`
}
type SymbolInformation struct {
	Name          string     `json:"name"`
	Kind          SymbolKind `json:"kind"`
	Location      Location   `json:"location"`
	ContainerName *string    `json:"containerName,omitempty"`
}
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}
type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}
