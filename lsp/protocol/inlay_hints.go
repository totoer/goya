package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type InlayHintKind uint32

const (
	InlayType      InlayHintKind = 1
	InlayParameter InlayHintKind = 2
)

type InlayHintLabelPart struct {
	Value    string    `json:"value"`
	Location *Location `json:"location,omitempty"`
}
type StringOrInlayHintLabelParts struct {
	String *string
	Parts  []InlayHintLabelPart
}

func (l StringOrInlayHintLabelParts) MarshalJSON() ([]byte, error) {
	if l.String != nil && l.Parts == nil {
		return json.Marshal(*l.String)
	}
	if l.String == nil && l.Parts != nil {
		return json.Marshal(l.Parts)
	}
	return nil, fmt.Errorf("inlay label requires exactly one string or label-part array")
}
func (l *StringOrInlayHintLabelParts) UnmarshalJSON(raw []byte) error {
	*l = StringOrInlayHintLabelParts{}
	raw = bytes.TrimSpace(raw)
	var text string
	if err := json.Unmarshal(raw, &text); err == nil && string(raw) != "null" {
		l.String = &text
		return nil
	}
	if err := json.Unmarshal(raw, &l.Parts); err != nil {
		return err
	}
	if l.Parts == nil {
		return fmt.Errorf("inlay label cannot be null")
	}
	return nil
}

type InlayHint struct {
	Position     Position                    `json:"position"`
	Label        StringOrInlayHintLabelParts `json:"label"`
	Kind         *InlayHintKind              `json:"kind,omitempty"`
	PaddingLeft  bool                        `json:"paddingLeft,omitempty"`
	PaddingRight bool                        `json:"paddingRight,omitempty"`
}
type InlayHintParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
}
