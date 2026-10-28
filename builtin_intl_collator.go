package goya

import (
	"slices"
	"strings"
	"unicode"

	"github.com/totoer/goya/unistring"
	"golang.org/x/text/cases"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

type intlCollatorObject struct {
	baseObject
	locale, usage, sensitivity, collation, caseFirst string
	numeric, ignorePunctuation                       bool
	comparison, withoutCase                          *collate.Collator
	boundCompare                                     *Object
	caseFold                                         cases.Caser
}

func (r *Runtime) intlCollatorReceiver(value Value) *intlCollatorObject {
	if object, ok := value.(*Object); ok {
		if collator, ok := object.self.(*intlCollatorObject); ok {
			return collator
		}
	}
	panic(r.NewTypeError("incompatible Intl.Collator receiver"))
}
func (r *Runtime) newIntlCollator(args []Value, newTarget *Object) *Object {
	state := r.intlState()
	prototype := r.getPrototypeFromCtor(newTarget, state.constructors["Collator"], state.prototypes["Collator"])
	requested := r.intlCanonicalLocales(intlArgument(args, 0))
	options := r.intlOptions(intlArgument(args, 1), true)
	usage := r.intlStringOption(options, "usage", []string{"sort", "search"}, "sort")
	r.intlStringOption(options, "localeMatcher", []string{"lookup", "best fit"}, "best fit")
	collation := r.intlUnicodeOption(options, "collation")
	resolutionOptions := make(map[string]string)
	if collation != "" {
		resolutionOptions["co"] = collation
	}
	if value := nilSafe(options.self.getStr("numeric", nil)); !IsUndefined(value) {
		if value.ToBoolean() {
			resolutionOptions["kn"] = "true"
		} else {
			resolutionOptions["kn"] = "false"
		}
	}
	caseFirst := r.intlStringOption(options, "caseFirst", []string{"upper", "lower", "false"}, "")
	if caseFirst != "" {
		resolutionOptions["kf"] = caseFirst
	}
	// These specializations are backed by the pinned x/text collation tables.
	data := intlResolveLocale(requested, nil, nil).dataLocale
	available := []string{"default"}
	switch strings.Split(data, "-")[0] {
	case "de":
		available = append(available, "phonebk")
	case "zh":
		available = append(available, "pinyin", "stroke")
	}
	if usage == "search" {
		available = []string{"default"}
	}
	resolved := intlResolveLocale(requested, map[string][]string{"co": available, "kn": {"false", "true"}, "kf": {"false", "lower", "upper"}}, resolutionOptions)
	sensitivity := r.intlStringOption(options, "sensitivity", []string{"base", "accent", "case", "variant"}, "variant")
	ignorePunctuation := r.intlBooleanOption(options, "ignorePunctuation", strings.HasPrefix(data, "th"))
	var collateOptions []collate.Option
	switch sensitivity {
	case "base":
		collateOptions = append(collateOptions, collate.IgnoreCase, collate.IgnoreDiacritics)
	case "accent":
		collateOptions = append(collateOptions, collate.IgnoreCase)
	case "case":
		collateOptions = append(collateOptions, collate.IgnoreCase, collate.IgnoreDiacritics)
	}
	numeric := resolved.values["kn"] == "true"
	if numeric {
		collateOptions = append(collateOptions, collate.Numeric)
	}
	tag, _ := language.Parse(resolved.dataLocale)
	if resolved.values["co"] != "default" {
		tag, _ = tag.SetTypeForKey("co", resolved.values["co"])
	}
	if usage == "search" {
		tag, _ = tag.SetTypeForKey("co", "search")
	}
	withoutCaseOptions := append(slices.Clone(collateOptions), collate.IgnoreCase)
	object := &Object{runtime: r}
	collator := &intlCollatorObject{baseObject: baseObject{val: object, prototype: prototype, class: classObject, extensible: true}, locale: resolved.locale, usage: usage, sensitivity: sensitivity, collation: resolved.values["co"], caseFirst: resolved.values["kf"], numeric: numeric, ignorePunctuation: ignorePunctuation, comparison: collate.New(tag, collateOptions...), withoutCase: collate.New(tag, withoutCaseOptions...)}
	object.self = collator
	collator.caseFold = cases.Lower(tag)
	collator.init()
	return object
}
func (collator *intlCollatorObject) compare(left, right string) int {
	if collator.ignorePunctuation {
		strip := func(s string) string {
			return strings.Map(func(c rune) rune {
				if unicode.IsPunct(c) || unicode.IsSpace(c) {
					return -1
				}
				return c
			}, s)
		}
		left, right = strip(left), strip(right)
	}
	left, right = norm.NFD.String(left), norm.NFD.String(right)
	foldedLeft, foldedRight := collator.caseFold.String(left), collator.caseFold.String(right)
	if collator.sensitivity == "case" {
		if order := collator.comparison.CompareString(foldedLeft, foldedRight); order != 0 {
			return order
		}
		return intlCompareCase(left, right, collator.caseFirst)
	}
	if collator.sensitivity == "base" || collator.sensitivity == "accent" {
		return collator.comparison.CompareString(foldedLeft, foldedRight)
	}
	if collator.caseFirst != "false" && collator.sensitivity == "variant" && collator.withoutCase.CompareString(foldedLeft, foldedRight) == 0 {
		if order := intlCompareCase(left, right, collator.caseFirst); order != 0 {
			return order
		}
	}
	return collator.comparison.CompareString(left, right)
}
func intlCompareCase(left, right, caseFirst string) int {
	var leftCases, rightCases []bool
	for _, c := range left {
		if unicode.IsUpper(c) || unicode.IsLower(c) || unicode.IsTitle(c) {
			leftCases = append(leftCases, unicode.IsUpper(c) || unicode.IsTitle(c))
		}
	}
	for _, c := range right {
		if unicode.IsUpper(c) || unicode.IsLower(c) || unicode.IsTitle(c) {
			rightCases = append(rightCases, unicode.IsUpper(c) || unicode.IsTitle(c))
		}
	}
	for i := 0; i < min(len(leftCases), len(rightCases)); i++ {
		if leftCases[i] != rightCases[i] {
			if leftCases[i] == (caseFirst == "upper") {
				return -1
			}
			return 1
		}
	}
	return 0
}
func (r *Runtime) getIntlCollator() *Object {
	return r.intlConstructor("Collator", true, r.newIntlCollator, func(prototype *Object) {
		prototype.self._putProp("resolvedOptions", r.newNativeFunc(func(call FunctionCall) Value {
			c := r.intlCollatorReceiver(call.This)
			return r.intlResult("locale", c.locale, "usage", c.usage, "sensitivity", c.sensitivity, "ignorePunctuation", c.ignorePunctuation, "collation", c.collation, "numeric", c.numeric, "caseFirst", c.caseFirst)
		}, "resolvedOptions", 0), true, false, true)
		prototype.self.defineOwnPropertyStr("compare", PropertyDescriptor{Configurable: FLAG_TRUE, Getter: r.newNativeFunc(func(call FunctionCall) Value {
			c := r.intlCollatorReceiver(call.This)
			if c.boundCompare == nil {
				c.boundCompare = r.newNativeFunc(func(call FunctionCall) Value {
					left := call.Argument(0).toString().String()
					right := call.Argument(1).toString().String()
					return intToValue(int64(c.compare(left, right)))
				}, unistring.String(""), 2)
			}
			return c.boundCompare
		}, "get compare", 0)}, true)
	}, 0)
}
