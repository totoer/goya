package goya

import (
	stdcontext "context"
	"fmt"
	execir "github.com/totoer/goya/ast"

	"github.com/totoer/goya/internal/tsfrontend"
	"github.com/totoer/goya/unistring"
)

type typeScriptProgram struct {
	analysisKey string
	version     string
	profile     string
	sources     []tsfrontend.SourceIdentity
	globals     map[string]string
	units       map[string]*compiledModule
	roots       []string
	scriptEntry bool
	hostModules map[string]map[string]string
	imports     map[string]map[string]tsfrontend.PreparedImport
	importRules map[string][]tsfrontend.PreparedImportRule
}

type compiledModule struct {
	id           string
	code         *Program
	module       bool
	async        bool
	commonJS     bool
	fingerprint  string
	json         bool
	dependencies []tsfrontend.IRDependency
	exports      []compiledModuleExport
	globals      []string
}
type compiledModuleExport struct {
	name, imported  string
	localName       string
	from            int
	namespace, star bool
}

// Compile checks the captured TypeScript project and lowers it directly
// into VM operations. Any frontend or backend error returns no Program. This is
// the developing TypeScript path; its remaining language/module support is
// tracked by the compatibility matrix.
func Compile(ctx stdcontext.Context, project Project, environment *Environment, options CompileOptions) (*Program, []Diagnostic, error) {
	var types *tsfrontend.TypeEnvironment
	if environment != nil {
		types = &tsfrontend.TypeEnvironment{Declarations: environment.declarations, SchemaID: environment.schemaDigest}
	}
	analysis, diagnostics, err := tsfrontend.Analyze(ctx, project, types, options)
	if err != nil || analysis == nil {
		return nil, diagnostics, err
	}
	lowered, backendDiagnostics, err := analysis.Lower(ctx)
	diagnostics = append(diagnostics, backendDiagnostics...)
	if err != nil || lowered == nil {
		return nil, diagnostics, err
	}
	program, backendDiagnostics, err := compileCheckedIR(lowered, environment)
	diagnostics = append(diagnostics, backendDiagnostics...)
	return program, diagnostics, err
}

// CompileProject is an alias for Compile.
func CompileProject(ctx stdcontext.Context, project Project, environment *Environment, options CompileOptions) (*Program, []Diagnostic, error) {
	return Compile(ctx, project, environment, options)
}

func compileCheckedIR(lowered *tsfrontend.ExecutionIR, environment *Environment, forceRoots ...bool) (*Program, []Diagnostic, error) {
	if lowered == nil {
		return nil, nil, fmt.Errorf("backend requires checked execution IR")
	}
	var diagnostics []Diagnostic
	info := &typeScriptProgram{analysisKey: lowered.Key(), version: TypeScriptVersion(), profile: lowered.Profile(), sources: lowered.Sources(), globals: make(map[string]string), units: make(map[string]*compiledModule), hostModules: make(map[string]map[string]string), imports: lowered.PreparedImports(), importRules: lowered.PreparedImportRules()}
	var modules []*tsfrontend.IRModule
	asModule := make(map[string]bool)
	if len(forceRoots) > 0 && forceRoots[0] {
		for _, id := range lowered.EntryPoints() {
			asModule[id] = true
		}
	}
	err := lowered.Visit(func(module *tsfrontend.IRModule) error {
		modules = append(modules, module)
		if module.IsExternalModule() {
			asModule[module.ID()] = true
		}
		for _, d := range module.Dependencies() {
			if !d.Host {
				asModule[d.ID] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	for _, module := range modules {
		for _, name := range module.RequiredStandardGlobals() {
			if name != "" && getGlobalObjectTemplate().props[unistring.NewFromString(name)] == nil {
				diagnostics = append(diagnostics, Diagnostic{Namespace: "goya", Code: 1104, Severity: "error", Message: fmt.Sprintf("standard global %q is not provided by this runtime profile", name), File: module.ID()})
			}
		}
		tree := module.Tree()
		external := asModule[module.ID()]
		if external {
			tree = module.FactoryTree()
		}
		code, err := compileAST(tree, module.Strict() || external, true, info)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Namespace: "goya", Code: 1102, Severity: "error", Message: err.Error(), File: module.ID()})
			continue
		}
		unit := &compiledModule{id: module.ID(), code: code, module: external, async: module.HasTopLevelAwait(), commonJS: module.IsCommonJS(), json: module.IsJSON(), fingerprint: moduleFingerprint(module, info.imports[module.ID()], info.importRules[module.ID()]), dependencies: module.Dependencies(), globals: module.RequiredGlobals()}
		for _, export := range module.Exports() {
			localName := ""
			if identifier, ok := export.Local.(*execir.Identifier); ok {
				localName = string(identifier.Name)
			}
			unit.exports = append(unit.exports, compiledModuleExport{name: export.Name, imported: export.Imported, from: export.From, namespace: export.Namespace, star: export.Star, localName: localName})
		}
		info.units[unit.id] = unit
	}
	if err != nil || HasTypeErrors(diagnostics) {
		return nil, diagnostics, err
	}
	for _, id := range lowered.EntryPoints() {
		if info.units[id] != nil {
			info.roots = append(info.roots, id)
		}
	}
	visited := make(map[string]bool)
	var require func(string)
	require = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		unit := info.units[id]
		if unit == nil {
			return
		}
		for _, name := range unit.globals {
			schema := ""
			if environment != nil {
				schema = environment.globals[name].Schema
			}
			info.globals[name] = schema
		}
		for _, dependency := range unit.dependencies {
			if !dependency.Host {
				require(dependency.ID)
				continue
			}
			required := info.hostModules[dependency.Specifier]
			if required == nil {
				required = make(map[string]string)
				info.hostModules[dependency.Specifier] = required
			}
			var descriptors map[string]hostDescriptor
			if environment != nil {
				descriptors = environment.modules[dependency.Specifier]
			}
			for _, name := range dependency.RequiredExports {
				required[name] = descriptors[name].Schema
			}
			if dependency.Namespace {
				if descriptors == nil {
					required[""] = ""
				}
				for name, d := range descriptors {
					required[name] = d.Schema
				}
			}
		}
	}
	for id := range info.units {
		require(id)
	}
	for _, specifier := range lowered.HostModuleRoots() {
		required := info.hostModules[specifier]
		if required == nil {
			required = make(map[string]string)
			info.hostModules[specifier] = required
		}
		var descriptors map[string]hostDescriptor
		if environment != nil {
			descriptors = environment.modules[specifier]
		}
		if descriptors == nil {
			required[""] = ""
		}
		for name, descriptor := range descriptors {
			required[name] = descriptor.Schema
		}
	}
	program := &Program{}
	if len(info.roots) == 1 {
		unit := info.units[info.roots[0]]
		if !unit.module && len(unit.dependencies) == 0 {
			program = unit.code
			info.scriptEntry = true
		}
	}
	program.typescript = info
	return program, diagnostics, nil
}

// BindingCompatibilityError reports a failed startup check before instructions
// or user initializers execute. A compatible schema may use different Go values.
type BindingCompatibilityError struct{ Name, Reason string }

func (e *BindingCompatibilityError) Error() string {
	return fmt.Sprintf("incompatible host binding %q: %s", e.Name, e.Reason)
}

func (r *Runtime) preflightTypeScript(p *Program) error {
	if p.typescript.version != TypeScriptVersion() {
		return &BindingCompatibilityError{Reason: "frontend version differs"}
	}
	for id, unit := range p.typescript.units {
		// A script runs directly and never reuses a canonical module instance.
		// Its prepared plugin units still participate in this preflight.
		if p.typescript.scriptEntry && id == p.typescript.roots[0] {
			continue
		}
		if existing := r.moduleInstances[id]; existing != nil {
			if existing.interruption != nil {
				return existing.interruption
			}
			if existing.code != nil && existing.code.fingerprint != unit.fingerprint {
				return &BindingCompatibilityError{Name: id, Reason: "loaded module has different checked code, compiler profile or import permissions"}
			}
		}
	}
	for _, specifier := range sortedKeys(p.typescript.hostModules) {
		if r.hostEnvironment == nil {
			return &BindingCompatibilityError{specifier, "missing host module implementation"}
		}
		actual, ok := r.hostEnvironment.modules[specifier]
		if !ok {
			return &BindingCompatibilityError{specifier, "missing host module implementation"}
		}
		for _, name := range sortedKeys(p.typescript.hostModules[specifier]) {
			schema := p.typescript.hostModules[specifier][name]
			if schema == "" || actual[name].Schema != schema {
				return &BindingCompatibilityError{specifier + ":" + name, "missing or incompatible module export"}
			}
		}
	}
	for _, name := range sortedKeys(p.typescript.globals) {
		if _, exists := r.global.stash.names[unistring.NewFromString(name)]; exists {
			return &BindingCompatibilityError{name, "global is shadowed by an existing lexical binding"}
		}
		schema := p.typescript.globals[name]
		if schema == "" || r.hostEnvironment == nil {
			return &BindingCompatibilityError{name, "missing registered implementation"}
		}
		actual, ok := r.hostEnvironment.globals[name]
		if !ok {
			return &BindingCompatibilityError{name, "missing registered implementation"}
		}
		if actual.Schema != schema {
			return &BindingCompatibilityError{name, "schema or conversion policy differs"}
		}
		property := r.GlobalObject().self.getOwnPropStr(unistring.NewFromString(name))
		if descriptor, ok := property.(*valueProperty); ok {
			if descriptor.accessor || descriptor.writable || descriptor.configurable {
				return &BindingCompatibilityError{name, "global property is no longer frozen"}
			}
			property = descriptor.value
		}
		if property == nil || !property.SameAs(r.hostGlobals[name]) {
			return &BindingCompatibilityError{name, "runtime binding was replaced"}
		}
	}
	return nil
}
