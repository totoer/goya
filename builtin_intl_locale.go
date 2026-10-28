package goya

import (
	"slices"
	"strconv"
	"strings"

	"github.com/totoer/goya/internal/cldrdata"
	"github.com/totoer/goya/internal/tzdata"
)

type intlLocaleObject struct {
	baseObject
	id cldrdata.LocaleID
}

func (locale *intlLocaleObject) intlLocaleID() cldrdata.LocaleID { return locale.id }
func (r *Runtime) intlLocaleReceiver(value Value) *intlLocaleObject {
	if object, ok := value.(*Object); ok {
		if locale, ok := object.self.(*intlLocaleObject); ok {
			return locale
		}
	}
	panic(r.NewTypeError("incompatible Intl.Locale receiver"))
}
func (r *Runtime) newIntlLocale(args []Value, newTarget *Object) *Object {
	state := r.intlState()
	prototype := r.getPrototypeFromCtor(newTarget, state.constructors["Locale"], state.prototypes["Locale"])
	value := intlArgument(args, 0)
	var tag string
	if object, ok := value.(*Object); ok {
		if locale, ok := object.self.(intlLocaleProvider); ok {
			tag = locale.intlLocaleID().String()
		} else {
			tag = value.toString().String()
		}
	} else if text, ok := value.(String); ok {
		tag = text.String()
	} else {
		panic(r.NewTypeError("Intl.Locale requires a string or object tag"))
	}
	options := r.intlOptions(intlArgument(args, 1), true)
	id, valid := cldrdata.ParseLocale(tag)
	if !valid {
		r.intlRangeError("invalid language tag")
	}
	for _, name := range []string{"language", "script", "region"} {
		text, present := r.intlOptionalStringOption(options, name)
		if !present {
			continue
		}
		test := ""
		switch name {
		case "language":
			test = text
		case "script":
			test = "und-" + text
		case "region":
			test = "und-" + text
		}
		parsed, ok := cldrdata.ParseLocale(test)
		good := ok
		switch name {
		case "language":
			good = good && !strings.Contains(text, "-")
		case "script":
			good = good && parsed.Script != "" && parsed.Region == "" && len(parsed.Variants) == 0 && len(parsed.Extensions) == 0
		case "region":
			good = good && parsed.Region != "" && parsed.Script == "" && len(parsed.Variants) == 0 && len(parsed.Extensions) == 0
		}
		if !good {
			r.intlRangeError("invalid Locale " + name)
		}
		switch name {
		case "language":
			id.Language = strings.ToLower(text)
		case "script":
			id.Script = strings.ToUpper(text[:1]) + strings.ToLower(text[1:])
		case "region":
			id.Region = strings.ToUpper(text)
		}
	}
	// Recanonicalize the combined base rather than discarding parts supplied by
	// the original tag when a language alias contributes a script or region.
	base := id.BaseName()
	canonical, valid := cldrdata.ParseLocale(base)
	if !valid {
		r.intlRangeError("invalid Locale language identifier")
	}
	id.Language, id.Script, id.Region, id.Variants = canonical.Language, canonical.Script, canonical.Region, canonical.Variants
	for _, entry := range []struct {
		name, key string
		allowed   []string
	}{
		{"calendar", "ca", nil}, {"collation", "co", nil}, {"hourCycle", "hc", []string{"h11", "h12", "h23", "h24"}}, {"caseFirst", "kf", []string{"upper", "lower", "false"}},
	} {
		value, present := r.intlOptionalStringOption(options, entry.name)
		if !present {
			continue
		}
		if len(entry.allowed) > 0 {
			if !slices.Contains(entry.allowed, value) {
				r.intlRangeError("invalid Locale " + entry.name)
			}
		} else if !intlUnicodeType(value) {
			r.intlRangeError("invalid Locale " + entry.name)
		}
		id.SetUnicodeValue(entry.key, strings.ToLower(value))
	}
	if value := nilSafe(options.self.getStr("numeric", nil)); !IsUndefined(value) {
		text := "false"
		if value.ToBoolean() {
			text = "true"
		}
		id.SetUnicodeValue("kn", text)
	}
	if value, present := r.intlOptionalStringOption(options, "numberingSystem"); present {
		if !intlUnicodeType(value) {
			r.intlRangeError("invalid Locale numberingSystem")
		}
		id.SetUnicodeValue("nu", strings.ToLower(value))
	}
	object := &Object{runtime: r}
	locale := &intlLocaleObject{baseObject: baseObject{val: object, prototype: prototype, class: classObject, extensible: true}, id: id}
	object.self = locale
	locale.init()
	return object
}
func (locale *intlLocaleObject) region() string {
	if override, ok := locale.id.UnicodeValue("rg"); ok && strings.HasSuffix(override, "zzzz") {
		candidate := strings.TrimSuffix(override, "zzzz")
		if len(candidate) == 2 || len(candidate) == 3 {
			return strings.ToUpper(candidate)
		}
	}
	if locale.id.Region != "" {
		return locale.id.Region
	}
	return locale.id.Maximize().Region
}
func intlLocaleWeekday(day string) int {
	return slices.Index([]string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}, day) + 1
}
func (locale *intlLocaleObject) weekInfo() Value {
	region := locale.region()
	data := cldrdata.Supplemental("weekData").Get("weekData")
	get := func(key string) string {
		value := data.Get(key, region).String()
		if value == "" {
			value = data.Get(key, "001").String()
		}
		return value
	}
	first := intlLocaleWeekday(get("firstDay"))
	if override, ok := locale.id.UnicodeValue("fw"); ok {
		if day := intlLocaleWeekday(override); day > 0 {
			first = day
		}
	}
	start, end := intlLocaleWeekday(get("weekendStart")), intlLocaleWeekday(get("weekendEnd"))
	var weekend []Value
	for day := start; ; {
		weekend = append(weekend, intToValue(int64(day)))
		if day == end {
			break
		}
		day = day%7 + 1
	}
	minimum, _ := strconv.Atoi(get("minDays"))
	r := locale.val.runtime
	return r.intlResult("firstDay", first, "weekend", r.newArrayValues(weekend), "minimalDays", minimum)
}
func (locale *intlLocaleObject) localeData() string {
	_, data := intlLookupLocale(locale.id.String())
	if data == "" {
		return "en"
	}
	return data
}
func (locale *intlLocaleObject) infoList(key string) []string {
	if value, ok := locale.id.UnicodeValue(key); ok {
		return []string{value}
	}
	data := locale.localeData()
	switch key {
	case "ca":
		return []string{"gregory", "iso8601"}
	case "co":
		switch locale.id.Language {
		case "de":
			return []string{"phonebk"}
		case "zh":
			return []string{"pinyin", "stroke"}
		}
		return []string{}
	case "hc":
		region := locale.region()
		entry := cldrdata.Supplemental("timeData").Get("timeData", region)
		if !entry.Exists() {
			entry = cldrdata.Supplemental("timeData").Get("timeData", "001")
		}
		var result []string
		for _, value := range strings.Fields(entry.Get("_allowed").String()) {
			cycle := "h12"
			switch value[0] {
			case 'H':
				cycle = "h23"
			case 'k':
				cycle = "h24"
			case 'K':
				cycle = "h11"
			}
			if !slices.Contains(result, cycle) {
				result = append(result, cycle)
			}
		}
		if len(result) == 0 {
			return intlHourCycles(data)
		}
		return result
	case "nu":
		numbers := cldrdata.Locale(data, "numbers").Get("numbers")
		result := []string{numbers.Get("defaultNumberingSystem").String()}
		other := numbers.Get("otherNumberingSystems")
		for _, name := range other.Keys() {
			system := other.Get(name).String()
			if slices.Contains(intlNumberingSystems(), system) && !slices.Contains(result, system) {
				result = append(result, system)
			}
		}
		return result
	}
	return nil
}
func (r *Runtime) getIntlLocale() *Object {
	return r.intlConstructor("Locale", false, r.newIntlLocale, func(prototype *Object) {
		for _, name := range []string{"baseName", "language", "script", "region", "calendar", "collation", "hourCycle", "caseFirst", "numeric", "numberingSystem"} {
			prototype.self.defineOwnPropertyStr(newStringValue(name).string(), PropertyDescriptor{Configurable: FLAG_TRUE, Getter: r.newNativeFunc(func(call FunctionCall) Value {
				locale := r.intlLocaleReceiver(call.This)
				switch name {
				case "baseName":
					return newStringValue(locale.id.BaseName())
				case "language":
					return newStringValue(locale.id.Language)
				case "script":
					if locale.id.Script != "" {
						return newStringValue(locale.id.Script)
					}
					return _undefined
				case "region":
					if locale.id.Region != "" {
						return newStringValue(locale.id.Region)
					}
					return _undefined
				case "numeric":
					value, _ := locale.id.UnicodeValue("kn")
					return r.toBoolean(value == "true")
				}
				key := map[string]string{"calendar": "ca", "collation": "co", "hourCycle": "hc", "caseFirst": "kf", "numberingSystem": "nu"}[name]
				if value, ok := locale.id.UnicodeValue(key); ok {
					return newStringValue(value)
				}
				return _undefined
			}, newStringValue("get "+name).string(), 0)}, true)
		}
		prototype.self._putProp("toString", r.newNativeFunc(func(call FunctionCall) Value { return newStringValue(r.intlLocaleReceiver(call.This).id.String()) }, "toString", 0), true, false, true)
		for _, name := range []string{"maximize", "minimize"} {
			prototype.self._putProp(newStringValue(name).string(), r.newNativeFunc(func(call FunctionCall) Value {
				id := r.intlLocaleReceiver(call.This).id
				if name == "maximize" {
					id = id.Maximize()
				} else {
					id = id.Minimize()
				}
				return r.newIntlLocale([]Value{newStringValue(id.String())}, r.getIntlLocale())
			}, newStringValue(name).string(), 0), true, false, true)
		}
		for _, entry := range []struct{ name, key string }{{"getCalendars", "ca"}, {"getCollations", "co"}, {"getHourCycles", "hc"}, {"getNumberingSystems", "nu"}} {
			prototype.self._putProp(newStringValue(entry.name).string(), r.newNativeFunc(func(call FunctionCall) Value {
				return r.intlStringArray(r.intlLocaleReceiver(call.This).infoList(entry.key))
			}, newStringValue(entry.name).string(), 0), true, false, true)
		}
		prototype.self._putProp("getWeekInfo", r.newNativeFunc(func(call FunctionCall) Value { return r.intlLocaleReceiver(call.This).weekInfo() }, "getWeekInfo", 0), true, false, true)
		prototype.self._putProp("getTimeZones", r.newNativeFunc(func(call FunctionCall) Value {
			locale := r.intlLocaleReceiver(call.This)
			region := locale.id.Region
			if region == "" {
				return _undefined
			}
			return r.intlStringArray(tzdata.RegionNames(region))
		}, "getTimeZones", 0), true, false, true)
		prototype.self._putProp("getTextInfo", r.newNativeFunc(func(call FunctionCall) Value {
			locale := r.intlLocaleReceiver(call.This)
			script := locale.id.Maximize().Script
			direction := "ltr"
			if slices.Contains([]string{"Arab", "Hebr", "Gara", "Sidt", "Thaa", "Nkoo", "Adlm", "Rohg", "Syrc", "Mand", "Samr", "Mend", "Merc", "Mero", "Phnx", "Armi", "Avst", "Narb", "Nbat", "Palm", "Hatr", "Prti", "Phli", "Phlp", "Sarb", "Orkh", "Hung", "Lydi", "Khar", "Sogd", "Sogo", "Chrs", "Elym", "Yezi"}, script) {
				direction = "rtl"
			}
			return r.intlResult("direction", direction)
		}, "getTextInfo", 0), true, false, true)
	}, 1)
}
