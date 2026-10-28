package tsfrontend

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/bundled"
	"github.com/totoer/goya/internal/tsfrontend/upstream/compiler"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tsoptions"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
)

// Project describes a virtual project, not paths to read from the OS.
type Project struct {
	// ConfigFile explicitly selects a configuration in the virtual catalog.
	// Empty preserves the original entry-point/options behavior.
	ConfigFile  string
	EntryPoints []string
	// ModuleRoots prepares additional source or host modules without starting them.
	ModuleRoots []string
	Files       map[string]string
	Resolver    SourceResolver
}

// SourceResolver supplies a consistent virtual project snapshot. It is called
// once before analysis. Returned files are copied and never reread from the
// resolver during checking or lowering.
type SourceResolver interface {
	Snapshot(context.Context) (map[string]string, error)
}

type ResolverFunc func(context.Context) (map[string]string, error)

func (f ResolverFunc) Snapshot(ctx context.Context) (map[string]string, error) { return f(ctx) }

// TypeEnvironment is the detached type side of a frozen host registry. The
// schema identity includes conversion policies, even if declarations coincide.
type TypeEnvironment struct {
	Declarations map[string]string
	SchemaID     string
}

//go:embed manifest.json
var baselineManifest string

const executionProfileVersion = "prepared-modules-no-string-compilation-v1"

var baselineDigest = sha256.Sum256([]byte(executionProfileVersion + "\x00" + baselineManifest))

// Options uses the compilerOptions property names from the pinned TypeScript
// version. Options are copied and validated before analysis.
type Options map[string]any

type Position struct {
	Offset int `json:"offset"` // UTF-8 byte offset in the original source.
	Line   int `json:"line"`   // One-based line number.
	Column int `json:"column"` // One-based UTF-16 column number, as in TypeScript diagnostics.
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Diagnostic struct {
	Namespace string       `json:"namespace"`
	Code      int32        `json:"code"`
	Severity  string       `json:"severity"`
	Message   string       `json:"message"`
	File      string       `json:"file,omitempty"`
	Range     *Range       `json:"range,omitempty"`
	Related   []Diagnostic `json:"related,omitempty"`
}

// Analysis can only be created by a successful Analyze. Its upstream program is
// private; integration backends must not turn an arbitrary AST into an Analysis.
type Analysis struct {
	program     *compiler.Program
	files       *sourceFS
	key         string
	backend     *analysisBackendState
	roots       []string
	moduleRoots []string
	profile     string
}

func (a *Analysis) Key() string { return a.key }

func Version() string { return core.Version() }

func IsIdentifier(name string) bool {
	return scanner.IsIdentifierText(name, core.LanguageVariantStandard)
}

func CanonicalSourceName(name string) (string, error) { return canonicalName(name) }

// Check analyzes a project without evaluating it or creating an executable VM.
func Check(ctx context.Context, project Project, environment *TypeEnvironment, options Options) ([]Diagnostic, error) {
	_, diagnostics, err := Analyze(ctx, project, environment, options)
	return diagnostics, err
}

// Analyze provides the checked representation for the integration backend. It
// returns no analysis when any error diagnostic is present.
func Analyze(ctx context.Context, project Project, environment *TypeEnvironment, options Options) (*Analysis, []Diagnostic, error) {
	return analyzeProject(ctx, project, environment, options, false)
}

func analyzeProject(ctx context.Context, project Project, environment *TypeEnvironment, options Options, allowJSONRoot bool) (*Analysis, []Diagnostic, error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("a non-nil context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !bundled.Embedded {
		return nil, []Diagnostic{integrationError(1001, "the frontend requires embedded TypeScript assets")}, nil
	}
	project.EntryPoints = slices.Clone(project.EntryPoints)
	project.ModuleRoots = slices.Clone(project.ModuleRoots)
	optionJSON, err := json.Marshal(options)
	if err != nil {
		return nil, []Diagnostic{integrationError(1004, "invalid compiler options: "+err.Error())}, nil
	}
	var declarations map[string]string
	var environmentID string
	if environment != nil {
		declarations, environmentID = maps.Clone(environment.Declarations), environment.SchemaID
	}
	files := make(map[string]string, len(project.Files)+len(declarations))
	for name, source := range project.Files {
		files[name] = source
	}
	if project.Resolver != nil {
		resolved, err := project.Resolver.Snapshot(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve TypeScript project: %w", err)
		}
		for name, source := range resolved {
			if _, exists := files[name]; exists {
				return nil, []Diagnostic{integrationError(1002, "resolver and explicit files overlap: "+name)}, nil
			}
			files[name] = source
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	for name, source := range declarations {
		if _, exists := files[name]; exists {
			return nil, []Diagnostic{integrationError(1002, "host declarations collide with project source "+name)}, nil
		}
		files[name] = source
	}
	snapshot, err := freezeFiles(files)
	if err != nil {
		return nil, []Diagnostic{integrationError(1002, err.Error())}, nil
	}
	if project.ConfigFile != "" {
		return analyzeConfiguredProject(ctx, project, snapshot, environmentID, options, optionJSON, allowJSONRoot)
	}
	moduleRoots, moduleSources, rootDiagnostics := normalizeModuleRoots(snapshot, project.ModuleRoots)
	if HasErrors(rootDiagnostics) {
		return nil, rootDiagnostics, nil
	}
	entryPoints := make([]string, 0, len(project.EntryPoints)+len(files))
	for _, name := range project.EntryPoints {
		canonical, err := canonicalName(name)
		if err != nil {
			return nil, []Diagnostic{integrationError(1002, err.Error())}, nil
		}
		entryPoints = append(entryPoints, canonical)
	}
	if len(project.EntryPoints) == 0 {
		for name := range snapshot.files {
			if isTypeScriptSource(name) {
				entryPoints = append(entryPoints, name)
			}
		}
		slices.Sort(entryPoints)
	} else {
		var declarationRoots []string
		for name := range snapshot.files {
			if strings.HasSuffix(name, ".d.ts") || strings.HasSuffix(name, ".d.mts") || strings.HasSuffix(name, ".d.cts") {
				declarationRoots = append(declarationRoots, name)
			}
		}
		slices.Sort(declarationRoots)
		entryPoints = append(entryPoints, declarationRoots...)
	}
	seenRoots := make(map[string]bool)
	orderedRoots := entryPoints[:0]
	for _, name := range entryPoints {
		if !seenRoots[name] {
			orderedRoots = append(orderedRoots, name)
			seenRoots[name] = true
		}
	}
	entryPoints = orderedRoots
	if len(entryPoints) == 0 {
		return nil, []Diagnostic{integrationError(1003, "the project has no TypeScript entry points")}, nil
	}
	for _, name := range entryPoints {
		if !isTypeScriptSource(name) && !(allowJSONRoot && strings.HasSuffix(name, ".json")) {
			return nil, []Diagnostic{integrationError(1003, "entry point must be TypeScript: "+name)}, nil
		}
	}

	// Round-trip to detach nested maps/slices and reject non-JSON option values.
	merged, optionDiagnostics, err := normalizeOptions(optionJSON)
	if err != nil || len(optionDiagnostics) > 0 {
		return nil, optionDiagnostics, err
	}
	analysisRoots := append(slices.Clone(entryPoints), moduleSources...)
	configJSON, err := json.Marshal(map[string]any{"compilerOptions": merged, "files": analysisRoots})
	if err != nil {
		return nil, nil, err
	}
	configSource := tsoptions.NewTsconfigSourceFileFromFilePath("/tsconfig.json", tspath.Path("/tsconfig.json"), string(configJSON))
	host := compiler.NewCompilerHost("/", profileFS(snapshot), bundled.LibPath(), nil, nil)
	config := tsoptions.ParseJsonSourceFileConfigFileContent(configSource, host, "/", nil, nil, "/tsconfig.json", nil, nil, nil)
	configErrors := append(slices.Clone(configSource.SourceFile.Diagnostics()), config.Errors...)
	if len(configErrors) > 0 {
		return nil, convertDiagnostics(configErrors, configSource.SourceFile), nil
	}
	// Validate the caller's option types first, including output-related options.
	// The embedded frontend itself never emits files; our backend consumes AST.
	config.CompilerOptions().NoEmit = core.TSTrue
	config.CompilerOptions().NoEmitOnError = core.TSTrue
	identity, err := json.Marshal(struct {
		Version     string
		Baseline    [32]byte
		Environment string
		Files       map[string]string
		Entries     []string
		Modules     []string
		Options     json.RawMessage
	}{Version(), baselineDigest, environmentID, snapshot.files, entryPoints, moduleRoots, configJSON})
	if err != nil {
		return nil, nil, err
	}
	digest := sha256.Sum256(identity)
	key := hex.EncodeToString(digest[:])
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if cached, ds := lookupAnalysis(key); cached != nil {
		return cached, ds, nil
	}
	program := compiler.NewProgram(compiler.ProgramOptions{Host: host, Config: config, SingleThreaded: core.TSTrue})
	raw := compiler.GetDiagnosticsOfAnyProgram(ctx, program, nil, false, program.GetBindDiagnostics, program.GetSemanticDiagnostics)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	result := convertDiagnostics(raw, configSource.SourceFile)
	result = append(result, ExecutionProfileDiagnostics(ctx, program, "")...)
	result = append(result, validateModuleRoots(ctx, program, moduleRoots)...)
	for _, source := range program.GetSourceFiles() {
		if source.ScriptKind == core.ScriptKindJSON {
			if diagnostic := validateJSONModule(source); diagnostic != nil {
				result = append(result, *diagnostic)
			}
		}
	}
	if HasErrors(result) {
		return nil, result, nil
	}
	profileJSON, _ := json.Marshal(merged)
	profile := sha256.Sum256(append([]byte(executionProfileVersion+"\x00"), profileJSON...))
	analysis := &Analysis{program: program, files: snapshot, key: key, profile: hex.EncodeToString(profile[:]), backend: &analysisBackendState{checked: true}, roots: slices.Clone(entryPoints), moduleRoots: moduleRoots}
	retainAnalysis(analysis, result)
	return analysis, result, nil
}

func HasErrors(diagnostics []Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return true
		}
	}
	return false
}

func isTypeScriptSource(name string) bool {
	return strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".tsx") || strings.HasSuffix(name, ".mts") || strings.HasSuffix(name, ".cts")
}

func integrationError(code int32, message string) Diagnostic {
	return Diagnostic{Namespace: "goya", Code: code, Severity: "error", Message: message}
}

func convertDiagnostics(source []*ast.Diagnostic, generatedConfig *ast.SourceFile) []Diagnostic {
	result := make([]Diagnostic, 0, len(source))
	seen := make(map[string]bool)
	for _, raw := range source {
		converted := convertDiagnostic(raw, generatedConfig)
		position := -1
		if converted.Range != nil {
			position = converted.Range.Start.Offset
		}
		identity := converted.File + ":" + strconv.Itoa(position) + ":" + strconv.Itoa(int(converted.Code)) + ":" + converted.Message
		if !seen[identity] {
			result = append(result, converted)
			seen[identity] = true
		}
	}
	slices.SortStableFunc(result, func(a, b Diagnostic) int {
		if order := strings.Compare(a.File, b.File); order != 0 {
			return order
		}
		apos, bpos := -1, -1
		if a.Range != nil {
			apos = a.Range.Start.Offset
		}
		if b.Range != nil {
			bpos = b.Range.Start.Offset
		}
		if apos != bpos {
			return apos - bpos
		}
		return int(a.Code - b.Code)
	})
	return result
}

func convertDiagnostic(raw *ast.Diagnostic, generatedConfig *ast.SourceFile) Diagnostic {
	result := Diagnostic{
		Namespace: "typescript", Code: raw.Code(), Severity: raw.Category().Name(),
		Message: diagnostics.Localize(locale.Default, nil, raw.MessageKey(), raw.MessageArgs()...),
	}
	for _, child := range raw.MessageChain() {
		result.Message += "\n  " + strings.ReplaceAll(convertDiagnostic(child, generatedConfig).Message, "\n", "\n  ")
	}
	// Options are supplied as a Go map, not as a caller-owned JSON source file.
	// Do not present generated tsconfig coordinates as original source positions.
	if file := raw.File(); file != nil && file != generatedConfig {
		result.File = file.FileName()
		if raw.Pos() >= 0 && raw.End() >= raw.Pos() {
			result.Range = &Range{Start: sourcePosition(file.Text(), raw.Pos()), End: sourcePosition(file.Text(), raw.End())}
		}
	}
	for _, related := range raw.RelatedInformation() {
		result.Related = append(result.Related, convertDiagnostic(related, generatedConfig))
	}
	return result
}

func sourcePosition(source string, offset int) Position {
	offset = max(0, min(offset, len(source)))
	line, column := 1, 1
	previousCR := false
	for _, character := range source[:offset] {
		switch character {
		case '\r':
			line++
			column = 1
			previousCR = true
		case '\n':
			if !previousCR {
				line++
			}
			column = 1
			previousCR = false
		case '\u2028', '\u2029':
			line++
			column = 1
			previousCR = false
		default:
			column += utf16.RuneLen(character)
			previousCR = false
		}
	}
	return Position{Offset: offset, Line: line, Column: column}
}
