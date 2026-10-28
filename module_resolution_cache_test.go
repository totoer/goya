package goya

import (
	"strings"
	"testing"
	"unsafe"
)

func TestModuleResolutionKey(t *testing.T) {
	info := &typeScriptProgram{analysisKey: "analysis"}
	key := func(name string) moduleResolutionKey {
		return preparedResolutionKey(info, "/dir/main.ts", name, moduleResolutionImport, nil)
	}
	base := key("./plugin")
	for _, name := range []string{"././plugin", "./other/../plugin", "/dir/./plugin"} {
		if got := key(name); got != base {
			t.Errorf("equivalent path %q: %+v != %+v", name, got, base)
		}
	}
	for _, name := range []string{"pkg/../plugin", "@alias/./plugin", "host:plugin"} {
		if got := key(name); got.specifier != name {
			t.Errorf("bare specifier %q changed to %q", name, got.specifier)
		}
	}
	for _, other := range []moduleResolutionKey{
		preparedResolutionKey(&typeScriptProgram{analysisKey: "other"}, base.from, "./plugin", base.phase, nil),
		preparedResolutionKey(info, "/other/main.ts", "/dir/plugin", base.phase, nil),
		preparedResolutionKey(info, base.from, "./plugin", moduleResolutionDefer, nil),
		preparedResolutionKey(info, base.from, "./plugin", moduleResolutionRequire, nil),
		preparedResolutionKey(info, base.from, "./plugin", base.phase, map[string]string{"type": "json"}),
		key("./plugin.js"),
	} {
		if other == base {
			t.Errorf("distinct context shares key: %+v", other)
		}
	}
	a := preparedResolutionKey(info, base.from, "./plugin", base.phase, map[string]string{"b": "2", "a": "1"})
	b := preparedResolutionKey(info, base.from, "./plugin", base.phase, map[string]string{"a": "1", "b": "2"})
	if a != b {
		t.Fatal("attribute order affects key")
	}
	if got := preparedResolutionKey(nil, base.from, "./plugin", base.phase, nil); got != (moduleResolutionKey{}) {
		t.Fatal("unprepared import has a cache key")
	}
}

func checkResolutionCache(t *testing.T, c *moduleResolutionCache) {
	t.Helper()
	if len(c.entries) != c.lru.Len() || len(c.entries) > c.limit || c.bytes > c.maxBytes {
		t.Fatalf("cache limits: entries=%d list=%d bytes=%d", len(c.entries), c.lru.Len(), c.bytes)
	}
	size := 0
	for e := c.lru.Front(); e != nil; e = e.Next() {
		entry := e.Value.(*moduleResolutionEntry)
		if c.entries[entry.key] != e {
			t.Fatal("list entry missing from map")
		}
		key := entry.key
		want := len(key.analysis) + len(key.from) + len(key.specifier) + len(key.attributes) + len(entry.target)
		if entry.size != want {
			t.Fatalf("entry size %d, want %d", entry.size, want)
		}
		size += want
	}
	if size != c.bytes {
		t.Fatalf("cache bytes %d, want %d", c.bytes, size)
	}
}

func TestModuleResolutionCacheLRU(t *testing.T) {
	key := func(s string) moduleResolutionKey {
		return moduleResolutionKey{specifier: s, phase: moduleResolutionImport}
	}
	for _, tc := range []struct {
		name            string
		limit, maxBytes int
	}{
		{"entries", 2, 100},
		{"bytes", 10, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &moduleResolutionCache{limit: tc.limit, maxBytes: tc.maxBytes}
			for _, name := range []string{"a", "b"} {
				c.retain(key(name), "x")
				checkResolutionCache(t, c)
			}
			if c.lookup(key("a")) != "x" {
				t.Fatal("cache miss")
			}
			c.retain(key("c"), "x")
			checkResolutionCache(t, c)
			if c.lookup(key("b")) != "" || c.lookup(key("a")) != "x" {
				t.Fatal("least recently used entry was not evicted")
			}
			c.retain(key("a"), "yy")
			checkResolutionCache(t, c)
			if c.lookup(key("a")) != "yy" {
				t.Fatal("replacement lost")
			}
		})
	}
	for _, c := range []*moduleResolutionCache{{limit: 0, maxBytes: 100}, {limit: 2, maxBytes: 0}} {
		c.retain(key("a"), "x")
		checkResolutionCache(t, c)
		if c.lookup(key("a")) != "" || c.entries != nil {
			t.Fatal("disabled cache retains entries")
		}
	}
	var c *moduleResolutionCache
	if c.lookup(key("a")) != "" {
		t.Fatal("nil cache hit")
	}
}

func TestModuleResolutionCacheOwnsStrings(t *testing.T) {
	buffer := strings.Repeat("abcdefgh", 1024)
	part := buffer[10:15]
	key := moduleResolutionKey{analysis: part, from: part, specifier: part, attributes: part, phase: moduleResolutionImport}
	c := &moduleResolutionCache{limit: 2, maxBytes: 100}
	c.retain(key, part)
	entry := c.lru.Front().Value.(*moduleResolutionEntry)
	for _, owned := range []string{entry.key.analysis, entry.key.from, entry.key.specifier, entry.key.attributes, entry.target} {
		if owned != part || unsafe.StringData(owned) == unsafe.StringData(part) {
			t.Fatal("retained string does not own its buffer")
		}
	}
	checkResolutionCache(t, c)
}

func TestModuleResolutionCacheOversizedAndEvicted(t *testing.T) {
	c := &moduleResolutionCache{limit: 2, maxBytes: 20}
	key := func(s string) moduleResolutionKey {
		return moduleResolutionKey{specifier: s, phase: moduleResolutionImport}
	}
	for _, name := range []string{"hot", "cold"} {
		c.retain(key(name), "x")
		checkResolutionCache(t, c)
	}
	front := c.lru.Front()
	before := c.bytes
	for _, k := range []moduleResolutionKey{key(strings.Repeat("z", 21)), key("hot")} {
		c.retain(k, strings.Repeat("x", 21))
		checkResolutionCache(t, c)
		if c.bytes != before || c.lru.Front() != front || len(c.entries) != 2 {
			t.Fatal("oversized entry disturbed useful cache entries")
		}
	}
	if c.lookup(key("hot")) != "x" {
		t.Fatal("hot entry lost")
	}
	evicted := c.entries[key("cold")]
	c.retain(key("new"), "x")
	checkResolutionCache(t, c)
	if c.lookup(key("cold")) != "" || evicted.Next() != nil || evicted.Prev() != nil {
		t.Fatal("evicted entry remains linked")
	}
	for _, e := range c.entries {
		if e == evicted {
			t.Fatal("evicted entry retained by map")
		}
	}
	for i := 0; i < 1000; i++ {
		c.retain(key(string(rune(i+100))), "x")
		checkResolutionCache(t, c)
	}
}
