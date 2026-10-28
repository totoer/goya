// Package lsp provides embedded TypeScript editor sessions and their LSP facade.
// The calling application owns sessions and source updates. Creating a session
// never starts a server, binds a Go runtime, or executes registered functions.
package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/totoer/goya"
	"github.com/totoer/goya/internal/tsfrontend"
	"github.com/totoer/goya/lsp/protocol"
)

// SessionOptions supplies the immutable initial project and type environment.
// Files, options, and the resolver result are detached during construction.
// Environment is already frozen by Registry.Freeze and is retained read-only.
type SessionOptions struct {
	FormattingOptionsProvider FormattingOptionsProvider
	Project                   goya.Project
	Environment               *goya.Environment
	CompilerOptions           goya.CompileOptions
	EditorOptions             EditorOptions
	// DocumentURIs maps virtual catalog names to client URIs. Unmapped sources
	// use file URIs; custom schemes and Windows paths need explicit mappings.
	DocumentURIs map[string]protocol.DocumentURI
	// ResultCacheLimits bounds deferred resolve/edit metadata. Nil uses defaults.
	ResultCacheLimits *RetentionLimits
	// EventBuffer bounds coalesced invalidation events (default 32, max 4096).
	EventBuffer int
	// MaxConcurrentRequests bounds active checker generations (default 8).
	MaxConcurrentRequests int
	// MaxSourceBytes bounds each captured catalog including host declarations
	// and overlays (default 16 MiB, excluding immutable compiler libraries).
	MaxSourceBytes int
	// MaxDiagnosticSubscriptions bounds automatic diagnostic streams (default 8).
	MaxDiagnosticSubscriptions int
}

// Snapshot is a detached view suitable for Check/Compile with these exact inputs.
// ProjectSnapshot exports a document's selected configured project; Snapshot
// preserves only the config selection explicitly supplied by the host.
// Mutating its Files or Options cannot change the session or other snapshots.
type Snapshot struct {
	Generation      uint64
	Project         goya.Project
	Environment     *goya.Environment
	CompilerOptions goya.CompileOptions
	EditorOptions   EditorOptions
}

// Session owns editor state. Methods may be called concurrently; Close is
// idempotent. The caller owns the session even when attaching a protocol facade.
type Session struct {
	codeActionEpoch           uint64
	diagnosticsReady          map[string]uint64
	sourceDiagnosticsReady    map[string]uint64
	formattingOptionsProvider FormattingOptionsProvider
	mu                        sync.RWMutex
	ctx                       context.Context
	cancel                    context.CancelFunc
	closed                    bool
	generation                uint64
	base                      map[string]string
	roots                     []string
	moduleRoots               []string
	configFile                string
	environment               *goya.Environment
	options                   goya.CompileOptions
	editorOptions             EditorOptions
	uriMappings               map[string]protocol.DocumentURI
	documents                 map[string]document
	workspace                 *workspaceState
	id                        uint64
	results                   resultStore
	events                    chan Event
	active                    sync.WaitGroup
	closedDone                chan struct{}
	requestSlots              chan struct{}
	maxSourceBytes            int
	sourceProject             goya.Project
	projectRevision           uint64
	diagnosticStreams         map[*DiagnosticStream]struct{}
	maxDiagnosticStreams      int
	background                sync.WaitGroup
}

var nextSessionID atomic.Uint64

type document struct {
	text         string
	version      int32
	savedVersion int32
}

func NewSession(ctx context.Context, options SessionOptions) (*Session, error) {
	if ctx == nil {
		return nil, &Error{Code: CodeInvalidOptions, Message: "a non-nil context is required"}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts, err := cloneOptions(options.CompilerOptions)
	if err != nil {
		return nil, err
	}
	if err := validateImplicitConfiguration(options.EditorOptions.ImplicitProjectConfiguration); err != nil {
		return nil, err
	}
	if err := validateFormatSettings(options.EditorOptions.Format); err != nil {
		return nil, err
	}
	editorOptions := cloneEditorOptions(options.EditorOptions)
	limits := RetentionLimits{MaxEntries: 64, MaxBytes: 4 << 20}
	if options.ResultCacheLimits != nil {
		limits = *options.ResultCacheLimits
	}
	if limits.MaxEntries < 0 || limits.MaxBytes < 0 {
		return nil, &Error{Code: CodeInvalidOptions, Message: "result retention limits must be nonnegative"}
	}
	capacity := options.EventBuffer
	if capacity == 0 {
		capacity = 32
	}
	if capacity < 0 || capacity > 4096 {
		return nil, &Error{Code: CodeInvalidOptions, Message: "event buffer must be between 1 and 4096"}
	}
	workers := options.MaxConcurrentRequests
	if workers == 0 {
		workers = 8
	}
	if workers < 1 || workers > 128 {
		return nil, &Error{Code: CodeInvalidOptions, Message: "concurrent request limit must be between 1 and 128"}
	}
	sourceLimit := options.MaxSourceBytes
	if sourceLimit == 0 {
		sourceLimit = 16 << 20
	}
	if sourceLimit < 0 {
		return nil, &Error{Code: CodeInvalidOptions, Message: "source byte limit must be positive"}
	}
	subscriptions := options.MaxDiagnosticSubscriptions
	if subscriptions == 0 {
		subscriptions = 8
	}
	if subscriptions < 1 || subscriptions > 128 {
		return nil, &Error{Code: CodeInvalidOptions, Message: "diagnostic subscription limit must be between 1 and 128"}
	}
	project := cloneProjectDescriptor(options.Project)
	roots := slices.Clone(project.EntryPoints)
	mappings, err := captureURIMappings(options.DocumentURIs)
	if err != nil {
		return nil, err
	}
	files, err := captureCatalog(ctx, project)
	if err != nil {
		return nil, err
	}
	if err := validateDocumentURIs(files, options.Environment, mappings); err != nil {
		return nil, err
	}
	if err := validateSourceBudget(files, options.Environment, sourceLimit); err != nil {
		return nil, err
	}
	owned, cancel := context.WithCancel(ctx)
	configFile := options.Project.ConfigFile
	if configFile != "" {
		configFile, err = tsfrontend.CanonicalSourceName(configFile)
		if err != nil {
			cancel()
			return nil, &Error{Code: CodeInvalidProject, Message: "invalid ConfigFile", Cause: err}
		}
	}
	return &Session{formattingOptionsProvider: options.FormattingOptionsProvider, ctx: owned, cancel: cancel, generation: 1, base: files, roots: roots, moduleRoots: slices.Clone(project.ModuleRoots), configFile: configFile, environment: options.Environment, options: opts, editorOptions: editorOptions, uriMappings: mappings, documents: map[string]document{}, id: nextSessionID.Add(1), results: newResultStore(limits), events: make(chan Event, capacity), closedDone: make(chan struct{}), requestSlots: make(chan struct{}, workers), maxSourceBytes: sourceLimit, sourceProject: project, projectRevision: 1, maxDiagnosticStreams: subscriptions}, nil
}

func (s *Session) Snapshot(ctx context.Context) (Snapshot, error) {
	if ctx == nil {
		return Snapshot{}, &Error{Code: CodeInvalidOptions, Message: "a non-nil context is required"}
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return Snapshot{}, ErrClosed
	}
	if err := s.ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	return s.snapshotLocked(), nil
}

func (s *Session) snapshotLocked() Snapshot {
	opts := cloneCapturedOptions(s.options)
	files := maps.Clone(s.base)
	for name, doc := range s.documents {
		files[name] = doc.text
	}
	return Snapshot{Generation: s.generation, Project: goya.Project{ConfigFile: s.configFile, Files: files, EntryPoints: slices.Clone(s.roots), ModuleRoots: slices.Clone(s.moduleRoots)}, Environment: s.environment, CompilerOptions: opts, EditorOptions: cloneEditorOptions(s.editorOptions)}
}

func (s *Session) Close() error {
	// Cancel before acquiring state so a running checker can release its lease.
	s.cancel()
	s.mu.Lock()
	if s.closed {
		done := s.closedDone
		s.mu.Unlock()
		<-done
		return nil
	}
	s.closed = true
	workspace := s.workspace
	s.workspace = nil
	closeNow := workspace != nil && workspace.refs == 0
	close(s.events)
	s.base = nil
	s.roots = nil
	s.moduleRoots = nil
	s.options = nil
	s.editorOptions = EditorOptions{}
	s.environment = nil
	s.sourceProject = goya.Project{}
	s.documents = nil
	s.uriMappings = nil
	s.results.clear()
	s.mu.Unlock()
	if closeNow {
		workspace.close()
	}
	s.active.Wait()
	s.background.Wait()
	close(s.closedDone)
	return nil
}

// ProjectInfo describes the configured or inferred project selected for a file.
// Configuration and package metadata are read only from the host catalog.
type ProjectInfo struct {
	Generation  uint64
	Configured  bool
	ConfigFile  string
	Files       []string
	Diagnostics []goya.Diagnostic
}

func (s *Session) ProjectInfo(ctx context.Context, name string) (ProjectInfo, error) {
	path, err := documentName(name)
	if err != nil {
		return ProjectInfo{}, err
	}
	lease, err := s.acquireWorkspace(ctx)
	if err != nil {
		return ProjectInfo{}, err
	}
	defer lease.release()
	info, err := lease.state.native.ProjectInfo(lease.ctx, path)
	if err != nil {
		return ProjectInfo{}, &Error{Code: CodeInvalidProject, Message: "select document project", Cause: err}
	}
	if err := lease.ctx.Err(); err != nil {
		return ProjectInfo{}, err
	}
	return ProjectInfo{Generation: lease.state.snapshot.Generation, Configured: info.Configured, ConfigFile: info.ConfigFile, Files: info.Files, Diagnostics: info.Diagnostics}, nil
}

// UpdateProject atomically replaces the base catalog. The resolver is captured
// once for this explicit update; open document overlays remain authoritative.
func (s *Session) UpdateProject(ctx context.Context, project goya.Project) error {
	return s.updateProject(ctx, project, nil)
}

func (s *Session) updateProject(ctx context.Context, project goya.Project, expectedRevision *uint64) error {
	if err := s.checkContext(ctx); err != nil {
		return err
	}
	project = cloneProjectDescriptor(project)
	roots := slices.Clone(project.EntryPoints)
	configFile := project.ConfigFile
	if configFile != "" {
		var err error
		configFile, err = tsfrontend.CanonicalSourceName(configFile)
		if err != nil {
			return &Error{Code: CodeInvalidProject, Message: "invalid ConfigFile", Cause: err}
		}
	}
	files, err := captureCatalog(ctx, project)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLocked(ctx); err != nil {
		return err
	}
	if expectedRevision != nil && s.projectRevision != *expectedRevision {
		return ErrContentModified
	}
	merged := maps.Clone(files)
	for name, doc := range s.documents {
		merged[name] = doc.text
	}
	if err := validateCatalog(merged); err != nil {
		return err
	}
	if err := validateDocumentURIs(merged, s.environment, s.uriMappings); err != nil {
		return err
	}
	if err := validateSourceBudget(merged, s.environment, s.maxSourceBytes); err != nil {
		return err
	}
	if err := validateSourceBudget(files, s.environment, s.maxSourceBytes); err != nil {
		return err
	}
	s.base = files
	s.roots = roots
	s.moduleRoots = slices.Clone(project.ModuleRoots)
	s.configFile = configFile
	s.sourceProject = project
	s.projectRevision++
	s.advanceGenerationLocked()
	return nil
}

// RefreshProject explicitly captures the most recently supplied resolver again.
// It never reads OS files. A concurrent base replacement supersedes the refresh.
func (s *Session) RefreshProject(ctx context.Context) error {
	s.mu.RLock()
	if err := s.checkLocked(ctx); err != nil {
		s.mu.RUnlock()
		return err
	}
	project := cloneProjectDescriptor(s.sourceProject)
	revision := s.projectRevision
	s.mu.RUnlock()
	if project.Resolver == nil {
		return &Error{Code: CodeInvalidProject, Message: "file events require a host resolver; supply the new catalog with UpdateProject"}
	}
	return s.updateProject(ctx, project, &revision)
}

func cloneProjectDescriptor(project goya.Project) goya.Project {
	project.Files = maps.Clone(project.Files)
	project.EntryPoints = slices.Clone(project.EntryPoints)
	project.ModuleRoots = slices.Clone(project.ModuleRoots)
	return project
}

// ProjectSnapshot selects the configured project for a document and exports
// its configuration context together with the current host-owned source catalog.
func (s *Session) ProjectSnapshot(ctx context.Context, name string) (Snapshot, error) {
	path, err := documentName(name)
	if err != nil {
		return Snapshot{}, err
	}
	lease, err := s.acquireWorkspace(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer lease.release()
	info, err := lease.state.native.ProjectInfo(lease.ctx, path)
	if err != nil {
		return Snapshot{}, &Error{Code: CodeInvalidProject, Message: "select configured snapshot", Cause: err}
	}
	snapshot := lease.state.snapshot
	var roots []string
	if snapshot.Project.ConfigFile != "" && info.ConfigFile == snapshot.Project.ConfigFile {
		roots = slices.Clone(snapshot.Project.EntryPoints)
	} else if !info.Configured {
		roots = []string{path}
	}
	if err := lease.ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	options := cloneCapturedOptions(snapshot.CompilerOptions)
	if !info.Configured && snapshot.EditorOptions.ImplicitProjectConfiguration != nil {
		effective := implicitCompilerOptions(snapshot.EditorOptions.ImplicitProjectConfiguration)
		for key, value := range options {
			effective[key] = value
		}
		options = effective
	}
	return Snapshot{Generation: snapshot.Generation, Project: goya.Project{ConfigFile: info.ConfigFile, EntryPoints: roots, ModuleRoots: slices.Clone(snapshot.Project.ModuleRoots), Files: maps.Clone(snapshot.Project.Files)}, Environment: snapshot.Environment, CompilerOptions: options, EditorOptions: cloneEditorOptions(snapshot.EditorOptions)}, nil
}

// UpdateEnvironment replaces frozen type/binding contracts for future requests.
func (s *Session) UpdateEnvironment(ctx context.Context, environment *goya.Environment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLocked(ctx); err != nil {
		return err
	}
	files := maps.Clone(s.base)
	for name, doc := range s.documents {
		files[name] = doc.text
	}
	if err := validateDocumentURIs(files, environment, s.uriMappings); err != nil {
		return err
	}
	if err := validateSourceBudget(files, environment, s.maxSourceBytes); err != nil {
		return err
	}
	if err := validateSourceBudget(s.base, environment, s.maxSourceBytes); err != nil {
		return err
	}
	s.environment = environment
	s.advanceGenerationLocked()
	return nil
}

func (s *Session) UpdateCompilerOptions(ctx context.Context, options goya.CompileOptions) error {
	copy, err := cloneOptions(options)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLocked(ctx); err != nil {
		return err
	}
	s.options = copy
	s.advanceGenerationLocked()
	return nil
}

// OpenDocument supplies an unsaved overlay using the project's virtual name.
func (s *Session) OpenDocument(ctx context.Context, name string, version int32, text string) error {
	path, err := documentName(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLocked(ctx); err != nil {
		return err
	}
	if _, ok := s.documents[path]; ok {
		return &Error{Code: CodeInvalidDocument, Message: "document already open: " + path}
	}
	merged := maps.Clone(s.base)
	for n, doc := range s.documents {
		merged[n] = doc.text
	}
	merged[path] = text
	if err := validateCatalog(merged); err != nil {
		return err
	}
	if err := validateDocumentURIs(merged, s.environment, s.uriMappings); err != nil {
		return err
	}
	if err := validateSourceBudget(merged, s.environment, s.maxSourceBytes); err != nil {
		return err
	}
	s.documents[path] = document{text: text, version: version, savedVersion: version}
	s.advanceGenerationLocked()
	return nil
}

// ChangeDocument applies an entire incremental notification atomically. Each
// range is relative to the text produced by the preceding change.
func (s *Session) ChangeDocument(ctx context.Context, name string, version int32, changes []protocol.ContentChange) error {
	path, err := documentName(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLocked(ctx); err != nil {
		return err
	}
	doc, ok := s.documents[path]
	if !ok {
		return &Error{Code: CodeInvalidDocument, Message: "document is not open: " + path}
	}
	if version <= doc.version {
		return &Error{Code: CodeContentModified, Message: "document version must increase: " + path}
	}
	text := doc.text
	for _, change := range changes {
		if change.Range == nil {
			text = change.Text
			continue
		}
		start, err := positionOffset(text, change.Range.Start)
		if err != nil {
			return err
		}
		end, err := positionOffset(text, change.Range.End)
		if err != nil {
			return err
		}
		if end < start {
			return &Error{Code: CodeInvalidDocument, Message: "change range ends before its start"}
		}
		text = text[:start] + change.Text + text[end:]
	}
	files := maps.Clone(s.base)
	for name, doc := range s.documents {
		files[name] = doc.text
	}
	files[path] = text
	if err := validateSourceBudget(files, s.environment, s.maxSourceBytes); err != nil {
		return err
	}
	doc.text = text
	doc.version = version
	s.documents[path] = doc
	s.advanceGenerationLocked()
	return nil
}

// SaveDocument records the save event without writing a file or replacing the
// host's base catalog. Supply accepted saved content through UpdateProject.
func (s *Session) SaveDocument(ctx context.Context, name string) error {
	path, err := documentName(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLocked(ctx); err != nil {
		return err
	}
	doc, ok := s.documents[path]
	if !ok {
		return &Error{Code: CodeInvalidDocument, Message: "document is not open: " + path}
	}
	doc.savedVersion = doc.version
	s.documents[path] = doc
	return nil
}

func (s *Session) CloseDocument(ctx context.Context, name string) error {
	path, err := documentName(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLocked(ctx); err != nil {
		return err
	}
	if _, ok := s.documents[path]; !ok {
		return &Error{Code: CodeInvalidDocument, Message: "document is not open: " + path}
	}
	delete(s.documents, path)
	s.advanceGenerationLocked()
	return nil
}

func documentName(name string) (string, error) {
	path, err := tsfrontend.CanonicalSourceName(name)
	if err != nil || path == "/" {
		return "", &Error{Code: CodeInvalidDocument, Message: "invalid virtual document name: " + name, Cause: err}
	}
	return path, nil
}

func (s *Session) checkContext(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.checkLocked(ctx)
}
func (s *Session) checkLocked(ctx context.Context) error {
	if ctx == nil {
		return &Error{Code: CodeInvalidOptions, Message: "a non-nil context is required"}
	}
	if s.closed {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.ctx.Err()
}

func cloneOptions(options goya.CompileOptions) (goya.CompileOptions, error) {
	data, err := json.Marshal(options)
	if err != nil {
		return nil, &Error{Code: CodeInvalidOptions, Message: "compiler options must be JSON values", Cause: err}
	}
	var out goya.CompileOptions
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, &Error{Code: CodeInvalidOptions, Message: "invalid compiler options", Cause: err}
	}
	return out, nil
}

func cloneCapturedOptions(options goya.CompileOptions) goya.CompileOptions {
	if options == nil {
		return nil
	}
	result := make(goya.CompileOptions, len(options))
	for key, value := range options {
		result[key] = cloneJSONValue(value)
	}
	return result
}
func cloneJSONValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, item := range v {
			result[key] = cloneJSONValue(item)
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = cloneJSONValue(item)
		}
		return result
	default:
		return value
	}
}

func captureCatalog(ctx context.Context, project goya.Project) (map[string]string, error) {
	files := maps.Clone(project.Files)
	if files == nil {
		files = map[string]string{}
	}
	if project.Resolver != nil {
		resolved, err := project.Resolver.Snapshot(ctx)
		if err != nil {
			return nil, &Error{Code: CodeInvalidProject, Message: "resolve project", Cause: err}
		}
		for name, source := range resolved {
			if _, ok := files[name]; ok {
				return nil, &Error{Code: CodeInvalidProject, Message: "resolver and explicit files overlap: " + name}
			}
			files[name] = source
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	canonical := make(map[string]string, len(files))
	for name, source := range files {
		path, err := tsfrontend.CanonicalSourceName(name)
		if err != nil || path == "/" {
			return nil, &Error{Code: CodeInvalidProject, Message: fmt.Sprintf("invalid source name %q", name), Cause: err}
		}
		if _, ok := canonical[path]; ok {
			return nil, &Error{Code: CodeInvalidProject, Message: "duplicate canonical source: " + path}
		}
		canonical[path] = source
	}
	if err := validateCatalog(canonical); err != nil {
		return nil, err
	}
	return canonical, nil
}

func validateCatalog(canonical map[string]string) error {
	for path := range canonical {
		for parent := strings.LastIndex(path, "/"); parent > 0; parent = strings.LastIndex(path[:parent], "/") {
			if _, ok := canonical[path[:parent]]; ok {
				return &Error{Code: CodeInvalidProject, Message: "virtual path is both file and directory: " + path[:parent]}
			}
		}
	}
	return nil
}

func validateSourceBudget(files map[string]string, env *goya.Environment, limit int) error {
	remaining := limit
	for name, text := range files {
		remaining -= len(name) + len(text)
		if remaining < 0 {
			return &Error{Code: CodeInvalidProject, Message: "virtual source catalog exceeds MaxSourceBytes"}
		}
	}
	for name, text := range env.TypeDeclarations() {
		remaining -= len(name) + len(text)
		if remaining < 0 {
			return &Error{Code: CodeInvalidProject, Message: "host declarations exceed MaxSourceBytes"}
		}
	}
	return nil
}
