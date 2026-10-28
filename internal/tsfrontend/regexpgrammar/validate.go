package regexpgrammar

import (
	"fmt"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
	"strings"
)

// Validate uses the pinned native grammar for constructor patterns as well
// as literals. Literal delimiters are introduced only for the scanner; no
// generated source is executed and this path does not invoke the type checker.
func Validate(pattern, flags string) error {
	var text strings.Builder
	text.WriteByte('/')
	escaped := false
	depth := 0
	unicode := strings.ContainsAny(flags, "uv")
	if !unicode {
		// Normalize Annex B identities only for syntax validation. The backend
		// still receives the original constructor pattern.
		hexadecimal := func(text string, count int) bool {
			if len(text) < count {
				return false
			}
			for _, code := range text[:count] {
				if !(code >= '0' && code <= '9' || code >= 'a' && code <= 'f' || code >= 'A' && code <= 'F') {
					return false
				}
			}
			return true
		}
		var identities strings.Builder
		for i := 0; i < len(pattern); i++ {
			if pattern[i] == '\\' && i+1 < len(pattern) {
				i++
				identity := pattern[i] == 'p' || pattern[i] == 'P' || pattern[i] == 'x' && !hexadecimal(pattern[i+1:], 2) || pattern[i] == 'u' && !hexadecimal(pattern[i+1:], 4)
				if !identity {
					identities.WriteByte('\\')
				}
			}
			identities.WriteByte(pattern[i])
		}
		pattern = identities.String()
	}
	for _, code := range pattern {
		if code == '\n' || code == '\r' || code == 0x2028 || code == 0x2029 {
			if escaped && unicode {
				return fmt.Errorf("invalid Unicode regular-expression escape")
			}
			if !escaped {
				text.WriteByte('\\')
			}
			switch code {
			case '\n':
				text.WriteByte('n')
			case '\r':
				text.WriteByte('r')
			case 0x2028:
				text.WriteString("u2028")
			case 0x2029:
				text.WriteString("u2029")
			}
			escaped = false
			continue
		}
		if !escaped {
			if code == '[' {
				depth++
			} else if code == ']' && depth > 0 {
				depth--
			}
			if code == '/' {
				if depth > 0 && strings.ContainsRune(flags, 'v') {
					return fmt.Errorf("unescaped slash in a Unicode set")
				}
				text.WriteByte('\\')
			}
		}
		text.WriteRune(code)
		if code == '\\' && !escaped {
			escaped = true
		} else {
			escaped = false
		}
	}
	if pattern == "" {
		text.WriteString("(?:)")
	}
	text.WriteByte('/')
	text.WriteString(flags)
	scan := scanner.NewScanner()
	scan.SetText(text.String())
	scan.SetScriptTarget(core.ScriptTargetLatest)
	var first error
	unknownProperty := false
	scan.SetOnError(func(message *diagnostics.Message, start, length int, args ...any) {
		// Property repertoire validation belongs to the runtime's Unicode data.
		// The pinned TypeScript scanner's alias list predates some scripts in
		// Unicode 17; its structural grammar remains useful for constructors.
		if message == diagnostics.Unknown_Unicode_property_value || message == diagnostics.Unknown_Unicode_property_name || message == diagnostics.Unknown_Unicode_property_name_or_value {
			unknownProperty = true
			return
		}
		if unknownProperty && message == diagnostics.Did_you_mean_0 {
			return
		}
		if first == nil {
			first = fmt.Errorf("invalid regular expression: %s", message.Localize(locale.Default, args...))
		}
	})
	kind := scan.Scan()
	if kind != ast.KindSlashToken && kind != ast.KindSlashEqualsToken {
		return fmt.Errorf("invalid regular expression")
	}
	if scan.ReScanSlashToken(true) != ast.KindRegularExpressionLiteral {
		return fmt.Errorf("invalid regular expression")
	}
	return first
}
