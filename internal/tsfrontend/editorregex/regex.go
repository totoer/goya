// Package editorregex evaluates TypeScript editor exclusion patterns using the
// same ECMAScript grammar and pure-Go engines as the embedding runtime.
package editorregex

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/dlclark/regexp2/v2"
	"github.com/totoer/goya/internal/tsfrontend/regexpgrammar"
	"github.com/totoer/goya/parser"
)

type Matcher struct {
	linear       *regexp.Regexp
	backtracking *regexp2.Regexp
	unicode      bool
}

// BudgetExceeded is caught at the editor service boundary. A failed match must
// abort the request, since treating it as false would produce partial results.
type BudgetExceeded struct{}

func (*BudgetExceeded) Error() string { return "editor regular-expression match budget exhausted" }

func Compile(pattern string) (*Matcher, error) {
	flags := ""
	if strings.HasPrefix(pattern, "/") {
		last := strings.LastIndex(pattern, "/")
		literal := last > 0
		for i := 1; i < last; i++ {
			if pattern[i] == '/' && pattern[i-1] != '\\' {
				literal = false
				break
			}
		}
		if literal {
			for _, flag := range pattern[last+1:] {
				if flag == 'i' || flag == 'u' {
					flags += string(flag)
				}
			}
			pattern = pattern[1:last]
		}
	}
	if err := regexpgrammar.Validate(pattern, flags); err != nil {
		return nil, err
	}
	unicode := strings.Contains(flags, "u")
	if !unicode {
		var b strings.Builder
		for _, r := range pattern {
			if r > 0xffff {
				a, z := utf16.EncodeRune(r)
				fmt.Fprintf(&b, `\u%04X\u%04X`, a, z)
			} else {
				b.WriteRune(r)
			}
		}
		pattern = b.String()
	}
	m := &Matcher{unicode: unicode}
	// Patterns containing captures/lookarounds use the ECMAScript backtracking
	// engine, as the runtime does; RE2 handles the remaining linear patterns.
	if !strings.Contains(pattern, "(") {
		transformed, err := parser.TransformRegExp(pattern, false, unicode)
		if err == nil {
			if strings.Contains(flags, "i") {
				transformed = "(?i:" + transformed + ")"
			}
			m.linear, _ = regexp.Compile(transformed)
		}
	}
	options := regexp2.ECMAScript
	if strings.Contains(flags, "i") {
		options |= regexp2.IgnoreCase
	}
	if unicode {
		options |= regexp2.Unicode
	}
	compiled, err := regexp2.Compile(pattern, options)
	if err != nil {
		return nil, err
	}
	// User preferences must not hold a checker lease indefinitely.
	compiled.MatchTimeout = 50 * time.Millisecond
	m.backtracking = compiled
	return m, nil
}

func (m *Matcher) MatchString(text string) bool {
	// A non-Unicode ECMAScript dot/class consumes UTF-16 units. RE2 can
	// handle the fast path only when every input rune is a single unit.
	if m.linear != nil && (m.unicode || !strings.ContainsFunc(text, func(r rune) bool { return r > 0xffff })) {
		return m.linear.MatchString(text)
	}
	runes := []rune(text)
	if !m.unicode {
		units := utf16.Encode(runes)
		runes = make([]rune, len(units))
		for i, unit := range units {
			runes[i] = rune(unit)
		}
	}
	matched, err := m.backtracking.MatchRunes(runes)
	if err != nil {
		panic(&BudgetExceeded{})
	}
	return matched
}
