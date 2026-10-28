// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the Apache License, Version 2.0. See ../LICENSE.typescript.
//
// Adapted for Goya from TypeScript 6.0.3 services/patternMatcher.ts at
// 050880ce59e30b356b686bd3144efe24f875ebc8. Matching uses UTF-16 code units,
// preserving the reference's word, casing, multi-chunk and dotted-name rules.
package editorpattern

import (
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"github.com/totoer/goya/internal/tsfrontend/upstream/stringutil"
	"strings"
	"unicode/utf16"
)

type Match struct {
	Kind          int
	CaseSensitive bool
}
type span struct{ start, end int }
type chunk struct {
	text, lower []uint16
	lowercase   bool
	characters  []span
}
type segment struct {
	total chunk
	words []chunk
}
type Matcher struct {
	segments []segment
	empty    bool
	cache    map[string][]span
}

func units(s string) []uint16 { return utf16.Encode([]rune(s)) }
func lower(c uint16) uint16 {
	if c < 127 {
		if c >= 'A' && c <= 'Z' {
			return c + 'a' - 'A'
		}
		return c
	}
	if c >= 0xd800 && c <= 0xdfff {
		return c
	}
	return units(stringutil.ToLowerJS(string(rune(c))))[0]
}
func upper(c uint16) bool {
	if c >= 'A' && c <= 'Z' {
		return true
	}
	if c < 127 || !scanner.IsIdentifierStart(rune(c)) {
		return false
	}
	text := string(rune(c))
	return text == stringutil.ToUpperJS(text)
}
func lowercase(c uint16) bool {
	if c >= 'a' && c <= 'z' {
		return true
	}
	if c < 127 || !scanner.IsIdentifierStart(rune(c)) {
		return false
	}
	text := string(rune(c))
	return text == stringutil.ToLowerJS(text)
}
func digit(c uint16) bool { return c >= '0' && c <= '9' }
func word(c uint16) bool  { return upper(c) || lowercase(c) || digit(c) || c == '_' || c == '$' }
func punctuation(c uint16) bool {
	return strings.ContainsRune("!\"#%&'()*,-./:;?@[\\]_{}", rune(c))
}
func allPunctuation(s []uint16) bool {
	for _, c := range s {
		if !punctuation(c) || c == '_' {
			return false
		}
	}
	return true
}
func spans(s []uint16, words bool) []span {
	var out []span
	start := 0
	for i := 1; i < len(s); i++ {
		transition := upper(s[i]) && (!words || !upper(s[i-1]))
		if words && i != start && i+1 < len(s) && upper(s[i]) && lowercase(s[i+1]) {
			allUpper := true
			for _, c := range s[start:i] {
				if !upper(c) {
					allUpper = false
					break
				}
			}
			transition = transition || allUpper
		}
		if punctuation(s[i-1]) || punctuation(s[i]) || digit(s[i-1]) != digit(s[i]) || transition {
			if !allPunctuation(s[start:i]) {
				out = append(out, span{start, i})
			}
			start = i
		}
	}
	if !allPunctuation(s[start:]) {
		out = append(out, span{start, len(s)})
	}
	return out
}
func newChunk(s []uint16) chunk {
	text := append([]uint16(nil), s...)
	low := units(stringutil.ToLowerJS(string(utf16.Decode(s))))
	equal := len(text) == len(low)
	if equal {
		for i, c := range text {
			if c != low[i] {
				equal = false
				break
			}
		}
	}
	return chunk{text, low, equal, spans(text, false)}
}

// The compiler scanner also treats NEL/ZWSP as trivia; JavaScript trim does not.
func jsTrimSpace(c rune) bool { return c != 0x85 && c != 0x200b && stringutil.IsWhiteSpaceLike(c) }
func New(pattern string) *Matcher {
	m := &Matcher{cache: map[string][]span{}}
	for _, part := range strings.Split(strings.TrimFunc(pattern, jsTrimSpace), ".") {
		s := units(strings.TrimFunc(part, jsTrimSpace))
		seg := segment{total: newChunk(s)}
		start := -1
		for i := 0; i <= len(s); i++ {
			if i < len(s) && word(s[i]) {
				if start < 0 {
					start = i
				}
			} else if start >= 0 {
				seg.words = append(seg.words, newChunk(s[start:i]))
				start = -1
			}
		}
		m.segments = append(m.segments, seg)
	}
	if len(m.segments) == 1 && len(m.segments[0].total.text) == 0 {
		m.empty = true
		return m
	}
	for _, s := range m.segments {
		if len(s.words) == 0 {
			return nil
		}
	}
	return m
}
func better(a, b *Match) *Match {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if b.Kind < a.Kind || b.Kind == a.Kind && b.CaseSensitive && !a.CaseSensitive {
		return b
	}
	return a
}
func starts(candidate []uint16, part span, pattern []uint16, search span, ignore bool) bool {
	if search.end-search.start > part.end-part.start {
		return false
	}
	for i := 0; i < search.end-search.start; i++ {
		a, b := candidate[part.start+i], pattern[search.start+i]
		if ignore {
			a, b = lower(a), lower(b)
		}
		if a != b {
			return false
		}
	}
	return true
}
func index(candidate, pattern []uint16, ignore bool) int {
	for i := 0; i <= len(candidate)-len(pattern); i++ {
		if starts(candidate, span{i, len(candidate)}, pattern, span{0, len(pattern)}, ignore) {
			return i
		}
	}
	return -1
}
func camel(candidate []uint16, parts []span, c chunk, ignore bool) bool {
	current := 0
	for _, part := range parts {
		got := false
		for current < len(c.characters) {
			cs := c.characters[current]
			if got && (!upper(c.text[c.characters[current-1].start]) || !upper(c.text[cs.start])) {
				break
			}
			if !starts(candidate, part, c.text, cs, ignore) {
				break
			}
			got = true
			part.start += cs.end - cs.start
			current++
		}
		if current == len(c.characters) {
			return true
		}
	}
	return current == len(c.characters)
}
func (m *Matcher) matchChunk(name string, c chunk) *Match {
	candidate := units(name)
	idx := index(candidate, c.lower, true)
	if idx == 0 {
		kind := 1
		if len(candidate) == len(c.text) {
			kind = 0
		}
		return &Match{kind, starts(candidate, span{0, len(candidate)}, c.text, span{0, len(c.text)}, false)}
	}
	parts, ok := m.cache[name]
	if !ok {
		parts = spans(candidate, true)
		m.cache[name] = parts
	}
	if c.lowercase {
		if idx < 0 {
			return nil
		}
		for _, p := range parts {
			if starts(candidate, p, c.text, span{0, len(c.text)}, true) {
				return &Match{2, starts(candidate, p, c.text, span{0, len(c.text)}, false)}
			}
		}
		if len(c.text) < len(candidate) && upper(candidate[idx]) {
			return &Match{2, false}
		}
	} else {
		if index(candidate, c.text, false) > 0 {
			return &Match{2, true}
		}
		if len(c.characters) > 0 {
			if camel(candidate, parts, c, false) {
				return &Match{3, true}
			}
			if camel(candidate, parts, c, true) {
				return &Match{3, false}
			}
		}
	}
	return nil
}
func (m *Matcher) matchSegment(name string, s segment) *Match {
	if !strings.ContainsAny(string(utf16.Decode(s.total.text)), " *") {
		if match := m.matchChunk(name, s.total); match != nil {
			return match
		}
	}
	var best *Match
	for _, c := range s.words {
		best = better(best, m.matchChunk(name, c))
	}
	return best
}

// Match returns the pinned reference ranking. Dotted patterns require enough
// container segments; the strongest matched segment determines the result.
func (m *Matcher) Match(name string, containers []string) *Match {
	if m == nil {
		return nil
	}
	if m.empty {
		return &Match{2, true}
	}
	match := m.matchSegment(name, m.segments[len(m.segments)-1])
	if match == nil {
		return nil
	}
	if len(m.segments) == 1 {
		return match
	}
	if len(m.segments)-1 > len(containers) {
		return nil
	}
	var best *Match
	for i, j := len(m.segments)-2, len(containers)-1; i >= 0; i, j = i-1, j-1 {
		best = better(best, m.matchSegment(containers[j], m.segments[i]))
	}
	return best
}
