package goya

import (
	"container/list"
	"encoding/json"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend"
)

const (
	moduleResolutionCacheEntries = 256
	moduleResolutionCacheBytes   = 64 << 10
)

type moduleResolutionPhase uint8

const (
	moduleResolutionImport moduleResolutionPhase = iota + 1
	moduleResolutionDefer
	moduleResolutionRequire
)

type moduleResolutionKey struct {
	analysis, from, specifier, attributes string
	phase                                 moduleResolutionPhase
}

func preparedResolutionKey(info *typeScriptProgram, from, specifier string, phase moduleResolutionPhase, attributes map[string]string) moduleResolutionKey {
	if info == nil {
		return moduleResolutionKey{}
	}
	key := moduleResolutionKey{
		analysis: info.analysisKey, from: from,
		specifier: tsfrontend.PreparedSpecifierKey(from, specifier), phase: phase,
	}
	if len(attributes) > 0 {
		encoded, _ := json.Marshal(attributes)
		key.attributes = string(encoded)
	}
	return key
}

type moduleResolutionEntry struct {
	key    moduleResolutionKey
	target string
	size   int
}

// moduleResolutionCache shares one budget across every program and loader in a
// Runtime. bytes counts owned key/target string bytes, not total Go heap usage;
// the entry limit bounds map/list overhead. A nil cache supports lookup.
type moduleResolutionCache struct {
	entries                map[moduleResolutionKey]*list.Element
	lru                    list.List
	limit, maxBytes, bytes int
}

func (c *moduleResolutionCache) lookup(key moduleResolutionKey) string {
	if c != nil {
		if e := c.entries[key]; e != nil {
			c.lru.MoveToFront(e)
			return e.Value.(*moduleResolutionEntry).target
		}
	}
	return ""
}

func (c *moduleResolutionCache) retain(key moduleResolutionKey, target string) {
	size := len(key.analysis) + len(key.from) + len(key.specifier) + len(key.attributes) + len(target)
	if key.phase == 0 || target == "" || c.limit <= 0 || c.maxBytes <= 0 || size > c.maxBytes {
		return
	}
	if e := c.entries[key]; e != nil {
		entry := e.Value.(*moduleResolutionEntry)
		c.bytes += size - entry.size
		entry.target, entry.size = strings.Clone(target), size
		c.lru.MoveToFront(e)
	} else {
		if c.entries == nil {
			c.entries = make(map[moduleResolutionKey]*list.Element)
		}
		// Own each field so a short substring cannot retain an uncounted buffer.
		key.analysis = strings.Clone(key.analysis)
		key.from = strings.Clone(key.from)
		key.specifier = strings.Clone(key.specifier)
		key.attributes = strings.Clone(key.attributes)
		entry := &moduleResolutionEntry{key: key, target: strings.Clone(target), size: size}
		c.entries[key] = c.lru.PushFront(entry)
		c.bytes += size
	}
	for len(c.entries) > c.limit || c.bytes > c.maxBytes {
		e := c.lru.Back()
		entry := e.Value.(*moduleResolutionEntry)
		delete(c.entries, entry.key)
		c.bytes -= entry.size
		c.lru.Remove(e)
	}
}

func (r *Runtime) retainModuleResolution(key moduleResolutionKey, target string) {
	if key.phase == 0 {
		return
	}
	if r.dynamicResolutions == nil {
		r.dynamicResolutions = &moduleResolutionCache{limit: moduleResolutionCacheEntries, maxBytes: moduleResolutionCacheBytes}
	}
	r.dynamicResolutions.retain(key, target)
}
