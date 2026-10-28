// Modified for Goya: internal import paths relocated by cmd/importupstream.

// Modified for Goya: embedded-language-service.

package autoimport

import (
	"context"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/collections"
	"github.com/totoer/goya/internal/tsfrontend/upstream/compiler"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsutil"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/internal/tsfrontend/upstream/module"
	"github.com/totoer/goya/internal/tsfrontend/upstream/modulespecifiers"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
)

type View struct {
	referenceCompletionCollecting bool
	referenceResolutionCollector  *[]tspath.Path

	referenceFutureHost    *referenceFutureSpecifierHost
	packageJsonAutoImports string
	registry               *Registry
	importingFile          *ast.SourceFile
	program                *compiler.Program
	preferences            modulespecifiers.UserPreferences
	projectKey             tspath.Path

	allowedEndings                   []modulespecifiers.ModuleSpecifierEnding
	conditions                       *collections.Set[string]
	shouldUseUriStyleNodeCoreModules core.Tristate
	existingImports                  *collections.MultiMap[ModuleID, existingImport]
	shouldUseRequireForFixes         *bool
}

func NewView(registry *Registry, importingFile *ast.SourceFile, projectKey tspath.Path, program *compiler.Program, preferences modulespecifiers.UserPreferences, packageJsonAutoImports string) *View {
	return &View{
		packageJsonAutoImports: packageJsonAutoImports,
		registry:               registry,
		importingFile:          importingFile,
		program:                program,
		projectKey:             projectKey,
		preferences:            preferences,
		conditions: collections.NewSetFromItems(
			module.GetConditions(program.Options(),
				program.GetDefaultResolutionModeForFile(importingFile))...,
		),
		shouldUseUriStyleNodeCoreModules: lsutil.ShouldUseUriStyleNodeCoreModules(importingFile, program),
	}
}

// A future-file view owns its metadata instead of mutating the borrowed
// program's source-file metadata map.
type referenceFutureSpecifierHost struct {
	*compiler.Program
	fileName string
	metadata ast.SourceFileMetaData
}

func (h *referenceFutureSpecifierHost) GetDefaultResolutionModeForFile(file ast.HasFileName) core.ResolutionMode {
	if file.FileName() != h.fileName {
		return h.Program.GetDefaultResolutionModeForFile(file)
	}
	options := h.Program.Options()
	resolution := options.GetModuleResolutionKind()
	if (resolution >= core.ModuleResolutionKindNode16 && resolution <= core.ModuleResolutionKindNodeNext) || options.GetResolvePackageJsonExports() || options.GetResolvePackageJsonImports() {
		return ast.GetImpliedNodeFormatForEmitWorker(file.FileName(), options.GetEmitModuleKind(), h.metadata)
	}
	return core.ResolutionModeNone
}
func (v *View) referenceSpecifierHost() modulespecifiers.ModuleSpecifierGenerationHost {
	if v.referenceFutureHost != nil {
		return v.referenceFutureHost
	}
	return v.program
}
func (v *View) referenceImportKind(export *Export) lsproto.ImportKind {
	if v.referenceFutureHost != nil && v.program.Options().VerbatimModuleSyntax.IsTrue() && ast.GetEmitModuleFormatOfFileWorker(v.importingFile.FileName(), v.program.Options(), v.referenceFutureHost.metadata) == core.ModuleKindCommonJS {
		return lsproto.ImportKindCommonJS
	}
	return getImportKind(v.importingFile, export, v.program)
}

func (v *View) getAllowedEndings() []modulespecifiers.ModuleSpecifierEnding {
	if v.allowedEndings == nil {
		resolutionMode := v.referenceSpecifierHost().GetDefaultResolutionModeForFile(v.importingFile)
		v.allowedEndings = modulespecifiers.GetAllowedEndingsInPreferredOrder(
			v.preferences,
			v.program,
			v.program.Options(),
			v.importingFile,
			"",
			resolutionMode,
		)
	}
	return v.allowedEndings
}

type QueryKind int

const (
	QueryKindWordPrefix QueryKind = iota
	QueryKindExactMatch
	QueryKindCaseInsensitiveMatch
)

func (v *View) Search(query string, kind QueryKind) []*Export {
	return core.Filter(v.SearchReferenceImports(query, kind), func(export *Export) bool { return !export.referenceImportOnly })
}

// Missing-import lookup can ask for inherited object/function members that the
// reference enumerator intentionally omits from completion suggestions.
func (v *View) SearchReferenceImports(query string, kind QueryKind) []*Export {
	searchFn := func(bucket *RegistryBucket) []*Export {
		switch kind {
		case QueryKindWordPrefix:
			return bucket.Index.SearchWordPrefix(query)
		case QueryKindExactMatch:
			return bucket.Index.Find(query, true)
		case QueryKindCaseInsensitiveMatch:
			return bucket.Index.Find(query, false)
		default:
			panic("unreachable")
		}
	}

	return v.search(searchFn)
}

func (v *View) SearchByExportID(id ExportID) []*Export {
	search := func(bucket *RegistryBucket) []*Export {
		return core.Filter(bucket.Index.entries, func(e *Export) bool {
			return e.ExportID == id
		})
	}

	return v.search(search)
}

func (v *View) search(searchFn func(*RegistryBucket) []*Export) []*Export {
	var results []*Export

	if bucket, ok := v.registry.projects[v.projectKey]; ok {
		exports := searchFn(bucket)
		results = slices.Grow(results, len(exports))
		for _, e := range exports {
			if string(e.ModuleID) == string(v.importingFile.Path()) {
				// Don't auto-import from the importing file itself
				continue
			}
			results = append(results, e)
		}
	}

	// Compute the set of packages accessible to the importing file.
	// This includes packages from package.json dependencies (aggregated from ancestor directories)
	// plus packages that are directly imported by the project's program files.
	// If no package.json is found, allowedPackages remains nil and all packages are allowed.
	var allowedPackages *collections.Set[string]
	tspath.ForEachAncestorDirectoryPath(v.importingFile.Path().GetDirectoryPath(), func(dirPath tspath.Path) (result any, stop bool) {
		if dir, ok := v.registry.directories[dirPath]; ok {
			if pj := dir.packageJson; pj.Exists() && pj.Contents.Parseable {
				// Initialize to empty set if this is the first package.json we've seen
				if allowedPackages == nil {
					allowedPackages = &collections.Set[string]{}
				}
				addPackageJsonDependencies(pj.Contents, allowedPackages)
			}
		}
		return nil, false
	})
	// Package-json discovery indexes declared dependencies. Already loaded
	// modules remain available independently of this optional provider.
	if allowedPackages == nil {
		allowedPackages = &collections.Set[string]{}
	}
	if v.packageJsonAutoImports == "off" {
		allowedPackages = &collections.Set[string]{}
	}
	if v.packageJsonAutoImports == "" || v.packageJsonAutoImports == "auto" {
		available := &collections.Set[string]{}
		tspath.ForEachAncestorDirectoryPath(v.importingFile.Path().GetDirectoryPath(), func(dirPath tspath.Path) (any, bool) {
			if bucket, ok := v.registry.nodeModules[dirPath]; ok {
				for name, files := range bucket.PackageFiles {
					if len(files) > 0 && allowedPackages.Has(name) {
						available.Add(name)
					}
				}
			}
			return nil, false
		})
		// The reference provider abandons proactive indexing above ten typed
		// dependencies in auto mode. Explicit on retains the complete catalog.
		if available.Len() > 10 {
			allowedPackages = &collections.Set[string]{}
		}
	}
	// If we found at least one package.json, also include packages directly imported by the project
	if allowedPackages != nil {
		if bucket, ok := v.registry.projects[v.projectKey]; ok {
			allowedPackages = allowedPackages.UnionedWith(bucket.ResolvedPackageNames)
		}
	}

	excludePackages := &collections.Set[string]{}
	tspath.ForEachAncestorDirectoryPath(v.importingFile.Path().GetDirectoryPath(), func(dirPath tspath.Path) (result any, stop bool) {
		if nodeModulesBucket, ok := v.registry.nodeModules[dirPath]; ok {
			exports := searchFn(nodeModulesBucket)
			results = slices.Grow(results, len(exports))
			for _, e := range exports {
				// Exclude packages found in lower node_modules (shadowing)
				if excludePackages.Has(e.PackageName) {
					continue
				}
				// If allowedPackages is nil, no package.json was found, so include all packages.
				// Otherwise, only include packages that are dependencies or directly imported.
				if allowedPackages != nil && !allowedPackages.Has(e.PackageName) && v.program.GetSourceFileByPath(e.Path) == nil {
					continue
				}
				results = append(results, e)
			}

			// As we go up the directory tree, exclude packages found in lower node_modules
			for pkgName := range nodeModulesBucket.PackageFiles {
				excludePackages.Add(pkgName)
			}
		}
		return nil, false
	})
	return results
}

type FixAndExport struct {
	ReferenceUnresolved              bool
	referenceResolutionCandidates    []*FixAndExport
	referenceResolutionRequiresCache bool
	referenceResolutionPaths         []tspath.Path
	referenceResolutionFirst         tspath.Path
	referenceResolutionGroup         string
	Fix                              *Fix
	Export                           *Export
}

// Reference completion resolution keeps the pinned per-importer memo and its
// cold-resolution/cache-attempt budgets separate from eager native fix building.
type referenceCompletionCache struct {
	mu    sync.Mutex
	paths map[tspath.Path]map[tspath.Path]bool
}

func (v *View) referenceResolvedCompletion(path tspath.Path) {
	cache := v.registry.referenceCompletionCache
	if cache == nil || path == "" {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	known := cache.paths[v.importingFile.Path()]
	if known == nil {
		known = map[tspath.Path]bool{}
		cache.paths[v.importingFile.Path()] = known
	}
	known[path] = true
}
func (v *View) ReferenceResolvedCompletion(candidate *FixAndExport) {
	if candidate == nil {
		return
	}
	for _, path := range candidate.referenceResolutionPaths {
		v.referenceResolvedCompletion(path)
	}
	v.referenceResolvedCompletion(candidate.Export.Path)
}
func (v *View) ReferenceCompletions(ctx context.Context, prefix string, position lsproto.Position, forJSX, isTypeOnlyLocation, allowIncomplete, fullResolution bool) ([]*FixAndExport, bool) {
	candidates := v.GetCompletions(ctx, prefix, position, forJSX, isTypeOnlyLocation)
	cache := v.registry.referenceCompletionCache
	if cache == nil {
		return candidates, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	known := cache.paths[v.importingFile.Path()]
	if known == nil {
		known = map[tspath.Path]bool{}
		cache.paths[v.importingFile.Path()] = known
	}
	order := map[tspath.Path]int{}
	for index, file := range v.program.GetSourceFiles() {
		order[file.Path()] = index
	}
	ranked := slices.Clone(candidates)
	slices.SortStableFunc(ranked, func(a, b *FixAndExport) int {
		ap, bp := a.referenceResolutionFirst, b.referenceResolutionFirst
		ai, ok := order[ap]
		if !ok {
			ai = len(order)
		}
		bi, ok := order[bp]
		if !ok {
			bi = len(order)
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
		if ap < bp {
			return -1
		}
		if ap > bp {
			return 1
		}
		return strings.Compare(a.Fix.Name, b.Fix.Name)
	})
	resolved, cacheAttempts := 0, 0
	skipped := false
	seen := map[string]bool{}
	result := make([]*FixAndExport, 0, len(candidates))
	compare := func(a, b *FixAndExport) int { return v.CompareFixesForRanking(a.Fix, b.Fix) }
	for _, candidate := range ranked {
		if ctx.Err() != nil {
			break
		}
		if seen[candidate.referenceResolutionGroup] {
			continue
		}
		seen[candidate.referenceResolutionGroup] = true
		all := candidate.referenceResolutionCandidates
		paths := candidate.referenceResolutionPaths
		// An existing import clause/specifier short-circuits new specifier lookup.
		for _, fix := range all {
			if fix.Fix.Kind == lsproto.AutoImportFixKindAddToExisting || fix.Fix.Kind == lsproto.AutoImportFixKindAddNew && !fix.referenceResolutionRequiresCache {
				paths = nil
				break
			}
		}
		selected := all
		valid := true
		switch {
		case modulespecifiers.PathIsBareSpecifier(string(candidate.Export.ModuleID)):
		case fullResolution || allowIncomplete && resolved < 100:
			for _, path := range paths {
				if !known[path] {
					known[path] = true
					resolved++
				}
			}
		case allowIncomplete && cacheAttempts < 1000:
			cacheAttempts++
			selected = nil
			// Cache-only lookup ranks all available export routes. A cached barrel can
			// win even when the original declaration has not yet been resolved.
			for _, fix := range all {
				if !fix.referenceResolutionRequiresCache || fix.Fix.Kind == lsproto.AutoImportFixKindUseNamespace || fix.Fix.Kind == lsproto.AutoImportFixKindAddToExisting || known[fix.Export.Path] {
					selected = append(selected, fix)
				}
			}
			valid = len(selected) > 0
		default:
			valid = false
		}
		if !valid {
			selected = all
			skipped = true
		}
		for _, fix := range core.MinAllFunc(selected, compare) {
			fix.ReferenceUnresolved = !valid
			result = append(result, fix)
		}
	}
	slices.SortFunc(result, func(a, b *FixAndExport) int { return v.CompareFixesForSorting(a.Fix, b.Fix) })
	return result, allowIncomplete && skipped
}

func (v *View) GetCompletions(ctx context.Context, prefix string, position lsproto.Position, forJSX bool, isTypeOnlyLocation bool) []*FixAndExport {
	previous := v.referenceCompletionCollecting
	v.referenceCompletionCollecting = true
	defer func() { v.referenceCompletionCollecting = previous; v.referenceResolutionCollector = nil }()
	results := v.Search(prefix, QueryKindWordPrefix)

	type exportGroupKey struct {
		target                     ExportID
		name                       string
		ambientModuleOrPackageName string
	}
	grouped := make(map[exportGroupKey][]*Export, len(results))
outer:
	for _, e := range results {
		name := e.Name()
		if !scanner.IsIdentifierText(name, core.LanguageVariantStandard) {
			continue
		}
		if forJSX && !(unicode.IsUpper(rune(name[0])) || e.IsRenameable()) {
			continue
		}
		target := e.ExportID
		if e.Target != (ExportID{}) {
			target = e.Target
		}
		key := exportGroupKey{
			target:                     target,
			name:                       name,
			ambientModuleOrPackageName: core.FirstNonZero(e.AmbientModuleName(), e.PackageName),
		}
		if e.PackageName == "@types/node" || strings.Contains(string(e.Path), "/node_modules/@types/node/") {
			if _, ok := core.UnprefixedNodeCoreModules[key.ambientModuleOrPackageName]; ok {
				// Group URI-style and non-URI style node core modules together so the ranking logic
				// is allowed to drop one if an explicit preference is detected.
				key.ambientModuleOrPackageName = "node:" + key.ambientModuleOrPackageName
			}
		}
		if existing, ok := grouped[key]; ok {
			for i, ex := range existing {
				if e.ExportID == ex.ExportID {
					grouped[key] = slices.Replace(existing, i, i+1, &Export{
						ExportID:                   e.ExportID,
						ModuleFileName:             e.ModuleFileName,
						PackageName:                e.PackageName,
						IsTypeOnly:                 e.IsTypeOnly || ex.IsTypeOnly,
						Syntax:                     min(e.Syntax, ex.Syntax),
						Flags:                      e.Flags | ex.Flags,
						ScriptElementKind:          min(e.ScriptElementKind, ex.ScriptElementKind),
						ScriptElementKindModifiers: e.ScriptElementKindModifiers | ex.ScriptElementKindModifiers,
						localName:                  e.localName,
						referenceCommonJSNamespace: e.referenceCommonJSNamespace,
						Target:                     e.Target,
						Path:                       e.Path,
					})
					continue outer
				}
			}
		}
		grouped[key] = append(grouped[key], e)
	}

	fixes := make([]*FixAndExport, 0, len(results))
	referenceSourceOrder := map[tspath.Path]int{}
	for index, file := range v.program.GetSourceFiles() {
		referenceSourceOrder[file.Path()] = index
	}
	compareFixes := func(a, b *FixAndExport) int {
		return v.CompareFixesForRanking(a.Fix, b.Fix)
	}

	for key, exps := range grouped {
		paths := []tspath.Path{}
		v.referenceResolutionCollector = &paths
		fixesForGroup := make([]*FixAndExport, 0, len(exps))
		for _, e := range exps {
			before := len(paths)
			computed := v.GetFixes(ctx, e, forJSX, isTypeOnlyLocation, &position)
			for _, fix := range computed {
				fixesForGroup = append(fixesForGroup, &FixAndExport{
					Fix:                              fix,
					Export:                           e,
					referenceResolutionRequiresCache: len(paths) > before,
				})
			}
		}
		first := tspath.Path("")
		order := referenceSourceOrder
		for _, export := range exps {
			if first == "" {
				first = export.Path
				continue
			}
			a, aok := order[export.Path]
			b, bok := order[first]
			if !aok {
				a = len(order)
			}
			if !bok {
				b = len(order)
			}
			if a < b || a == b && export.Path < first {
				first = export.Path
			}
		}
		group := string(key.target.ModuleID) + "\x00" + key.target.ExportName + "\x00" + key.name + "\x00" + key.ambientModuleOrPackageName
		for _, candidate := range fixesForGroup {
			candidate.referenceResolutionCandidates = fixesForGroup
			candidate.referenceResolutionPaths = slices.Clone(paths)
			candidate.referenceResolutionFirst = first
			candidate.referenceResolutionGroup = group
		}
		fixes = append(fixes, core.MinAllFunc(fixesForGroup, compareFixes)...)
	}

	// The client will do additional sorting by SortText and Label, so we don't
	// need to consider the name in our sorting here; we only need to produce a
	// stable relative ordering between completions that the client will consider
	// equivalent.
	slices.SortFunc(fixes, func(a, b *FixAndExport) int {
		return v.CompareFixesForSorting(a.Fix, b.Fix)
	})

	return fixes
}
