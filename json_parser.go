package goya

import (
	"errors"
	"strconv"

	"github.com/totoer/goya/unistring"
)

type jsonParseRecord struct {
	value      Value
	start, end int
	properties map[unistring.String]*jsonParseRecord
	elements   []*jsonParseRecord
}
type languageJSONParser struct {
	r             *Runtime
	source        String
	index, length int
}

func (p *languageJSONParser) invalid() {
	if p.index >= p.length {
		panic(p.r.newErrorf(p.r.getSyntaxError(), "Unexpected end of JSON input"))
	}
	panic(p.r.newErrorf(p.r.getSyntaxError(), "Invalid JSON at position %d", p.index))
}
func (p *languageJSONParser) peek() uint16 {
	if p.index >= p.length {
		return 0
	}
	return p.source.CharAt(p.index)
}
func (p *languageJSONParser) space() {
	for p.index < p.length {
		switch p.peek() {
		case ' ', '\t', '\n', '\r':
			p.index++
		default:
			return
		}
	}
}
func (p *languageJSONParser) consume(c uint16) {
	if p.index >= p.length || p.peek() != c {
		p.invalid()
	}
	p.index++
}
func (p *languageJSONParser) literal(text string) {
	for _, c := range text {
		p.consume(uint16(c))
	}
}
func (p *languageJSONParser) stringValue() String {
	p.consume('"')
	var units []uint16
	for p.index < p.length {
		if p.index&1023 == 0 {
			p.r.vm.checkInterrupt()
		}
		c := p.peek()
		p.index++
		if c == '"' {
			return StringFromUTF16(units)
		}
		if c < 0x20 {
			p.invalid()
		}
		if c == '\\' {
			if p.index >= p.length {
				p.invalid()
			}
			c = p.peek()
			p.index++
			switch c {
			case '"', '\\', '/':
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'u':
				c = 0
				for range 4 {
					if p.index >= p.length {
						p.invalid()
					}
					digit := p.peek()
					p.index++
					var value uint16
					switch {
					case digit >= '0' && digit <= '9':
						value = digit - '0'
					case digit >= 'a' && digit <= 'f':
						value = digit - 'a' + 10
					case digit >= 'A' && digit <= 'F':
						value = digit - 'A' + 10
					default:
						p.invalid()
					}
					c = c<<4 | value
				}
			default:
				p.invalid()
			}
		}
		units = append(units, c)
	}
	p.invalid()
	return nil
}
func (p *languageJSONParser) number() Value {
	start := p.index
	if p.peek() == '-' {
		p.index++
	}
	if p.index >= p.length {
		p.invalid()
	}
	if p.peek() == '0' {
		p.index++
	} else {
		if p.peek() < '1' || p.peek() > '9' {
			p.invalid()
		}
		for p.peek() >= '0' && p.peek() <= '9' {
			p.index++
		}
	}
	if p.peek() == '.' {
		p.index++
		if p.peek() < '0' || p.peek() > '9' {
			p.invalid()
		}
		for p.peek() >= '0' && p.peek() <= '9' {
			p.index++
		}
	}
	if p.peek() == 'e' || p.peek() == 'E' {
		p.index++
		if p.peek() == '+' || p.peek() == '-' {
			p.index++
		}
		if p.peek() < '0' || p.peek() > '9' {
			p.invalid()
		}
		for p.peek() >= '0' && p.peek() <= '9' {
			p.index++
		}
	}
	value, err := strconv.ParseFloat(p.source.Substring(start, p.index).String(), 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		p.invalid()
	}
	return floatToValue(value)
}
func (p *languageJSONParser) value() *jsonParseRecord {
	p.r.vm.checkInterrupt()
	p.space()
	record := &jsonParseRecord{start: p.index}
	switch p.peek() {
	case '"':
		record.value = p.stringValue()
	case 't':
		p.literal("true")
		record.value = valueTrue
	case 'f':
		p.literal("false")
		record.value = valueFalse
	case 'n':
		p.literal("null")
		record.value = _null
	case '[':
		p.index++
		p.space()
		var values []Value
		if p.peek() != ']' {
			for {
				child := p.value()
				record.elements = append(record.elements, child)
				values = append(values, child.value)
				p.space()
				if p.peek() == ']' {
					break
				}
				p.consume(',')
			}
		}
		p.consume(']')
		record.value = p.r.newArrayValues(values)
	case '{':
		p.index++
		p.space()
		object := p.r.NewObject()
		record.properties = make(map[unistring.String]*jsonParseRecord)
		if p.peek() != '}' {
			for {
				p.space()
				if p.peek() != '"' {
					p.invalid()
				}
				key := p.stringValue()
				p.space()
				p.consume(':')
				child := p.value()
				record.properties[key.string()] = child
				createDataPropertyOrThrow(object, key, child.value)
				p.space()
				if p.peek() == '}' {
					break
				}
				p.consume(',')
			}
		}
		p.consume('}')
		record.value = object
	default:
		record.value = p.number()
	}
	record.end = p.index
	return record
}
func (r *Runtime) parseLanguageJSON(source String) *jsonParseRecord {
	p := languageJSONParser{r: r, source: source, length: source.Length()}
	record := p.value()
	p.space()
	if p.index != p.length {
		p.invalid()
	}
	return record
}

func (r *Runtime) reviveLanguageJSON(reviver func(FunctionCall) Value, holder *Object, name Value, record *jsonParseRecord, source String) Value {
	r.vm.checkInterrupt()
	value := nilSafe(holder.get(name, nil))
	context := r.NewObject()
	if record != nil && !record.value.SameAs(value) {
		record = nil
	}
	if object, ok := value.(*Object); ok {
		if isArray(object) {
			length := toLength(object.self.getStr("length", nil))
			for i := int64(0); i < length; i++ {
				var child *jsonParseRecord
				if record != nil && i < int64(len(record.elements)) {
					child = record.elements[i]
				}
				key := asciiString(strconv.FormatInt(i, 10))
				next := r.reviveLanguageJSON(reviver, object, key, child, source)
				if IsUndefined(next) {
					object.delete(key, false)
				} else {
					createDataProperty(object, key, next)
				}
			}
		} else {
			for _, key := range object.self.stringKeys(false, nil) {
				var child *jsonParseRecord
				if record != nil {
					child = record.properties[key.string()]
				}
				next := r.reviveLanguageJSON(reviver, object, key, child, source)
				if IsUndefined(next) {
					object.delete(key, false)
				} else {
					createDataProperty(object, key, next)
				}
			}
		}
	} else if record != nil {
		createDataPropertyOrThrow(context, asciiString("source"), source.Substring(record.start, record.end))
	}
	return reviver(FunctionCall{This: holder, Arguments: []Value{name, value, context}})
}

type rawJSONObject struct {
	baseObject
	source String
}
