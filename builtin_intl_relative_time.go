package goya

import (
	"github.com/totoer/goya/internal/cldrdata"
	"math"
	"slices"
	"strconv"
	"strings"
)

type intlRelativeTimeFormatObject struct {
	baseObject
	locale, dataLocale, numberingSystem, style, numeric string
	number                                              *intlNumberFormatObject
	plural                                              *intlPluralRulesObject
}

func (r *Runtime) intlRelativeTimeReceiver(value Value) *intlRelativeTimeFormatObject {
	if object, ok := value.(*Object); ok {
		if format, ok := object.self.(*intlRelativeTimeFormatObject); ok {
			return format
		}
	}
	panic(r.NewTypeError("incompatible Intl.RelativeTimeFormat receiver"))
}
func (r *Runtime) newIntlRelativeTimeFormat(args []Value, newTarget *Object) *Object {
	state := r.intlState()
	prototype := r.getPrototypeFromCtor(newTarget, state.constructors["RelativeTimeFormat"], state.prototypes["RelativeTimeFormat"])
	requested := r.intlCanonicalLocales(intlArgument(args, 0))
	options := r.intlOptions(intlArgument(args, 1), true)
	r.intlStringOption(options, "localeMatcher", []string{"lookup", "best fit"}, "best fit")
	numbering := r.intlUnicodeOption(options, "numberingSystem")
	resolved := intlNumberingResolution(requested, numbering)
	style := r.intlStringOption(options, "style", []string{"long", "short", "narrow"}, "long")
	numeric := r.intlStringOption(options, "numeric", []string{"always", "auto"}, "always")
	nfOptions := r.newBaseObject(nil, classObject).val
	createDataPropertyOrThrow(nfOptions, asciiString("numberingSystem"), newStringValue(resolved.values["nu"]))
	formatter := r.newIntlNumberFormat([]Value{newStringValue(resolved.locale), nfOptions}, r.getIntlNumberFormat()).self.(*intlNumberFormatObject)
	rules := r.newIntlPluralRules([]Value{newStringValue(resolved.locale)}, r.getIntlPluralRules()).self.(*intlPluralRulesObject)
	object := &Object{runtime: r}
	format := &intlRelativeTimeFormatObject{baseObject: baseObject{val: object, prototype: prototype, class: classObject, extensible: true}, locale: resolved.locale, dataLocale: resolved.dataLocale, numberingSystem: resolved.values["nu"], style: style, numeric: numeric, number: formatter, plural: rules}
	object.self = format
	format.init()
	return object
}
func (format *intlRelativeTimeFormatObject) parts(value Value, unitValue Value) []intlPart {
	number := value.ToNumber().ToFloat()
	unit := unitValue.toString().String()
	if math.IsNaN(number) || math.IsInf(number, 0) {
		format.val.runtime.intlRangeError("relative time requires a finite value")
	}
	singular := strings.TrimSuffix(unit, "s")
	if !slices.Contains([]string{"year", "quarter", "month", "week", "day", "hour", "minute", "second"}, singular) {
		format.val.runtime.intlRangeError("invalid relative time unit")
	}
	fields := cldrdata.Locale(format.dataLocale, "dateFields").Get("dates", "fields")
	field := fields.Get(singular)
	if format.style != "long" {
		if alternate := fields.Get(singular + "-" + format.style); alternate.Exists() {
			field = alternate
		}
	}
	if format.numeric == "auto" {
		literal := field.Get("relative-type-" + strconv.FormatFloat(number, 'f', -1, 64)).String()
		if number == 0 {
			literal = field.Get("relative-type-0").String()
		}
		if literal != "" {
			return []intlPart{{kind: "literal", text: literal}}
		}
	}
	direction := "future"
	if math.Signbit(number) {
		direction = "past"
	}
	category, _ := format.plural.resolve(math.Abs(number))
	patterns := field.Get("relativeTime-type-" + direction)
	pattern := patterns.Get("relativeTimePattern-count-" + category).String()
	if pattern == "" {
		pattern = patterns.Get("relativeTimePattern-count-other").String()
	}
	numeric := format.number.formatParts(cldrdata.DecimalFromString(strconv.FormatFloat(math.Abs(number), 'g', -1, 64)))
	for i := range numeric {
		if numeric[i].kind != "literal" {
			numeric[i].unit = singular
		}
	}
	prefix, suffix, ok := strings.Cut(pattern, "{0}")
	if !ok {
		return []intlPart{{kind: "literal", text: pattern}}
	}
	var parts []intlPart
	parts = appendIntlPart(parts, "literal", prefix)
	parts = append(parts, numeric...)
	return appendIntlPart(parts, "literal", suffix)
}
func (r *Runtime) getIntlRelativeTimeFormat() *Object {
	return r.intlConstructor("RelativeTimeFormat", false, r.newIntlRelativeTimeFormat, func(prototype *Object) {
		prototype.self._putProp("resolvedOptions", r.newNativeFunc(func(call FunctionCall) Value {
			f := r.intlRelativeTimeReceiver(call.This)
			return r.intlResult("locale", f.locale, "style", f.style, "numeric", f.numeric, "numberingSystem", f.numberingSystem)
		}, "resolvedOptions", 0), true, false, true)
		for _, name := range []string{"format", "formatToParts"} {
			prototype.self._putProp(newStringValue(name).string(), r.newNativeFunc(func(call FunctionCall) Value {
				f := r.intlRelativeTimeReceiver(call.This)
				parts := f.parts(call.Argument(0), call.Argument(1))
				if name == "formatToParts" {
					return r.intlPartsArray(parts, false)
				}
				return newStringValue(intlPartsText(parts))
			}, newStringValue(name).string(), 2), true, false, true)
		}
	}, 0)
}
