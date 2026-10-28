// Modified for Goya: internal import paths relocated by cmd/importupstream.

// Modified for Goya: embedded-language-service.

package autoimport

import (
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/modulespecifiers"
)

func (v *View) GetModuleSpecifier(
	export *Export,
	userPreferences modulespecifiers.UserPreferences,
) (referenceSpecifier string, referenceKind modulespecifiers.ResultKind) {
	defer func() {
		if referenceSpecifier == "" || modulespecifiers.PathIsBareSpecifier(string(export.ModuleID)) {
			return
		}
		if v.referenceResolutionCollector != nil {
			*v.referenceResolutionCollector = append(*v.referenceResolutionCollector, export.Path)
		}
		if !v.referenceCompletionCollecting {
			v.referenceResolvedCompletion(export.Path)
		}
	}()
	// Ambient module
	if modulespecifiers.PathIsBareSpecifier(string(export.ModuleID)) {
		specifier := string(export.ModuleID)
		if modulespecifiers.IsExcludedByRegex(specifier, userPreferences.AutoImportSpecifierExcludeRegexes) {
			return "", modulespecifiers.ResultKindNone
		}
		return string(export.ModuleID), modulespecifiers.ResultKindAmbient
	}

	if export.PackageName != "" {
		if entrypoints, ok := v.registry.entrypoints[export.Path]; ok {
			for _, entrypoint := range entrypoints {
				if entrypoint.IncludeConditions.IsSubsetOf(v.conditions) && !v.conditions.Intersects(entrypoint.ExcludeConditions) {
					specifier := modulespecifiers.ProcessEntrypointEnding(
						entrypoint,
						userPreferences,
						v.referenceSpecifierHost(),
						v.program.Options(),
						v.importingFile,
						v.getAllowedEndings(),
					)

					if !modulespecifiers.IsExcludedByRegex(specifier, userPreferences.AutoImportSpecifierExcludeRegexes) {
						return specifier, modulespecifiers.ResultKindNodeModules
					}
				}
			}
			return "", modulespecifiers.ResultKindNone
		}
	}

	cache := v.registry.specifierCache[v.importingFile.Path()]
	// Closed edit targets may share prepared project indexes without a per-file
	// specifier memo. The memo is optional; compute from the same frozen program.
	if export.PackageName == "" && cache != nil {
		if specifier, ok := cache.Load(export.Path); ok {
			if specifier == "" {
				return "", modulespecifiers.ResultKindNone
			}
			return specifier, modulespecifiers.ResultKindRelative
		}
	}

	specifiers, kind := modulespecifiers.GetModuleSpecifiersForFileWithInfo(
		v.importingFile,
		export.ModuleFileName,
		v.program.Options(),
		v.referenceSpecifierHost(),
		userPreferences,
		modulespecifiers.ModuleSpecifierOptions{},
		true,
	)
	// !!! unsure when this could return multiple specifiers combined with the
	//     new node_modules code. Possibly with local symlinks, which should be
	//     very rare.
	for _, specifier := range specifiers {
		if strings.Contains(specifier, "/node_modules/") {
			continue
		}
		if cache != nil {
			cache.Store(export.Path, specifier)
		}
		return specifier, kind
	}
	if cache != nil {
		cache.Store(export.Path, "")
	}
	return "", modulespecifiers.ResultKindNone
}
