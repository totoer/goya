package lsp

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/totoer/goya/lsp/protocol"
)

type DiagnosticEventKind string

const (
	DiagnosticEventResult   DiagnosticEventKind = "diagnostics"
	DiagnosticEventClear    DiagnosticEventKind = "clear"
	DiagnosticEventProgress DiagnosticEventKind = "progress"
	DiagnosticEventError    DiagnosticEventKind = "error"
)

type DiagnosticProgress struct {
	Kind      string
	Total     int
	Cancelled bool
}
type DiagnosticEvent struct {
	Kind        DiagnosticEventKind
	Generation  uint64
	URI         protocol.DocumentURI
	Diagnostics *DiagnosticResult
	Progress    *DiagnosticProgress
	Error       error
	Eager       bool
}
type diagnosticAnalyzer func(context.Context, *querySnapshot, string, *DiagnosticOptions) (DiagnosticResult, error)

// DiagnosticStream checks open TS/TSX documents after source/settings changes.
// Input invalidations coalesce; output is backpressured without blocking session
// state. Every result carries its generation. Close cancels work and closes Events.
type DiagnosticStream struct {
	session    *Session
	ctx        context.Context
	cancel     context.CancelFunc
	stopParent func() bool
	tick       chan struct{}
	events     chan DiagnosticEvent
	done       chan struct{}
	options    DiagnosticOptions
	analyze    diagnosticAnalyzer
}

func (s *Session) WatchDiagnostics(ctx context.Context, options *DiagnosticOptions) (*DiagnosticStream, error) {
	return s.watchDiagnostics(ctx, options, nil)
}
func (s *Session) watchDiagnostics(ctx context.Context, options *DiagnosticOptions, analyze diagnosticAnalyzer) (*DiagnosticStream, error) {
	s.mu.Lock()
	if err := s.checkLocked(ctx); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if len(s.diagnosticStreams) >= s.maxDiagnosticStreams {
		s.mu.Unlock()
		return nil, &Error{Code: CodeInvalidOptions, Message: "diagnostic subscription limit reached"}
	}
	owned, cancel := context.WithCancel(s.ctx)
	caps := diagnosticCapabilities(options)
	w := &DiagnosticStream{session: s, ctx: owned, cancel: cancel, tick: make(chan struct{}, 1), events: make(chan DiagnosticEvent), done: make(chan struct{}), options: DiagnosticOptions{Capabilities: &caps}, analyze: analyze}
	w.stopParent = context.AfterFunc(ctx, cancel)
	if w.analyze == nil {
		w.analyze = func(ctx context.Context, q *querySnapshot, name string, options *DiagnosticOptions) (DiagnosticResult, error) {
			work, err := s.prepareDiagnostics(q, name, options)
			if err != nil {
				return DiagnosticResult{}, err
			}
			return work(ctx)
		}
	}
	if s.diagnosticStreams == nil {
		s.diagnosticStreams = map[*DiagnosticStream]struct{}{}
	}
	s.diagnosticStreams[w] = struct{}{}
	s.background.Add(1)
	s.mu.Unlock()
	w.tick <- struct{}{}
	go w.run()
	return w, nil
}
func (w *DiagnosticStream) Events() <-chan DiagnosticEvent { return w.events }
func (w *DiagnosticStream) Close() error                   { w.cancel(); <-w.done; return nil }
func (s *Session) signalDiagnosticStreamsLocked() {
	for w := range s.diagnosticStreams {
		select {
		case w.tick <- struct{}{}:
		default:
		}
	}
}

type diagnosticJob struct {
	generation uint64
	cancel     context.CancelFunc
}
type diagnosticJobEvent struct {
	id, generation uint64
	result         *DiagnosticResult
	err            error
	done           bool
}

func diagnosticDocument(name string) bool {
	for _, extension := range []string{".ts", ".tsx", ".mts", ".cts"} {
		if strings.HasSuffix(name, extension) {
			return true
		}
	}
	return false
}

func (w *DiagnosticStream) run() {
	s := w.session
	var workers sync.WaitGroup
	jobs := map[uint64]diagnosticJob{}
	results := make(chan diagnosticJobEvent, 1)
	defer func() {
		w.cancel()
		w.stopParent()
		for _, job := range jobs {
			job.cancel()
		}
		workers.Wait()
		s.mu.Lock()
		delete(s.diagnosticStreams, w)
		s.mu.Unlock()
		close(w.events)
		close(w.done)
		s.background.Done()
	}()
	var latest *querySnapshot
	var names []string
	live := map[protocol.DocumentURI]bool{}
	published := map[protocol.DocumentURI]bool{}
	eager := map[protocol.DocumentURI]bool{}
	var clears, eagerClears []protocol.DocumentURI
	var pendingResult *DiagnosticResult
	var pendingError error
	var nextID, currentJob, finished, visible, begun uint64
	visibleTotal := 0
	pending := false
	refresh := func() bool {
		q, err := s.captureQuery(w.ctx)
		if err != nil {
			return false
		}
		previous := latest
		latest = q
		names = nil
		live = map[protocol.DocumentURI]bool{}
		for name, version := range q.versions {
			if !diagnosticDocument(name) {
				continue
			}
			names = append(names, name)
			uri := sourceURI(name, q.snapshot.Environment, q.mappings)
			live[uri] = true
			if q.snapshot.EditorOptions.Diagnostics.EagerClear && previous != nil {
				if old, ok := previous.versions[name]; ok && old != version {
					eager[uri] = true
				}
			}
		}
		slices.Sort(names)
		clears = nil
		for uri := range published {
			if !live[uri] {
				clears = append(clears, uri)
			}
		}
		slices.Sort(clears)
		eagerClears = nil
		for uri := range eager {
			if !live[uri] || !q.snapshot.EditorOptions.Diagnostics.EagerClear {
				delete(eager, uri)
			} else {
				eagerClears = append(eagerClears, uri)
			}
		}
		slices.Sort(eagerClears)
		for _, job := range jobs {
			if job.generation != q.snapshot.Generation {
				job.cancel()
			}
		}
		if pendingResult != nil && pendingResult.Generation != q.snapshot.Generation {
			pendingResult = nil
		}
		pendingError = nil
		pending = len(names) > 0 && currentJob != q.snapshot.Generation
		if len(names) == 0 {
			currentJob = q.snapshot.Generation
			finished = q.snapshot.Generation
		}
		return true
	}
	start := func() {
		if !pending || len(jobs) >= 2 {
			return
		}
		pending = false
		nextID++
		id := nextID
		q := latest
		generation := q.snapshot.Generation
		currentJob = generation
		ctx, cancel := context.WithCancel(w.ctx)
		jobs[id] = diagnosticJob{generation: generation, cancel: cancel}
		files := slices.Clone(names)
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer cancel()
			defer func() {
				select {
				case results <- diagnosticJobEvent{id: id, generation: generation, done: true}:
				case <-w.ctx.Done():
				}
			}()
			for _, name := range files {
				if ctx.Err() != nil {
					return
				}
				result, err := w.analyze(ctx, q, name, &w.options)
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					select {
					case results <- diagnosticJobEvent{id: id, generation: generation, err: err}:
					case <-ctx.Done():
						return
					}
					continue
				}
				if result.Generation == generation {
					s.noteSourceDiagnosticsReady(name, generation)
				}
				select {
				case results <- diagnosticJobEvent{id: id, generation: generation, result: &result}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	for {
		if w.ctx.Err() != nil {
			return
		}
		if latest != nil {
			s.mu.RLock()
			generation := s.generation
			s.mu.RUnlock()
			if generation != latest.snapshot.Generation {
				if !refresh() {
					return
				}
			}
		}
		start()
		var event DiagnosticEvent
		var output chan DiagnosticEvent
		if latest != nil {
			generation := latest.snapshot.Generation
			switch {
			case visible != 0 && (visible != generation || finished == visible && pendingResult == nil && pendingError == nil):
				event = DiagnosticEvent{Kind: DiagnosticEventProgress, Generation: visible, Progress: &DiagnosticProgress{Kind: "end", Total: visibleTotal, Cancelled: visible != generation}}
			case len(clears) > 0:
				uri := clears[0]
				event = DiagnosticEvent{Kind: DiagnosticEventClear, Generation: generation, URI: uri, Diagnostics: &DiagnosticResult{Generation: generation, URI: uri, Items: []protocol.Diagnostic{}}}
			case len(eagerClears) > 0:
				uri := eagerClears[0]
				event = DiagnosticEvent{Kind: DiagnosticEventClear, Generation: generation, URI: uri, Eager: true, Diagnostics: &DiagnosticResult{Generation: generation, URI: uri, Items: []protocol.Diagnostic{}}}
			case currentJob == generation && begun != generation && len(names) > 0:
				event = DiagnosticEvent{Kind: DiagnosticEventProgress, Generation: generation, Progress: &DiagnosticProgress{Kind: "begin", Total: len(names)}}
			case pendingResult != nil:
				event = DiagnosticEvent{Kind: DiagnosticEventResult, Generation: generation, URI: pendingResult.URI, Diagnostics: pendingResult}
			case pendingError != nil:
				event = DiagnosticEvent{Kind: DiagnosticEventError, Generation: generation, Error: pendingError}
			}
			if event.Kind != "" {
				output = w.events
			}
		}
		input := results
		if pendingResult != nil || pendingError != nil {
			input = nil
		}
		// Copy bookkeeping before handing a detached result to the caller.
		sentKind, sentURI, sentGeneration, sentEager := event.Kind, event.URI, event.Generation, event.Eager
		progressKind := ""
		progressTotal := 0
		if event.Progress != nil {
			progressKind = event.Progress.Kind
			progressTotal = event.Progress.Total
		}
		select {
		case <-w.ctx.Done():
			return
		case <-w.tick:
			if !refresh() {
				return
			}
		case message := <-input:
			if message.done {
				delete(jobs, message.id)
				if latest != nil && message.generation == latest.snapshot.Generation {
					finished = message.generation
				}
				continue
			}
			if latest == nil || message.generation != latest.snapshot.Generation {
				continue
			}
			pendingResult = message.result
			pendingError = message.err
		case output <- event:
			switch sentKind {
			case DiagnosticEventResult:
				published[sentURI] = true
				pendingResult = nil
			case DiagnosticEventError:
				pendingError = nil
			case DiagnosticEventClear:
				delete(published, sentURI)
				if sentEager {
					delete(eager, sentURI)
					eagerClears = eagerClears[1:]
					if len(eagerClears) == 0 {
						eagerClears = nil
					}
				} else {
					clears = clears[1:]
					if len(clears) == 0 {
						clears = nil
					}
				}
			case DiagnosticEventProgress:
				if progressKind == "begin" {
					visible = sentGeneration
					begun = sentGeneration
					visibleTotal = progressTotal
				} else {
					visible = 0
				}
			}
		}
	}
}
