package goya

import (
	"strings"
	"unicode/utf8"

	"github.com/totoer/goya/internal/cldrdata"
)

type intlListFormatObject struct {
	baseObject
	locale, dataLocale, kind, style string
}
type intlListPart struct {
	kind  string
	value String
}

func (r *Runtime) intlListReceiver(value Value) *intlListFormatObject {
	if object, ok := value.(*Object); ok {
		if format, ok := object.self.(*intlListFormatObject); ok {
			return format
		}
	}
	panic(r.NewTypeError("incompatible Intl.ListFormat receiver"))
}
func (r *Runtime) newIntlListFormat(args []Value, newTarget *Object) *Object {
	state := r.intlState()
	prototype := r.getPrototypeFromCtor(newTarget, state.constructors["ListFormat"], state.prototypes["ListFormat"])
	requested := r.intlCanonicalLocales(intlArgument(args, 0))
	options := r.intlOptions(intlArgument(args, 1), false)
	r.intlStringOption(options, "localeMatcher", []string{"lookup", "best fit"}, "best fit")
	resolved := intlResolveLocale(requested, nil, nil)
	kind := r.intlStringOption(options, "type", []string{"conjunction", "disjunction", "unit"}, "conjunction")
	style := r.intlStringOption(options, "style", []string{"long", "short", "narrow"}, "long")
	object := &Object{runtime: r}
	format := &intlListFormatObject{baseObject: baseObject{val: object, prototype: prototype, class: classObject, extensible: true}, locale: resolved.locale, dataLocale: resolved.dataLocale, kind: kind, style: style}
	object.self = format
	format.init()
	return object
}
func intlListContextPattern(pattern, language string, next String) string {
	// CLDR contextual conjunction forms. The original string is never converted
	// back from UTF-8, so isolated UTF-16 surrogates remain intact in the result.
	text := strings.ToLower(next.String())
	switch language {
	case "es":
		if strings.Contains(pattern, " y ") && (strings.HasPrefix(text, "i") || strings.HasPrefix(text, "hi") && !strings.HasPrefix(text, "hia") && !strings.HasPrefix(text, "hie")) {
			return strings.ReplaceAll(pattern, " y ", " e ")
		}
		if strings.Contains(pattern, " o ") && (strings.HasPrefix(text, "o") || strings.HasPrefix(text, "ho") || strings.HasPrefix(text, "8") || text == "11" || strings.HasPrefix(text, "11 ")) {
			return strings.ReplaceAll(pattern, " o ", " u ")
		}
	case "he":
		first, _ := utf8.DecodeRuneInString(text)
		if first < 0x0590 || first > 0x05ff {
			return strings.ReplaceAll(pattern, " ו{1}", " ו-{1}")
		}
	}
	return pattern
}
func appendIntlListPart(parts []intlListPart, kind string, value String) []intlListPart {
	if kind == "literal" && value.Length() == 0 {
		return parts
	}
	if kind == "literal" && len(parts) > 0 && parts[len(parts)-1].kind == "literal" {
		var builder StringBuilder
		builder.WriteString(parts[len(parts)-1].value)
		builder.WriteString(value)
		parts[len(parts)-1].value = builder.String()
		return parts
	}
	return append(parts, intlListPart{kind, value})
}
func intlListPattern(pattern string, left, right []intlListPart) []intlListPart {
	var parts []intlListPart
	for len(pattern) > 0 {
		index := strings.IndexByte(pattern, '{')
		if index < 0 {
			return appendIntlListPart(parts, "literal", newStringValue(pattern))
		}
		parts = appendIntlListPart(parts, "literal", newStringValue(pattern[:index]))
		if strings.HasPrefix(pattern[index:], "{0}") {
			for _, part := range left {
				parts = appendIntlListPart(parts, part.kind, part.value)
			}
		} else if strings.HasPrefix(pattern[index:], "{1}") {
			for _, part := range right {
				parts = appendIntlListPart(parts, part.kind, part.value)
			}
		} else {
			panic("invalid embedded CLDR list pattern")
		}
		pattern = pattern[index+3:]
	}
	return parts
}
func (format *intlListFormatObject) parts(list []String) []intlListPart {
	if len(list) == 0 {
		return nil
	}
	element := func(value String) []intlListPart { return []intlListPart{{"element", value}} }
	if len(list) == 1 {
		return element(list[0])
	}
	key := map[string]string{"conjunction": "standard", "disjunction": "or", "unit": "unit"}[format.kind]
	if format.style != "long" {
		key += "-" + format.style
	}
	patterns := cldrdata.Locale(format.dataLocale, "listPatterns").Get("listPatterns", "listPattern-type-"+key)
	// Expand the right-associated templates iteratively. This preserves the
	// standard order without copying the accumulated tail for every element.
	type task struct {
		kind  string
		value String
		index int
	}
	stack := []task{{index: 0}}
	var parts []intlListPart
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current.kind != "" {
			parts = appendIntlListPart(parts, current.kind, current.value)
			continue
		}
		i := current.index
		if i == len(list)-1 {
			parts = appendIntlListPart(parts, "element", list[i])
			continue
		}
		key := "middle"
		if len(list) == 2 {
			key = "2"
		} else if i == 0 {
			key = "start"
		} else if i == len(list)-2 {
			key = "end"
		}
		pattern := intlListContextPattern(patterns.Get(key).String(), strings.Split(format.dataLocale, "-")[0], list[i+1])
		var expansion []task
		for len(pattern) > 0 {
			index := strings.IndexByte(pattern, '{')
			if index < 0 {
				expansion = append(expansion, task{kind: "literal", value: newStringValue(pattern)})
				break
			}
			if index > 0 {
				expansion = append(expansion, task{kind: "literal", value: newStringValue(pattern[:index])})
			}
			if strings.HasPrefix(pattern[index:], "{0}") {
				expansion = append(expansion, task{kind: "element", value: list[i]})
			} else if strings.HasPrefix(pattern[index:], "{1}") {
				expansion = append(expansion, task{index: i + 1})
			} else {
				panic("invalid embedded CLDR list pattern")
			}
			pattern = pattern[index+3:]
		}
		for j := len(expansion) - 1; j >= 0; j-- {
			stack = append(stack, expansion[j])
		}
	}

	return parts
}
func (r *Runtime) getIntlListFormat() *Object {
	return r.intlConstructor("ListFormat", false, r.newIntlListFormat, func(prototype *Object) {
		prototype.self._putProp("resolvedOptions", r.newNativeFunc(func(call FunctionCall) Value {
			f := r.intlListReceiver(call.This)
			return r.intlResult("locale", f.locale, "type", f.kind, "style", f.style)
		}, "resolvedOptions", 0), true, false, true)
		for _, name := range []string{"format", "formatToParts"} {
			prototype.self._putProp(newStringValue(name).string(), r.newNativeFunc(func(call FunctionCall) Value {
				f := r.intlListReceiver(call.This)
				var list []String
				if !IsUndefined(call.Argument(0)) {
					r.getIterator(call.Argument(0), nil).iterate(func(value Value) {
						r.vm.checkInterrupt()
						text, ok := value.(String)
						if !ok {
							panic(r.NewTypeError("Intl.ListFormat elements must be strings"))
						}
						list = append(list, text)
					})
				}
				parts := f.parts(list)
				if name == "formatToParts" {
					values := make([]Value, len(parts))
					for i, part := range parts {
						values[i] = r.intlResult("type", part.kind, "value", part.value)
					}
					return r.newArrayValues(values)
				}
				var builder StringBuilder
				for _, part := range parts {
					builder.WriteString(part.value)
				}
				return builder.String()
			}, newStringValue(name).string(), 1), true, false, true)
		}
	}, 0)
}
