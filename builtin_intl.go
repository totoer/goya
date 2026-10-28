package goya

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/totoer/goya/internal/cldrdata"
	"github.com/totoer/goya/unistring"
)

type intlRuntimeState struct {
	namespace    *Object
	constructors map[string]*Object
	prototypes   map[string]*Object
}
type intlLocaleProvider interface{ intlLocaleID() cldrdata.LocaleID }

func (r *Runtime) intlState() *intlRuntimeState {
	if r.internationalization == nil {
		r.internationalization = &intlRuntimeState{constructors: make(map[string]*Object), prototypes: make(map[string]*Object)}
	}
	return r.internationalization
}
func (r *Runtime) getIntl() *Object {
	state := r.intlState()
	if state.namespace == nil {
		object := r.NewObject()
		state.namespace = object
		object.self._putSym(SymToStringTag, valueProp(asciiString("Intl"), false, false, true))
		object.self._putProp("getCanonicalLocales", r.newNativeFunc(func(call FunctionCall) Value { return r.intlStringArray(r.intlCanonicalLocales(call.Argument(0))) }, "getCanonicalLocales", 1), true, false, true)
		object.self._putProp("supportedValuesOf", r.newNativeFunc(r.intlSupportedValues, "supportedValuesOf", 1), true, false, true)
		object.self._putProp("Locale", r.getIntlLocale(), true, false, true)
		object.self._putProp("DisplayNames", r.getIntlDisplayNames(), true, false, true)
		object.self._putProp("ListFormat", r.getIntlListFormat(), true, false, true)
		object.self._putProp("Segmenter", r.getIntlSegmenter(), true, false, true)
		object.self._putProp("DurationFormat", r.getIntlDurationFormat(), true, false, true)
		object.self._putProp("DateTimeFormat", r.getIntlDateTimeFormat(), true, false, true)
		object.self._putProp("Collator", r.getIntlCollator(), true, false, true)
		object.self._putProp("NumberFormat", r.getIntlNumberFormat(), true, false, true)
		object.self._putProp("PluralRules", r.getIntlPluralRules(), true, false, true)
		object.self._putProp("RelativeTimeFormat", r.getIntlRelativeTimeFormat(), true, false, true)
	}
	return state.namespace
}
func (r *Runtime) intlStringArray(strings []string) *Object {
	values := make([]Value, len(strings))
	for i, s := range strings {
		values[i] = newStringValue(s)
	}
	return r.newArrayValues(values)
}
func (r *Runtime) intlRangeError(message string) { panic(r.newError(r.getRangeError(), message)) }
func (r *Runtime) intlCanonicalLocales(value Value) []string {
	var result []string
	appendLocale := func(value Value) {
		var tag string
		if object, ok := value.(*Object); ok {
			if locale, ok := object.self.(intlLocaleProvider); ok {
				tag = locale.intlLocaleID().String()
			} else {
				tag = value.toString().String()
			}
		} else {
			if _, ok := value.(String); !ok {
				panic(r.NewTypeError("locale list elements must be strings or objects"))
			}
			tag = value.toString().String()
		}
		id, valid := cldrdata.ParseLocale(tag)
		if !valid {
			r.intlRangeError("invalid language tag: " + tag)
		}
		canonical := id.String()
		if !slices.Contains(result, canonical) {
			result = append(result, canonical)
		}
	}
	if IsUndefined(value) {
		return result
	}
	if _, ok := value.(String); ok {
		appendLocale(value)
		return result
	}
	if object, ok := value.(*Object); ok {
		if _, ok := object.self.(intlLocaleProvider); ok {
			appendLocale(value)
			return result
		}
	}
	object := value.ToObject(r)
	length := toLength(object.self.getStr("length", nil))
	for i := int64(0); i < length; i++ {
		r.vm.checkInterrupt()
		index := valueInt(i)
		if object.self.hasPropertyIdx(index) {
			appendLocale(nilSafe(object.self.getIdx(index, nil)))
		}
	}
	return result
}
func (r *Runtime) intlOptions(value Value, coerce bool) *Object {
	if IsUndefined(value) {
		return r.newBaseObject(nil, classObject).val
	}
	if object, ok := value.(*Object); ok {
		return object
	}
	if coerce {
		return value.ToObject(r)
	}
	panic(r.NewTypeError("Intl options must be an object"))
}
func (r *Runtime) intlStringOption(options *Object, name string, allowed []string, fallback string) string {
	value := nilSafe(options.self.getStr(unistring.NewFromString(name), nil))
	if IsUndefined(value) {
		return fallback
	}
	s := value.toString().String()
	if len(allowed) > 0 && !slices.Contains(allowed, s) {
		r.intlRangeError("invalid Intl option " + name)
	}
	return s
}
func (r *Runtime) intlOptionalStringOption(options *Object, name string) (string, bool) {
	value := nilSafe(options.self.getStr(unistring.NewFromString(name), nil))
	if IsUndefined(value) {
		return "", false
	}
	return value.toString().String(), true
}
func (r *Runtime) intlUnicodeOption(options *Object, name string) string {
	value, present := r.intlOptionalStringOption(options, name)
	if present && !intlUnicodeType(value) {
		r.intlRangeError("invalid Intl " + name)
	}
	return value
}
func (r *Runtime) intlBooleanOption(options *Object, name string, fallback bool) bool {
	value := nilSafe(options.self.getStr(unistring.NewFromString(name), nil))
	if IsUndefined(value) {
		return fallback
	}
	return value.ToBoolean()
}
func (r *Runtime) intlNumberOption(options *Object, name string, minimum, maximum, fallback int) int {
	return r.intlDefaultNumberOption(nilSafe(options.self.getStr(unistring.NewFromString(name), nil)), name, minimum, maximum, fallback)
}
func (r *Runtime) intlDefaultNumberOption(value Value, name string, minimum, maximum, fallback int) int {
	if IsUndefined(value) {
		return fallback
	}
	n := value.ToNumber().ToFloat()
	if math.IsNaN(n) || n < float64(minimum) || n > float64(maximum) {
		r.intlRangeError("invalid Intl numeric option " + name)
	}
	return int(math.Floor(n))
}
func intlUnicodeType(value string) bool {
	if value == "" {
		return false
	}
	for _, part := range strings.Split(value, "-") {
		if len(part) < 3 || len(part) > 8 {
			return false
		}
		for _, c := range []byte(part) {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
				return false
			}
		}
	}
	return true
}
func (r *Runtime) intlCheckUnicodeType(value, name string) {
	if value != "" && !intlUnicodeType(value) {
		r.intlRangeError("invalid Intl " + name)
	}
}

var intlAvailableLocales = sync.OnceValue(func() map[string]string {
	locales := make(map[string]string)
	for _, locale := range cldrdata.Locales() {
		locales[locale] = locale
	}
	locales["en-US"] = "en"
	return locales
})

func intlLookupLocale(request string) (string, string) {
	id, ok := cldrdata.ParseLocale(request)
	if !ok {
		return "", ""
	}
	base := id.BaseName()
	for base != "" {
		if data, ok := intlAvailableLocales()[base]; ok {
			return base, data
		}
		i := strings.LastIndexByte(base, '-')
		if i < 0 {
			break
		}
		base = base[:i]
	}
	return "", ""
}

type intlLocaleResolution struct {
	locale, dataLocale string
	values             map[string]string
}

func intlResolveLocale(requested []string, supported map[string][]string, options map[string]string) intlLocaleResolution {
	locale, dataLocale := "en-US", "en"
	var request cldrdata.LocaleID
	for _, candidate := range requested {
		if matched, data := intlLookupLocale(candidate); matched != "" {
			locale, dataLocale = matched, data
			request, _ = cldrdata.ParseLocale(candidate)
			break
		}
	}
	resolved, _ := cldrdata.ParseLocale(locale)
	result := intlLocaleResolution{dataLocale: dataLocale, values: make(map[string]string)}
	keys := make([]string, 0, len(supported))
	for key := range supported {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		available := supported[key]
		value := available[0]
		addition := ""
		if requestedValue, ok := request.UnicodeValue(key); ok && slices.Contains(available, requestedValue) {
			value = requestedValue
			addition = requestedValue
		}
		if option, ok := options[key]; ok && slices.Contains(available, option) && option != value {
			value = option
			addition = ""
		}
		result.values[key] = value
		if addition != "" {
			resolved.SetUnicodeValue(key, addition)
		}
	}
	result.locale = resolved.String()
	return result
}
func (r *Runtime) intlSupportedLocales(call FunctionCall) Value {
	requested := r.intlCanonicalLocales(call.Argument(0))
	options := r.intlOptions(call.Argument(1), true)
	r.intlStringOption(options, "localeMatcher", []string{"lookup", "best fit"}, "best fit")
	supported := make([]string, 0, len(requested))
	for _, locale := range requested {
		if match, _ := intlLookupLocale(locale); match != "" {
			supported = append(supported, locale)
		}
	}
	return r.intlStringArray(supported)
}
func (r *Runtime) intlConstructor(name string, callable bool, build func([]Value, *Object) *Object, configure func(*Object), length int) *Object {
	state := r.intlState()
	if ctor := state.constructors[name]; ctor != nil {
		return ctor
	}
	ctor := &Object{runtime: r}
	prototype := r.NewObject()
	state.constructors[name] = ctor
	state.prototypes[name] = prototype
	r.newNativeFuncAndConstruct(ctor, func(call FunctionCall) Value {
		if !callable {
			panic(r.NewTypeError("Intl.%s requires new", name))
		}
		return build(call.Arguments, ctor)
	}, func(args []Value, newTarget *Object) *Object {
		if newTarget == nil {
			newTarget = ctor
		}
		return build(args, newTarget)
	}, prototype, unistring.NewFromString(name), intToValue(int64(length)))
	prototype.self._putProp("constructor", ctor, true, false, true)
	prototype.self._putSym(SymToStringTag, valueProp(newStringValue("Intl."+name), false, false, true))
	if name != "Locale" {
		ctor.self._putProp("supportedLocalesOf", r.newNativeFunc(r.intlSupportedLocales, "supportedLocalesOf", 1), true, false, true)
	}
	configure(prototype)
	return ctor
}
func intlArgument(args []Value, index int) Value {
	if index < len(args) {
		return args[index]
	}
	return _undefined
}
func (r *Runtime) intlResult(properties ...any) *Object {
	object := r.NewObject()
	for i := 0; i < len(properties); i += 2 {
		var value Value
		switch v := properties[i+1].(type) {
		case Value:
			value = v
		case string:
			value = newStringValue(v)
		case bool:
			value = r.toBoolean(v)
		case int:
			value = intToValue(int64(v))
		default:
			panic("invalid internal Intl result value")
		}
		createDataPropertyOrThrow(object, newStringValue(properties[i].(string)), value)
	}
	return object
}
func intlIndexString(index int) string { return strconv.Itoa(index) }
