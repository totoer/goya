package goya

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/totoer/goya/internal/cldrdata"
)

type intlDateToken struct {
	letter  byte
	width   int
	literal string
}

func intlDateTokens(pattern string) []intlDateToken {
	var tokens []intlDateToken
	quoted := false
	appendLiteral := func(text string) {
		if len(tokens) > 0 && tokens[len(tokens)-1].letter == 0 {
			tokens[len(tokens)-1].literal += text
		} else {
			tokens = append(tokens, intlDateToken{literal: text})
		}
	}
	for len(pattern) > 0 {
		c, size := utf8.DecodeRuneInString(pattern)
		pattern = pattern[size:]
		if c == '\'' {
			if strings.HasPrefix(pattern, "'") {
				appendLiteral("'")
				pattern = pattern[1:]
			} else {
				quoted = !quoted
			}
			continue
		}
		if !quoted && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			width := 1
			for len(pattern) > 0 && pattern[0] == byte(c) {
				width++
				pattern = pattern[1:]
			}
			tokens = append(tokens, intlDateToken{letter: byte(c), width: width})
		} else {
			appendLiteral(string(c))
		}
	}
	return tokens
}
func intlDateTokenField(token intlDateToken) (string, string) {
	width := token.width
	numeric := func() string {
		if width == 2 {
			return "2-digit"
		}
		return "numeric"
	}
	text := func() string {
		if width >= 5 {
			return "narrow"
		}
		if width == 4 {
			return "long"
		}
		return "short"
	}
	switch token.letter {
	case 'y', 'u':
		return "year", numeric()
	case 'M', 'L':
		if width < 3 {
			return "month", numeric()
		}
		return "month", text()
	case 'd':
		if width > 2 {
			return "!unsupported", ""
		}
		return "day", numeric()
	case 'E', 'e', 'c':
		return "weekday", text()
	case 'G':
		return "era", text()
	case 'h', 'H', 'K', 'k':
		return "hour", numeric()
	case 'm':
		return "minute", numeric()
	case 's':
		return "second", numeric()
	case 'S':
		return "fractionalSecondDigits", strings.Repeat("S", width)
	case 'B', 'b':
		return "dayPeriod", text()
	case 'a':
		return "", ""
	case 'z':
		if width >= 4 {
			return "timeZoneName", "long"
		}
		return "timeZoneName", "short"
	case 'v':
		if width >= 4 {
			return "timeZoneName", "longGeneric"
		}
		return "timeZoneName", "shortGeneric"
	case 'O':
		if width >= 4 {
			return "timeZoneName", "longOffset"
		}
		return "timeZoneName", "shortOffset"
	case 0:
		return "", ""
	}
	return "!unsupported", ""
}
func intlDateFields(tokens []intlDateToken) (map[string]string, bool) {
	fields := make(map[string]string)
	for _, token := range tokens {
		key, value := intlDateTokenField(token)
		if key == "!unsupported" {
			return nil, false
		}
		if key != "" {
			fields[key] = value
		}
	}
	return fields, true
}
func intlDateFieldWidth(field, value string) int {
	if field == "fractionalSecondDigits" {
		return len(value)
	}
	switch value {
	case "2-digit":
		return 2
	case "numeric":
		return 1
	case "narrow":
		return 5
	case "long":
		return 4
	case "short":
		return 3
	case "longGeneric", "longOffset":
		return 4
	default:
		return 1
	}
}
func intlAdjustDateTokens(tokens []intlDateToken, requested map[string]string, hourCycle string) []intlDateToken {
	result := make([]intlDateToken, 0, len(tokens)+2)
	hasHour, hasPeriod, hasFraction, hasZone := false, false, false, false
	for _, token := range tokens {
		field, _ := intlDateTokenField(token)
		if field == "hour" {
			hasHour = true
			switch hourCycle {
			case "h11":
				token.letter = 'K'
			case "h12":
				token.letter = 'h'
			case "h24":
				token.letter = 'k'
				if requested["hour"] == "" {
					token.width = 2
				}
			default:
				token.letter = 'H'
				if requested["hour"] == "" {
					token.width = 2
				}
			}
		}
		if token.letter == 'a' || field == "dayPeriod" {
			hasPeriod = true
			if hourCycle == "h23" || hourCycle == "h24" {
				if requested["dayPeriod"] == "" {
					if len(result) > 0 && result[len(result)-1].letter == 0 {
						result[len(result)-1].literal = strings.TrimRight(result[len(result)-1].literal, " \u00a0\u202f")
					}
					continue
				}
			}
		}
		if value := requested[field]; value != "" {
			width := intlDateFieldWidth(field, value)
			sameMonthCategory := field != "month" || (token.width < 3) == (width < 3)
			if sameMonthCategory && (value != "numeric" || token.width != 2 || field == "year") {
				token.width = width
			}
			if field == "dayPeriod" {
				token.letter = 'B'
			}
			if field == "timeZoneName" {
				switch value {
				case "shortGeneric", "longGeneric":
					token.letter = 'v'
				case "shortOffset", "longOffset":
					token.letter = 'O'
				default:
					token.letter = 'z'
				}
			}
		}
		if field == "fractionalSecondDigits" {
			hasFraction = true
		}
		if field == "timeZoneName" {
			hasZone = true
		}
		result = append(result, token)
		if field == "second" && requested["fractionalSecondDigits"] != "" && !hasFraction {
			// Existing fractional tokens are handled below; avoid appending two.
			existing := false
			for _, other := range tokens {
				if other.letter == 'S' {
					existing = true
					break
				}
			}
			if !existing {
				result = append(result, intlDateToken{literal: "{decimal}"}, intlDateToken{letter: 'S', width: len(requested["fractionalSecondDigits"])})
				hasFraction = true
			}
		}
	}
	if hasHour && !hasPeriod && (hourCycle == "h11" || hourCycle == "h12") {
		result = append(result, intlDateToken{literal: "\u202f"}, intlDateToken{letter: 'a', width: 1})
	}
	if requested["fractionalSecondDigits"] != "" && !hasFraction {
		result = append(result, intlDateToken{letter: 'S', width: len(requested["fractionalSecondDigits"])})
	}
	if requested["timeZoneName"] != "" && !hasZone {
		if len(result) > 0 {
			result = append(result, intlDateToken{literal: " "})
		}
		letter := byte('z')
		value := requested["timeZoneName"]
		if strings.HasSuffix(value, "Generic") {
			letter = 'v'
		} else if strings.HasSuffix(value, "Offset") {
			letter = 'O'
		}
		result = append(result, intlDateToken{letter: letter, width: intlDateFieldWidth("timeZoneName", value)})
	}
	return result
}
func intlPatternString(node cldrdata.Node) string {
	if text := node.String(); text != "" {
		return text
	}
	return node.Get("_value").String()
}
func intlDateMismatchPenalty(field, wanted, actual string, basic bool) int {
	if wanted == actual {
		return 0
	}
	if wanted == "" {
		return 20
	}
	if actual == "" {
		return 120
	}
	if field == "timeZoneName" {
		switch wanted {
		case "short", "shortGeneric":
			if actual == "shortOffset" {
				return 1
			}
			if actual == "longOffset" {
				return 4
			}
			if wanted == "short" && actual == "long" || wanted == "shortGeneric" && actual == "longGeneric" {
				return 3
			}
		case "long", "longGeneric":
			if actual == "longOffset" {
				return 1
			}
			if actual == "shortOffset" {
				return 9
			}
			if wanted == "long" && actual == "short" || wanted == "longGeneric" && actual == "shortGeneric" {
				return 8
			}
		case "shortOffset":
			if actual == "longOffset" {
				return 3
			}
		case "longOffset":
			if actual == "shortOffset" {
				return 8
			}
		}
		return 120
	}
	if !basic {
		isNumeric := func(value string) bool { return value == "numeric" || value == "2-digit" }
		if isNumeric(wanted) && isNumeric(actual) {
			return 1
		}
		if field == "month" && isNumeric(wanted) != isNumeric(actual) {
			return 80
		}
	}
	values := []string{"2-digit", "numeric", "narrow", "short", "long"}
	wi, ai := slices.Index(values, wanted), slices.Index(values, actual)
	if field == "fractionalSecondDigits" {
		wi, ai = len(wanted)-1, len(actual)-1
	}
	delta := max(-2, min(2, ai-wi))
	switch delta {
	case 2:
		return 6
	case 1:
		return 3
	case -1:
		return 6
	case -2:
		return 8
	}
	return 0
}

func intlDatePattern(calendar cldrdata.Node, requested map[string]string, hourCycle, matcher string) []intlDateToken {
	if len(requested) == 0 {
		return nil
	}
	patterns := calendar.Get("dateTimeFormats", "availableFormats")
	var best []intlDateToken
	bestScore := -1 << 30
	keys := patterns.Keys()
	keys = append(keys, "@fraction", "@period")
	for _, key := range keys {
		if strings.Contains(key, "-alt-") {
			continue
		}
		pattern := intlPatternString(patterns.Get(key))
		if key == "@fraction" {
			pattern = "S"
		}
		if key == "@period" {
			pattern = "B"
		}
		tokens := intlDateTokens(pattern)
		fields, valid := intlDateFields(tokens)
		if !valid || len(fields) == 0 {
			continue
		}
		// Reject a 12-hour/24-hour candidate for the other clock. Their locale
		// punctuation and implicit AM/PM fields are not interchangeable.
		wrongClock := false
		for _, token := range tokens {
			if token.letter == 'h' || token.letter == 'K' {
				wrongClock = hourCycle == "h23" || hourCycle == "h24"
			}
			if token.letter == 'H' || token.letter == 'k' {
				wrongClock = hourCycle == "h11" || hourCycle == "h12"
			}
		}
		if wrongClock {
			continue
		}
		tokens = intlAdjustDateTokens(tokens, requested, hourCycle)
		fields, _ = intlDateFields(tokens)
		score := 0
		for _, field := range []string{"weekday", "era", "year", "month", "day", "dayPeriod", "hour", "minute", "second", "fractionalSecondDigits", "timeZoneName"} {
			want, got := requested[field], fields[field]
			score -= intlDateMismatchPenalty(field, want, got, matcher == "basic")
		}
		if score > bestScore {
			bestScore = score
			best = tokens
		}
	}
	return best
}
func intlJoinDatePatterns(pattern string, date, time []intlDateToken) []intlDateToken {
	var result []intlDateToken
	for pattern != "" {
		at := strings.IndexByte(pattern, '{')
		if at < 0 {
			result = append(result, intlDateTokens(pattern)...)
			break
		}
		if at > 0 {
			result = append(result, intlDateTokens(pattern[:at])...)
		}
		pattern = pattern[at:]
		if strings.HasPrefix(pattern, "{0}") {
			result = append(result, time...)
			pattern = pattern[3:]
		} else if strings.HasPrefix(pattern, "{1}") {
			result = append(result, date...)
			pattern = pattern[3:]
		} else {
			panic("invalid embedded datetime join pattern")
		}
	}
	return result
}
func intlDefaultHourCycle(locale string) string {
	id, ok := cldrdata.ParseLocale(locale)
	if !ok {
		return "h12"
	}
	region := id.Maximize().Region
	preferred := cldrdata.Supplemental("timeData").Get("timeData", region, "_preferred").String()
	switch preferred {
	case "H":
		return "h23"
	case "k":
		return "h24"
	case "K":
		return "h11"
	default:
		return "h12"
	}
}
func intlHourCycles(locale string) []string {
	first := intlDefaultHourCycle(locale)
	result := []string{first}
	for _, cycle := range []string{"h11", "h12", "h23", "h24"} {
		if !slices.Contains(result, cycle) {
			result = append(result, cycle)
		}
	}
	return result
}
