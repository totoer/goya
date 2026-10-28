package cldrdata

import (
	"slices"
	"strings"
	"sync"
)

// LocaleID is a detached parsed Unicode BCP 47 identifier. Unknown but
// structurally valid subtags are preserved, independently of available data.
type LocaleID struct {
	Language, Script, Region string
	Variants                 []string
	Extensions               map[string][]string
}

func asciiAlpha(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}
func asciiDigit(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func asciiAlnum(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
func languageSubtag(s string) bool {
	return (len(s) == 2 || len(s) == 3 || len(s) >= 5 && len(s) <= 8) && asciiAlpha(s)
}
func regionSubtag(s string) bool { return len(s) == 2 && asciiAlpha(s) || len(s) == 3 && asciiDigit(s) }
func variantSubtag(s string) bool {
	return (len(s) >= 5 && len(s) <= 8 || len(s) == 4 && s[0] >= '0' && s[0] <= '9') && asciiAlnum(s)
}
func unicodeKey(s string) bool { return len(s) == 2 && asciiAlnum(s) && s[1] >= 'a' && s[1] <= 'z' }
func transformKey(s string) bool {
	return len(s) == 2 && s[0] >= 'a' && s[0] <= 'z' && s[1] >= '0' && s[1] <= '9'
}

func parseBase(parts []string) (LocaleID, int, bool) {
	id := LocaleID{Extensions: make(map[string][]string)}
	if len(parts) == 0 || !languageSubtag(parts[0]) {
		return id, 0, false
	}
	id.Language = parts[0]
	i := 1
	if i < len(parts) && len(parts[i]) == 4 && asciiAlpha(parts[i]) {
		id.Script = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		i++
	}
	if i < len(parts) && regionSubtag(parts[i]) {
		id.Region = strings.ToUpper(parts[i])
		i++
	}
	for i < len(parts) && variantSubtag(parts[i]) {
		if slices.Contains(id.Variants, parts[i]) {
			return id, 0, false
		}
		id.Variants = append(id.Variants, parts[i])
		i++
	}
	return id, i, true
}

// ParseLocale validates the Unicode locale grammar, discards duplicate Unicode
// keywords after their first occurrence, and applies pinned CLDR aliases.
func ParseLocale(tag string) (LocaleID, bool) {
	parts := strings.Split(strings.ToLower(tag), "-")
	id, i, ok := parseBase(parts)
	if !ok {
		return id, false
	}
	for i < len(parts) {
		key := parts[i]
		if len(key) != 1 || !asciiAlnum(key) {
			return id, false
		}
		i++
		if _, exists := id.Extensions[key]; exists {
			return id, false
		}
		start := i
		if key == "x" {
			for i < len(parts) {
				if len(parts[i]) < 1 || len(parts[i]) > 8 || !asciiAlnum(parts[i]) {
					return id, false
				}
				i++
			}
		} else {
			for i < len(parts) && len(parts[i]) != 1 {
				if len(parts[i]) < 2 || len(parts[i]) > 8 || !asciiAlnum(parts[i]) {
					return id, false
				}
				i++
			}
		}
		if i == start {
			return id, false
		}
		values := parts[start:i]
		switch key {
		case "u":
			var valid bool
			values, valid = canonicalUnicodeExtension(values)
			if !valid {
				return id, false
			}
		case "t":
			var valid bool
			values, valid = canonicalTransformExtension(values)
			if !valid {
				return id, false
			}
		}
		id.Extensions[key] = slices.Clone(values)
	}
	id.canonicalizeBase()
	return id, true
}

func canonicalUnicodeExtension(parts []string) ([]string, bool) {
	attributes := make([]string, 0)
	fields := make(map[string][]string)
	i := 0
	for i < len(parts) && len(parts[i]) >= 3 {
		if !slices.Contains(attributes, parts[i]) {
			attributes = append(attributes, parts[i])
		}
		i++
	}
	for i < len(parts) {
		key := parts[i]
		if !unicodeKey(key) {
			return nil, false
		}
		i++
		start := i
		for i < len(parts) && len(parts[i]) >= 3 {
			i++
		}
		if _, exists := fields[key]; !exists {
			value := strings.Join(parts[start:i], "-")
			value = CanonicalUnicodeValue(key, value)
			if value == "true" {
				value = ""
			}
			if value == "" {
				fields[key] = nil
			} else {
				fields[key] = strings.Split(value, "-")
			}
		}
	}
	slices.Sort(attributes)
	out := attributes
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		out = append(out, key)
		out = append(out, fields[key]...)
	}
	return out, true
}
func canonicalTransformExtension(parts []string) ([]string, bool) {
	var out []string
	i := 0
	if !transformKey(parts[0]) {
		id, n, ok := parseBase(parts)
		if !ok {
			return nil, false
		}
		i = n
		id.canonicalizeBase()
		out = strings.Split(strings.ToLower(id.BaseName()), "-")
	}
	fields := make(map[string][]string)
	for i < len(parts) {
		key := parts[i]
		if !transformKey(key) {
			return nil, false
		}
		i++
		start := i
		for i < len(parts) && len(parts[i]) >= 3 {
			i++
		}
		if start == i {
			return nil, false
		}
		if _, exists := fields[key]; !exists {
			fields[key] = parts[start:i]
		}
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		out = append(out, key)
		out = append(out, fields[key]...)
	}
	return out, true
}

func CanonicalUnicodeValue(key, value string) string {
	switch key {
	case "kb", "kc", "kh", "kk", "kn":
		if value == "yes" {
			return "true"
		}
	case "ca":
		switch value {
		case "islamicc":
			return "islamic-civil"
		case "ethiopic-amete-alem":
			return "ethioaa"
		}
	case "ks":
		switch value {
		case "primary":
			return "level1"
		case "tertiary":
			return "level3"
		}
	case "ms":
		if value == "imperial" {
			return "uksystem"
		}
	}
	return value
}

type languageAlias struct{ from, to LocaleID }

var languageAliases = sync.OnceValue(func() map[string][]languageAlias {
	aliases := Supplemental("aliases").Get("metadata", "alias", "languageAlias")
	result := make(map[string][]languageAlias)
	for _, key := range aliases.Keys() {
		from, n, ok := parseBase(strings.Split(strings.ToLower(key), "-"))
		if !ok || n != len(strings.Split(key, "-")) {
			continue
		}
		to, _, ok := parseBase(strings.Split(strings.ToLower(aliases.Get(key, "_replacement").String()), "-"))
		if ok {
			result[from.Language] = append(result[from.Language], languageAlias{from, to})
		}
	}
	weight := func(id LocaleID) int {
		n := len(id.Variants)
		if id.Script != "" {
			n++
		}
		if id.Region != "" {
			n++
		}
		return n
	}
	for language, entries := range result {
		slices.SortStableFunc(entries, func(a, b languageAlias) int { return weight(b.from) - weight(a.from) })
		result[language] = entries
	}
	return result
})

func (id *LocaleID) canonicalizeBase() {
	aliases := Supplemental("aliases").Get("metadata", "alias")
	// Compound aliases match subsets of the base, independent of variant order.
	// Unmatched script, region and variants survive the most specific replacement.
	for pass := 0; pass < 8; pass++ {
		before := id.BaseName()
		for _, alias := range slices.Concat(languageAliases()[id.Language], languageAliases()["und"]) {
			from, to := alias.from, alias.to
			if from.Script != "" && from.Script != id.Script || from.Region != "" && from.Region != id.Region {
				continue
			}
			matches := true
			for _, variant := range from.Variants {
				if !slices.Contains(id.Variants, variant) {
					matches = false
					break
				}
			}
			if !matches {
				continue
			}
			if to.Language != "und" {
				id.Language = to.Language
			}
			if from.Script != "" || id.Script == "" {
				id.Script = to.Script
			}
			if from.Region != "" || id.Region == "" {
				id.Region = to.Region
			}
			variants := slices.Clone(to.Variants)
			for _, variant := range id.Variants {
				if !slices.Contains(from.Variants, variant) && !slices.Contains(variants, variant) {
					variants = append(variants, variant)
				}
			}
			id.Variants = variants
			break
		}
		if before == id.BaseName() {
			break
		}
	}
	replacement := ""
	if replacement = aliases.Get("scriptAlias", id.Script, "_replacement").String(); replacement != "" {
		id.Script = replacement
	}
	if replacement = aliases.Get("territoryAlias", id.Region, "_replacement").String(); replacement != "" {
		regions := strings.Fields(replacement)
		id.Region = regions[0]
		if len(regions) > 1 {
			if likely, _, ok := parseBase(strings.Split(strings.ToLower(Supplemental("likelySubtags").Get("likelySubtags", id.Language).String()), "-")); ok && slices.Contains(regions, likely.Region) {
				id.Region = likely.Region
			}
		}
	}
	for i, variant := range id.Variants {
		if replacement = aliases.Get("variantAlias", variant, "_replacement").String(); replacement != "" {
			id.Variants[i] = replacement
		}
	}
	slices.Sort(id.Variants)
}
func (id LocaleID) BaseName() string {
	parts := []string{id.Language}
	if id.Script != "" {
		parts = append(parts, id.Script)
	}
	if id.Region != "" {
		parts = append(parts, id.Region)
	}
	parts = append(parts, id.Variants...)
	return strings.Join(parts, "-")
}
func (id LocaleID) String() string {
	out := id.BaseName()
	keys := make([]string, 0, len(id.Extensions))
	for key := range id.Extensions {
		if key != "x" {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	if _, ok := id.Extensions["x"]; ok {
		keys = append(keys, "x")
	}
	for _, key := range keys {
		out += "-" + key + "-" + strings.Join(id.Extensions[key], "-")
	}
	return out
}
func (id LocaleID) UnicodeValue(key string) (string, bool) {
	parts := id.Extensions["u"]
	for i := 0; i < len(parts); {
		if len(parts[i]) != 2 {
			i++
			continue
		}
		k := parts[i]
		i++
		start := i
		for i < len(parts) && len(parts[i]) != 2 {
			i++
		}
		if k == key {
			value := strings.Join(parts[start:i], "-")
			if value == "" {
				value = "true"
			}
			return value, true
		}
	}
	return "", false
}
func (id *LocaleID) SetUnicodeValue(key, value string) {
	parts := id.Extensions["u"]
	out := make([]string, 0, len(parts)+3)
	for i := 0; i < len(parts); {
		if len(parts[i]) != 2 {
			out = append(out, parts[i])
			i++
			continue
		}
		start := i
		k := parts[i]
		i++
		for i < len(parts) && len(parts[i]) != 2 {
			i++
		}
		if k != key {
			out = append(out, parts[start:i]...)
		}
	}
	if value != "" {
		out = append(out, key)
		if value != "true" {
			out = append(out, strings.Split(CanonicalUnicodeValue(key, value), "-")...)
		}
	}
	if len(out) == 0 {
		delete(id.Extensions, "u")
		return
	}
	id.Extensions["u"], _ = canonicalUnicodeExtension(out)
}

func (id LocaleID) Maximize() LocaleID {
	keys := []string{id.Language + "-" + id.Script + "-" + id.Region, id.Language + "-" + id.Script, id.Language + "-" + id.Region, id.Language, "und-" + id.Script, "und-" + id.Region, "und"}
	for _, key := range keys {
		value := Supplemental("likelySubtags").Get("likelySubtags", key).String()
		if value == "" {
			continue
		}
		likely, _, ok := parseBase(strings.Split(strings.ToLower(value), "-"))
		if !ok {
			continue
		}
		if id.Language == "und" {
			id.Language = likely.Language
		}
		if id.Script == "" {
			id.Script = likely.Script
		}
		if id.Region == "" {
			id.Region = likely.Region
		}
		return id
	}
	return id
}
func (id LocaleID) Minimize() LocaleID {
	maximized := id.Maximize()
	equal := func(other LocaleID) bool {
		m := other.Maximize()
		return m.Language == maximized.Language && m.Script == maximized.Script && m.Region == maximized.Region
	}
	for _, trial := range []LocaleID{{Language: maximized.Language}, {Language: maximized.Language, Region: maximized.Region}, {Language: maximized.Language, Script: maximized.Script}} {
		if equal(trial) {
			id.Language = trial.Language
			id.Script = trial.Script
			id.Region = trial.Region
			return id
		}
	}
	return id
}
