package goya

import (
	"slices"
	"strings"

	"github.com/totoer/goya/internal/cldrdata"
)

type intlDisplayNamesObject struct {
	baseObject
	locale, dataLocale, style, kind, fallback, languageDisplay string
}

func (r *Runtime) intlDisplayNamesReceiver(value Value) *intlDisplayNamesObject {
	if object, ok := value.(*Object); ok {
		if format, ok := object.self.(*intlDisplayNamesObject); ok {
			return format
		}
	}
	panic(r.NewTypeError("incompatible Intl.DisplayNames receiver"))
}
func (r *Runtime) newIntlDisplayNames(args []Value, newTarget *Object) *Object {
	state := r.intlState()
	prototype := r.getPrototypeFromCtor(newTarget, state.constructors["DisplayNames"], state.prototypes["DisplayNames"])
	requested := r.intlCanonicalLocales(intlArgument(args, 0))
	if IsUndefined(intlArgument(args, 1)) {
		panic(r.NewTypeError("Intl.DisplayNames options are required"))
	}
	options := r.intlOptions(intlArgument(args, 1), false)
	r.intlStringOption(options, "localeMatcher", []string{"lookup", "best fit"}, "best fit")
	resolved := intlResolveLocale(requested, nil, nil)
	style := r.intlStringOption(options, "style", []string{"long", "short", "narrow"}, "long")
	kindValue := nilSafe(options.self.getStr("type", nil))
	if IsUndefined(kindValue) {
		panic(r.NewTypeError("Intl.DisplayNames type is required"))
	}
	kind := kindValue.toString().String()
	if !slices.Contains([]string{"language", "region", "script", "currency", "calendar", "dateTimeField"}, kind) {
		r.intlRangeError("invalid display name type")
	}
	fallback := r.intlStringOption(options, "fallback", []string{"code", "none"}, "code")
	languageDisplay := r.intlStringOption(options, "languageDisplay", []string{"dialect", "standard"}, "dialect")
	object := &Object{runtime: r}
	format := &intlDisplayNamesObject{baseObject: baseObject{val: object, prototype: prototype, class: classObject, extensible: true}, locale: resolved.locale, dataLocale: resolved.dataLocale, kind: kind, style: style, fallback: fallback, languageDisplay: languageDisplay}
	object.self = format
	format.init()
	return object
}
func intlASCIIAlpha(text string) bool {
	if text == "" {
		return false
	}
	for _, c := range []byte(text) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}
func intlASCIIDigit(text string) bool {
	if text == "" {
		return false
	}
	for _, c := range []byte(text) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

var intlDateTimeFieldCodes = map[string]string{"era": "era", "year": "year", "quarter": "quarter", "month": "month", "weekOfYear": "week", "weekday": "weekday", "day": "day", "dayPeriod": "dayperiod", "hour": "hour", "minute": "minute", "second": "second", "timeZoneName": "zone"}

func (f *intlDisplayNamesObject) canonicalCode(code string) string {
	valid := false
	switch f.kind {
	case "language":
		id, ok := cldrdata.ParseLocale(code)
		valid = ok && len(id.Extensions) == 0
		if valid {
			return id.String()
		}
	case "region":
		valid = len(code) == 2 && intlASCIIAlpha(code) || len(code) == 3 && intlASCIIDigit(code)
		code = strings.ToUpper(code)
	case "script":
		valid = len(code) == 4 && intlASCIIAlpha(code)
		if valid {
			code = strings.ToUpper(code[:1]) + strings.ToLower(code[1:])
		}
	case "currency":
		valid = len(code) == 3 && intlASCIIAlpha(code)
		code = strings.ToUpper(code)
	case "calendar":
		valid = intlUnicodeType(code)
		code = strings.ToLower(code)
	case "dateTimeField":
		_, valid = intlDateTimeFieldCodes[code]
	}
	if !valid {
		f.val.runtime.intlRangeError("invalid " + f.kind + " display name code")
	}
	return code
}
func (f *intlDisplayNamesObject) styled(node cldrdata.Node, key string) string {
	if f.style == "narrow" {
		if text := node.Get(key + "-alt-narrow").String(); text != "" {
			return text
		}
	}
	if f.style != "long" {
		if text := node.Get(key + "-alt-short").String(); text != "" {
			return text
		}
	}
	return node.Get(key).String()
}
func (f *intlDisplayNamesObject) languageName(code string) string {
	names := cldrdata.Locale(f.dataLocale, "languages").Get("localeDisplayNames", "languages")
	id, _ := cldrdata.ParseLocale(code)
	// CLDR dialect names can consume the complete identifier, or its language
	// and region/script pair, leaving the remaining qualifiers visible.
	name := ""
	if f.languageDisplay == "dialect" {
		name = f.styled(names, code)
		if name != "" {
			return name
		}
		for _, candidate := range []struct {
			code           string
			script, region bool
		}{
			{id.Language + "-" + id.Script + "-" + id.Region, true, true},
			{id.Language + "-" + id.Script, true, false},
			{id.Language + "-" + id.Region, false, true},
		} {
			if text := f.styled(names, candidate.code); text != "" {
				name = text
				if candidate.script {
					id.Script = ""
				}
				if candidate.region {
					id.Region = ""
				}
				break
			}
		}
	}
	if name == "" {
		name = f.styled(names, id.Language)
	}
	if name == "" {
		return ""
	}
	var qualifiers []string
	if id.Script != "" {
		text := f.styled(cldrdata.Locale(f.dataLocale, "scripts").Get("localeDisplayNames", "scripts"), id.Script)
		if text == "" {
			if f.fallback == "none" {
				return ""
			}
			text = id.Script
		}
		qualifiers = append(qualifiers, text)
	}
	if id.Region != "" {
		text := f.styled(cldrdata.Locale(f.dataLocale, "territories").Get("localeDisplayNames", "territories"), id.Region)
		if text == "" {
			if f.fallback == "none" {
				return ""
			}
			text = id.Region
		}
		qualifiers = append(qualifiers, text)
	}
	for _, variant := range id.Variants {
		text := f.styled(cldrdata.Locale(f.dataLocale, "variants").Get("localeDisplayNames", "variants"), strings.ToUpper(variant))
		if text == "" {
			if f.fallback == "none" {
				return ""
			}
			text = variant
		}
		qualifiers = append(qualifiers, text)
	}
	if len(qualifiers) == 0 {
		return name
	}
	patterns := cldrdata.Locale(f.dataLocale, "localeDisplayNames").Get("localeDisplayNames", "localeDisplayPattern")
	details := qualifiers[0]
	for _, qualifier := range qualifiers[1:] {
		details = intlDisplayPattern(patterns.Get("localeSeparator").String(), details, qualifier)
	}
	// Parentheses belonging to a qualifier must not collide with the outer pair.
	open, close := "(", ")"
	if strings.Contains(patterns.Get("localePattern").String(), "（") {
		open, close = "（", "）"
	}
	name = strings.ReplaceAll(strings.ReplaceAll(name, open, "["), close, "]")
	details = strings.ReplaceAll(strings.ReplaceAll(details, open, "["), close, "]")
	return intlDisplayPattern(patterns.Get("localePattern").String(), name, details)
}
func intlDisplayPattern(pattern, left, right string) string {
	return strings.NewReplacer("{0}", left, "{1}", right).Replace(pattern)
}
func (f *intlDisplayNamesObject) displayName(code string) string {
	switch f.kind {
	case "language":
		return f.languageName(code)
	case "region":
		return f.styled(cldrdata.Locale(f.dataLocale, "territories").Get("localeDisplayNames", "territories"), code)
	case "script":
		return f.styled(cldrdata.Locale(f.dataLocale, "scripts").Get("localeDisplayNames", "scripts"), code)
	case "currency":
		return cldrdata.Locale(f.dataLocale, "currencies").Get("numbers", "currencies", code, "displayName").String()
	case "calendar":
		key := code
		switch key {
		case "gregory":
			key = "gregorian"
		case "ethioaa":
			key = "ethiopic-amete-alem"
		}
		return f.styled(cldrdata.Locale(f.dataLocale, "localeDisplayNames").Get("localeDisplayNames", "types", "calendar"), key)
	case "dateTimeField":
		field := intlDateTimeFieldCodes[code]
		if f.style != "long" {
			field += "-" + f.style
		}
		fields := cldrdata.Locale(f.dataLocale, "dateFields").Get("dates", "fields")
		text := fields.Get(field, "displayName").String()
		if text == "" {
			text = fields.Get(intlDateTimeFieldCodes[code], "displayName").String()
		}
		return text
	}
	return ""
}
func (r *Runtime) getIntlDisplayNames() *Object {
	return r.intlConstructor("DisplayNames", false, r.newIntlDisplayNames, func(prototype *Object) {
		prototype.self._putProp("resolvedOptions", r.newNativeFunc(func(call FunctionCall) Value {
			f := r.intlDisplayNamesReceiver(call.This)
			object := r.intlResult("locale", f.locale, "style", f.style, "type", f.kind, "fallback", f.fallback)
			if f.kind == "language" {
				createDataPropertyOrThrow(object, asciiString("languageDisplay"), newStringValue(f.languageDisplay))
			}
			return object
		}, "resolvedOptions", 0), true, false, true)
		prototype.self._putProp("of", r.newNativeFunc(func(call FunctionCall) Value {
			f := r.intlDisplayNamesReceiver(call.This)
			code := f.canonicalCode(call.Argument(0).toString().String())
			if text := f.displayName(code); text != "" {
				return newStringValue(text)
			}
			if f.fallback == "code" {
				return newStringValue(code)
			}
			return _undefined
		}, "of", 1), true, false, true)
	}, 2)
}
