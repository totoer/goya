package lsp

import (
	"context"
	"maps"
	"sync"

	"github.com/totoer/goya/internal/tsfrontend/editor"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
)

// A generation owns its immutable inputs and one serialized native checker.
// Retired generations close only after their last active/queued request exits.
type workspaceState struct {
	snapshot  Snapshot
	documents map[string]int32
	native    *editor.Workspace
	gate      chan struct{}
	refs      int // protected by Session.mu
}

func (w *workspaceState) close() {
	if w.native != nil {
		w.native.Close()
	}
}

type workspaceLease struct {
	state     *workspaceState
	ctx       context.Context
	cleanup   context.CancelFunc
	session   *Session
	holdsGate bool
	once      sync.Once
}

func (l *workspaceLease) release() {
	l.once.Do(func() {
		if l.holdsGate {
			l.state.gate <- struct{}{}
		}
		l.cleanup()
		s := l.session
		s.mu.Lock()
		l.state.refs--
		closeNow := s.workspace != l.state && l.state.refs == 0
		s.mu.Unlock()
		if closeNow {
			l.state.close()
		}
		s.active.Done()
	})
}

func (s *Session) acquireWorkspace(ctx context.Context) (*workspaceLease, error) {
	return s.acquireWorkspaceSnapshot(ctx, nil)
}

func (s *Session) acquireWorkspaceSnapshot(ctx context.Context, captured *querySnapshot) (*workspaceLease, error) {
	if ctx == nil {
		return nil, &Error{Code: CodeInvalidOptions, Message: "a non-nil context is required"}
	}
	requestCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	select {
	case <-requestCtx.Done():
		stop()
		cancel()
		return nil, requestCtx.Err()
	case s.requestSlots <- struct{}{}:
	}
	cleanup := func() { <-s.requestSlots; stop(); cancel() }
	s.mu.Lock()
	if err := s.checkLocked(requestCtx); err != nil {
		s.mu.Unlock()
		cleanup()
		return nil, err
	}
	var retired *workspaceState
	generation := s.generation
	if captured != nil {
		generation = captured.snapshot.Generation
	}
	var w *workspaceState
	newState := func(snapshot Snapshot) *workspaceState {
		versions := map[string]int32{}
		if captured != nil {
			versions = maps.Clone(captured.versions)
		} else {
			for name, doc := range s.documents {
				versions[name] = doc.version
			}
		}
		value := &workspaceState{snapshot: snapshot, documents: versions, gate: make(chan struct{}, 1)}
		value.gate <- struct{}{}
		return value
	}
	if s.workspace != nil && s.workspace.snapshot.Generation == generation {
		w = s.workspace
	} else if generation == s.generation {
		retired = s.workspace
		snapshot := s.snapshotLocked()
		if captured != nil {
			snapshot = captured.snapshot
		}
		s.workspace = newState(snapshot)
		w = s.workspace
	} else {
		w = newState(captured.snapshot)
	}
	w.refs++
	s.active.Add(1)
	closeOld := retired != nil && retired.refs == 0
	s.mu.Unlock()
	if closeOld {
		retired.close()
	}
	if tag, ok := locale.Parse(w.snapshot.EditorOptions.Locale); ok {
		requestCtx = locale.WithLocale(requestCtx, tag)
	}
	lease := &workspaceLease{state: w, ctx: requestCtx, cleanup: cleanup, session: s}
	select {
	case <-requestCtx.Done():
		lease.release()
		return nil, requestCtx.Err()
	case <-w.gate:
		lease.holdsGate = true
	}
	if err := requestCtx.Err(); err != nil {
		lease.release()
		return nil, err
	}
	if w.native == nil {
		files := maps.Clone(w.snapshot.Project.Files)
		for name, text := range w.snapshot.Environment.TypeDeclarations() {
			files[name] = text
		}
		workspace, err := editor.NewWorkspaceWithInferredOptions(requestCtx, s.ctx, files, w.snapshot.CompilerOptions, implicitCompilerOptions(w.snapshot.EditorOptions.ImplicitProjectConfiguration), w.snapshot.Project.ConfigFile, w.snapshot.Project.EntryPoints)
		if err != nil {
			lease.release()
			return nil, &Error{Code: CodeInvalidProject, Message: "create editor project", Cause: err}
		}
		workspace.ConfigurePreferences(nativeEditorPreferences(w.snapshot.EditorOptions))
		workspace.OpenDocuments(requestCtx, files, w.documents)
		w.native = workspace
	}
	if err := requestCtx.Err(); err != nil {
		lease.release()
		return nil, err
	}
	return lease, nil
}
