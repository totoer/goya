package goya

import (
	stdcontext "context"

	"github.com/totoer/goya/internal/tsfrontend"
)

// Project is an in-memory TypeScript project. Names are virtual paths, not OS
// paths. Files and entry points are copied for each analysis. ConfigFile optionally
// selects a configuration from that catalog, including extends/references.
// Empty ConfigFile preserves entry-point selection without config discovery.
type Project = tsfrontend.Project
type SourceResolver = tsfrontend.SourceResolver
type ResolverFunc = tsfrontend.ResolverFunc

// CompileOptions contains compilerOptions settings for the pinned TypeScript
// version. Checking is strict by default and cannot be disabled.
type CompileOptions = tsfrontend.Options

// Diagnostic describes a compiler error, warning, or related source location.
// TypeScript diagnostics retain their original integer code in the "typescript"
// namespace; integration diagnostics use the separate "goya" namespace.
type Diagnostic = tsfrontend.Diagnostic
type SourcePosition = tsfrontend.Position
type SourceRange = tsfrontend.Range

// Environment is a frozen set of host type declarations and binding contracts.
// A nil or zero Environment exposes no host API. Registry construction supplies
// declarations and bindings together; applications do not mutate this value.
type Environment struct {
	declarations map[string]string
	schemaDigest string
	globals      map[string]hostDescriptor
	modules      map[string]map[string]hostDescriptor
}

// TypeScriptVersion reports the exact pinned TypeScript version. The default
// execution profile uses ES2025 standard declarations without external APIs.
func TypeScriptVersion() string { return tsfrontend.Version() }

// Check performs TypeScript analysis without creating a runtime or executing
// script or registered host functions. Error diagnostics are returned as data;
// err is reserved for failures such as cancellation. The returned source
// columns are one-based UTF-16 positions; offsets are UTF-8 byte offsets.
func Check(ctx stdcontext.Context, project Project, environment *Environment, options CompileOptions) ([]Diagnostic, error) {
	var types *tsfrontend.TypeEnvironment
	if environment != nil {
		types = &tsfrontend.TypeEnvironment{Declarations: environment.declarations, SchemaID: environment.schemaDigest}
	}
	return tsfrontend.Check(ctx, project, types, options)
}

// HasTypeErrors reports whether a diagnostic list contains any error severity.
func HasTypeErrors(diagnostics []Diagnostic) bool { return tsfrontend.HasErrors(diagnostics) }

// SetCompilationCacheLimits clears and configures bounded analysis retention.
// Defaults are two analyses and 16 MiB of captured sources. Either zero disables
// retention. Resolver snapshots and binding compatibility are checked each time.
func SetCompilationCacheLimits(entries, maxSourceBytes int) {
	tsfrontend.SetAnalysisCacheLimits(entries, maxSourceBytes)
}
