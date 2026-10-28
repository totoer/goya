package goya

import (
	"github.com/totoer/goya/internal/cldrdata"
	"math"
)

type intlPluralRulesObject struct {
	baseObject
	locale, dataLocale, kind string
	digits                   cldrdata.DecimalRounding
}

func (r *Runtime) intlPluralRulesReceiver(value Value) *intlPluralRulesObject {
	if object, ok := value.(*Object); ok {
		if rules, ok := object.self.(*intlPluralRulesObject); ok {
			return rules
		}
	}
	panic(r.NewTypeError("incompatible Intl.PluralRules receiver"))
}
func (r *Runtime) newIntlPluralRules(args []Value, newTarget *Object) *Object {
	state := r.intlState()
	prototype := r.getPrototypeFromCtor(newTarget, state.constructors["PluralRules"], state.prototypes["PluralRules"])
	requested := r.intlCanonicalLocales(intlArgument(args, 0))
	options := r.intlOptions(intlArgument(args, 1), true)
	r.intlStringOption(options, "localeMatcher", []string{"lookup", "best fit"}, "best fit")
	resolved := intlResolveLocale(requested, nil, nil)
	kind := r.intlStringOption(options, "type", []string{"cardinal", "ordinal"}, "cardinal")
	digits := r.intlDigitOptions(options, 0, 3, "standard")
	object := &Object{runtime: r}
	rules := &intlPluralRulesObject{baseObject: baseObject{val: object, prototype: prototype, class: classObject, extensible: true}, locale: resolved.locale, dataLocale: resolved.dataLocale, kind: kind, digits: digits}
	object.self = rules
	rules.init()
	return object
}
func (rules *intlPluralRulesObject) resolve(number float64) (string, string) {
	if math.IsNaN(number) {
		return "other", "NaN"
	}
	if math.IsInf(number, 1) {
		return "other", "Infinity"
	}
	if math.IsInf(number, -1) {
		return "other", "-Infinity"
	}
	rounded := rules.digits.Round(cldrdata.DecimalFromFloat64(number))
	return cldrdata.PluralCategory(rules.dataLocale, rules.kind, rounded.Text, 0), rounded.Text
}
func (r *Runtime) getIntlPluralRules() *Object {
	return r.intlConstructor("PluralRules", false, r.newIntlPluralRules, func(prototype *Object) {
		prototype.self._putProp("resolvedOptions", r.newNativeFunc(func(call FunctionCall) Value {
			rules := r.intlPluralRulesReceiver(call.This)
			result := r.intlResult("locale", rules.locale, "type", rules.kind)
			r.intlResolvedDigits(result, rules.digits)
			createDataPropertyOrThrow(result, asciiString("pluralCategories"), r.intlStringArray(cldrdata.PluralCategories(rules.dataLocale, rules.kind)))
			r.intlResolvedRounding(result, rules.digits)
			return result
		}, "resolvedOptions", 0), true, false, true)
		prototype.self._putProp("select", r.newNativeFunc(func(call FunctionCall) Value {
			rules := r.intlPluralRulesReceiver(call.This)
			category, _ := rules.resolve(call.Argument(0).ToNumber().ToFloat())
			return newStringValue(category)
		}, "select", 1), true, false, true)
		prototype.self._putProp("selectRange", r.newNativeFunc(func(call FunctionCall) Value {
			rules := r.intlPluralRulesReceiver(call.This)
			start, end := call.Argument(0), call.Argument(1)
			if IsUndefined(start) || IsUndefined(end) {
				panic(r.NewTypeError("plural range endpoints are required"))
			}
			left, right := start.ToNumber().ToFloat(), end.ToNumber().ToFloat()
			if math.IsNaN(left) || math.IsNaN(right) {
				r.intlRangeError("invalid plural range")
			}
			first, firstText := rules.resolve(left)
			last, lastText := rules.resolve(right)
			if firstText == lastText {
				return newStringValue(first)
			}
			if rules.kind == "ordinal" {
				return asciiString("other")
			}
			return newStringValue(cldrdata.PluralRangeCategory(rules.dataLocale, first, last))
		}, "selectRange", 2), true, false, true)
	}, 0)
}
