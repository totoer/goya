package tsfrontend

import "github.com/totoer/goya/internal/tsfrontend/regexpgrammar"

// ValidateRegExp uses the shared pinned grammar for literals and constructors.
func ValidateRegExp(pattern, flags string) error { return regexpgrammar.Validate(pattern, flags) }
