package lsp

import (
	"container/list"
	"fmt"
	"strconv"
	"strings"
)

// ResultHandle is opaque, belongs to one session/generation, and may expire.
// Keep it unchanged when resolving completion, code actions, lenses or edits.
type ResultHandle string

// RetentionLimits bounds deferred result metadata independently of active work.
// Either zero disables retention; oversized results are immediately expired.
type RetentionLimits struct{ MaxEntries, MaxBytes int }

type retainedResult struct {
	id    uint64
	value any
	size  int
}
type resultStore struct {
	limits  RetentionLimits
	entries map[uint64]*list.Element
	order   list.List
	bytes   int
	nextID  uint64
}

func newResultStore(limits RetentionLimits) resultStore {
	return resultStore{limits: limits, entries: map[uint64]*list.Element{}}
}
func (r *resultStore) clear() { clear(r.entries); r.order.Init(); r.bytes = 0 }
func (s *Session) advanceGenerationLocked() {
	s.generation++
	s.results.clear()
	clear(s.diagnosticsReady)
	clear(s.sourceDiagnosticsReady)
	s.publishInvalidationLocked()
	s.signalDiagnosticStreamsLocked()
}

// Values must be detached DTOs, never AST/checker/program pointers. Callers
// account for retained strings/slices and keep metadata private until cloned.
func (s *Session) retainLocked(value any, size int) ResultHandle {
	r := &s.results
	r.nextID++
	handle := ResultHandle(fmt.Sprintf("%x:%x:%x", s.id, s.generation, r.nextID))
	size = max(size, 1)
	if r.limits.MaxEntries == 0 || r.limits.MaxBytes == 0 || size > r.limits.MaxBytes {
		return handle
	}
	r.entries[r.nextID] = r.order.PushFront(retainedResult{id: r.nextID, value: value, size: size})
	r.bytes += size
	for len(r.entries) > r.limits.MaxEntries || r.bytes > r.limits.MaxBytes {
		last := r.order.Back()
		entry := last.Value.(retainedResult)
		delete(r.entries, entry.id)
		r.bytes -= entry.size
		r.order.Remove(last)
	}
	return handle
}

func (s *Session) resolveLocked(handle ResultHandle) (any, error) {
	if len(handle) > 64 {
		return nil, &Error{Code: CodeInvalidHandle, Message: "invalid editor result handle"}
	}
	parts := strings.Split(string(handle), ":")
	if len(parts) != 3 {
		return nil, &Error{Code: CodeInvalidHandle, Message: "invalid editor result handle"}
	}
	var values [3]uint64
	for i, part := range parts {
		value, err := strconv.ParseUint(part, 16, 64)
		if err != nil {
			return nil, &Error{Code: CodeInvalidHandle, Message: "invalid editor result handle", Cause: err}
		}
		values[i] = value
	}
	if values[0] != s.id {
		return nil, &Error{Code: CodeInvalidHandle, Message: "editor result belongs to another session"}
	}
	if values[1] != s.generation {
		return nil, ErrContentModified
	}
	element := s.results.entries[values[2]]
	if element == nil {
		return nil, ErrResultExpired
	}
	s.results.order.MoveToFront(element)
	return element.Value.(retainedResult).value, nil
}
