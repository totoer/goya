package goya

// ProgramManifest describes the immutable compilation profile and the exact
// registered contracts required before execution. Values and Go addresses are
// deliberately absent. Each call returns detached slices.
type ProgramManifest struct {
	TypeScriptVersion string
	ProfileID         string
	AnalysisID        string
	EntryPoints       []string
	Sources           []ProgramSource
	HostBindings      []RequiredHostBinding
}
type ProgramSource struct{ ID, SHA256 string }
type RequiredHostBinding struct{ Module, Name, SchemaID string }

func (p *Program) Manifest() ProgramManifest {
	if p == nil || p.typescript == nil {
		return ProgramManifest{}
	}
	info := p.typescript
	m := ProgramManifest{TypeScriptVersion: info.version, ProfileID: info.profile, AnalysisID: info.analysisKey, EntryPoints: append([]string(nil), info.roots...)}
	for _, s := range info.sources {
		m.Sources = append(m.Sources, ProgramSource{s.ID, s.SHA256})
	}
	for _, name := range sortedKeys(info.globals) {
		m.HostBindings = append(m.HostBindings, RequiredHostBinding{Name: name, SchemaID: info.globals[name]})
	}
	for _, module := range sortedKeys(info.hostModules) {
		for _, name := range sortedKeys(info.hostModules[module]) {
			m.HostBindings = append(m.HostBindings, RequiredHostBinding{Module: module, Name: name, SchemaID: info.hostModules[module][name]})
		}
	}
	return m
}
