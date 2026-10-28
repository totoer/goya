package goya

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/totoer/goya/internal/cldrdata"
)

type intlDateTimeFormatObject struct {
	baseObject
	locale, dataLocale, calendar, numberingSystem, timeZoneName, hourCycle string
	location                                                               *time.Location
	dateStyle, timeStyle                                                   string
	tokens                                                                 []intlDateToken
	fields                                                                 map[string]string
	boundFormat                                                            *Object
}

func (r *Runtime) intlDateTimeReceiver(value Value) *intlDateTimeFormatObject {
	if object, ok := value.(*Object); ok {
		if formatter, ok := object.self.(*intlDateTimeFormatObject); ok {
			return formatter
		}
	}
	panic(r.NewTypeError("incompatible Intl.DateTimeFormat receiver"))
}
func (r *Runtime) newIntlDateTimeFormat(args []Value, newTarget *Object) *Object {
	return r.createIntlDateTimeFormat(args, newTarget, "any", "date")
}
func (r *Runtime) createIntlDateTimeFormat(args []Value, newTarget *Object, required, defaults string) *Object {
	state := r.intlState()
	prototype := r.getPrototypeFromCtor(newTarget, state.constructors["DateTimeFormat"], state.prototypes["DateTimeFormat"])
	requested := r.intlCanonicalLocales(intlArgument(args, 0))
	options := r.intlOptions(intlArgument(args, 1), true)
	r.intlStringOption(options, "localeMatcher", []string{"lookup", "best fit"}, "best fit")
	calendar := r.intlUnicodeOption(options, "calendar")
	numbering := r.intlUnicodeOption(options, "numberingSystem")
	hour12Value := nilSafe(options.self.getStr("hour12", nil))
	hour12Specified := !IsUndefined(hour12Value)
	hour12 := false
	if hour12Specified {
		hour12 = hour12Value.ToBoolean()
	}
	hourCycle := r.intlStringOption(options, "hourCycle", []string{"h11", "h12", "h23", "h24"}, "")
	dataLocale := intlResolveLocale(requested, nil, nil).dataLocale
	nuDefault := cldrdata.Locale(dataLocale, "numbers").Get("numbers", "defaultNumberingSystem").String()
	numberingSystems := []string{nuDefault}
	for _, system := range intlNumberingSystems() {
		if system != nuDefault {
			numberingSystems = append(numberingSystems, system)
		}
	}
	resolutionOptions := make(map[string]string)
	if calendar != "" {
		resolutionOptions["ca"] = calendar
	}
	if numbering != "" {
		resolutionOptions["nu"] = numbering
	}
	if hourCycle != "" {
		resolutionOptions["hc"] = hourCycle
	}
	if hour12Specified {
		cycle := intlDefaultHourCycle(dataLocale)
		if hour12 {
			if cycle != "h11" {
				cycle = "h12"
			}
		} else {
			if cycle != "h24" {
				cycle = "h23"
			}
		}
		resolutionOptions["hc"] = cycle
	}
	resolved := intlResolveLocale(requested, map[string][]string{"ca": {"gregory", "iso8601"}, "nu": numberingSystems, "hc": intlHourCycles(dataLocale)}, resolutionOptions)
	if hour12Specified {
		id, _ := cldrdata.ParseLocale(resolved.locale)
		id.SetUnicodeValue("hc", "")
		resolved.locale = id.String()
	}
	f := &intlDateTimeFormatObject{locale: resolved.locale, dataLocale: resolved.dataLocale, calendar: resolved.values["ca"], numberingSystem: resolved.values["nu"], hourCycle: resolved.values["hc"], timeZoneName: r.timeZoneName, location: r.timeZone}
	zoneValue := nilSafe(options.self.getStr("timeZone", nil))
	if IsUndefined(zoneValue) {
		f.timeZoneName, f.location = r.timeZoneName, r.timeZone
	} else {
		name, location, ok := intlResolveTimeZone(zoneValue.toString().String())
		if !ok {
			r.intlRangeError("invalid timeZone")
		}
		f.timeZoneName, f.location = name, location
	}

	fields := make(map[string]string)
	for _, row := range []struct {
		name    string
		allowed []string
	}{
		{"weekday", []string{"narrow", "short", "long"}}, {"era", []string{"narrow", "short", "long"}}, {"year", []string{"2-digit", "numeric"}}, {"month", []string{"2-digit", "numeric", "narrow", "short", "long"}}, {"day", []string{"2-digit", "numeric"}}, {"dayPeriod", []string{"narrow", "short", "long"}}, {"hour", []string{"2-digit", "numeric"}}, {"minute", []string{"2-digit", "numeric"}}, {"second", []string{"2-digit", "numeric"}},
	} {
		if value := r.intlStringOption(options, row.name, row.allowed, ""); value != "" {
			fields[row.name] = value
		}
	}
	if digits := r.intlNumberOption(options, "fractionalSecondDigits", 1, 3, -1); digits >= 0 {
		fields["fractionalSecondDigits"] = strings.Repeat("S", digits)
	}
	if zone := r.intlStringOption(options, "timeZoneName", []string{"short", "long", "shortOffset", "longOffset", "shortGeneric", "longGeneric"}, ""); zone != "" {
		fields["timeZoneName"] = zone
	}
	matcher := r.intlStringOption(options, "formatMatcher", []string{"basic", "best fit"}, "best fit")
	f.dateStyle = r.intlStringOption(options, "dateStyle", []string{"full", "long", "medium", "short"}, "")
	f.timeStyle = r.intlStringOption(options, "timeStyle", []string{"full", "long", "medium", "short"}, "")
	if f.dateStyle != "" || f.timeStyle != "" {
		if len(fields) > 0 {
			panic(r.NewTypeError("dateStyle/timeStyle cannot be combined with component options"))
		}
		if required == "date" && f.timeStyle != "" || required == "time" && f.dateStyle != "" {
			panic(r.NewTypeError("incompatible Date locale style"))
		}
	}
	calendarData := cldrdata.Locale(f.dataLocale, "ca-gregorian").Get("dates", "calendars", "gregorian")
	if f.dateStyle != "" || f.timeStyle != "" {
		var dateTokens, timeTokens []intlDateToken
		if f.dateStyle != "" {
			dateTokens = intlDateTokens(intlPatternString(calendarData.Get("dateFormats", f.dateStyle)))
		}
		if f.timeStyle != "" {
			timeTokens = intlAdjustDateTokens(intlDateTokens(intlPatternString(calendarData.Get("timeFormats", f.timeStyle))), nil, f.hourCycle)
		}
		if len(dateTokens) > 0 && len(timeTokens) > 0 {
			f.tokens = intlJoinDatePatterns(intlPatternString(calendarData.Get("dateTimeFormats", f.dateStyle)), dateTokens, timeTokens)
		} else {
			f.tokens = append(dateTokens, timeTokens...)
		}
	} else {
		needDefaults := true
		if required == "date" || required == "any" {
			for _, key := range []string{"weekday", "year", "month", "day"} {
				if fields[key] != "" {
					needDefaults = false
				}
			}
		}
		if required == "time" || required == "any" {
			for _, key := range []string{"dayPeriod", "hour", "minute", "second", "fractionalSecondDigits"} {
				if fields[key] != "" {
					needDefaults = false
				}
			}
		}
		if needDefaults {
			if defaults == "date" || defaults == "all" {
				for _, key := range []string{"year", "month", "day"} {
					fields[key] = "numeric"
				}
			}
			if defaults == "time" || defaults == "all" {
				for _, key := range []string{"hour", "minute", "second"} {
					fields[key] = "numeric"
				}
			}
		}
		dateFields, timeFields := make(map[string]string), make(map[string]string)
		for key, value := range fields {
			if slices.Contains([]string{"weekday", "era", "year", "month", "day"}, key) {
				dateFields[key] = value
			} else {
				timeFields[key] = value
			}
		}
		// A timezone name accompanies the date when no clock fields were requested.
		if len(timeFields) == 1 && timeFields["timeZoneName"] != "" {
			dateFields["timeZoneName"] = timeFields["timeZoneName"]
			clear(timeFields)
		}
		dateTokens := intlDatePattern(calendarData, dateFields, f.hourCycle, matcher)
		timeTokens := intlDatePattern(calendarData, timeFields, f.hourCycle, matcher)
		if len(dateTokens) > 0 && len(timeTokens) > 0 {
			f.tokens = intlJoinDatePatterns(intlPatternString(calendarData.Get("dateTimeFormats", "medium")), dateTokens, timeTokens)
		} else {
			f.tokens = append(dateTokens, timeTokens...)
		}
	}
	var valid bool
	f.fields, valid = intlDateFields(f.tokens)
	if !valid || len(f.tokens) == 0 {
		panic("invalid embedded DateTimeFormat pattern")
	}
	object := &Object{runtime: r}
	f.baseObject = baseObject{val: object, prototype: prototype, class: classObject, extensible: true}
	object.self = f
	f.init()
	return object
}
func intlDateWidth(width int) string {
	if width >= 5 {
		return "narrow"
	}
	if width == 4 {
		return "wide"
	}
	return "abbreviated"
}
func intlDayPeriod(locale string, instant time.Time, flexible bool) string {
	if !flexible {
		if instant.Hour() < 12 {
			return "am"
		}
		return "pm"
	}
	id, _ := cldrdata.ParseLocale(locale)
	rules := cldrdata.Supplemental("dayPeriods").Get("dayPeriodRuleSet", locale)
	if !rules.Exists() {
		rules = cldrdata.Supplemental("dayPeriods").Get("dayPeriodRuleSet", id.Language)
	}
	minute := instant.Hour()*60 + instant.Minute()
	parse := func(text string) int {
		hours, minutes, _ := strings.Cut(text, ":")
		h, _ := strconv.Atoi(hours)
		m, _ := strconv.Atoi(minutes)
		return h*60 + m
	}
	for _, name := range rules.Keys() {
		if at := rules.Get(name, "_at").String(); at != "" && minute == parse(at) && instant.Second() == 0 && instant.Nanosecond() == 0 {
			return name
		}
	}
	for _, name := range rules.Keys() {
		rule := rules.Get(name)
		from, before := rule.Get("_from").String(), rule.Get("_before").String()
		if from == "" || before == "" {
			continue
		}
		start, end := parse(from), parse(before)
		if start < end && minute >= start && minute < end || start >= end && (minute >= start || minute < end) {
			return name
		}
	}
	if instant.Hour() < 12 {
		return "am"
	}
	return "pm"
}
func (f *intlDateTimeFormatObject) parts(milliseconds int64) []intlPart {
	instant := timeFromMsec(milliseconds).In(f.location)
	calendar := cldrdata.Locale(f.dataLocale, "ca-gregorian").Get("dates", "calendars", "gregorian")
	var parts []intlPart
	for _, token := range f.tokens {
		if token.letter == 0 {
			text := token.literal
			if text == "{decimal}" {
				text = intlNumericSymbols(f.dataLocale, f.numberingSystem).Get("decimal").String()
			}
			parts = appendIntlPart(parts, "literal", text)
			continue
		}
		field, _ := intlDateTokenField(token)
		text := ""
		numeric := false
		number := 0
		switch token.letter {
		case 'y', 'u':
			number = instant.Year()
			if number <= 0 {
				number = 1 - number
			}
			if token.width == 2 {
				number %= 100
			}
			numeric = true
		case 'M', 'L':
			if token.width < 3 {
				number = int(instant.Month())
				numeric = true
			} else {
				context := "format"
				if token.letter == 'L' {
					context = "stand-alone"
				}
				text = calendar.Get("months", context, intlDateWidth(token.width), strconv.Itoa(int(instant.Month()))).String()
			}
		case 'd':
			number = instant.Day()
			numeric = true
		case 'E', 'e', 'c':
			context := "format"
			if token.letter == 'c' {
				context = "stand-alone"
			}
			key := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}[int(instant.Weekday())]
			text = calendar.Get("days", context, intlDateWidth(token.width), key).String()
		case 'G':
			key := "1"
			if instant.Year() <= 0 {
				key = "0"
			}
			width := "eraAbbr"
			if token.width == 4 {
				width = "eraNames"
			} else if token.width >= 5 {
				width = "eraNarrow"
			}
			text = calendar.Get("eras", width, key).String()
		case 'h':
			number = instant.Hour() % 12
			if number == 0 {
				number = 12
			}
			numeric = true
		case 'K':
			number = instant.Hour() % 12
			numeric = true
		case 'H':
			number = instant.Hour()
			numeric = true
		case 'k':
			number = instant.Hour()
			if number == 0 {
				number = 24
			}
			numeric = true
		case 'm':
			number = instant.Minute()
			numeric = true
		case 's':
			number = instant.Second()
			numeric = true
		case 'S':
			field = "fractionalSecond"
			text = fmt.Sprintf("%03d", instant.Nanosecond()/1e6)[:token.width]
			text = intlLocalizeDigits(text, f.numberingSystem)
		case 'a', 'B', 'b':
			field = "dayPeriod"
			key := intlDayPeriod(f.dataLocale, instant, token.letter != 'a')
			text = calendar.Get("dayPeriods", "format", intlDateWidth(token.width), key).String()
			if text == "" {
				text = calendar.Get("dayPeriods", "format", "wide", key).String()
			}
		case 'z', 'v', 'O':
			_, style := intlDateTokenField(token)
			text = intlTimeZoneDisplay(f.dataLocale, f.numberingSystem, f.timeZoneName, style, instant)
		}
		if numeric {
			text = strconv.Itoa(number)
			if token.width == 2 && len(text) < 2 {
				text = "0" + text
			}
			text = intlLocalizeDigits(text, f.numberingSystem)
		}
		if text == "" {
			panic("missing embedded datetime field")
		}
		parts = appendIntlPart(parts, field, text)
	}
	return parts
}
func (f *intlDateTimeFormatObject) argument(value Value) int64 {
	r := f.val.runtime
	var number float64
	if IsUndefined(value) {
		if clipped, ok := timeToMsecClip(r.now()); ok {
			return clipped
		}
		number = math.NaN()
	} else {
		number = value.ToNumber().ToFloat()
	}
	clipped, ok := dateClip(number)
	if !ok {
		r.intlRangeError("invalid date value")
	}
	return clipped
}
func (f *intlDateTimeFormatObject) resolvedOptions() *Object {
	r := f.val.runtime
	result := r.intlResult("locale", f.locale, "calendar", f.calendar, "numberingSystem", f.numberingSystem, "timeZone", f.timeZoneName)
	put := func(name string, value Value) { createDataPropertyOrThrow(result, newStringValue(name), value) }
	if f.fields["hour"] != "" {
		put("hourCycle", newStringValue(f.hourCycle))
		put("hour12", r.toBoolean(f.hourCycle == "h11" || f.hourCycle == "h12"))
	}
	if f.dateStyle != "" || f.timeStyle != "" {
		if f.dateStyle != "" {
			put("dateStyle", newStringValue(f.dateStyle))
		}
		if f.timeStyle != "" {
			put("timeStyle", newStringValue(f.timeStyle))
		}
		return result
	}
	for _, name := range []string{"weekday", "era", "year", "month", "day", "dayPeriod", "hour", "minute", "second", "fractionalSecondDigits", "timeZoneName"} {
		if value := f.fields[name]; value != "" {
			if name == "fractionalSecondDigits" {
				put(name, intToValue(int64(len(value))))
			} else {
				put(name, newStringValue(value))
			}
		}
	}
	return result
}
func (f *intlDateTimeFormatObject) rangeParts(start, end Value) []intlPart {
	r := f.val.runtime
	if IsUndefined(start) || IsUndefined(end) {
		panic(r.NewTypeError("date range endpoints are required"))
	}
	startNumber, endNumber := start.ToNumber().ToFloat(), end.ToNumber().ToFloat()
	first, firstValid := dateClip(startNumber)
	last, lastValid := dateClip(endNumber)
	if !firstValid || !lastValid {
		r.intlRangeError("invalid date range")
	}
	if first > last {
		r.intlRangeError("date range is reversed")
	}
	left, right := f.parts(first), f.parts(last)
	if intlPartsText(left) == intlPartsText(right) {
		for i := range left {
			left[i].source = "shared"
		}
		return left
	}
	for i := range left {
		left[i].source = "startRange"
	}
	for i := range right {
		right[i].source = "endRange"
	}
	pattern := cldrdata.Locale(f.dataLocale, "ca-gregorian").Get("dates", "calendars", "gregorian", "dateTimeFormats", "intervalFormats", "intervalFormatFallback").String()
	prefix, rest, _ := strings.Cut(pattern, "{0}")
	between, suffix, _ := strings.Cut(rest, "{1}")
	var parts []intlPart
	if prefix != "" {
		parts = append(parts, intlPart{kind: "literal", text: prefix, source: "shared"})
	}
	parts = append(parts, left...)
	parts = append(parts, intlPart{kind: "literal", text: between, source: "shared"})
	parts = append(parts, right...)
	if suffix != "" {
		parts = append(parts, intlPart{kind: "literal", text: suffix, source: "shared"})
	}
	return parts
}
func (r *Runtime) getIntlDateTimeFormat() *Object {
	return r.intlConstructor("DateTimeFormat", true, r.newIntlDateTimeFormat, func(prototype *Object) {
		prototype.self._putProp("resolvedOptions", r.newNativeFunc(func(call FunctionCall) Value { return r.intlDateTimeReceiver(call.This).resolvedOptions() }, "resolvedOptions", 0), true, false, true)
		prototype.self.defineOwnPropertyStr("format", PropertyDescriptor{Configurable: FLAG_TRUE, Getter: r.newNativeFunc(func(call FunctionCall) Value {
			f := r.intlDateTimeReceiver(call.This)
			if f.boundFormat == nil {
				f.boundFormat = r.newNativeFunc(func(call FunctionCall) Value {
					return newStringValue(intlPartsText(f.parts(f.argument(call.Argument(0)))))
				}, "", 1)
			}
			return f.boundFormat
		}, "get format", 0)}, true)
		prototype.self._putProp("formatToParts", r.newNativeFunc(func(call FunctionCall) Value {
			f := r.intlDateTimeReceiver(call.This)
			return r.intlPartsArray(f.parts(f.argument(call.Argument(0))), false)
		}, "formatToParts", 1), true, false, true)
		for _, name := range []string{"formatRange", "formatRangeToParts"} {
			prototype.self._putProp(newStringValue(name).string(), r.newNativeFunc(func(call FunctionCall) Value {
				f := r.intlDateTimeReceiver(call.This)
				parts := f.rangeParts(call.Argument(0), call.Argument(1))
				if name == "formatRangeToParts" {
					return r.intlPartsArray(parts, true)
				}
				return newStringValue(intlPartsText(parts))
			}, newStringValue(name).string(), 2), true, false, true)
		}
	}, 0)
}

func (r *Runtime) dateLocaleFormat(call FunctionCall, defaults string) Value {
	object, ok := call.This.(*Object)
	if !ok {
		panic(r.NewTypeError("Date locale formatting requires a Date"))
	}
	date, ok := object.self.(*dateObject)
	if !ok {
		panic(r.NewTypeError("Date locale formatting requires a Date"))
	}
	if !date.isSet() {
		return stringInvalidDate
	}
	milliseconds := date.msec
	required := defaults
	if required == "all" {
		required = "any"
	}
	formatter := r.createIntlDateTimeFormat([]Value{call.Argument(0), call.Argument(1)}, r.getIntlDateTimeFormat(), required, defaults).self.(*intlDateTimeFormatObject)
	return newStringValue(intlPartsText(formatter.parts(milliseconds)))
}
