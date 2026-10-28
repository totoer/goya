package cldrdata

import (
	"math/big"
	"strings"
)

var pluralCategoryOrder = []string{"zero", "one", "two", "few", "many", "other"}

func pluralRules(locale, kind string) Node {
	document := "plurals"
	key := "plurals-type-cardinal"
	if kind == "ordinal" {
		document = "ordinals"
		key = "plurals-type-ordinal"
	}
	data := Supplemental(document).Get(key)
	for locale != "" {
		if rules := data.Get(locale); rules.Exists() {
			return rules
		}
		at := strings.LastIndexByte(locale, '-')
		if at < 0 {
			break
		}
		locale = locale[:at]
	}
	return Node{}
}
func PluralCategories(locale, kind string) []string {
	rules := pluralRules(locale, kind)
	out := make([]string, 0, 6)
	for _, category := range pluralCategoryOrder {
		if category == "other" || rules.Get("pluralRule-count-"+category).Exists() {
			out = append(out, category)
		}
	}
	return out
}
func pluralOperands(text string, exponent int) map[string]*big.Rat {
	integer, fraction, _ := strings.Cut(text, ".")
	integer = strings.TrimLeft(integer, "+-")
	if exponent != 0 {
		digits := integer + fraction
		point := len(integer) + exponent
		if point <= 0 {
			integer = "0"
			fraction = strings.Repeat("0", -point) + digits
		} else if point >= len(digits) {
			integer = digits + strings.Repeat("0", point-len(digits))
			fraction = ""
		} else {
			integer, fraction = digits[:point], digits[point:]
		}
	}
	numeric, ok := new(big.Rat).SetString(integer + "." + fraction)
	if !ok {
		panic("invalid internal plural decimal")
	}
	number := func(s string) *big.Rat {
		if s == "" {
			return new(big.Rat)
		}
		value, ok := new(big.Int).SetString(s, 10)
		if !ok {
			panic("invalid internal plural operand")
		}
		return new(big.Rat).SetInt(value)
	}
	trimmed := strings.TrimRight(fraction, "0")
	return map[string]*big.Rat{
		"n": numeric, "i": number(integer), "v": new(big.Rat).SetInt64(int64(len(fraction))), "w": new(big.Rat).SetInt64(int64(len(trimmed))), "f": number(fraction), "t": number(trimmed), "c": new(big.Rat).SetInt64(int64(exponent)), "e": new(big.Rat).SetInt64(int64(exponent)),
	}
}
func pluralRelation(condition string, operands map[string]*big.Rat) bool {
	fields := strings.Fields(condition)
	if len(fields) < 3 {
		panic("invalid embedded plural relation")
	}
	operand := operands[fields[0]]
	if operand == nil {
		panic("unknown embedded plural operand")
	}
	operand = new(big.Rat).Set(operand)
	i := 1
	if fields[i] == "%" {
		modulus, ok := new(big.Int).SetString(fields[i+1], 10)
		if !ok || modulus.Sign() <= 0 {
			panic("invalid embedded plural modulus")
		}
		quotient := new(big.Int).Quo(operand.Num(), new(big.Int).Mul(operand.Denom(), modulus))
		operand.Sub(operand, new(big.Rat).SetInt(new(big.Int).Mul(quotient, modulus)))
		i += 2
	}
	operator := fields[i]
	i++
	if operator != "=" && operator != "!=" {
		panic("unknown embedded plural operator")
	}
	ranges := strings.Join(fields[i:], "")
	matched := false
	if operand.IsInt() {
		for _, item := range strings.Split(ranges, ",") {
			lower, upper, hasRange := strings.Cut(item, "..")
			if !hasRange {
				upper = lower
			}
			lo, ok := new(big.Rat).SetString(lower)
			if !ok {
				panic("invalid embedded plural lower bound")
			}
			hi, ok := new(big.Rat).SetString(upper)
			if !ok {
				panic("invalid embedded plural upper bound")
			}
			if operand.Cmp(lo) >= 0 && operand.Cmp(hi) <= 0 {
				matched = true
				break
			}
		}
	}
	if operator == "!=" {
		return !matched
	}
	return matched
}
func pluralCondition(condition string, operands map[string]*big.Rat) bool {
	for _, alternative := range strings.Split(condition, " or ") {
		matches := true
		for _, relation := range strings.Split(alternative, " and ") {
			if !pluralRelation(relation, operands) {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

// PluralCategory evaluates the pinned locale rule against the visible rounded
// decimal representation. Visible trailing zeros and large integer operands
// remain exact; compactExponent supplies the CLDR c/e operands.
func PluralCategory(locale, kind, text string, compactExponent int) string {
	rules := pluralRules(locale, kind)
	operands := pluralOperands(text, compactExponent)
	for _, category := range pluralCategoryOrder[:5] {
		rule := rules.Get("pluralRule-count-" + category).String()
		condition, _, _ := strings.Cut(rule, "@")
		condition = strings.TrimSpace(condition)
		if condition != "" && pluralCondition(condition, operands) {
			return category
		}
	}
	return "other"
}
func PluralRangeCategory(locale, start, end string) string {
	rules := Supplemental("pluralRanges").Get("plurals", locale)
	if !rules.Exists() {
		base, _, _ := strings.Cut(locale, "-")
		rules = Supplemental("pluralRanges").Get("plurals", base)
	}
	result := rules.Get("pluralRange-start-" + start + "-end-" + end).String()
	if result == "" {
		return "other"
	}
	return result
}
