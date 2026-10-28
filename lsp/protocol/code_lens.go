package protocol

type CodeLensKind uint8

const (
	ReferencesCodeLens CodeLensKind = iota
	ImplementationsCodeLens
)

type CodeLensData struct {
	URI    DocumentURI  `json:"uri"`
	Type   CodeLensKind `json:"type"`
	Handle string       `json:"handle,omitempty"`
	Index  int          `json:"index"`
}
type CodeLens struct {
	Range   Range         `json:"range"`
	Command *Command      `json:"command,omitempty"`
	Data    *CodeLensData `json:"data,omitempty"`
}
type CodeLensOptions struct {
	ResolveProvider bool `json:"resolveProvider"`
}
