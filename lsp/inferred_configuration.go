package lsp

import "strings"

// ImplicitProjectConfiguration applies only to inferred editor projects.
// Explicit host compiler options take priority; configured projects retain
// their config options. Effective options are exported by ProjectSnapshot.
type ImplicitProjectConfiguration struct {
	ExperimentalDecorators *bool  `json:"experimentalDecorators,omitempty"`
	Module                 string `json:"module,omitempty"`
	Strict                 *bool  `json:"strict,omitempty"`
	StrictFunctionTypes    *bool  `json:"strictFunctionTypes,omitempty"`
	StrictNullChecks       *bool  `json:"strictNullChecks,omitempty"`
	Target                 string `json:"target,omitempty"`
}

func implicitCompilerOptions(settings *ImplicitProjectConfiguration) map[string]any {
	if settings == nil {
		return nil
	}
	module, target := "esnext", "es2024"
	if settings.Module != "" {
		module = settings.Module
	}
	if settings.Target != "" {
		target = settings.Target
	}
	result := map[string]any{"module": module, "target": target, "strict": true, "experimentalDecorators": settings.ExperimentalDecorators != nil && *settings.ExperimentalDecorators}
	// The pinned reference only sends these properties when they are true.
	// A false value therefore inherits the effective `strict` setting.
	// In TS 6 strict itself defaults to true when TLS omits a false value.
	if preferenceEnabled(settings.StrictFunctionTypes) {
		result["strictFunctionTypes"] = true
	}
	if preferenceEnabled(settings.StrictNullChecks) {
		result["strictNullChecks"] = true
	}
	return result
}
func validateImplicitConfiguration(settings *ImplicitProjectConfiguration) error {
	if settings == nil {
		return nil
	}
	for _, entry := range []struct{ value, allowed string }{
		{settings.Module, ",none,commonjs,amd,umd,system,es6,es2015,es2020,es2022,esnext,node16,node18,node20,nodenext,preserve,"},
		{settings.Target, ",es3,es5,es6,es2015,es2016,es2017,es2018,es2019,es2020,es2021,es2022,es2023,es2024,es2025,esnext,"},
	} {
		if entry.value != "" && !strings.Contains(entry.allowed, ","+strings.ToLower(entry.value)+",") {
			return &Error{Code: CodeInvalidOptions, Message: "invalid implicit project compiler option: " + entry.value}
		}
	}
	return nil
}
func cloneImplicitConfiguration(settings *ImplicitProjectConfiguration) *ImplicitProjectConfiguration {
	if settings == nil {
		return nil
	}
	result := *settings
	copyBool := func(value *bool) *bool {
		if value == nil {
			return nil
		}
		copy := *value
		return &copy
	}
	result.ExperimentalDecorators = copyBool(settings.ExperimentalDecorators)
	result.Strict = copyBool(settings.Strict)
	result.StrictFunctionTypes = copyBool(settings.StrictFunctionTypes)
	result.StrictNullChecks = copyBool(settings.StrictNullChecks)
	return &result
}
