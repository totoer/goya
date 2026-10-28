package lsp

import (
	"encoding/json"
	"fmt"
)

// OrganizeImportsCaseSensitivity retains the reference's boolean-or-auto union.
type OrganizeImportsCaseSensitivity string

const (
	OrganizeImportsCaseAuto        OrganizeImportsCaseSensitivity = "auto"
	OrganizeImportsCaseSensitive   OrganizeImportsCaseSensitivity = "sensitive"
	OrganizeImportsCaseInsensitive OrganizeImportsCaseSensitivity = "insensitive"
)

func (v OrganizeImportsCaseSensitivity) MarshalJSON() ([]byte, error) {
	switch v {
	case OrganizeImportsCaseAuto:
		return []byte(`"auto"`), nil
	case OrganizeImportsCaseSensitive:
		return []byte("false"), nil
	case OrganizeImportsCaseInsensitive:
		return []byte("true"), nil
	}
	return nil, fmt.Errorf("invalid organize imports case sensitivity %q", v)
}
func (v *OrganizeImportsCaseSensitivity) UnmarshalJSON(raw []byte) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	switch value {
	case nil:
		*v = ""
	case true:
		*v = OrganizeImportsCaseInsensitive
	case false:
		*v = OrganizeImportsCaseSensitive
	case "auto":
		*v = OrganizeImportsCaseAuto
	default:
		return fmt.Errorf("organizeImportsIgnoreCase requires a boolean or auto")
	}
	return nil
}

// OrganizeImportsCaseFirst retains the reference's upper/lower/false union.
type OrganizeImportsCaseFirst string

const (
	OrganizeImportsCaseDefault OrganizeImportsCaseFirst = "false"
	OrganizeImportsUpperFirst  OrganizeImportsCaseFirst = "upper"
	OrganizeImportsLowerFirst  OrganizeImportsCaseFirst = "lower"
)

func (v OrganizeImportsCaseFirst) MarshalJSON() ([]byte, error) {
	switch v {
	case OrganizeImportsCaseDefault:
		return []byte("false"), nil
	case OrganizeImportsUpperFirst, OrganizeImportsLowerFirst:
		return json.Marshal(string(v))
	}
	return nil, fmt.Errorf("invalid organize imports case-first value %q", v)
}
func (v *OrganizeImportsCaseFirst) UnmarshalJSON(raw []byte) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	switch value {
	case nil:
		*v = ""
	case false:
		*v = OrganizeImportsCaseDefault
	case "upper":
		*v = OrganizeImportsUpperFirst
	case "lower":
		*v = OrganizeImportsLowerFirst
	default:
		return fmt.Errorf("organizeImportsCaseFirst requires upper, lower, or false")
	}
	return nil
}
