// Module helper algorithms adapted from the pinned TypeScript helpers.
// See internal/tsfrontend/LICENSE.typescript (Apache-2.0).
package goya

import (
	"regexp"
	"strings"
	"unicode/utf16"
)

var tsExtensionPattern = regexp.MustCompile(`\.([tT][sS][xX])$|((?:\.[dD])?)((?:\.[^./]+?)?)\.([cCmM]?)[tT][sS]$`)

func (r *Runtime) tsRewriteImportExtension(c FunctionCall) Value {
	v, ok := c.Argument(0).(String)
	if !ok {
		return c.Argument(0)
	}
	path := v.String()
	if !strings.HasPrefix(path, "./") && !strings.HasPrefix(path, "../") {
		return v
	}
	m := tsExtensionPattern.FindStringSubmatchIndex(path)
	if m == nil {
		return v
	}
	part := func(index int) string {
		start, end := m[index*2], m[index*2+1]
		if start < 0 {
			return ""
		}
		return path[start:end]
	}
	suffix := ""
	if part(1) != "" {
		suffix = ".js"
		if c.Argument(1).ToBoolean() {
			suffix = ".jsx"
		}
	} else {
		d, ext, cm := part(2), part(3), part(4)
		if d != "" && (ext == "" || cm == "") {
			return v
		}
		suffix = d + ext + "." + strings.ToLower(cm) + "js"
	}
	return v.Substring(0, len(utf16.Encode([]rune(path[:m[0]])))).Concat(newStringValue(suffix))
}

func (r *Runtime) tsCreateBinding(c FunctionCall) Value {
	target, source := c.Argument(0).ToObject(r), c.Argument(1).ToObject(r)
	key, alias := toPropertyKey(c.Argument(2)), c.Argument(3)
	if IsUndefined(alias) {
		alias = key
	} else {
		alias = toPropertyKey(alias)
	}
	desc := source.getOwnProp(key)
	if property, ok := desc.(*valueProperty); desc == nil || ok && (property.accessor && !nilSafe(source.Get("__esModule")).ToBoolean() || !property.accessor && (property.writable || property.configurable)) || !ok {
		target.defineOwnProperty(alias, PropertyDescriptor{Getter: r.newNativeFunc(func(FunctionCall) Value { return nilSafe(source.get(key, nil)) }, "", 0), Enumerable: FLAG_TRUE, Configurable: FLAG_FALSE}, true)
	} else {
		target.defineOwnProperty(alias, r.toPropertyDescriptor(r.object_getOwnPropertyDescriptor(FunctionCall{Arguments: []Value{source, key}})), true)
	}
	return _undefined
}

func (r *Runtime) tsSetModuleDefault(c FunctionCall) Value {
	c.Argument(0).ToObject(r).defineOwnProperty(asciiString("default"), PropertyDescriptor{Value: c.Argument(1), Enumerable: FLAG_TRUE, Writable: FLAG_FALSE, Configurable: FLAG_FALSE}, true)
	return _undefined
}

func (r *Runtime) tsImportDefault(c FunctionCall) Value {
	v := c.Argument(0)
	if v.ToBoolean() && nilSafe(r.getVStr(v, "__esModule")).ToBoolean() {
		return v
	}
	o := r.NewObject()
	r.tsSet(o, "default", v)
	return o
}

func (r *Runtime) tsImportStar(c FunctionCall) Value {
	v := c.Argument(0)
	if v.ToBoolean() && nilSafe(r.getVStr(v, "__esModule")).ToBoolean() {
		return v
	}
	target := r.NewObject()
	if !IsNull(v) && !IsUndefined(v) {
		source := v.ToObject(r)
		for _, key := range source.self.stringKeys(true, nil) {
			if key.String() != "default" {
				r.tsCreateBinding(FunctionCall{Arguments: []Value{target, source, key}})
			}
		}
	}
	r.tsSetModuleDefault(FunctionCall{Arguments: []Value{target, v}})
	return target
}

func (r *Runtime) tsExportStar(c FunctionCall) Value {
	source, target := c.Argument(0), c.Argument(1).ToObject(r)
	if IsNull(source) || IsUndefined(source) {
		return _undefined
	}
	for item, next := enumerateRecursive(source.ToObject(r))(); next != nil; item, next = next() {
		if item.name.String() != "default" && !target.hasOwnProperty(item.name) {
			r.tsCreateBinding(FunctionCall{Arguments: []Value{target, source, item.name}})
		}
	}
	return _undefined
}
