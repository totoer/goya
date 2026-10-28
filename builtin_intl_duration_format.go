package goya

import (
	"math"
	"math/big"
	"strings"

	"github.com/totoer/goya/internal/cldrdata"
)

var intlDurationUnits = []string{"years", "months", "weeks", "days", "hours", "minutes", "seconds", "milliseconds", "microseconds", "nanoseconds"}

type intlDurationUnitOptions struct{ style, display string }
type intlDurationFormatObject struct {
	baseObject
	locale, dataLocale, numberingSystem, style string
	units                                      [10]intlDurationUnitOptions
	fractionalDigits                           int
	hourMinuteSeparator, minuteSecondSeparator string
	list                                       *intlListFormatObject
}
type intlDurationRecord struct {
	values [10]*big.Int
	sign   int
}

func (r *Runtime) intlDurationReceiver(value Value) *intlDurationFormatObject {
	if object, ok := value.(*Object); ok {
		if format, ok := object.self.(*intlDurationFormatObject); ok {
			return format
		}
	}
	panic(r.NewTypeError("incompatible Intl.DurationFormat receiver"))
}
func intlDurationNumeric(style string) bool { return style == "numeric" || style == "2-digit" }
func intlDurationSeparator(pattern string, left, right byte) string {
	first := strings.IndexByte(pattern, left)
	last := strings.IndexByte(pattern, right)
	if first < 0 || last <= first {
		panic("invalid embedded CLDR duration pattern")
	}
	for first < last && pattern[first] == left {
		first++
	}
	return strings.ReplaceAll(pattern[first:last], "'", "")
}
func (r *Runtime) newIntlDurationFormat(args []Value, newTarget *Object) *Object {
	state := r.intlState()
	prototype := r.getPrototypeFromCtor(newTarget, state.constructors["DurationFormat"], state.prototypes["DurationFormat"])
	requested := r.intlCanonicalLocales(intlArgument(args, 0))
	options := r.intlOptions(intlArgument(args, 1), false)
	r.intlStringOption(options, "localeMatcher", []string{"lookup", "best fit"}, "best fit")
	numbering := r.intlUnicodeOption(options, "numberingSystem")
	resolved := intlNumberingResolution(requested, numbering)
	style := r.intlStringOption(options, "style", []string{"long", "short", "narrow", "digital"}, "short")
	data := cldrdata.Locale(resolved.dataLocale, "units").Get("units")
	hm := data.Get("durationUnit-type-hm", "durationUnitPattern").String()
	ms := data.Get("durationUnit-type-ms", "durationUnitPattern").String()
	f := &intlDurationFormatObject{locale: resolved.locale, dataLocale: resolved.dataLocale, numberingSystem: resolved.values["nu"], style: style, hourMinuteSeparator: intlDurationSeparator(hm, 'h', 'm'), minuteSecondSeparator: intlDurationSeparator(ms, 'm', 's')}
	previous := ""
	for i, unit := range intlDurationUnits {
		allowed := []string{"long", "short", "narrow"}
		if i >= 4 {
			allowed = append(allowed, "numeric")
			if i <= 6 {
				allowed = append(allowed, "2-digit")
			}
		}
		unitStyle := r.intlStringOption(options, unit, allowed, "")
		displayDefault := "always"
		if unitStyle == "" {
			switch {
			case style == "digital":
				unitStyle = "short"
				if i >= 4 {
					unitStyle = "numeric"
				}
				if i < 4 || i > 6 {
					displayDefault = "auto"
				}
			case previous == "fractional" || intlDurationNumeric(previous):
				unitStyle = "numeric"
				if i != 5 && i != 6 {
					displayDefault = "auto"
				}
			default:
				unitStyle = style
				displayDefault = "auto"
			}
		}
		if i >= 7 && unitStyle == "numeric" {
			unitStyle = "fractional"
			displayDefault = "auto"
		}
		display := r.intlStringOption(options, unit+"Display", []string{"auto", "always"}, displayDefault)
		if display == "always" && unitStyle == "fractional" || previous == "fractional" && unitStyle != "fractional" || intlDurationNumeric(previous) && unitStyle != "fractional" && !intlDurationNumeric(unitStyle) {
			r.intlRangeError("incompatible duration unit style")
		}
		if i == 4 && strings.HasPrefix(hm, "hh") {
			unitStyle = "2-digit"
		}
		if (i == 5 || i == 6) && intlDurationNumeric(previous) {
			unitStyle = "2-digit"
		}
		f.units[i] = intlDurationUnitOptions{unitStyle, display}
		if i >= 4 && i <= 8 {
			previous = unitStyle
		}
	}
	f.fractionalDigits = r.intlNumberOption(options, "fractionalDigits", 0, 9, -1)
	listStyle := style
	if style == "digital" {
		listStyle = "short"
	}
	listOptions := r.newBaseObject(nil, classObject).val
	createDataPropertyOrThrow(listOptions, asciiString("type"), asciiString("unit"))
	createDataPropertyOrThrow(listOptions, asciiString("style"), newStringValue(listStyle))
	f.list = r.newIntlListFormat([]Value{newStringValue(f.locale), listOptions}, r.getIntlListFormat()).self.(*intlListFormatObject)
	object := &Object{runtime: r}
	f.baseObject = baseObject{val: object, prototype: prototype, class: classObject, extensible: true}
	object.self = f
	f.init()
	return object
}
func (r *Runtime) intlToDurationRecord(value Value) intlDurationRecord {
	object, ok := value.(*Object)
	if !ok {
		if _, isString := value.(String); isString {
			r.intlRangeError("duration strings are not accepted")
		}
		panic(r.NewTypeError("duration must be an object"))
	}
	var record intlDurationRecord
	present := false
	for _, index := range []int{3, 4, 8, 7, 5, 1, 9, 6, 2, 0} {
		value := nilSafe(object.self.getStr(newStringValue(intlDurationUnits[index]).string(), nil))
		integer := new(big.Int)
		if !IsUndefined(value) {
			present = true
			number := value.ToNumber().ToFloat()
			if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number {
				r.intlRangeError("duration fields must be finite integers")
			}
			new(big.Float).SetFloat64(number).Int(integer)
		}
		record.values[index] = integer
	}
	if !present {
		panic(r.NewTypeError("duration requires at least one field"))
	}
	for _, value := range record.values {
		sign := value.Sign()
		if sign == 0 {
			continue
		}
		if record.sign != 0 && sign != record.sign {
			r.intlRangeError("duration fields must have the same sign")
		}
		record.sign = sign
	}
	limit := new(big.Int).Lsh(big.NewInt(1), 32)
	for _, index := range []int{0, 1, 2} {
		if new(big.Int).Abs(record.values[index]).Cmp(limit) >= 0 {
			r.intlRangeError("duration calendar field exceeds its bound")
		}
	}
	nanos := new(big.Int)
	factors := []int64{86400000000000, 3600000000000, 60000000000, 1000000000, 1000000, 1000, 1}
	for i, factor := range factors {
		nanos.Add(nanos, new(big.Int).Mul(record.values[i+3], big.NewInt(factor)))
	}
	bound := new(big.Int).Mul(new(big.Int).Lsh(big.NewInt(1), 53), big.NewInt(1000000000))
	if new(big.Int).Abs(nanos).Cmp(bound) >= 0 {
		r.intlRangeError("duration exceeds its normalized seconds bound")
	}
	return record
}
func (f *intlDurationFormatObject) value(record intlDurationRecord, index int, fraction bool) cldrdata.Decimal {
	coefficient := new(big.Int).Set(record.values[index])
	exponent := 0
	if fraction {
		for next := index + 1; next < len(f.units); next++ {
			if f.units[next].style != "fractional" {
				break
			}
			coefficient.Mul(coefficient, big.NewInt(1000))
			coefficient.Add(coefficient, record.values[next])
			exponent -= 3
		}
	}
	value := cldrdata.DecimalFromInteger(coefficient)
	value.Exponent = exponent
	return value
}
func (f *intlDurationFormatObject) numberParts(value cldrdata.Decimal, index int, numeric, fraction, signDisplayed bool, recordSign int) []intlPart {
	r := f.val.runtime
	options := r.newBaseObject(nil, classObject).val
	set := func(key string, value Value) { createDataPropertyOrThrow(options, newStringValue(key), value) }
	set("numberingSystem", newStringValue(f.numberingSystem))
	if numeric {
		set("useGrouping", valueFalse)
		if f.units[index].style == "2-digit" {
			set("minimumIntegerDigits", intToValue(2))
		}
	} else {
		set("style", asciiString("unit"))
		set("unit", newStringValue(strings.TrimSuffix(intlDurationUnits[index], "s")))
		set("unitDisplay", newStringValue(f.units[index].style))
	}
	if fraction {
		minimum, maximum := 0, 9
		if f.fractionalDigits >= 0 {
			minimum = f.fractionalDigits
			maximum = minimum
		}
		set("minimumFractionDigits", intToValue(int64(minimum)))
		set("maximumFractionDigits", intToValue(int64(maximum)))
		set("roundingMode", asciiString("trunc"))
	}
	if !signDisplayed {
		set("signDisplay", asciiString("never"))
	} else if value.IsZero() && recordSign < 0 {
		value.Negative = true
	}
	number := r.newIntlNumberFormat([]Value{newStringValue(f.locale), options}, r.getIntlNumberFormat()).self.(*intlNumberFormatObject)
	parts := number.formatParts(value)
	for i := range parts {
		parts[i].unit = strings.TrimSuffix(intlDurationUnits[index], "s")
	}
	return parts
}
func (f *intlDurationFormatObject) numericParts(record intlDurationRecord, first int, signDisplayed bool) []intlPart {
	var displayed [3]bool
	values := [3]cldrdata.Decimal{f.value(record, 4, false), f.value(record, 5, false), f.value(record, 6, true)}
	displayed[0] = first == 4 && (!values[0].IsZero() || f.units[4].display == "always")
	displayed[2] = !values[2].IsZero() || f.units[6].display == "always"
	displayed[1] = first <= 5 && (displayed[0] && displayed[2] || !values[1].IsZero() || f.units[5].display == "always")
	var parts []intlPart
	for i := 0; i < 3; i++ {
		if !displayed[i] {
			continue
		}
		if i == 1 && displayed[0] {
			parts = append(parts, intlPart{kind: "literal", text: f.hourMinuteSeparator})
		}
		if i == 2 && displayed[1] {
			parts = append(parts, intlPart{kind: "literal", text: f.minuteSecondSeparator})
		}
		parts = append(parts, f.numberParts(values[i], i+4, true, i == 2, signDisplayed, record.sign)...)
		signDisplayed = false
	}
	return parts
}
func (f *intlDurationFormatObject) parts(record intlDurationRecord) []intlPart {
	var groups [][]intlPart
	signDisplayed := true
	for index, unit := range f.units {
		if intlDurationNumeric(unit.style) {
			if parts := f.numericParts(record, index, signDisplayed); len(parts) > 0 {
				groups = append(groups, parts)
			}
			break
		}
		fraction := index+1 < len(f.units) && f.units[index+1].style == "fractional"
		value := f.value(record, index, fraction)
		if !value.IsZero() || unit.display == "always" {
			groups = append(groups, f.numberParts(value, index, false, fraction, signDisplayed, record.sign))
			signDisplayed = false
		}
		if fraction {
			break
		}
	}
	strings := make([]String, len(groups))
	for i, group := range groups {
		strings[i] = newStringValue(intlPartsText(group))
	}
	var parts []intlPart
	index := 0
	for _, part := range f.list.parts(strings) {
		if part.kind == "element" {
			parts = append(parts, groups[index]...)
			index++
		} else {
			parts = append(parts, intlPart{kind: "literal", text: part.value.String()})
		}
	}
	return parts
}
func (r *Runtime) getIntlDurationFormat() *Object {
	return r.intlConstructor("DurationFormat", false, r.newIntlDurationFormat, func(prototype *Object) {
		prototype.self._putProp("resolvedOptions", r.newNativeFunc(func(call FunctionCall) Value {
			f := r.intlDurationReceiver(call.This)
			object := r.intlResult("locale", f.locale, "numberingSystem", f.numberingSystem, "style", f.style)
			for i, unit := range intlDurationUnits {
				style := f.units[i].style
				if style == "fractional" {
					style = "numeric"
				}
				createDataPropertyOrThrow(object, newStringValue(unit), newStringValue(style))
				createDataPropertyOrThrow(object, newStringValue(unit+"Display"), newStringValue(f.units[i].display))
			}
			if f.fractionalDigits >= 0 {
				createDataPropertyOrThrow(object, asciiString("fractionalDigits"), intToValue(int64(f.fractionalDigits)))
			}
			return object
		}, "resolvedOptions", 0), true, false, true)
		for _, name := range []string{"format", "formatToParts"} {
			prototype.self._putProp(newStringValue(name).string(), r.newNativeFunc(func(call FunctionCall) Value {
				f := r.intlDurationReceiver(call.This)
				parts := f.parts(r.intlToDurationRecord(call.Argument(0)))
				if name == "formatToParts" {
					return r.intlPartsArray(parts, false)
				}
				return newStringValue(intlPartsText(parts))
			}, newStringValue(name).string(), 1), true, false, true)
		}
	}, 0)
}
