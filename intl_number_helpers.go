package goya

import (
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/totoer/goya/internal/cldrdata"
	"github.com/totoer/goya/unistring"
)

type intlPart struct{ kind, text, source, unit string }

func intlPartsText(parts []intlPart) string {
	var result strings.Builder
	for _, part := range parts {
		result.WriteString(part.text)
	}
	return result.String()
}
func appendIntlPart(parts []intlPart, kind, text string) []intlPart {
	if text == "" {
		return parts
	}
	if kind == "literal" && len(parts) > 0 && parts[len(parts)-1].kind == kind && parts[len(parts)-1].source == "" {
		parts[len(parts)-1].text += text
		return parts
	}
	return append(parts, intlPart{kind: kind, text: text})
}
func (r *Runtime) intlPartsArray(parts []intlPart, ranged bool) *Object {
	values := make([]Value, len(parts))
	for i, part := range parts {
		object := r.intlResult("type", part.kind, "value", part.text)
		if ranged {
			createDataPropertyOrThrow(object, asciiString("source"), newStringValue(part.source))
		}
		if part.unit != "" {
			createDataPropertyOrThrow(object, asciiString("unit"), newStringValue(part.unit))
		}
		values[i] = object
	}
	return r.newArrayValues(values)
}
func (r *Runtime) intlMathematicalValue(value Value) cldrdata.Decimal {
	if object, ok := value.(*Object); ok {
		value = object.toPrimitiveNumber()
	}
	if integer, ok := value.(*valueBigInt); ok {
		return cldrdata.DecimalFromInteger((*big.Int)(integer))
	}
	if text, ok := value.(String); ok {
		return cldrdata.DecimalFromString(text.String())
	}
	number := value.ToNumber()
	if f := number.ToFloat(); f == 0 && math.Signbit(f) {
		return cldrdata.Decimal{Coefficient: new(big.Int), Negative: true}
	}
	return cldrdata.DecimalFromString(number.toString().String())
}
func (r *Runtime) intlDigitOptions(options *Object, minimumFraction, maximumFraction int, notation string) cldrdata.DecimalRounding {
	result := cldrdata.DecimalRounding{MinimumInteger: r.intlNumberOption(options, "minimumIntegerDigits", 1, 21, 1)}
	get := func(name string) Value { return nilSafe(options.self.getStr(unistring.NewFromString(name), nil)) }
	mnfd, mxfd, mnsd, mxsd := get("minimumFractionDigits"), get("maximumFractionDigits"), get("minimumSignificantDigits"), get("maximumSignificantDigits")
	result.Increment = r.intlNumberOption(options, "roundingIncrement", 1, 5000, 1)
	if !slices.Contains([]int{1, 2, 5, 10, 20, 25, 50, 100, 200, 250, 500, 1000, 2000, 2500, 5000}, result.Increment) {
		r.intlRangeError("invalid roundingIncrement")
	}
	result.Mode = r.intlStringOption(options, "roundingMode", []string{"ceil", "floor", "expand", "trunc", "halfCeil", "halfFloor", "halfExpand", "halfTrunc", "halfEven"}, "halfExpand")
	priority := r.intlStringOption(options, "roundingPriority", []string{"auto", "morePrecision", "lessPrecision"}, "auto")
	result.TrailingZero = r.intlStringOption(options, "trailingZeroDisplay", []string{"auto", "stripIfInteger"}, "auto")
	if result.Increment != 1 {
		maximumFraction = minimumFraction
	}
	hasSignificant := !IsUndefined(mnsd) || !IsUndefined(mxsd)
	hasFraction := !IsUndefined(mnfd) || !IsUndefined(mxfd)
	needSignificant, needFraction := true, true
	if priority == "auto" {
		needSignificant = hasSignificant
		if needSignificant || !hasFraction && notation == "compact" {
			needFraction = false
		}
	}
	if needSignificant {
		result.MinimumSignificant = r.intlDefaultNumberOption(mnsd, "minimumSignificantDigits", 1, 21, 1)
		result.MaximumSignificant = r.intlDefaultNumberOption(mxsd, "maximumSignificantDigits", result.MinimumSignificant, 21, 21)
	}
	if needFraction {
		if hasFraction {
			mn := r.intlDefaultNumberOption(mnfd, "minimumFractionDigits", 0, 100, -1)
			mx := r.intlDefaultNumberOption(mxfd, "maximumFractionDigits", 0, 100, -1)
			if mn < 0 {
				mn = min(minimumFraction, mx)
			} else if mx < 0 {
				mx = max(maximumFraction, mn)
			} else if mn > mx {
				r.intlRangeError("minimumFractionDigits exceeds maximumFractionDigits")
			}
			result.MinimumFraction, result.MaximumFraction = mn, mx
		} else {
			result.MinimumFraction, result.MaximumFraction = minimumFraction, maximumFraction
		}
	}
	switch {
	case !needSignificant && !needFraction:
		result.MinimumSignificant = 1
		result.MaximumSignificant = 2
		result.Type = "morePrecision"
	case priority != "auto":
		result.Type = priority
	case hasSignificant:
		result.Type = "significant"
	default:
		result.Type = "fraction"
	}
	if result.Increment != 1 {
		if result.Type != "fraction" {
			panic(r.NewTypeError("roundingIncrement requires fraction rounding"))
		}
		if result.MaximumFraction != result.MinimumFraction {
			r.intlRangeError("roundingIncrement requires equal fraction limits")
		}
	}
	return result
}
func (r *Runtime) intlResolvedDigits(result *Object, options cldrdata.DecimalRounding) {
	put := func(name string, value Value) { createDataPropertyOrThrow(result, newStringValue(name), value) }
	put("minimumIntegerDigits", intToValue(int64(options.MinimumInteger)))
	if options.Type != "significant" {
		put("minimumFractionDigits", intToValue(int64(options.MinimumFraction)))
		put("maximumFractionDigits", intToValue(int64(options.MaximumFraction)))
	}
	if options.Type != "fraction" {
		put("minimumSignificantDigits", intToValue(int64(options.MinimumSignificant)))
		put("maximumSignificantDigits", intToValue(int64(options.MaximumSignificant)))
	}
}
func (r *Runtime) intlResolvedRounding(result *Object, options cldrdata.DecimalRounding) {
	priority := options.Type
	if priority == "significant" || priority == "fraction" {
		priority = "auto"
	}
	for _, property := range []struct {
		name  string
		value Value
	}{{"roundingIncrement", intToValue(int64(options.Increment))}, {"roundingMode", newStringValue(options.Mode)}, {"roundingPriority", newStringValue(priority)}, {"trailingZeroDisplay", newStringValue(options.TrailingZero)}} {
		createDataPropertyOrThrow(result, newStringValue(property.name), property.value)
	}
}

var intlNumberingSystems = sync.OnceValue(func() []string {
	data := cldrdata.Supplemental("numberingSystems").Get("numberingSystems")
	var systems []string
	for _, key := range data.Keys() {
		if data.Get(key, "_type").String() == "numeric" && len([]rune(data.Get(key, "_digits").String())) == 10 {
			systems = append(systems, key)
		}
	}
	return systems
})

func intlNumberingResolution(requested []string, option string) intlLocaleResolution {
	data := intlResolveLocale(requested, nil, nil).dataLocale
	defaultSystem := cldrdata.Locale(data, "numbers").Get("numbers", "defaultNumberingSystem").String()
	available := []string{defaultSystem}
	for _, system := range intlNumberingSystems() {
		if system != defaultSystem {
			available = append(available, system)
		}
	}
	options := make(map[string]string)
	if option != "" {
		options["nu"] = option
	}
	return intlResolveLocale(requested, map[string][]string{"nu": available}, options)
}
func intlLocalizeDigits(text, system string) string {
	if system == "latn" {
		return text
	}
	digits := []rune(cldrdata.Supplemental("numberingSystems").Get("numberingSystems", system, "_digits").String())
	if len(digits) != 10 {
		panic("missing numeric digits")
	}
	return strings.Map(func(c rune) rune {
		if c >= '0' && c <= '9' {
			return digits[c-'0']
		}
		return c
	}, text)
}
func intlNumericSymbols(locale, system string) cldrdata.Node {
	numbers := cldrdata.Locale(locale, "numbers").Get("numbers")
	symbols := numbers.Get("symbols-numberSystem-" + system)
	if !symbols.Exists() {
		if system == "arab" || system == "arabext" {
			symbols = cldrdata.Locale("ar", "numbers").Get("numbers", "symbols-numberSystem-arab")
		}
		if !symbols.Exists() {
			symbols = numbers.Get("symbols-numberSystem-latn")
		}
	}
	return symbols
}
func intlNumberData(locale, system, kind string) cldrdata.Node {
	numbers := cldrdata.Locale(locale, "numbers").Get("numbers")
	value := numbers.Get(kind + "-numberSystem-" + system)
	if !value.Exists() {
		value = numbers.Get(kind + "-numberSystem-latn")
	}
	return value
}
func intlCurrencyDigits(currency string) int {
	data := cldrdata.Supplemental("currencyData").Get("currencyData", "fractions")
	text := data.Get(currency, "_digits").String()
	if text == "" {
		text = data.Get("DEFAULT", "_digits").String()
	}
	digits, err := strconv.Atoi(text)
	if err != nil {
		return 2
	}
	return digits
}

var intlSimpleUnits = map[string]string{
	"acre": "area-acre", "bit": "digital-bit", "byte": "digital-byte", "celsius": "temperature-celsius", "centimeter": "length-centimeter", "day": "duration-day", "degree": "angle-degree", "fahrenheit": "temperature-fahrenheit", "fluid-ounce": "volume-fluid-ounce", "foot": "length-foot", "gallon": "volume-gallon", "gigabit": "digital-gigabit", "gigabyte": "digital-gigabyte", "gram": "mass-gram", "hectare": "area-hectare", "hour": "duration-hour", "inch": "length-inch", "kilobit": "digital-kilobit", "kilobyte": "digital-kilobyte", "kilogram": "mass-kilogram", "kilometer": "length-kilometer", "liter": "volume-liter", "megabit": "digital-megabit", "megabyte": "digital-megabyte", "meter": "length-meter", "microsecond": "duration-microsecond", "mile": "length-mile", "mile-scandinavian": "length-mile-scandinavian", "milliliter": "volume-milliliter", "millimeter": "length-millimeter", "millisecond": "duration-millisecond", "minute": "duration-minute", "month": "duration-month", "nanosecond": "duration-nanosecond", "ounce": "mass-ounce", "percent": "concentr-percent", "petabyte": "digital-petabyte", "pound": "mass-pound", "second": "duration-second", "stone": "mass-stone", "terabit": "digital-terabit", "terabyte": "digital-terabyte", "week": "duration-week", "yard": "length-yard", "year": "duration-year",
}

func intlValidUnit(unit string) bool {
	if intlSimpleUnits[unit] != "" {
		return true
	}
	left, right, ok := strings.Cut(unit, "-per-")
	return ok && intlSimpleUnits[left] != "" && intlSimpleUnits[right] != ""
}
func intlUnitPattern(locale, unit, style, category string) string {
	units := cldrdata.Locale(locale, "units").Get("units", style)
	pattern := func(node cldrdata.Node) string {
		value := node.Get("unitPattern-count-" + category).String()
		if value == "" {
			value = node.Get("unitPattern-count-other").String()
		}
		return value
	}
	if key := intlSimpleUnits[unit]; key != "" {
		return pattern(units.Get(key))
	}
	for _, key := range units.Keys() {
		if strings.HasSuffix(key, "-"+unit) {
			if p := pattern(units.Get(key)); p != "" {
				return p
			}
		}
	}
	left, right, _ := strings.Cut(unit, "-per-")
	numerator := intlUnitPattern(locale, left, style, category)
	denominator := units.Get(intlSimpleUnits[right])
	if per := denominator.Get("perUnitPattern").String(); per != "" {
		return strings.ReplaceAll(per, "{0}", numerator)
	}
	name := strings.TrimSpace(strings.ReplaceAll(pattern(denominator), "{0}", ""))
	per := units.Get("per", "compoundUnitPattern").String()
	return strings.ReplaceAll(strings.ReplaceAll(per, "{0}", numerator), "{1}", name)
}
func intlUnitAffix(parts []intlPart, text, kind string) []intlPart {
	left := strings.TrimLeftFunc(text, unicode.IsSpace)
	prefix := text[:len(text)-len(left)]
	trimmed := strings.TrimRightFunc(left, unicode.IsSpace)
	suffix := left[len(trimmed):]
	parts = appendIntlPart(parts, "literal", prefix)
	parts = appendIntlPart(parts, kind, trimmed)
	return appendIntlPart(parts, "literal", suffix)
}
func intlApplyUnitPattern(pattern string, number []intlPart, kind string) []intlPart {
	prefix, suffix, ok := strings.Cut(pattern, "{0}")
	if !ok {
		// Some CLDR singular/dual forms spell out the quantity in the unit
		// itself (for example Arabic "two hours"). Keep the sign, including
		// its direction marks, while omitting the redundant numeric field.
		var parts []intlPart
		for _, part := range number {
			if part.kind != "literal" && part.kind != "minusSign" && part.kind != "plusSign" {
				break
			}
			parts = append(parts, part)
		}
		return intlUnitAffix(parts, pattern, kind)
	}
	parts := intlUnitAffix(nil, prefix, kind)
	parts = append(parts, number...)
	return intlUnitAffix(parts, suffix, kind)
}
