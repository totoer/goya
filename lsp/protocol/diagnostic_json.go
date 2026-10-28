package protocol

import "encoding/json"

// Numeric TypeScript codes remain directly available in Code. CodeString
// preserves client diagnostics whose LSP code uses the string alternative.
func (d Diagnostic) MarshalJSON() ([]byte, error) {
	type alias Diagnostic
	var code any
	if d.CodeString != nil {
		code = *d.CodeString
	} else if d.Code != 0 {
		code = d.Code
	}
	return json.Marshal(struct {
		alias
		Code any `json:"code,omitempty"`
	}{alias(d), code})
}
func (d *Diagnostic) UnmarshalJSON(raw []byte) error {
	type alias Diagnostic
	var wire struct {
		*alias
		Code json.RawMessage `json:"code"`
	}
	wire.alias = (*alias)(d)
	*d = Diagnostic{}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	if len(wire.Code) == 0 || string(wire.Code) == "null" {
		return nil
	}
	if wire.Code[0] == '"' {
		var value string
		if err := json.Unmarshal(wire.Code, &value); err != nil {
			return err
		}
		d.CodeString = &value
		return nil
	}
	return json.Unmarshal(wire.Code, &d.Code)
}

type DiagnosticCodeDescription struct {
	Href string `json:"href"`
}
