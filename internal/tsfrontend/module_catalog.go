package tsfrontend

import (
	"context"
	"encoding/json"
	"path"
	"slices"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/modulespecifiers"
	"github.com/totoer/goya/internal/tsfrontend/upstream/outputpaths"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
)

// PreparedSpecifierKey canonicalizes only virtual path spelling, never reads a
// source file and never performs module resolution. Bare aliases stay exact.
func PreparedSpecifierKey(from, name string) string {
	if tspath.PathIsRelative(name) {
		return path.Join(path.Dir(from), name)
	}
	if strings.HasPrefix(name, "/") {
		return path.Clean(name)
	}
	return name
}

// LookupPreparedImport applies frozen aliases to a finite catalog. An empty ID
// records an existing but unauthorized target and must not fall through.
func LookupPreparedImport(entries map[string]PreparedImport, rules []PreparedImportRule, from, name string) (PreparedImport, bool) {
	if entry, ok := entries[PreparedSpecifierKey(from, name)]; ok {
		return entry, entry.ID != ""
	}
	if tspath.PathIsRelative(name) || strings.HasPrefix(name, "/") {
		return PreparedImport{}, false
	}
	matchedPaths := false
	for _, rule := range rules {
		if rule.Kind == "paths" && matchedPaths {
			continue
		}
		capture, ok := patternCapture(rule.Pattern, name)
		if !ok {
			continue
		}
		if rule.Kind == "package" {
			parts := tspath.GetPathComponents(capture, "")
			if slices.Contains(parts, ".") || slices.Contains(parts, "..") || slices.Contains(parts, "node_modules") {
				return PreparedImport{}, false
			}
		}
		if rule.Kind == "paths" {
			matchedPaths = true
		}
		for _, target := range rule.Targets {
			key := path.Clean(strings.ReplaceAll(target, "*", capture))
			if entry, exists := entries[key]; exists {
				return entry, entry.ID != ""
			}
		}
		if rule.Kind == "package" {
			return PreparedImport{}, false
		}
	}
	return PreparedImport{}, false
}

func (a *Analysis) prepareImportCatalog(ctx context.Context, ir *ExecutionIR) error {
	allowed := make(map[string]bool)
	hosts := make(map[string]bool)
	computed := false
	for _, name := range ir.hostRoots {
		hosts[name] = true
	}
	for _, m := range ir.modules {
		allowed[m.id] = true
		computed = computed || m.computedImport
	}
	if computed {
		for _, m := range ir.modules {
			for _, d := range m.dependencies {
				if d.Host {
					hosts[d.Specifier] = true
				}
			}
		}
	}
	ir.hostRoots = ir.hostRoots[:0]
	for name := range hosts {
		ir.hostRoots = append(ir.hostRoots, name)
	}
	slices.Sort(ir.hostRoots)
	ir.imports = make(map[string]map[string]PreparedImport)
	ir.importRules = make(map[string][]PreparedImportRule)
	for _, m := range ir.modules {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(m.dynamicDependencies) == 0 && !m.computedImport {
			continue
		}
		from := a.program.GetSourceFile(m.id)
		if from == nil {
			continue
		}
		entries := make(map[string]PreparedImport)
		ir.imports[m.id] = entries
		for name := range hosts {
			entries[name] = PreparedImport{ID: name, Host: true}
		}
		candidates := make(map[string]bool)
		for _, d := range m.dynamicDependencies {
			candidates[d.Specifier] = true
		}
		options := a.program.Options()
		if redirect := a.program.GetRedirectForResolution(from); redirect != nil {
			options = redirect.CompilerOptions()
		}
		catalogFiles := make([]*IRModule, 0, len(a.files.files))
		for name := range a.files.files {
			if isTypeScriptSource(name) || strings.HasSuffix(name, ".json") {
				catalogFiles = append(catalogFiles, &IRModule{id: name})
			}
		}
		a.collectCatalogAliases(options, catalogFiles, candidates)
		ir.importRules[m.id] = preparedPathRules(options)
		ir.importRules[m.id] = append(ir.importRules[m.id], a.constantPackageRules(m.id, allowed)...)
		// Preserve existence decisions for unprepared paths to prevent fallback
		// to a different authorized file when an earlier mapping resolves.
		if len(ir.importRules[m.id]) > 0 {
			for name := range a.files.files {
				if isTypeScriptSource(name) || strings.HasSuffix(name, ".json") {
					for _, variant := range modulePathVariants(name) {
						candidates[variant] = true
					}
				}
			}
		}
		for _, target := range ir.modules {
			variants := modulePathVariants(target.id)
			if reference := a.program.GetProjectReferenceFromSource(tspath.Path(target.id)); reference != nil {
				output := outputpaths.GetOutputJSFileNameWorker(reference.Source, reference.Resolved.CompilerOptions(), reference.Resolved)
				variants = append(variants, output, reference.OutputDts)
			}
			for _, variant := range variants {
				candidates[variant] = true
				relative := tspath.GetRelativePathFromDirectory(path.Dir(m.id), variant, tspath.ComparePathsOptions{UseCaseSensitiveFileNames: true})
				if !tspath.PathIsRelative(relative) {
					relative = "./" + relative
				}
				candidates[relative] = true
			}
			for _, preference := range []modulespecifiers.ImportModuleSpecifierPreference{
				modulespecifiers.ImportModuleSpecifierPreferenceRelative,
				modulespecifiers.ImportModuleSpecifierPreferenceNonRelative,
			} {
				names, _ := modulespecifiers.GetModuleSpecifiersForFileWithInfo(from, target.id, options, a.program, modulespecifiers.UserPreferences{ImportModuleSpecifierPreference: preference}, modulespecifiers.ModuleSpecifierOptions{OverrideImportMode: core.ModuleKindESNext}, false)
				for _, name := range names {
					candidates[name] = true
				}
			}
		}
		for name := range candidates {
			if hosts[name] {
				continue
			}
			resolution := a.program.ResolveModuleName(name, m.id, core.ModuleKindESNext)
			if resolution == nil || resolution.ResolvedFileName == "" {
				key := PreparedSpecifierKey(m.id, name)
				if _, exists := entries[key]; !exists {
					entries[key] = PreparedImport{}
				}
				continue
			}
			target := resolution.ResolvedFileName
			if source := a.program.GetSourceFileForResolvedModule(target); source != nil {
				target = source.FileName()
			} else if redirect := a.program.GetParseFileRedirect(target); redirect != "" {
				target = redirect
			}
			entry := PreparedImport{}
			if allowed[target] {
				entry.ID = target
			}
			entries[PreparedSpecifierKey(m.id, name)] = entry
		}
	}
	return nil
}

// A wildcard may map every matching name to one fixed file. Such an alias has
// infinitely many spellings, so retain its normalized rule instead of samples.
func (a *Analysis) constantPackageRules(from string, allowed map[string]bool) []PreparedImportRule {
	patterns := make(map[string]struct{})
	probeChars := make(map[rune]bool)
	maxPath := 0
	for name := range a.files.files {
		maxPath = max(maxPath, len(name))
		if reference := a.program.GetProjectReferenceFromSource(tspath.Path(name)); reference != nil {
			maxPath = max(maxPath, len(reference.OutputDts))
		}
	}
	scope := a.program.GetNearestAncestorDirectoryWithPackageJson(path.Dir(from))
	for name, text := range a.files.files {
		if path.Base(name) != "package.json" {
			continue
		}
		var metadata struct {
			Name    string         `json:"name"`
			Exports any            `json:"exports"`
			Imports map[string]any `json:"imports"`
		}
		if json.Unmarshal([]byte(text), &metadata) != nil {
			continue
		}
		for alias := range metadata.Imports {
			for _, r := range alias {
				probeChars[r] = true
			}
		}
		if exports, ok := metadata.Exports.(map[string]any); ok {
			for alias := range exports {
				for _, r := range alias {
					probeChars[r] = true
				}
			}
		}
		if path.Dir(name) == scope {
			for alias := range metadata.Imports {
				if strings.Contains(alias, "*") {
					patterns[alias] = struct{}{}
				}
			}
		}
		for _, packageName := range a.applicablePackageNames(from, name, metadata.Name, scope) {
			if exports, ok := metadata.Exports.(map[string]any); ok {
				for alias := range exports {
					if strings.HasPrefix(alias, "./") {
						full := packageName + strings.TrimPrefix(alias, ".")
						if strings.Contains(full, "*") {
							patterns[full] = struct{}{}
						}
					}
				}
			}
		}
	}
	probeRune := rune(0xE000)
	for probeChars[probeRune] {
		probeRune++
	}
	// A capture longer than every frozen file/output path cannot accidentally
	// resolve a wildcard arm. The native resolver therefore decides its actual
	// constant fallback, including conditional order, arrays and null blockers.
	probe := strings.Repeat(string(probeRune), maxPath+1)
	var rules []PreparedImportRule
	for pattern := range patterns {
		sample := strings.ReplaceAll(pattern, "*", probe)
		resolution := a.program.ResolveModuleName(sample, from, core.ModuleKindESNext)
		if resolution == nil {
			rules = append(rules, PreparedImportRule{Pattern: pattern, Kind: "package"})
			continue
		}
		target := resolution.ResolvedFileName
		if source := a.program.GetSourceFileForResolvedModule(target); source != nil {
			target = source.FileName()
		}
		if allowed[target] {
			rules = append(rules, PreparedImportRule{Pattern: pattern, Targets: []string{target}, Kind: "package"})
		} else {
			rules = append(rules, PreparedImportRule{Pattern: pattern, Kind: "package"})
		}
	}
	slices.SortFunc(rules, func(a, b PreparedImportRule) int {
		ap, as, _ := strings.Cut(a.Pattern, "*")
		bp, bs, _ := strings.Cut(b.Pattern, "*")
		if len(ap) != len(bp) {
			return len(bp) - len(ap)
		}
		if len(as) != len(bs) {
			return len(bs) - len(as)
		}
		return strings.Compare(a.Pattern, b.Pattern)
	})
	return rules
}

func (a *Analysis) applicablePackageNames(from, filename, name, scope string) []string {
	directory := path.Dir(filename)
	var names []string
	if directory == scope && name != "" {
		names = append(names, name)
	}
	installed := installedPackageName(filename)
	if installed == "" {
		return names
	}
	for dir := path.Dir(from); ; dir = path.Dir(dir) {
		candidate := path.Join(dir, "node_modules", installed, "package.json")
		if a.files.FileExists(candidate) {
			if candidate == filename && !slices.Contains(names, installed) {
				names = append(names, installed)
			}
			return names
		}
		if dir == "/" {
			break
		}
	}
	return names
}

func installedPackageName(filename string) string {
	directory := path.Dir(filename)
	const marker = "/node_modules/"
	index := strings.LastIndex(directory, marker)
	if index < 0 {
		return ""
	}
	installed := directory[index+len(marker):]
	parts := strings.Split(installed, "/")
	if (strings.HasPrefix(installed, "@") && len(parts) != 2) || (!strings.HasPrefix(installed, "@") && len(parts) != 1) {
		return ""
	}
	return installed
}

func preparedPathRules(options *core.CompilerOptions) []PreparedImportRule {
	base := options.BaseUrl
	if base == "" {
		base = options.PathsBasePath
	}
	if base == "" {
		base = path.Dir(options.ConfigFilePath)
	}
	if !strings.HasPrefix(base, "/") {
		base = path.Join("/", base)
	}
	var rules []PreparedImportRule
	if options.Paths != nil {
		for alias, targets := range options.Paths.Entries() {
			copy := slices.Clone(targets)
			for i, target := range copy {
				if !strings.HasPrefix(target, "/") {
					copy[i] = path.Join(base, target)
				}
			}
			rules = append(rules, PreparedImportRule{Pattern: alias, Targets: copy, Kind: "paths"})
		}
	}
	slices.SortStableFunc(rules, func(a, b PreparedImportRule) int {
		ap, as, astar := strings.Cut(a.Pattern, "*")
		bp, bs, bstar := strings.Cut(b.Pattern, "*")
		if !astar && bstar {
			return -1
		}
		if astar && !bstar {
			return 1
		}
		if len(ap) != len(bp) {
			return len(bp) - len(ap)
		}
		return len(bs) - len(as)
	})
	if options.BaseUrl != "" {
		rules = append(rules, PreparedImportRule{Pattern: "*", Targets: []string{path.Join(options.BaseUrl, "*")}, Kind: "baseUrl"})
	}
	return rules
}

func patternCapture(pattern, value string) (string, bool) {
	before, after, star := strings.Cut(pattern, "*")
	if !star {
		return "", pattern == value
	}
	if len(value) < len(before)+len(after) || !strings.HasPrefix(value, before) || !strings.HasSuffix(value, after) {
		return "", false
	}
	return value[len(before) : len(value)-len(after)], true
}

func (a *Analysis) collectCatalogAliases(options *core.CompilerOptions, modules []*IRModule, candidates map[string]bool) {
	type externalAlias struct{ alias, target string }
	var externalAliases []externalAlias
	add := func(alias, target, base string) {
		if !strings.Contains(alias, "*") {
			candidates[alias] = true
			return
		}
		candidates[strings.ReplaceAll(alias, "*", "")] = true
		pattern := target
		if !strings.HasPrefix(pattern, "/") {
			pattern = path.Join(base, pattern)
		}
		for _, m := range modules {
			for _, variant := range modulePathVariants(m.id) {
				if capture, ok := patternCapture(pattern, variant); ok && strings.Contains(pattern, "*") {
					candidates[strings.ReplaceAll(alias, "*", capture)] = true
				}
			}
		}
	}
	base := options.BaseUrl
	if base == "" {
		base = options.PathsBasePath
	}
	if base == "" {
		base = path.Dir(options.ConfigFilePath)
	}
	if !strings.HasPrefix(base, "/") {
		base = path.Join("/", base)
	}
	if options.Paths != nil {
		for alias, targets := range options.Paths.Entries() {
			for _, target := range targets {
				add(alias, target, base)
			}
		}
	}
	for name, text := range a.files.files {
		if path.Base(name) != "package.json" {
			continue
		}
		var metadata struct {
			Name    string         `json:"name"`
			Exports any            `json:"exports"`
			Imports map[string]any `json:"imports"`
		}
		if json.Unmarshal([]byte(text), &metadata) != nil {
			continue
		}
		isImports := true
		var targets func(string, any)
		targets = func(alias string, raw any) {
			switch value := raw.(type) {
			case string:
				if isImports && strings.Contains(alias, "*") && strings.Contains(value, "*") && !tspath.PathIsRelative(value) && !strings.HasPrefix(value, "/") {
					externalAliases = append(externalAliases, externalAlias{alias, value})
				}
				add(alias, value, path.Dir(name))
			case []any:
				for _, item := range value {
					targets(alias, item)
				}
			case map[string]any:
				for _, item := range value {
					targets(alias, item)
				}
			}
		}
		for alias, raw := range metadata.Imports {
			if !strings.Contains(alias, "*") {
				candidates[alias] = true
			}
			targets(alias, raw)
		}
		isImports = false
		packageNames := []string{metadata.Name}
		if installed := installedPackageName(name); installed != "" && installed != metadata.Name {
			packageNames = append(packageNames, installed)
		}
		for _, packageName := range packageNames {
			if packageName == "" {
				continue
			}
			if exports, ok := metadata.Exports.(map[string]any); ok {
				for key, raw := range exports {
					if key == "." {
						targets(packageName, raw)
					} else if strings.HasPrefix(key, "./") {
						if !strings.Contains(key, "*") {
							candidates[packageName+strings.TrimPrefix(key, ".")] = true
						}
						targets(packageName+strings.TrimPrefix(key, "."), raw)
					} else {
						targets(packageName, raw)
					}
				}
			} else {
				targets(packageName, metadata.Exports)
			}
		}
	}
	// External imports map package specifiers rather than file paths. Derive
	// their spellings from the finite file-backed aliases, including chains;
	// the native resolver below still validates scope and conditional branches.
	for range externalAliases {
		var names []string
		for name := range candidates {
			names = append(names, name)
		}
		changed := false
		for _, mapping := range externalAliases {
			for _, name := range names {
				if capture, ok := patternCapture(mapping.target, name); ok {
					alias := strings.ReplaceAll(mapping.alias, "*", capture)
					if !candidates[alias] {
						candidates[alias] = true
						changed = true
					}
				}
			}
		}
		if !changed {
			break
		}
	}
}

func modulePathVariants(name string) []string {
	result := []string{name}
	for _, decl := range []struct{ ext, output string }{{".d.ts", ".js"}, {".d.mts", ".mjs"}, {".d.cts", ".cjs"}} {
		if strings.HasSuffix(name, decl.ext) {
			base := strings.TrimSuffix(name, decl.ext)
			sourceExt := strings.TrimPrefix(decl.ext, ".d")
			return append(result, base+decl.output, base+sourceExt, base)
		}
	}
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if ext == ".ts" || ext == ".tsx" {
		result = append(result, base+".js", base+".jsx", base)
	}
	if ext == ".mts" {
		result = append(result, base+".mjs")
	}
	if ext == ".cts" {
		result = append(result, base+".cjs")
	}
	if path.Base(base) == "index" {
		result = append(result, path.Dir(base))
	}
	return result
}
