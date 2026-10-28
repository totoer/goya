package goya

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/totoer/goya/unistring"
)

const hex = "0123456789abcdef"

func (r *Runtime) builtinJSON_parse(call FunctionCall) Value {
	source := call.Argument(0).toString()
	record := r.parseLanguageJSON(source)
	if object, ok := call.Argument(1).(*Object); ok {
		if reviver, ok := object.self.assertCallable(); ok {
			holder := r.NewObject()
			createDataPropertyOrThrow(holder, stringEmpty, record.value)
			return r.reviveLanguageJSON(reviver, holder, stringEmpty, record, source)
		}
	}
	return record.value
}

type _builtinJSON_stringifyContext struct {
	r                *Runtime
	stack            []*Object
	propertyList     []Value
	replacerFunction func(FunctionCall) Value
	gap, indent      String
	buf              jsonTextBuffer
}

func (r *Runtime) builtinJSON_stringify(call FunctionCall) Value {
	ctx := _builtinJSON_stringifyContext{
		r: r,
	}

	replacer, _ := call.Argument(1).(*Object)
	if replacer != nil {
		if isArray(replacer) {
			length := toLength(replacer.self.getStr("length", nil))
			seen := map[unistring.String]bool{}
			propertyList := make([]Value, length)
			length = 0
			for index := range propertyList {
				var name String
				value := replacer.self.getIdx(valueInt(int64(index)), nil)
				switch v := value.(type) {
				case valueFloat, valueInt, String:
					name = value.toString()
				case *Object:
					switch v.self.className() {
					case classNumber, classString:
						name = value.toString()
					default:
						continue
					}
				default:
					continue
				}
				if seen[name.string()] {
					continue
				}
				seen[name.string()] = true
				propertyList[length] = name
				length += 1
			}
			ctx.propertyList = propertyList[0:length]
		} else if c, ok := replacer.self.assertCallable(); ok {
			ctx.replacerFunction = c
		}
	}
	if spaceValue := call.Argument(2); spaceValue != _undefined {
		if o, ok := spaceValue.(*Object); ok {
			switch oImpl := o.self.(type) {
			case *primitiveValueObject:
				switch oImpl.pValue.(type) {
				case valueInt, valueFloat:
					spaceValue = o.ToNumber()
				}
			case *stringObject:
				spaceValue = o.ToString()
			}
		}
		switch value := spaceValue.(type) {
		case valueInt, valueFloat:
			number := value.ToFloat()
			if number >= 1 {
				count := 10
				if number < 10 {
					count = int(math.Trunc(number))
				}
				ctx.gap = asciiString(strings.Repeat(" ", count))
			}
		case String:
			ctx.gap = value.Substring(0, min(10, value.Length()))
		}

	}

	if ctx.do(call.Argument(0)) {
		return ctx.buf.Value()
	}

	return _undefined
}

func (ctx *_builtinJSON_stringifyContext) do(v Value) bool {
	ctx.indent = stringEmpty
	holder := ctx.r.NewObject()
	createDataPropertyOrThrow(holder, stringEmpty, v)
	return ctx.str(stringEmpty, holder)
}

func (ctx *_builtinJSON_stringifyContext) str(key Value, holder *Object) bool {
	value := nilSafe(holder.get(key, nil))

	switch value.(type) {
	case *Object, *valueBigInt:
		if toJSON, ok := ctx.r.getVStr(value, "toJSON").(*Object); ok {
			if c, ok := toJSON.self.assertCallable(); ok {
				value = c(FunctionCall{
					This:      value,
					Arguments: []Value{key},
				})
			}
		}
	}

	if ctx.replacerFunction != nil {
		value = ctx.replacerFunction(FunctionCall{
			This:      holder,
			Arguments: []Value{key, value},
		})
	}

	if o, ok := value.(*Object); ok {
		switch o1 := o.self.(type) {
		case *primitiveValueObject:
			switch pValue := o1.pValue.(type) {
			case valueInt, valueFloat:
				value = o.ToNumber()
			default:
				value = pValue
			}
		case *stringObject:
			value = o.toString()
		case *objectGoReflect:
			if o1.toJson != nil {
				value = ctx.r.ToValue(o1.toJson())
			} else if v, ok := o1.origValue.Interface().(json.Marshaler); ok {
				b, err := v.MarshalJSON()
				if err != nil {
					panic(ctx.r.NewGoError(err))
				}
				ctx.buf.Write(b)
				return true
			} else {
				switch o1.className() {
				case classNumber:
					value = o1.val.ordinaryToPrimitiveNumber()
				case classString:
					value = o1.val.ordinaryToPrimitiveString()
				case classBoolean:
					if o.ToInteger() != 0 {
						value = valueTrue
					} else {
						value = valueFalse
					}
				}
				if o1.exportType() == typeBigInt {
					value = o1.val.ordinaryToPrimitiveNumber()
				}
			}
		}
	}

	switch value1 := value.(type) {
	case valueBool:
		if value1 {
			ctx.buf.WriteString("true")
		} else {
			ctx.buf.WriteString("false")
		}
	case String:
		ctx.quote(value1)
	case valueInt:
		ctx.buf.WriteString(value.String())
	case valueFloat:
		if !math.IsNaN(float64(value1)) && !math.IsInf(float64(value1), 0) {
			ctx.buf.WriteString(value.String())
		} else {
			ctx.buf.WriteString("null")
		}
	case valueNull:
		ctx.buf.WriteString("null")
	case *valueBigInt:
		ctx.r.typeErrorResult(true, "Do not know how to serialize a BigInt")
	case *Object:
		if raw, ok := value1.self.(*rawJSONObject); ok {
			ctx.buf.WriteValue(raw.source)
			return true
		}
		for _, object := range ctx.stack {
			if value1.SameAs(object) {
				ctx.r.typeErrorResult(true, "Converting circular structure to JSON")
			}
		}
		ctx.stack = append(ctx.stack, value1)
		defer func() { ctx.stack = ctx.stack[:len(ctx.stack)-1] }()
		if _, ok := value1.self.assertCallable(); !ok {
			if isArray(value1) {
				ctx.ja(value1)
			} else {
				ctx.jo(value1)
			}
		} else {
			return false
		}
	default:
		return false
	}
	return true
}

func (ctx *_builtinJSON_stringifyContext) ja(array *Object) {
	var stepback String
	if ctx.gap != nil && ctx.gap.Length() != 0 {
		stepback = ctx.indent
		ctx.indent = ctx.indent.Concat(ctx.gap)
	}
	length := toLength(array.self.getStr("length", nil))
	if length == 0 {
		ctx.indent = stepback
		ctx.buf.WriteString("[]")
		return
	}

	ctx.buf.WriteByte('[')
	var separator String
	if ctx.gap != nil && ctx.gap.Length() != 0 {
		ctx.buf.WriteByte('\n')
		ctx.buf.WriteValue(ctx.indent)
		separator = asciiString(",\n").Concat(ctx.indent)
	} else {
		separator = asciiString(",")
	}

	for i := int64(0); i < length; i++ {
		if !ctx.str(asciiString(strconv.FormatInt(i, 10)), array) {
			ctx.buf.WriteString("null")
		}
		if i < length-1 {
			ctx.buf.WriteValue(separator)
		}
	}
	if ctx.gap != nil && ctx.gap.Length() != 0 {
		ctx.buf.WriteByte('\n')
		ctx.buf.WriteValue(stepback)
		ctx.indent = stepback
	}
	ctx.buf.WriteByte(']')
}

func (ctx *_builtinJSON_stringifyContext) jo(object *Object) {
	var stepback String
	if ctx.gap != nil && ctx.gap.Length() != 0 {
		stepback = ctx.indent
		ctx.indent = ctx.indent.Concat(ctx.gap)
	}

	ctx.buf.WriteByte('{')
	mark := ctx.buf.Len()
	var separator String
	if ctx.gap != nil && ctx.gap.Length() != 0 {
		ctx.buf.WriteByte('\n')
		ctx.buf.WriteValue(ctx.indent)
		separator = asciiString(",\n").Concat(ctx.indent)
	} else {
		separator = asciiString(",")
	}

	var props []Value
	if ctx.propertyList == nil {
		props = object.self.stringKeys(false, nil)
	} else {
		props = ctx.propertyList
	}

	empty := true
	for _, name := range props {
		off := ctx.buf.Len()
		if !empty {
			ctx.buf.WriteValue(separator)
		}
		ctx.quote(name.toString())
		if ctx.gap != nil && ctx.gap.Length() != 0 {
			ctx.buf.WriteString(": ")
		} else {
			ctx.buf.WriteByte(':')
		}
		if ctx.str(name, object) {
			if empty {
				empty = false
			}
		} else {
			ctx.buf.Truncate(off)
		}
	}

	if empty {
		ctx.buf.Truncate(mark)
		ctx.indent = stepback
	} else {
		if ctx.gap != nil && ctx.gap.Length() != 0 {
			ctx.buf.WriteByte('\n')
			ctx.buf.WriteValue(stepback)
			ctx.indent = stepback
		}
	}
	ctx.buf.WriteByte('}')
}

func (ctx *_builtinJSON_stringifyContext) quote(str String) {
	ctx.buf.WriteByte('"')
	reader := &lenientUtf16Decoder{utf16Reader: str.utf16Reader()}
	for {
		r, _, err := reader.ReadRune()
		if err != nil {
			break
		}
		switch r {
		case '"', '\\':
			ctx.buf.WriteByte('\\')
			ctx.buf.WriteByte(byte(r))
		case 0x08:
			ctx.buf.WriteString(`\b`)
		case 0x09:
			ctx.buf.WriteString(`\t`)
		case 0x0A:
			ctx.buf.WriteString(`\n`)
		case 0x0C:
			ctx.buf.WriteString(`\f`)
		case 0x0D:
			ctx.buf.WriteString(`\r`)
		default:
			if r < 0x20 {
				ctx.buf.WriteString(`\u00`)
				ctx.buf.WriteByte(hex[r>>4])
				ctx.buf.WriteByte(hex[r&0xF])
			} else {
				if utf16.IsSurrogate(r) {
					ctx.buf.WriteString(`\u`)
					ctx.buf.WriteByte(hex[r>>12])
					ctx.buf.WriteByte(hex[(r>>8)&0xF])
					ctx.buf.WriteByte(hex[(r>>4)&0xF])
					ctx.buf.WriteByte(hex[r&0xF])
				} else {
					ctx.buf.WriteRune(r)
				}
			}
		}
	}
	ctx.buf.WriteByte('"')
}

func (r *Runtime) builtinJSON_rawJSON(call FunctionCall) Value {
	source := call.Argument(0).toString()
	if source.Length() == 0 {
		panic(r.newSyntaxError("empty raw JSON"))
	}
	record := r.parseLanguageJSON(source)
	if record.start != 0 || record.end != source.Length() {
		panic(r.newSyntaxError("raw JSON must not contain surrounding whitespace"))
	}
	if _, ok := record.value.(*Object); ok {
		panic(r.newSyntaxError("raw JSON must be a primitive"))
	}
	object := &Object{runtime: r}
	raw := &rawJSONObject{baseObject: baseObject{val: object, class: classObject, extensible: true}, source: source}
	object.self = raw
	raw.init()
	raw._putProp("rawJSON", source, false, true, false)
	raw.preventExtensions(true)
	return object
}
func (r *Runtime) builtinJSON_isRawJSON(call FunctionCall) Value {
	if object, ok := call.Argument(0).(*Object); ok {
		if _, ok := object.self.(*rawJSONObject); ok {
			return valueTrue
		}
	}
	return valueFalse
}

func (r *Runtime) getJSON() *Object {
	ret := r.global.JSON
	if ret == nil {
		JSON := r.newBaseObject(r.global.ObjectPrototype, classObject)
		ret = JSON.val
		r.global.JSON = ret
		JSON._putProp("parse", r.newNativeFunc(r.builtinJSON_parse, "parse", 2), true, false, true)
		JSON._putProp("stringify", r.newNativeFunc(r.builtinJSON_stringify, "stringify", 3), true, false, true)
		JSON._putProp("rawJSON", r.newNativeFunc(r.builtinJSON_rawJSON, "rawJSON", 1), true, false, true)
		JSON._putProp("isRawJSON", r.newNativeFunc(r.builtinJSON_isRawJSON, "isRawJSON", 1), true, false, true)
		JSON._putSym(SymToStringTag, valueProp(asciiString(classJSON), false, false, true))
	}
	return ret
}
