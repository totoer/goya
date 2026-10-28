package tsfrontend

import (
	"context"
	"slices"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/compiler"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
)

func normalizeModuleRoots(files *sourceFS, roots []string) (normalized, sources []string, diagnostics []Diagnostic) {
	for _, name := range roots {
		if name == "" || strings.ContainsAny(name, "\x00\\") {
			diagnostics = append(diagnostics, integrationError(1113, "invalid prepared module root: "+name))
			continue
		}
		canonical, err := canonicalName(name)
		if err == nil && files.FileExists(canonical) {
			if tspath.IsDeclarationFileName(canonical) || !(isTypeScriptSource(canonical) || strings.HasSuffix(canonical, ".json")) {
				diagnostics = append(diagnostics, integrationError(1113, "prepared source root must be an executable module: "+name))
				continue
			}
			normalized = append(normalized, canonical)
			sources = append(sources, canonical)
		} else if tspath.PathIsRelative(name) || strings.HasPrefix(name, "/") {
			diagnostics = append(diagnostics, integrationError(1113, "prepared module source not found: "+name))
		} else {
			normalized = append(normalized, name)
		}
	}
	slices.Sort(normalized)
	normalized = slices.Compact(normalized)
	slices.Sort(sources)
	sources = slices.Compact(sources)
	return
}

func validateModuleRoots(ctx context.Context, program *compiler.Program, roots []string) []Diagnostic {
	if len(roots) == 0 || ctx.Err() != nil {
		return nil
	}
	checker, release := program.GetTypeChecker(ctx)
	defer release()
	ambients := make(map[string]bool)
	for _, symbol := range checker.GetAmbientModules() {
		ambients[strings.Trim(symbol.Name, "\"")] = true
	}
	var diagnostics []Diagnostic
	for _, name := range roots {
		if strings.HasPrefix(name, "/") {
			source := program.GetSourceFileForResolvedModule(name)
			if source == nil || (source.ScriptKind != core.ScriptKindJSON && !ast.IsExternalModule(source)) {
				diagnostics = append(diagnostics, integrationError(1113, "prepared source root must be an executable module: "+name))
			} else if ambients[name] || ambients[strings.TrimPrefix(name, "/")] {
				diagnostics = append(diagnostics, integrationError(1113, "ambiguous source and host module root: "+name))
			}
		} else if !ambients[name] {
			diagnostics = append(diagnostics, integrationError(1113, "prepared host module is not declared: "+name))
		}
	}
	return diagnostics
}
