package goya

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	regexsyntax "github.com/dlclark/regexp2/v2/syntax"
	"github.com/totoer/goya/internal/unicode17"
)

type regexpCodeRange struct{ first, last rune }
type regexpUnicodeSet struct {
	ranges  []regexpCodeRange
	strings map[string][]rune
}

func regexpNormalizeRanges(ranges []regexpCodeRange) []regexpCodeRange {
	slices.SortFunc(ranges, func(a, b regexpCodeRange) int {
		if a.first < b.first {
			return -1
		}
		if a.first > b.first {
			return 1
		}
		return int(a.last - b.last)
	})
	result := ranges[:0]
	for _, entry := range ranges {
		if len(result) > 0 && entry.first <= result[len(result)-1].last+1 {
			result[len(result)-1].last = max(result[len(result)-1].last, entry.last)
		} else {
			result = append(result, entry)
		}
	}
	return result
}
func regexpRangeOperation(left, right []regexpCodeRange, operation string) []regexpCodeRange {
	if operation == "union" {
		return regexpNormalizeRanges(append(slices.Clone(left), right...))
	}
	var result []regexpCodeRange
	next := 0
	for _, entry := range left {
		first := entry.first
		for next < len(right) && right[next].last < first {
			next++
		}
		for j := next; j < len(right) && right[j].first <= entry.last; j++ {
			other := right[j]
			if operation == "intersection" {
				result = append(result, regexpCodeRange{max(first, other.first), min(entry.last, other.last)})
			} else {
				if first < other.first {
					result = append(result, regexpCodeRange{first, other.first - 1})
				}
				first = max(first, other.last+1)
			}
		}
		if operation == "subtraction" && first <= entry.last {
			result = append(result, regexpCodeRange{first, entry.last})
		}
	}
	return result
}
func regexpSetOperation(left, right regexpUnicodeSet, operation string) regexpUnicodeSet {
	result := regexpUnicodeSet{ranges: regexpRangeOperation(left.ranges, right.ranges, operation)}
	if len(left.strings)+len(right.strings) > 0 {
		result.strings = make(map[string][]rune)
	}
	for key, value := range left.strings {
		_, present := right.strings[key]
		if operation == "union" || operation == "intersection" && present || operation == "subtraction" && !present {
			result.strings[key] = value
		}
	}
	if operation == "union" {
		for key, value := range right.strings {
			result.strings[key] = value
		}
	}
	return result
}
func regexpComplementSet(set regexpUnicodeSet) (regexpUnicodeSet, error) {
	if len(set.strings) > 0 {
		return regexpUnicodeSet{}, fmt.Errorf("a Unicode string set cannot be complemented")
	}
	return regexpUnicodeSet{ranges: regexpRangeOperation([]regexpCodeRange{{0, unicode.MaxRune}}, set.ranges, "subtraction")}, nil
}
func regexpFoldCode(code rune) rune {
	smallest := code
	for next := unicode.SimpleFold(code); next != code; next = unicode.SimpleFold(next) {
		smallest = min(smallest, next)
	}
	return smallest
}
func regexpFoldRange(first, last rune, ignoreCase bool) regexpUnicodeSet {
	ranges := []regexpCodeRange{{first, last}}
	if ignoreCase && !(first == 0 && last == unicode.MaxRune) {
		for code := first; code <= last; code++ {
			for next := unicode.SimpleFold(code); next != code; next = unicode.SimpleFold(next) {
				ranges = append(ranges, regexpCodeRange{next, next})
			}
		}
	}
	return regexpUnicodeSet{ranges: regexpNormalizeRanges(ranges)}
}
func (set *regexpUnicodeSet) addString(value []rune, ignoreCase bool) {
	if len(value) == 1 {
		set.ranges = regexpRangeOperation(set.ranges, regexpFoldRange(value[0], value[0], ignoreCase).ranges, "union")
		return
	}
	value = slices.Clone(value)
	var key strings.Builder
	for i, code := range value {
		if ignoreCase {
			code = regexpFoldCode(code)
			value[i] = code
		}
		key.WriteByte(byte(code >> 24))
		key.WriteByte(byte(code >> 16))
		key.WriteByte(byte(code >> 8))
		key.WriteByte(byte(code))
	}
	if set.strings == nil {
		set.strings = make(map[string][]rune)
	}
	set.strings[key.String()] = value
}

type regexpScalarSetResult struct {
	ranges []regexpCodeRange
	err    error
}

var regexpScalarSets sync.Map

func regexpScalarSet(expression string, ignoreCase bool) (regexpUnicodeSet, error) {
	key := expression
	if ignoreCase {
		key += ":i"
	}
	cached, ok := regexpScalarSets.Load(key)
	if !ok {
		build := sync.OnceValue(func() regexpScalarSetResult {
			options := regexsyntax.ECMAScript | regexsyntax.Unicode
			if ignoreCase {
				options |= regexsyntax.IgnoreCase
			}
			tree, err := regexsyntax.Parse("["+expression+"]", regexsyntax.ParseOptions{RegexOptions: options})
			if err != nil {
				return regexpScalarSetResult{err: err}
			}
			node := tree.Root
			for len(node.Children) == 1 {
				node = node.Children[0]
			}
			contains := func(code rune) bool {
				switch node.T {
				case regexsyntax.NtSet:
					return node.Set.Contains(code)
				case regexsyntax.NtOne:
					return code == node.Ch || ignoreCase && regexpFoldCode(code) == regexpFoldCode(node.Ch)
				case regexsyntax.NtNotone:
					return !(code == node.Ch || ignoreCase && regexpFoldCode(code) == regexpFoldCode(node.Ch))
				case regexsyntax.NtNothing:
					return false
				}
				panic("unexpected scalar Unicode property tree")
			}
			var ranges []regexpCodeRange
			start := rune(-1)
			for code := rune(0); code <= unicode.MaxRune; code++ {
				if contains(code) {
					if start < 0 {
						start = code
					}
				} else if start >= 0 {
					ranges = append(ranges, regexpCodeRange{start, code - 1})
					start = -1
				}
			}
			if start >= 0 {
				ranges = append(ranges, regexpCodeRange{start, unicode.MaxRune})
			}
			return regexpScalarSetResult{ranges: ranges}
		})
		cached, _ = regexpScalarSets.LoadOrStore(key, build)
	}
	result := cached.(func() regexpScalarSetResult)()
	return regexpUnicodeSet{ranges: result.ranges}, result.err
}
func regexpWriteCode(builder *strings.Builder, code rune) {
	if code <= 0xffff {
		fmt.Fprintf(builder, "\\u%04X", code)
	} else {
		fmt.Fprintf(builder, "\\u{%X}", code)
	}
}
func (set regexpUnicodeSet) pattern(ignoreCase bool) string {
	var alternatives []string
	values := make([][]rune, 0, len(set.strings))
	for _, value := range set.strings {
		values = append(values, value)
	}
	slices.SortFunc(values, func(left, right []rune) int {
		if len(left) != len(right) {
			return len(right) - len(left)
		}
		return slices.Compare(left, right)
	})
	empty := false
	for _, value := range values {
		if len(value) == 0 {
			empty = true
			continue
		}
		var literal strings.Builder
		if ignoreCase {
			literal.WriteString("(?i:")
		}
		for _, code := range value {
			regexpWriteCode(&literal, code)
		}
		if ignoreCase {
			literal.WriteByte(')')
		}
		alternatives = append(alternatives, literal.String())
	}
	if len(set.ranges) > 0 {
		var scalar strings.Builder
		scalar.WriteByte('[')
		for _, entry := range set.ranges {
			regexpWriteCode(&scalar, entry.first)
			if entry.last != entry.first {
				scalar.WriteByte('-')
				regexpWriteCode(&scalar, entry.last)
			}
		}
		scalar.WriteByte(']')
		alternatives = append(alternatives, scalar.String())
	}
	if empty {
		alternatives = append(alternatives, "")
	}
	if len(alternatives) == 0 {
		return "(?!)"
	}
	// Folding is already applied to every set operand before complement and set
	// operations. Disable the engine's ambient folding on the resulting class.
	return "(?-i:" + strings.Join(alternatives, "|") + ")"
}

type regexpUnicodeSetParser struct {
	source     string
	pos        int
	ignoreCase bool
	depth      int
}

func (p *regexpUnicodeSetParser) fail() error {
	return fmt.Errorf("invalid Unicode set at byte %d", p.pos)
}
func (p *regexpUnicodeSetParser) character() (rune, error) {
	if p.pos >= len(p.source) {
		return 0, p.fail()
	}
	code, size := utf8.DecodeRuneInString(p.source[p.pos:])
	p.pos += size
	if code != '\\' {
		return code, nil
	}
	if p.pos >= len(p.source) {
		return 0, p.fail()
	}
	code, size = utf8.DecodeRuneInString(p.source[p.pos:])
	p.pos += size
	switch code {
	case 'b':
		return '\b', nil
	case 'f':
		return '\f', nil
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 't':
		return '\t', nil
	case 'v':
		return '\v', nil
	case '0':
		return 0, nil
	case 'c':
		if p.pos >= len(p.source) {
			return 0, p.fail()
		}
		code = rune(p.source[p.pos])
		p.pos++
		return code & 31, nil
	case 'x', 'u':
		digits := 2
		if code == 'u' {
			digits = 4
		}
		if code == 'u' && p.pos < len(p.source) && p.source[p.pos] == '{' {
			p.pos++
			end := strings.IndexByte(p.source[p.pos:], '}')
			if end < 0 {
				return 0, p.fail()
			}
			value, ok := decodeHex(p.source[p.pos : p.pos+end])
			p.pos += end + 1
			if !ok || value > unicode.MaxRune {
				return 0, p.fail()
			}
			return rune(value), nil
		}
		if p.pos+digits > len(p.source) {
			return 0, p.fail()
		}
		value, ok := decodeHex(p.source[p.pos : p.pos+digits])
		p.pos += digits
		if !ok {
			return 0, p.fail()
		}
		return rune(value), nil
	}
	return code, nil
}
func (p *regexpUnicodeSetParser) property() (regexpUnicodeSet, error) {
	start := p.pos
	negate := p.source[p.pos+1] == 'P'
	p.pos += 2
	if p.pos >= len(p.source) || p.source[p.pos] != '{' {
		return regexpUnicodeSet{}, p.fail()
	}
	p.pos++
	end := strings.IndexByte(p.source[p.pos:], '}')
	if end < 0 {
		return regexpUnicodeSet{}, p.fail()
	}
	name := p.source[p.pos : p.pos+end]
	p.pos += end + 1
	if values, ok := unicode17.EmojiStrings(name); ok {
		if negate {
			return regexpUnicodeSet{}, p.fail()
		}
		var set regexpUnicodeSet
		for _, value := range values {
			set.addString([]rune(value), p.ignoreCase)
		}
		return set, nil
	}
	expression := p.source[start:p.pos]
	if negate {
		expression = "\\p{" + name + "}"
	}
	set, err := regexpScalarSet(expression, p.ignoreCase)
	if err != nil {
		return set, err
	}
	if negate {
		return regexpComplementSet(set)
	}
	return set, nil
}
func (p *regexpUnicodeSetParser) operand() (set regexpUnicodeSet, character rune, single bool, err error) {
	if p.pos >= len(p.source) {
		err = p.fail()
		return
	}
	if p.source[p.pos] == '[' {
		set, err = p.class()
		return
	}
	if p.source[p.pos] == '\\' && p.pos+1 < len(p.source) {
		switch p.source[p.pos+1] {
		case 'p', 'P':
			set, err = p.property()
			return
		case 'd', 'D', 's', 'S', 'w', 'W':
			expression := p.source[p.pos : p.pos+2]
			p.pos += 2
			set, err = regexpScalarSet(expression, p.ignoreCase)
			return
		case 'q':
			p.pos += 2
			if p.pos >= len(p.source) || p.source[p.pos] != '{' {
				err = p.fail()
				return
			}
			p.pos++
			var value []rune
			for p.pos < len(p.source) {
				code := p.source[p.pos]
				if code == '|' || code == '}' {
					set.addString(value, p.ignoreCase)
					value = nil
					p.pos++
					if code == '}' {
						return
					}
					continue
				}
				var next rune
				next, err = p.character()
				if err != nil {
					return
				}
				value = append(value, next)
			}
			err = p.fail()
			return
		}
	}
	character, err = p.character()
	single = true
	if err == nil {
		set = regexpFoldRange(character, character, p.ignoreCase)
	}
	return
}
func (p *regexpUnicodeSetParser) class() (regexpUnicodeSet, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > 1000 {
		return regexpUnicodeSet{}, fmt.Errorf("Unicode set nesting is too deep")
	}
	if p.pos >= len(p.source) || p.source[p.pos] != '[' {
		return regexpUnicodeSet{}, p.fail()
	}
	p.pos++
	negate := p.pos < len(p.source) && p.source[p.pos] == '^'
	if negate {
		p.pos++
	}
	var result regexpUnicodeSet
	mode := ""
	first := true
	for p.pos < len(p.source) && p.source[p.pos] != ']' {
		operation := "union"
		if !first {
			if strings.HasPrefix(p.source[p.pos:], "&&") {
				operation = "intersection"
				p.pos += 2
			} else if strings.HasPrefix(p.source[p.pos:], "--") {
				operation = "subtraction"
				p.pos += 2
			}
			if mode == "" {
				mode = operation
			} else if mode != operation {
				return result, p.fail()
			}
		}
		operand, low, single, err := p.operand()
		if err != nil {
			return result, err
		}
		if p.pos < len(p.source) && p.source[p.pos] == '-' && !strings.HasPrefix(p.source[p.pos:], "--") {
			p.pos++
			_, high, highSingle, err := p.operand()
			if err != nil {
				return result, err
			}
			if !single || !highSingle || low > high {
				return result, p.fail()
			}
			operand = regexpFoldRange(low, high, p.ignoreCase)
		}
		if first {
			result = operand
			first = false
		} else {
			result = regexpSetOperation(result, operand, operation)
		}
	}
	if p.pos >= len(p.source) {
		return result, p.fail()
	}
	p.pos++
	if negate {
		return regexpComplementSet(result)
	}
	return result, nil
}
func lowerRegExpUnicodeSets(source string, ignoreCase bool) (string, error) {
	var builder strings.Builder
	var parents []bool
	for pos := 0; pos < len(source); {
		switch source[pos] {
		case '[':
			parser := regexpUnicodeSetParser{source: source, pos: pos, ignoreCase: ignoreCase}
			set, err := parser.class()
			if err != nil {
				return "", err
			}
			builder.WriteString(set.pattern(ignoreCase))
			pos = parser.pos
		case '\\':
			if pos+2 < len(source) && (source[pos+1] == 'p' || source[pos+1] == 'P') && source[pos+2] == '{' {
				parser := regexpUnicodeSetParser{source: source, pos: pos, ignoreCase: ignoreCase}
				set, err := parser.property()
				if err != nil {
					return "", err
				}
				builder.WriteString(set.pattern(ignoreCase))
				pos = parser.pos
			} else {
				builder.WriteByte(source[pos])
				pos++
				if pos < len(source) {
					code, size := utf8.DecodeRuneInString(source[pos:])
					builder.WriteRune(code)
					pos += size
				}
			}
		case '(':
			parents = append(parents, ignoreCase)
			end := pos + 2
			if pos+1 < len(source) && source[pos+1] == '?' {
				for end < len(source) && strings.ContainsRune("ims-", rune(source[end])) {
					end++
				}
				if end > pos+2 && end < len(source) && source[end] == ':' {
					remove := false
					for _, flag := range source[pos+2 : end] {
						if flag == '-' {
							remove = true
						} else if flag == 'i' {
							ignoreCase = !remove
						}
					}
					builder.WriteString(source[pos : end+1])
					pos = end + 1
					continue
				}
			}
			builder.WriteByte('(')
			pos++
		case ')':
			if len(parents) > 0 {
				ignoreCase = parents[len(parents)-1]
				parents = parents[:len(parents)-1]
			}
			builder.WriteByte(')')
			pos++
		default:
			code, size := utf8.DecodeRuneInString(source[pos:])
			builder.WriteRune(code)
			pos += size
		}
	}
	return builder.String(), nil
}
