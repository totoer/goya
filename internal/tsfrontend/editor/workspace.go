// Package editor adapts the pinned native project system to host-owned sources.
// Native compiler and project objects never enter the public editor API.
package editor

import (
	"context"
	"net/url"
	"slices"
	"sync"

	"github.com/totoer/goya/internal/tsfrontend"
	"github.com/totoer/goya/internal/tsfrontend/upstream/bundled"
	"github.com/totoer/goya/internal/tsfrontend/upstream/collections"
	"github.com/totoer/goya/internal/tsfrontend/upstream/compiler"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsutil"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/internal/tsfrontend/upstream/project"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tsoptions"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
)

type Workspace struct {
	session    *project.Session
	normalizer *tsfrontend.ProjectNormalizer
	configFile string
	cancel     context.CancelFunc
	closeOnce  sync.Once
}

type ProjectInfo struct {
	Configured  bool
	ConfigFile  string
	Files       []string
	Diagnostics []tsfrontend.Diagnostic
}

func NewWorkspace(ctx context.Context, files map[string]string, options tsfrontend.Options, configFile string, entryPoints []string) (*Workspace, error) {
	return NewWorkspaceWithLifetime(ctx, ctx, files, options, configFile, entryPoints)
}

func NewWorkspaceWithLifetime(ctx, lifetime context.Context, files map[string]string, options tsfrontend.Options, configFile string, entryPoints []string) (*Workspace, error) {
	return NewWorkspaceWithInferredOptions(ctx, lifetime, files, options, nil, configFile, entryPoints)
}

func NewWorkspaceWithInferredOptions(ctx, lifetime context.Context, files map[string]string, options, inferred tsfrontend.Options, configFile string, entryPoints []string) (*Workspace, error) {
	fs, err := tsfrontend.NewEditorSourceFS(files)
	if err != nil {
		return nil, err
	}
	var roots []string
	for name := range files {
		if tspath.IsDeclarationFileName(name) {
			roots = append(roots, name)
		}
	}
	slices.Sort(roots)
	normalizer, ds, err := tsfrontend.NewProjectNormalizer(fs, options, roots)
	if err != nil {
		return nil, err
	}
	if len(ds) > 0 {
		return nil, &ConfigurationError{Diagnostics: ds}
	}
	if err := normalizer.SelectRoots(configFile, entryPoints); err != nil {
		return nil, err
	}
	normalize := normalizer.Apply
	if inferred != nil {
		implicit, ds, err := tsfrontend.NewProjectNormalizer(fs, inferred, nil)
		if err != nil {
			return nil, err
		}
		if len(ds) > 0 {
			return nil, &ConfigurationError{Diagnostics: ds}
		}
		normalize = func(config *tsoptions.ParsedCommandLine) {
			if config != nil && config.ConfigName() == "" {
				implicit.Apply(config)
			}
			normalizer.Apply(config)
		}
	}
	ctx = lsproto.WithClientCapabilities(ctx, &lsproto.ResolvedClientCapabilities{})
	background, cancel := context.WithCancel(lifetime)
	s := project.NewSession(&project.SessionInit{
		BackgroundCtx: background, FS: fs, Client: client{},
		Options: &project.SessionOptions{Embedded: true, CurrentDirectory: "/", DefaultLibraryPath: bundled.LibPath(), PositionEncoding: lsproto.PositionEncodingKindUTF16, NormalizeCommandLine: normalize},
	})
	preferences := lsutil.NewDefaultUserPreferences()
	// TLS preserves the compiler category for noUnused/style diagnostics.
	preferences.ReportStyleChecksAsWarnings = core.TSFalse
	preferences.AllowTextChangesInNewFiles = core.TSTrue
	s.InitializeWithUserConfig(preferences)
	s.DidChangeCompilerOptionsForInferredProjects(ctx, &core.CompilerOptions{
		Strict: core.TSTrue, Target: core.ScriptTargetES2025, Module: core.ModuleKindESNext, ModuleResolution: core.ModuleResolutionKindBundler, Lib: []string{"lib.es2025.d.ts"}, Types: []string{}, NoEmit: core.TSTrue,
	})
	w := &Workspace{session: s, normalizer: normalizer, configFile: configFile, cancel: cancel}
	if configFile != "" {
		projects := collections.NewSetWithSizeHint[string](1)
		projects.Add(configFile)
		snapshot, err := s.APIUpdate(ctx, project.FileChangeSummary{}, &project.APISnapshotRequest{OpenProjects: projects})
		if snapshot != nil {
			snapshot.Deref(s)
		}
		if err != nil {
			w.Close()
			return nil, err
		}
	}
	return w, nil
}

type ConfigurationError struct{ Diagnostics []tsfrontend.Diagnostic }

func (e *ConfigurationError) Error() string {
	if len(e.Diagnostics) > 0 {
		return e.Diagnostics[0].Message
	}
	return "invalid editor configuration"
}

func (w *Workspace) Close() {
	w.closeOnce.Do(func() { w.cancel(); w.session.Close(); w.session.WaitForBackgroundTasks() })
}

func (w *Workspace) ProjectInfo(ctx context.Context, name string) (ProjectInfo, error) {
	p, err := w.projectForFile(ctx, name)
	if err != nil {
		return ProjectInfo{}, err
	}
	info := ProjectInfo{Configured: p.Kind == project.KindConfigured}
	if info.Configured {
		info.ConfigFile = p.ConfigFileName()
	}
	for _, file := range p.GetProgram().GetSourceFiles() {
		if !bundled.IsBundled(file.FileName()) {
			info.Files = append(info.Files, file.FileName())
		}
	}
	slices.Sort(info.Files)
	info.Diagnostics = tsfrontend.EditorDiagnostics(p.GetProjectDiagnostics(ctx))
	info.Diagnostics = append(info.Diagnostics, w.normalizer.Diagnostics()...)
	return info, nil
}

func (w *Workspace) projectForFile(ctx context.Context, name string) (*project.Project, error) {
	ctx = lsproto.WithClientCapabilities(ctx, &lsproto.ResolvedClientCapabilities{})
	uri := lsproto.DocumentUri((&url.URL{Scheme: "file", Path: name}).String())
	// Discover the file's own configured project even if a previously loaded
	// importing project already contains it through a project-reference redirect.
	if _, err := w.session.GetProjectsForFile(ctx, uri); err != nil {
		return nil, err
	}
	if err := w.ensureReferencedProject(ctx, name); err != nil {
		return nil, err
	}
	if _, err := w.session.GetLanguageService(ctx, uri); err != nil {
		return nil, err
	}
	p, _, _, err := w.session.GetLanguageServiceAndProjectsForFile(ctx, uri)
	if err != nil {
		return nil, err
	}
	return w.projectForSnapshot(w.session.Snapshot(), p, name), nil
}

func (w *Workspace) referenceConfig(snapshot *project.Snapshot, name string) string {
	if w.configFile == "" {
		return ""
	}
	selected := snapshot.ProjectCollection.ConfiguredProject(tspath.Path(w.configFile))
	if selected == nil || selected.GetProgram() == nil {
		return ""
	}
	file := selected.GetProgram().GetSourceFile(name)
	if file == nil {
		return ""
	}
	if config := selected.GetProgram().GetRedirectForResolution(file); config != nil && config.ConfigName() != w.configFile {
		return config.ConfigName()
	}
	return ""
}

func (w *Workspace) ensureReferencedProject(ctx context.Context, name string) error {
	snapshot := w.session.Snapshot()
	config := w.referenceConfig(snapshot, name)
	if config == "" || snapshot.ProjectCollection.ConfiguredProject(tspath.Path(config)) != nil {
		return nil
	}
	projects := collections.NewSetWithSizeHint[string](1)
	projects.Add(config)
	updated, err := w.session.APIUpdate(ctx, project.FileChangeSummary{}, &project.APISnapshotRequest{OpenProjects: projects})
	if updated != nil {
		updated.Deref(w.session)
	}
	return err
}

func (w *Workspace) projectForSnapshot(snapshot *project.Snapshot, fallback *project.Project, name string) *project.Project {
	if w.configFile == "" {
		return fallback
	}
	if config := w.referenceConfig(snapshot, name); config != "" {
		if referenced := snapshot.ProjectCollection.ConfiguredProject(tspath.Path(config)); referenced != nil && referenced.GetProgram() != nil {
			return referenced
		}
	}
	selected := snapshot.ProjectCollection.ConfiguredProject(tspath.Path(w.configFile))
	if selected != nil && selected.GetProgram() != nil && selected.GetProgram().GetSourceFile(name) != nil {
		return selected
	}
	return fallback
}

// CheckDiagnostics reads the editor program without manufacturing a checked
// Analysis. It also allows adapters to compare compiler inputs before filters.
func (w *Workspace) CheckDiagnostics(ctx context.Context, name string) ([]tsfrontend.Diagnostic, error) {
	p, err := w.projectForFile(ctx, name)
	if err != nil {
		return nil, err
	}
	program := p.GetProgram()
	ds := compiler.GetDiagnosticsOfAnyProgram(ctx, program, nil, false, program.GetBindDiagnostics, program.GetSemanticDiagnostics)
	result := tsfrontend.EditorDiagnostics(ds)
	result = append(result, tsfrontend.ExecutionProfileDiagnostics(ctx, program, "")...)
	result = append(result, w.normalizer.Diagnostics()...)
	return result, ctx.Err()
}

func (w *Workspace) ExecutionProfileDiagnostics(ctx context.Context, name string) ([]tsfrontend.Diagnostic, error) {
	p, err := w.projectForFile(ctx, name)
	if err != nil {
		return nil, err
	}
	return tsfrontend.ExecutionProfileDiagnostics(ctx, p.GetProgram(), name), ctx.Err()
}

// Embedded sessions neither watch OS paths nor publish background diagnostics.
// Public adapters deliver host events and request-scoped diagnostic results.
type client struct{}

func (client) WatchFiles(context.Context, project.WatcherID, []*lsproto.FileSystemWatcher) error {
	return nil
}
func (client) UnwatchFiles(context.Context, project.WatcherID) error { return nil }
func (client) RefreshDiagnostics(context.Context) error              { return nil }
func (client) PublishDiagnostics(context.Context, *lsproto.PublishDiagnosticsParams) error {
	return nil
}
func (client) RefreshInlayHints(context.Context) error                     { return nil }
func (client) RefreshCodeLens(context.Context) error                       { return nil }
func (client) ProgressStart(*diagnostics.Message, ...any)                  {}
func (client) ProgressFinish(*diagnostics.Message, ...any)                 {}
func (client) SendTelemetry(context.Context, lsproto.TelemetryEvent) error { return nil }
func (client) IsActive() bool                                              { return true }
