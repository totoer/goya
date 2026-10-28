package goya

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/totoer/goya/internal/cldrdata"
)

type intlNumberFormatObject struct {
	baseObject
	locale, dataLocale, numberingSystem                               string
	style, currency, currencyDisplay, currencySign, unit, unitDisplay string
	notation, compactDisplay, grouping, signDisplay                   string
	digits                                                            cldrdata.DecimalRounding
	boundFormat                                                       *Object
}

func (r *Runtime) intlNumberFormatReceiver(value Value) *intlNumberFormatObject {
	if object, ok := value.(*Object); ok {
		if formatter, ok := object.self.(*intlNumberFormatObject); ok {
			return formatter
		}
	}
	panic(r.NewTypeError("incompatible Intl.NumberFormat receiver"))
}
func (r *Runtime) newIntlNumberFormat(args []Value, newTarget *Object) *Object {
	state := r.intlState()
	prototype := r.getPrototypeFromCtor(newTarget, state.constructors["NumberFormat"], state.prototypes["NumberFormat"])
	requested := r.intlCanonicalLocales(intlArgument(args, 0))
	options := r.intlOptions(intlArgument(args, 1), true)
	r.intlStringOption(options, "localeMatcher", []string{"lookup", "best fit"}, "best fit")
	numbering := r.intlUnicodeOption(options, "numberingSystem")
	resolved := intlNumberingResolution(requested, numbering)
	f := &intlNumberFormatObject{locale: resolved.locale, dataLocale: resolved.dataLocale, numberingSystem: resolved.values["nu"]}
	f.style = r.intlStringOption(options, "style", []string{"decimal", "percent", "currency", "unit"}, "decimal")
	currency, currencyPresent := r.intlOptionalStringOption(options, "currency")
	if !currencyPresent {
		if f.style == "currency" {
			panic(r.NewTypeError("currency style requires currency"))
		}
	} else {
		if len(currency) != 3 {
			r.intlRangeError("invalid currency code")
		}
		for _, c := range currency {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
				r.intlRangeError("invalid currency code")
			}
		}
	}
	currencyDisplay := r.intlStringOption(options, "currencyDisplay", []string{"code", "symbol", "narrowSymbol", "name"}, "symbol")
	currencySign := r.intlStringOption(options, "currencySign", []string{"standard", "accounting"}, "standard")
	unit, unitPresent := r.intlOptionalStringOption(options, "unit")
	if !unitPresent {
		if f.style == "unit" {
			panic(r.NewTypeError("unit style requires unit"))
		}
	} else if !intlValidUnit(unit) {
		r.intlRangeError("invalid unit identifier")
	}
	unitDisplay := r.intlStringOption(options, "unitDisplay", []string{"short", "narrow", "long"}, "short")
	if f.style == "currency" {
		f.currency = strings.ToUpper(currency)
		f.currencyDisplay = currencyDisplay
		f.currencySign = currencySign
	}
	if f.style == "unit" {
		f.unit = unit
		f.unitDisplay = unitDisplay
	}
	f.notation = r.intlStringOption(options, "notation", []string{"standard", "scientific", "engineering", "compact"}, "standard")
	minimumFraction, maximumFraction := 0, 3
	if f.style == "currency" && f.notation == "standard" {
		minimumFraction = intlCurrencyDigits(f.currency)
		maximumFraction = minimumFraction
	} else if f.style == "percent" {
		maximumFraction = 0
	}
	f.digits = r.intlDigitOptions(options, minimumFraction, maximumFraction, f.notation)
	compactDisplay := r.intlStringOption(options, "compactDisplay", []string{"short", "long"}, "short")
	f.grouping = "auto"
	if f.notation == "compact" {
		f.compactDisplay = compactDisplay
		f.grouping = "min2"
	}
	if value := nilSafe(options.self.getStr("useGrouping", nil)); !IsUndefined(value) {
		if value == valueTrue {
			f.grouping = "always"
		} else if !value.ToBoolean() {
			f.grouping = "false"
		} else {
			grouping := value.toString().String()
			if !slices.Contains([]string{"min2", "auto", "always", "true", "false"}, grouping) {
				r.intlRangeError("invalid useGrouping")
			}
			if grouping != "true" && grouping != "false" {
				f.grouping = grouping
			}
		}
	}
	f.signDisplay = r.intlStringOption(options, "signDisplay", []string{"auto", "never", "always", "exceptZero", "negative"}, "auto")
	object := &Object{runtime: r}
	f.baseObject = baseObject{val: object, prototype: prototype, class: classObject, extensible: true}
	object.self = f
	f.init()
	return object
}
func (f *intlNumberFormatObject) resolvedOptions() *Object {
	r := f.val.runtime
	result := r.intlResult("locale", f.locale, "numberingSystem", f.numberingSystem, "style", f.style)
	put := func(name string, value Value) { createDataPropertyOrThrow(result, newStringValue(name), value) }
	if f.style == "currency" {
		put("currency", newStringValue(f.currency))
		put("currencyDisplay", newStringValue(f.currencyDisplay))
		put("currencySign", newStringValue(f.currencySign))
	}
	if f.style == "unit" {
		put("unit", newStringValue(f.unit))
		put("unitDisplay", newStringValue(f.unitDisplay))
	}
	r.intlResolvedDigits(result, f.digits)
	if f.grouping == "false" {
		put("useGrouping", valueFalse)
	} else {
		put("useGrouping", newStringValue(f.grouping))
	}
	put("notation", newStringValue(f.notation))
	if f.notation == "compact" {
		put("compactDisplay", newStringValue(f.compactDisplay))
	}
	put("signDisplay", newStringValue(f.signDisplay))
	r.intlResolvedRounding(result, f.digits)
	return result
}
func (f *intlNumberFormatObject) currencyText(category string) string {
	data := cldrdata.Locale(f.dataLocale, "currencies").Get("numbers", "currencies", f.currency)
	var text string
	switch f.currencyDisplay {
	case "code":
		text = f.currency
	case "name":
		text = data.Get("displayName-count-" + category).String()
		if text == "" {
			text = data.Get("displayName-count-other").String()
		}
		if text == "" {
			text = data.Get("displayName").String()
		}
	case "narrowSymbol":
		text = data.Get("symbol-alt-narrow").String()
		if text == "" {
			text = data.Get("symbol").String()
		}
	default:
		text = data.Get("symbol").String()
	}
	if text == "" {
		text = f.currency
	}
	return text
}
func (f *intlNumberFormatObject) compactPattern(magnitude int, category string) (string, int) {
	patterns := intlNumberData(f.dataLocale, f.numberingSystem, "decimalFormats").Get(f.compactDisplay, "decimalFormat")
	for at := magnitude; at >= 0; at-- {
		// The embedded tables have only a small finite set of powers. Avoid
		// allocating a huge key for an arbitrarily large BigInt magnitude.
		if at > 20 {
			at = 20
		}
		key := "1" + strings.Repeat("0", at) + "-count-" + category
		pattern := patterns.Get(key).String()
		if pattern == "" {
			pattern = patterns.Get("1" + strings.Repeat("0", at) + "-count-other").String()
		}
		if pattern == "" {
			continue
		}
		if pattern == "0" {
			return "0", 0
		}
		zeros := strings.Count(pattern, "0")
		if zeros == 0 {
			return "0", 0
		}
		return pattern, at - zeros + 1
	}
	return "0", 0
}
func (f *intlNumberFormatObject) exponent(magnitude int) int {
	switch f.notation {
	case "scientific":
		return magnitude
	case "engineering":
		remainder := magnitude % 3
		if remainder < 0 {
			remainder += 3
		}
		return magnitude - remainder
	case "compact":
		_, exponent := f.compactPattern(magnitude, "other")
		return exponent
	}
	return 0
}
func (f *intlNumberFormatObject) numericParts(value cldrdata.Decimal) ([]intlPart, cldrdata.RoundedDecimal, int) {
	symbols := intlNumericSymbols(f.dataLocale, f.numberingSystem)
	if value.Special != "" {
		symbol := symbols.Get(value.Special).String()
		if value.Special == "infinity" {
			symbol = symbols.Get("infinity").String()
		} else {
			symbol = symbols.Get("nan").String()
		}
		return []intlPart{{kind: value.Special, text: symbol}}, cldrdata.RoundedDecimal{Value: value, Text: value.Special}, 0
	}
	if f.style == "percent" {
		value = value.Scale(2)
	}
	magnitude := value.Magnitude()
	exponent := f.exponent(magnitude)
	rounded := f.digits.Round(value.Scale(-exponent))
	if !value.IsZero() && rounded.Magnitude+exponent != magnitude {
		exponent = f.exponent(rounded.Magnitude + exponent)
		rounded = f.digits.Round(value.Scale(-exponent))
	}
	integer, fraction, hasFraction := strings.Cut(rounded.Text, ".")
	var parts []intlPart
	groupingPattern := intlNumberData(f.dataLocale, f.numberingSystem, "decimalFormats").Get("standard").String()
	groupingPattern, _, _ = strings.Cut(groupingPattern, ".")
	primary, secondary := 3, 3
	if comma := strings.LastIndexByte(groupingPattern, ','); comma >= 0 {
		primary = len(groupingPattern) - comma - 1
		if before := strings.LastIndexByte(groupingPattern[:comma], ','); before >= 0 {
			secondary = comma - before - 1
		} else {
			secondary = primary
		}
	}
	minimumGrouping := 1
	if f.grouping == "auto" {
		if n, err := strconv.Atoi(cldrdata.Locale(f.dataLocale, "numbers").Get("numbers", "minimumGroupingDigits").String()); err == nil {
			minimumGrouping = n
		}
	} else if f.grouping == "min2" {
		minimumGrouping = 2
	}
	if f.grouping != "false" && len(integer) >= primary+minimumGrouping {
		var chunks []string
		end := len(integer)
		size := primary
		for end > size {
			chunks = append(chunks, integer[end-size:end])
			end -= size
			size = secondary
		}
		chunks = append(chunks, integer[:end])
		slices.Reverse(chunks)
		for i, chunk := range chunks {
			if i > 0 {
				parts = appendIntlPart(parts, "group", symbols.Get("group").String())
			}
			parts = appendIntlPart(parts, "integer", intlLocalizeDigits(chunk, f.numberingSystem))
		}
	} else {
		parts = appendIntlPart(parts, "integer", intlLocalizeDigits(integer, f.numberingSystem))
	}
	if hasFraction {
		parts = appendIntlPart(parts, "decimal", symbols.Get("decimal").String())
		parts = appendIntlPart(parts, "fraction", intlLocalizeDigits(fraction, f.numberingSystem))
	}
	if f.notation == "scientific" || f.notation == "engineering" {
		parts = appendIntlPart(parts, "exponentSeparator", symbols.Get("exponential").String())
		if exponent < 0 {
			parts = intlSymbolParts(parts, "exponentMinusSign", symbols.Get("minusSign").String())
		}
		parts = appendIntlPart(parts, "exponentInteger", intlLocalizeDigits(strconv.Itoa(max(exponent, -exponent)), f.numberingSystem))
	} else if f.notation == "compact" {
		category := cldrdata.PluralCategory(f.dataLocale, "cardinal", rounded.Text, exponent)
		pattern, _ := f.compactPattern(rounded.Magnitude+exponent, category)
		first, last := strings.IndexByte(pattern, '0'), strings.LastIndexByte(pattern, '0')
		if first >= 0 {
			prefix := strings.ReplaceAll(pattern[:first], "'", "")
			suffix := strings.ReplaceAll(pattern[last+1:], "'", "")
			out := intlUnitAffix(nil, prefix, "compact")
			out = append(out, parts...)
			parts = intlUnitAffix(out, suffix, "compact")
		}
	}
	return parts, rounded, exponent
}
func intlSymbolParts(parts []intlPart, kind, text string) []intlPart {
	start, end := 0, len(text)
	for start < end {
		c, size := utf8.DecodeRuneInString(text[start:end])
		if !unicode.Is(unicode.Cf, c) {
			break
		}
		start += size
	}
	for end > start {
		c, size := utf8.DecodeLastRuneInString(text[start:end])
		if !unicode.Is(unicode.Cf, c) {
			break
		}
		end -= size
	}
	parts = appendIntlPart(parts, "literal", text[:start])
	parts = appendIntlPart(parts, kind, text[start:end])
	return appendIntlPart(parts, "literal", text[end:])
}
func intlNumberPatternAffix(parts []intlPart, affix, currency string, symbols cldrdata.Node) []intlPart {
	quoted := false
	for len(affix) > 0 {
		c, size := utf8.DecodeRuneInString(affix)
		affix = affix[size:]
		if c == '\'' {
			if strings.HasPrefix(affix, "'") {
				parts = appendIntlPart(parts, "literal", "'")
				affix = affix[1:]
			} else {
				quoted = !quoted
			}
			continue
		}
		if !quoted {
			switch c {
			case '¤':
				for strings.HasPrefix(affix, "¤") {
					affix = strings.TrimPrefix(affix, "¤")
				}
				parts = appendIntlPart(parts, "currency", currency)
				continue
			case '%':
				parts = intlSymbolParts(parts, "percentSign", symbols.Get("percentSign").String())
				continue
			case '-':
				parts = intlSymbolParts(parts, "minusSign", symbols.Get("minusSign").String())
				continue
			case '+':
				parts = intlSymbolParts(parts, "plusSign", symbols.Get("plusSign").String())
				continue
			}
		}
		parts = appendIntlPart(parts, "literal", string(c))
	}
	return parts
}
func (f *intlNumberFormatObject) formatParts(value cldrdata.Decimal) []intlPart {
	numeric, rounded, exponent := f.numericParts(value)
	negative := rounded.Value.Negative
	zero := rounded.Value.Special == "" && rounded.Value.IsZero()
	nan := value.Special == "nan"
	sign := ""
	switch f.signDisplay {
	case "never":
	case "always":
		if negative {
			sign = "-"
		} else {
			sign = "+"
		}
	case "exceptZero":
		if !zero && !nan {
			if negative {
				sign = "-"
			} else {
				sign = "+"
			}
		}
	case "negative":
		if negative && !zero {
			sign = "-"
		}
	default:
		if negative {
			sign = "-"
		}
	}
	category := "other"
	if value.Special == "" {
		category = cldrdata.PluralCategory(f.dataLocale, "cardinal", rounded.Text, exponent)
	}
	symbols := intlNumericSymbols(f.dataLocale, f.numberingSystem)
	if f.style == "unit" || f.style == "currency" && f.currencyDisplay == "name" {
		if sign != "" {
			kind := "plusSign"
			key := "plusSign"
			if sign == "-" {
				kind, key = "minusSign", "minusSign"
			}
			signed := intlSymbolParts(nil, kind, symbols.Get(key).String())
			numeric = append(signed, numeric...)
		}
		if f.style == "unit" {
			return intlApplyUnitPattern(intlUnitPattern(f.dataLocale, f.unit, f.unitDisplay, category), numeric, "unit")
		}
		pattern := intlNumberData(f.dataLocale, f.numberingSystem, "currencyFormats").Get("unitPattern-count-" + category).String()
		if pattern == "" {
			pattern = "{0} {1}"
		}
		return intlApplyUnitPattern(strings.ReplaceAll(pattern, "{1}", f.currencyText(category)), numeric, "currency")
	}
	pattern := intlNumberData(f.dataLocale, f.numberingSystem, "decimalFormats").Get("standard").String()
	currency := ""
	if f.style == "percent" {
		pattern = intlNumberData(f.dataLocale, f.numberingSystem, "percentFormats").Get("standard").String()
	}
	if f.style == "currency" {
		currency = f.currencyText(category)
		name := f.currencySign
		if name == "" || name == "standard" {
			name = "standard"
		}
		data := intlNumberData(f.dataLocale, f.numberingSystem, "currencyFormats")
		first, _ := utf8.DecodeRuneInString(currency)
		last, _ := utf8.DecodeLastRuneInString(currency)
		if unicode.IsLetter(first) || unicode.IsLetter(last) {
			if alternate := data.Get(name + "-alphaNextToNumber").String(); alternate != "" {
				pattern = alternate
			} else {
				pattern = data.Get(name).String()
			}
		} else {
			pattern = data.Get(name).String()
		}
	}
	positive, negativePattern, hasNegative := strings.Cut(pattern, ";")
	pattern = positive
	if sign == "-" {
		if hasNegative {
			pattern = negativePattern
		} else {
			pattern = "-" + positive
		}
	} else if sign == "+" {
		pattern = "+" + positive
	}
	start := strings.IndexAny(pattern, "#0")
	end := strings.LastIndexAny(pattern, "#0.,")
	if start < 0 || end < start {
		panic("invalid embedded number pattern")
	}
	parts := intlNumberPatternAffix(nil, pattern[:start], currency, symbols)
	parts = append(parts, numeric...)
	return intlNumberPatternAffix(parts, pattern[end+1:], currency, symbols)
}
func (f *intlNumberFormatObject) rangeParts(start, end Value) []intlPart {
	r := f.val.runtime
	if IsUndefined(start) || IsUndefined(end) {
		panic(r.NewTypeError("number range endpoints are required"))
	}
	left, right := r.intlMathematicalValue(start), r.intlMathematicalValue(end)
	if left.Special == "nan" || right.Special == "nan" {
		r.intlRangeError("invalid number range")
	}
	first, last := f.formatParts(left), f.formatParts(right)
	data := intlNumberData(f.dataLocale, f.numberingSystem, "miscPatterns")
	if intlPartsText(first) == intlPartsText(last) {
		pattern := data.Get("approximately").String()
		prefix, suffix, _ := strings.Cut(pattern, "{0}")
		parts := intlUnitAffix(nil, prefix, "approximatelySign")
		parts = append(parts, first...)
		parts = intlUnitAffix(parts, suffix, "approximatelySign")
		for i := range parts {
			parts[i].source = "shared"
		}
		return parts
	}
	for i := range first {
		first[i].source = "startRange"
	}
	for i := range last {
		last[i].source = "endRange"
	}
	pattern := data.Get("range").String()
	before, after, _ := strings.Cut(pattern, "{0}")
	between, tail, _ := strings.Cut(after, "{1}")
	var parts []intlPart
	if before != "" {
		parts = append(parts, intlPart{kind: "literal", text: before, source: "shared"})
	}
	parts = append(parts, first...)
	if between != "" {
		parts = append(parts, intlPart{kind: "literal", text: between, source: "shared"})
	}
	parts = append(parts, last...)
	if tail != "" {
		parts = append(parts, intlPart{kind: "literal", text: tail, source: "shared"})
	}
	return parts
}
func (r *Runtime) getIntlNumberFormat() *Object {
	return r.intlConstructor("NumberFormat", true, r.newIntlNumberFormat, func(prototype *Object) {
		prototype.self._putProp("resolvedOptions", r.newNativeFunc(func(call FunctionCall) Value { return r.intlNumberFormatReceiver(call.This).resolvedOptions() }, "resolvedOptions", 0), true, false, true)
		prototype.self.defineOwnPropertyStr("format", PropertyDescriptor{Configurable: FLAG_TRUE, Getter: r.newNativeFunc(func(call FunctionCall) Value {
			f := r.intlNumberFormatReceiver(call.This)
			if f.boundFormat == nil {
				f.boundFormat = r.newNativeFunc(func(call FunctionCall) Value {
					return newStringValue(intlPartsText(f.formatParts(r.intlMathematicalValue(call.Argument(0)))))
				}, "", 1)
			}
			return f.boundFormat
		}, "get format", 0)}, true)
		prototype.self._putProp("formatToParts", r.newNativeFunc(func(call FunctionCall) Value {
			f := r.intlNumberFormatReceiver(call.This)
			return r.intlPartsArray(f.formatParts(r.intlMathematicalValue(call.Argument(0))), false)
		}, "formatToParts", 1), true, false, true)
		for _, name := range []string{"formatRange", "formatRangeToParts"} {
			prototype.self._putProp(newStringValue(name).string(), r.newNativeFunc(func(call FunctionCall) Value {
				f := r.intlNumberFormatReceiver(call.This)
				parts := f.rangeParts(call.Argument(0), call.Argument(1))
				if name == "formatRangeToParts" {
					return r.intlPartsArray(parts, true)
				}
				return newStringValue(intlPartsText(parts))
			}, newStringValue(name).string(), 2), true, false, true)
		}
	}, 0)
}
