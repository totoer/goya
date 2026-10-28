package tsfrontend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/bundled"
	"github.com/totoer/goya/internal/tsfrontend/upstream/collections"
	"github.com/totoer/goya/internal/tsfrontend/upstream/compiler"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tsoptions"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
	"github.com/totoer/goya/internal/tsfrontend/upstream/vfs"
)

func analyzeConfiguredProject(ctx context.Context, project Project, snapshot *sourceFS, environmentID string, options Options, optionJSON []byte, allowJSONRoot bool) (*Analysis, []Diagnostic, error) {
	var capturedOptions Options
	if err := json.Unmarshal(optionJSON, &capturedOptions); err != nil {
		return nil, nil, err
	}
	host, config, ds, err := prepareConfiguredProject(snapshot, project, capturedOptions)
	if err != nil || config == nil || HasErrors(ds) {
		return nil, ds, err
	}
	for _, root := range config.FileNames() {
		if !isTypeScriptSource(root) && !(allowJSONRoot && strings.HasSuffix(root, ".json")) {
			return nil, append(ds, integrationError(1003, "entry point must be TypeScript: "+root)), nil
		}
	}
	if len(config.FileNames()) == 0 {
		return nil, append(ds, integrationError(1003, "the project has no TypeScript entry points")), nil
	}
	startupRoots := slices.Clone(config.FileNames())
	moduleRoots, moduleSources, rootDiagnostics := normalizeModuleRoots(snapshot, project.ModuleRoots)
	ds = append(ds, rootDiagnostics...)
	if HasErrors(ds) {
		return nil, ds, nil
	}
	if len(moduleSources) > 0 {
		prepared := tsoptions.NewParsedCommandLine(config.CompilerOptions(), append(slices.Clone(startupRoots), moduleSources...), tspath.ComparePathsOptions{UseCaseSensitiveFileNames: true, CurrentDirectory: config.GetCurrentDirectory()})
		prepared.ConfigFile, prepared.Raw, prepared.Errors = config.ConfigFile, config.Raw, config.Errors
		prepared.ParsedConfig.ProjectReferences = config.ProjectReferences()
		config = prepared
	}
	// The integration never invokes the emitter. Only the selected program gets
	// noEmit; forcing it onto referenced configs would spuriously trigger 6310.
	config.CompilerOptions().NoEmit = core.TSTrue
	config.CompilerOptions().NoEmitOnError = core.TSTrue
	identity, err := json.Marshal(struct {
		Version     string
		Baseline    [32]byte
		Environment string
		Files       map[string]string
		ConfigFile  string
		Entries     []string
		Modules     []string
		Options     json.RawMessage
	}{Version(), baselineDigest, environmentID, snapshot.files, config.ConfigName(), startupRoots, moduleRoots, optionJSON})
	if err != nil {
		return nil, nil, err
	}
	digest := sha256.Sum256(identity)
	key := hex.EncodeToString(digest[:])
	if cached, diagnostics := lookupAnalysis(key); cached != nil {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		return cached, diagnostics, nil
	}
	program := compiler.NewProgram(compiler.ProgramOptions{Host: host, Config: config, UseSourceOfProjectReference: true, SingleThreaded: core.TSTrue})
	raw := compiler.GetDiagnosticsOfAnyProgram(ctx, program, nil, false, program.GetBindDiagnostics, program.GetSemanticDiagnostics)
	// A referenced source is treated as a declaration by the importing checker.
	// Check each referenced project's own program as well, so errors in its
	// implementation cannot pass the execution gate through a valid signature.
	for _, child := range host.referencedConfigs(config.ConfigName()) {
		childOptions := child.CompilerOptions().Clone()
		childOptions.NoEmit = core.TSTrue
		childOptions.NoEmitOnError = core.TSTrue
		checked := tsoptions.NewParsedCommandLine(childOptions, slices.Clone(child.FileNames()), tspath.ComparePathsOptions{UseCaseSensitiveFileNames: true, CurrentDirectory: child.GetCurrentDirectory()})
		checked.ConfigFile = child.ConfigFile
		checked.Raw = child.Raw
		checked.Errors = child.Errors
		checked.ParsedConfig.ProjectReferences = child.ProjectReferences()
		childProgram := compiler.NewProgram(compiler.ProgramOptions{Host: host, Config: checked, UseSourceOfProjectReference: true, SingleThreaded: core.TSTrue})
		raw = append(raw, compiler.GetDiagnosticsOfAnyProgram(ctx, childProgram, nil, false, childProgram.GetBindDiagnostics, childProgram.GetSemanticDiagnostics)...)
		if err := ctx.Err(); err != nil {
			return nil, ds, err
		}
	}
	ds = append(ds, convertDiagnostics(compiler.SortAndDeduplicateDiagnostics(raw), nil)...)
	ds = append(ds, host.Diagnostics()...)
	ds = append(ds, ExecutionProfileDiagnostics(ctx, program, "")...)
	ds = append(ds, validateModuleRoots(ctx, program, moduleRoots)...)
	for _, source := range program.GetSourceFiles() {
		if source.ScriptKind == core.ScriptKindJSON {
			if diagnostic := validateJSONModule(source); diagnostic != nil {
				ds = append(ds, *diagnostic)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, ds, err
	}
	if HasErrors(ds) {
		return nil, ds, nil
	}
	profileJSON, err := json.Marshal(config.CompilerOptions())
	if err != nil {
		return nil, ds, err
	}
	profile := sha256.Sum256(append([]byte(executionProfileVersion+"\x00"), profileJSON...))
	a := &Analysis{program: program, files: snapshot, key: key, backend: &analysisBackendState{checked: true}, roots: startupRoots, moduleRoots: moduleRoots, profile: hex.EncodeToString(profile[:])}
	retainAnalysis(a, ds)
	return a, ds, nil
}

func compilerDefaults() Options {
	return Options{"strict": true, "target": "es2025", "module": "esnext", "moduleResolution": "bundler", "lib": []string{"es2025"}, "types": []string{}, "noEmit": true, "noEmitOnError": true}
}

func normalizeOptions(raw []byte) (Options, []Diagnostic, error) {
	merged := compilerDefaults()
	var explicit Options
	if err := json.Unmarshal(raw, &explicit); err != nil {
		return nil, nil, err
	}
	for key, value := range explicit {
		merged[key] = value
	}
	for _, key := range []string{"noCheck", "skipLibCheck", "skipDefaultLibCheck", "noResolve", "allowJs", "listFilesOnly"} {
		if enabled, _ := merged[key].(bool); enabled {
			return nil, []Diagnostic{integrationError(1005, "compiler option is incompatible with checked TypeScript compilation: "+key)}, nil
		}
	}
	return merged, nil, nil
}

// ProjectNormalizer applies the same host overrides, defaults and mandatory
// checking boundary to configured compiler programs and native editor projects.
type ProjectNormalizer struct {
	fs          vfs.FS
	explicit    *core.CompilerOptions
	keys        map[string]bool
	roots       []string
	configFile  string
	entryPoints []string
	mu          sync.Mutex
	diagnostics []Diagnostic
}

// SelectRoots applies explicit host entry points only to the selected config.
// Referenced projects retain their own roots for source/output redirection.
func (n *ProjectNormalizer) SelectRoots(configFile string, entries []string) error {
	n.configFile = configFile
	for _, name := range entries {
		path, err := canonicalName(name)
		if err != nil {
			return err
		}
		n.entryPoints = append(n.entryPoints, path)
	}
	return nil
}

func NewProjectNormalizer(fs vfs.FS, options Options, roots []string) (*ProjectNormalizer, []Diagnostic, error) {
	raw, err := json.Marshal(options)
	if err != nil {
		return nil, []Diagnostic{integrationError(1004, "invalid compiler options: "+err.Error())}, nil
	}
	_, ds, err := normalizeOptions(raw)
	if err != nil || len(ds) > 0 {
		return nil, ds, err
	}
	var detached Options
	if err := json.Unmarshal(raw, &detached); err != nil {
		return nil, nil, err
	}
	data, err := json.Marshal(map[string]any{"compilerOptions": detached, "files": []string{"/__goya_option_validation__.ts"}})
	if err != nil {
		return nil, nil, err
	}
	source := tsoptions.NewTsconfigSourceFileFromFilePath("/__goya_options__.json", tspath.Path("/__goya_options__.json"), string(data))
	host := compiler.NewCompilerHost("/", fs, bundled.LibPath(), nil, nil)
	parsed := tsoptions.ParseJsonSourceFileConfigFileContent(source, host, "/", nil, nil, "/__goya_options__.json", nil, nil, nil)
	rawErrors := append(slices.Clone(source.SourceFile.Diagnostics()), parsed.Errors...)
	if len(rawErrors) > 0 {
		return nil, convertDiagnostics(rawErrors, source.SourceFile), nil
	}
	n := &ProjectNormalizer{fs: fs, explicit: parsed.CompilerOptions(), keys: map[string]bool{}, roots: slices.Clone(roots)}
	for key := range detached {
		n.keys[key] = true
	}
	return n, nil, nil
}

func (n *ProjectNormalizer) Apply(config *tsoptions.ParsedCommandLine) {
	if config == nil {
		return
	}
	parsed := *config.ParsedConfig
	options := config.CompilerOptions().Clone()
	// Copy all explicitly supplied fields, including zero-valued enum/null
	// overrides. Paths were resolved against the host's virtual root above.
	dst, src := reflect.ValueOf(options).Elem(), reflect.ValueOf(n.explicit).Elem()
	typ := dst.Type()
	for i := range typ.NumField() {
		if !typ.Field(i).IsExported() {
			continue
		}
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if n.keys[name] {
			dst.Field(i).Set(src.Field(i))
		}
	}
	if n.keys["paths"] {
		options.PathsBasePath = n.explicit.PathsBasePath
	}
	if options.Strict == core.TSUnknown {
		options.Strict = core.TSTrue
	}
	if options.Target == core.ScriptTargetNone {
		options.Target = core.ScriptTargetES2025
	}
	if options.Module == core.ModuleKindNone && !n.hasOption(config, "module") {
		options.Module = core.ModuleKindESNext
	}
	if options.ModuleResolution == core.ModuleResolutionKindUnknown {
		options.ModuleResolution = core.ModuleResolutionKindBundler
	}
	if options.Lib == nil {
		options.Lib = []string{"lib.es2025.d.ts"}
	}
	if options.Types == nil {
		options.Types = []string{}
	}
	for _, item := range []struct {
		name  string
		value core.Tristate
	}{
		{"noCheck", options.NoCheck}, {"skipLibCheck", options.SkipLibCheck}, {"skipDefaultLibCheck", options.SkipDefaultLibCheck}, {"noResolve", options.NoResolve}, {"allowJs", options.AllowJs}, {"listFilesOnly", options.ListFilesOnly},
	} {
		if item.value.IsTrue() {
			n.mu.Lock()
			n.diagnostics = append(n.diagnostics, integrationError(1005, "compiler option is incompatible with checked TypeScript compilation: "+item.name))
			n.mu.Unlock()
		}
	}
	parsed.CompilerOptions = options
	parsed.FileNames = slices.Clone(config.FileNames())
	if len(n.entryPoints) > 0 && config.ConfigName() == n.configFile {
		parsed.FileNames = slices.Clone(n.entryPoints)
		// Inferred editor projects also contain other open documents. Keep those
		// analysis roots so unsaved exports remain available for auto-imports;
		// explicit entries still select the executable compilation roots.
		if n.configFile == "" {
			for _, name := range config.FileNames() {
				if !slices.Contains(parsed.FileNames, name) {
					parsed.FileNames = append(parsed.FileNames, name)
				}
			}
		}
	}
	for _, root := range n.roots {
		if !slices.Contains(parsed.FileNames, root) {
			parsed.FileNames = append(parsed.FileNames, root)
		}
	}
	config.ParsedConfig = &parsed
}

// ModuleKindNone is both a valid explicit setting and the native zero value.
// Inspect the parsed JSONC (including inherited configs) to preserve that choice.
func (n *ProjectNormalizer) hasOption(config *tsoptions.ParsedCommandLine, key string) bool {
	if n.keys[key] {
		return true
	}
	has := func(raw any) bool {
		if object, ok := raw.(*collections.OrderedMap[string, any]); ok {
			if options, ok := object.GetOrZero("compilerOptions").(*collections.OrderedMap[string, any]); ok {
				return options.GetOrZero(key) != nil
			}
		}
		return false
	}
	if has(config.Raw) {
		return true
	}
	for _, name := range config.ExtendedSourceFiles() {
		if text, ok := n.fs.ReadFile(name); ok {
			raw, _ := tsoptions.ParseConfigFileTextToJson(name, tspath.Path(name), text)
			if has(raw) {
				return true
			}
		}
	}
	return false
}

func (n *ProjectNormalizer) Diagnostics() []Diagnostic {
	n.mu.Lock()
	defer n.mu.Unlock()
	var unique []Diagnostic
	seen := map[string]bool{}
	for _, d := range n.diagnostics {
		if !seen[d.Message] {
			seen[d.Message] = true
			unique = append(unique, d)
		}
	}
	return cloneDiagnostics(unique)
}

type configuredHost struct {
	compiler.CompilerHost
	normalizer *ProjectNormalizer
	mu         sync.Mutex
	configs    map[string]*tsoptions.ParsedCommandLine
	errors     []*ast.Diagnostic
}

func (h *configuredHost) referencedConfigs(root string) []*tsoptions.ParsedCommandLine {
	h.mu.Lock()
	defer h.mu.Unlock()
	var names []string
	for name, cfg := range h.configs {
		if name != root && cfg != nil {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	var result []*tsoptions.ParsedCommandLine
	for _, name := range names {
		result = append(result, h.configs[name])
	}
	return result
}

func (h *configuredHost) GetResolvedProjectReference(name string, path tspath.Path) *tsoptions.ParsedCommandLine {
	h.mu.Lock()
	defer h.mu.Unlock()
	if config, ok := h.configs[name]; ok {
		return config
	}
	config, errors := tsoptions.GetParsedCommandLineOfConfigFilePath(name, path, nil, nil, h, nil)
	h.errors = append(h.errors, errors...)
	if config != nil {
		h.normalizer.Apply(config)
		h.errors = append(h.errors, config.Errors...)
	}
	h.configs[name] = config
	return config
}

func (h *configuredHost) Diagnostics() []Diagnostic {
	h.mu.Lock()
	raw := slices.Clone(h.errors)
	h.mu.Unlock()
	return append(convertDiagnostics(raw, nil), h.normalizer.Diagnostics()...)
}

func prepareConfiguredProject(snapshot *sourceFS, project Project, options Options) (*configuredHost, *tsoptions.ParsedCommandLine, []Diagnostic, error) {
	name, err := canonicalName(project.ConfigFile)
	if err != nil {
		return nil, nil, []Diagnostic{integrationError(1002, err.Error())}, nil
	}
	var declarations []string
	for path := range snapshot.files {
		if tspath.IsDeclarationFileName(path) {
			declarations = append(declarations, path)
		}
	}
	slices.Sort(declarations)
	fs := profileFS(snapshot)
	normalizer, ds, err := NewProjectNormalizer(fs, options, declarations)
	if err != nil || len(ds) > 0 {
		return nil, nil, ds, err
	}
	if err := normalizer.SelectRoots(name, project.EntryPoints); err != nil {
		return nil, nil, []Diagnostic{integrationError(1002, err.Error())}, nil
	}
	host := &configuredHost{CompilerHost: compiler.NewCompilerHost("/", fs, bundled.LibPath(), nil, nil), normalizer: normalizer, configs: map[string]*tsoptions.ParsedCommandLine{}}
	config := host.GetResolvedProjectReference(name, tspath.Path(name))
	if config == nil {
		return host, nil, host.Diagnostics(), nil
	}
	return host, config, host.Diagnostics(), nil
}
