// Package editorcollation implements the pinned editor's import comparers.
// Primary-key decoding follows golang.org/x/text/collate v0.43.0; its license
// and patent notices are retained in docs/licenses/golang.org_x_text/.
package editorcollation

import (
	"slices"
	"unicode"
	"unicode/utf16"

	"github.com/totoer/goya/internal/tsfrontend/upstream/stringutil"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

func Ordinal(ignoreCase bool) func(string, string) int {
	return func(a, b string) int {
		if ignoreCase {
			a = stringutil.ToLowerJS(a)
			b = stringutil.ToLowerJS(b)
		}
		return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b)))
	}
}

type Options struct {
	Locale                       string
	IgnoreCase, Numeric, Accents bool
	CaseFirst                    string
}

// Unicode creates request-owned collators: Collator's scratch buffers are never
// shared between sessions or concurrently executing requests.
func Unicode(options Options) func(string, string) int {
	locale := options.Locale
	if locale == "" || locale == "auto" {
		locale = "en"
	}
	tag, err := language.Parse(locale)
	if err != nil {
		tag = language.English
	}
	// Intl.Collator recognizes only co/kf/kn extensions. Explicit editor options
	// override kf/kn; preserve co without adopting other x/text tag options.
	base, script, region := tag.Raw()
	parts := []any{base, script, region}
	for _, variant := range tag.Variants() {
		parts = append(parts, variant)
	}
	if order := tag.TypeForKey("co"); order != "" {
		if extension, err := language.ParseExtension("u-co-" + order); err == nil {
			parts = append(parts, extension)
		}
	}
	if resolved, err := language.Compose(parts...); err == nil {
		tag = resolved
	}
	settings := []collate.Option{}
	if options.Numeric {
		settings = append(settings, collate.Numeric)
	}
	if !options.Accents {
		settings = append(settings, collate.IgnoreDiacritics)
		if !options.IgnoreCase {
			// Apply the case level separately from primary collation below.
			settings = append(settings, collate.IgnoreCase)
		}
	}
	if options.IgnoreCase {
		settings = append(settings, collate.IgnoreCase)
	}
	comparer := collate.New(tag, settings...)
	insensitive := collate.New(tag, append(slices.Clone(settings), collate.IgnoreCase)...)
	letters := collate.New(tag)
	return func(a, b string) int {
		if !options.IgnoreCase && !options.Accents {
			if order := insensitive.CompareString(a, b); order != 0 {
				return order
			}
			return compareCase(letters, a, b, options.CaseFirst == "upper")
		}
		if !options.IgnoreCase && (options.CaseFirst == "upper" || options.CaseFirst == "lower") && insensitive.CompareString(a, b) == 0 {
			if order := compareCase(letters, a, b, options.CaseFirst == "upper"); order != 0 {
				return order
			}
		}
		return comparer.CompareString(a, b)
	}
}
func compareCase(letters *collate.Collator, a, b string, upperFirst bool) int {
	ac, bc := caseWeights(letters, a, upperFirst), caseWeights(letters, b, upperFirst)
	for i := 0; i < min(len(ac), len(bc)); i++ {
		if ac[i] < bc[i] {
			return -1
		}
		if ac[i] > bc[i] {
			return 1
		}
	}
	return 0
}
func caseWeights(letters *collate.Collator, text string, upperFirst bool) []byte {
	result := []byte{}
	var buffer collate.Buffer
	for _, character := range text {
		weights := []byte{}
		for _, part := range norm.NFKD.String(string(character)) {
			upper := unicode.IsUpper(part) || unicode.IsTitle(part)
			if !upper && !unicode.IsLower(part) {
				continue
			}
			weight := byte(1)
			if upper != upperFirst {
				weight = 2
			}
			weights = append(weights, weight)
		}
		if len(weights) == 0 {
			continue
		}
		// A locale can expand one cased character (German phonebook umlauts,
		// sharp s, ligatures) into multiple primary collation elements.
		buffer.Reset()
		key := letters.KeyFromString(&buffer, string(character))
		elements := 0
		for i := 0; i+1 < len(key); {
			if key[i] == 0 && key[i+1] == 0 {
				break
			}
			if key[i]&0x80 != 0 {
				i += 3
			} else {
				i += 2
			}
			elements++
		}
		for len(weights) < elements {
			weights = append(weights, weights[len(weights)-1])
		}
		result = append(result, weights...)
	}
	return result
}
