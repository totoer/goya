package tsfrontend

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	execir "github.com/totoer/goya/ast"
)

// ExecutionIR is a checked, internal handoff. The initial instruction selection
// reuses Goya's execution AST as its operation vocabulary. Its source file and
// explicit ranges refer to the immutable TypeScript snapshot, without printing
// or reparsing generated JavaScript.
type ExecutionIR struct {
	key         string
	profile     string
	sources     []SourceIdentity
	modules     []*IRModule
	roots       []string
	hostRoots   []string
	imports     map[string]map[string]PreparedImport
	importRules map[string][]PreparedImportRule
}

type SourceIdentity struct{ ID, SHA256 string }

func (ir *ExecutionIR) Profile() string           { return ir.profile }
func (ir *ExecutionIR) Sources() []SourceIdentity { return slices.Clone(ir.sources) }

type IRModule struct {
	id                  string
	tree                *execir.Program
	ranges              map[execir.Node]Range
	external            bool
	strict              bool
	async               bool
	commonJS            bool
	profile             string
	json                bool
	globalNames         []string
	standardGlobalNames []string
	dependencies        []IRDependency
	dynamicDependencies []IRDependency
	computedImport      bool
	exports             []IRExport
}

func (ir *ExecutionIR) Key() string {
	if ir == nil {
		return ""
	}
	return ir.key
}

// Visit hands each privately owned tree to the trusted bytecode backend. There
// is deliberately no public constructor taking an arbitrary AST or source key.
func (ir *ExecutionIR) Visit(backend func(*IRModule) error) error {
	if ir == nil || ir.key == "" {
		return fmt.Errorf("backend requires checked execution IR")
	}
	for _, module := range ir.modules {
		if err := backend(module); err != nil {
			return err
		}
	}
	return nil
}
func (m *IRModule) ID() string                                 { return m.id }
func (m *IRModule) Tree() *execir.Program                      { return m.tree }
func (m *IRModule) IsExternalModule() bool                     { return m.external }
func (m *IRModule) Strict() bool                               { return m.strict }
func (m *IRModule) HasTopLevelAwait() bool                     { return m.async }
func (m *IRModule) IsCommonJS() bool                           { return m.commonJS }
func (m *IRModule) Profile() string                            { return m.profile }
func (m *IRModule) IsJSON() bool                               { return m.json }
func (m *IRModule) RequiredGlobals() []string                  { return slices.Clone(m.globalNames) }
func (m *IRModule) RequiredStandardGlobals() []string          { return slices.Clone(m.standardGlobalNames) }
func (m *IRModule) SourceRange(node execir.Node) (Range, bool) { r, ok := m.ranges[node]; return r, ok }

func (ir *ExecutionIR) EntryPoints() []string     { return slices.Clone(ir.roots) }
func (ir *ExecutionIR) HostModuleRoots() []string { return slices.Clone(ir.hostRoots) }

// PreparedImport names executable code or an approved host namespace. It carries
// no frontend state and cannot load additional source text.
type PreparedImport struct {
	ID   string
	Host bool
}

// PreparedImportRule is a normalized path alias; targets contain no source or
// package/configuration JSON. Lookup is restricted by the catalog's target set.
type PreparedImportRule struct {
	Pattern string
	Targets []string
	Kind    string
}

func (ir *ExecutionIR) PreparedImportRules() map[string][]PreparedImportRule {
	result := make(map[string][]PreparedImportRule, len(ir.importRules))
	for from, rules := range ir.importRules {
		copy := slices.Clone(rules)
		for i := range copy {
			copy[i].Targets = slices.Clone(copy[i].Targets)
		}
		result[from] = copy
	}
	return result
}

func (ir *ExecutionIR) PreparedImports() map[string]map[string]PreparedImport {
	result := make(map[string]map[string]PreparedImport, len(ir.imports))
	for from, entries := range ir.imports {
		result[from] = maps.Clone(entries)
	}
	return result
}
func (m *IRModule) Dependencies() []IRDependency {
	result := slices.Clone(m.dependencies)
	for i := range result {
		result[i].Attributes = maps.Clone(result[i].Attributes)
		result[i].RequiredExports = slices.Clone(result[i].RequiredExports)
	}
	return result
}
func (m *IRModule) Exports() []IRExport { return slices.Clone(m.exports) }

// A frontend program's lazily materialized emit/checker data is protected while
// lowering. All products have their own execution trees and emit contexts.
type analysisBackendState struct {
	mu      sync.Mutex
	checked bool
}

func (a *Analysis) Lower(ctx context.Context) (*ExecutionIR, []Diagnostic, error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("a non-nil context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if a == nil || a.program == nil || a.backend == nil || !a.backend.checked {
		return nil, []Diagnostic{integrationError(1100, "backend requires a successfully checked analysis")}, nil
	}
	a.backend.mu.Lock()
	defer a.backend.mu.Unlock()
	return lowerAnalysis(ctx, a)
}
