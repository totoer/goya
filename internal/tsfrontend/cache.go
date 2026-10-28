package tsfrontend

import (
	"container/list"
	"sync"
)

type analysisCacheEntry struct {
	key         string
	analysis    *Analysis
	diagnostics []Diagnostic
	size        int
}

var checkedAnalyses = struct {
	sync.Mutex
	entries                map[string]*list.Element
	lru                    list.List
	limit, maxBytes, bytes int
}{entries: make(map[string]*list.Element), limit: 2, maxBytes: 16 << 20}

// SetAnalysisCacheLimits bounds retained analysis count and captured source
// bytes. Setting either bound to zero disables retention. Executable Programs
// do not retain frontend analyses. It is safe to reconfigure between calls.
func SetAnalysisCacheLimits(entries, maxSourceBytes int) {
	checkedAnalyses.Lock()
	defer checkedAnalyses.Unlock()
	checkedAnalyses.limit = max(0, entries)
	checkedAnalyses.maxBytes = max(0, maxSourceBytes)
	checkedAnalyses.entries = make(map[string]*list.Element)
	checkedAnalyses.lru.Init()
	checkedAnalyses.bytes = 0
}
func cloneDiagnostics(ds []Diagnostic) []Diagnostic {
	if ds == nil {
		return nil
	}
	out := make([]Diagnostic, len(ds))
	for i, d := range ds {
		out[i] = d
		if d.Range != nil {
			r := *d.Range
			out[i].Range = &r
		}
		out[i].Related = cloneDiagnostics(d.Related)
	}
	return out
}
func lookupAnalysis(key string) (*Analysis, []Diagnostic) {
	checkedAnalyses.Lock()
	defer checkedAnalyses.Unlock()
	if e := checkedAnalyses.entries[key]; e != nil {
		checkedAnalyses.lru.MoveToFront(e)
		entry := e.Value.(*analysisCacheEntry)
		copy := *entry.analysis
		return &copy, cloneDiagnostics(entry.diagnostics)
	}
	return nil, nil
}
func retainAnalysis(a *Analysis, ds []Diagnostic) {
	size := 0
	for name, text := range a.files.files {
		size += len(name) + len(text)
	}
	checkedAnalyses.Lock()
	defer checkedAnalyses.Unlock()
	if checkedAnalyses.limit == 0 || checkedAnalyses.maxBytes == 0 || size > checkedAnalyses.maxBytes {
		return
	}
	if checkedAnalyses.entries[a.key] != nil {
		return
	}
	copy := *a
	e := checkedAnalyses.lru.PushFront(&analysisCacheEntry{a.key, &copy, cloneDiagnostics(ds), size})
	checkedAnalyses.entries[a.key] = e
	checkedAnalyses.bytes += size
	for checkedAnalyses.lru.Len() > checkedAnalyses.limit || checkedAnalyses.bytes > checkedAnalyses.maxBytes {
		e := checkedAnalyses.lru.Back()
		entry := e.Value.(*analysisCacheEntry)
		delete(checkedAnalyses.entries, entry.key)
		checkedAnalyses.bytes -= entry.size
		checkedAnalyses.lru.Remove(e)
	}
}
