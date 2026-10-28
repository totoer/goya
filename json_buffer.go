package goya

import (
	"unicode/utf16"
	"unicode/utf8"
)

// JSON text can contain raw ill-formed UTF-16 through rawJSON or indentation.
// Keep code units until the VM receives its result; Go byte exports escape lone
// surrogates because UTF-8 cannot represent them.
type jsonTextBuffer struct{ units []uint16 }

func (b *jsonTextBuffer) WriteString(value string) {
	for _, ch := range value {
		b.WriteRune(ch)
	}
}
func (b *jsonTextBuffer) WriteValue(value String) {
	for i := 0; i < value.Length(); i++ {
		b.units = append(b.units, value.CharAt(i))
	}
}
func (b *jsonTextBuffer) WriteRune(ch rune) {
	if ch <= 0xffff {
		b.units = append(b.units, uint16(ch))
	} else {
		a, c := utf16.EncodeRune(ch)
		b.units = append(b.units, uint16(a), uint16(c))
	}
}
func (b *jsonTextBuffer) WriteByte(ch byte)   { b.units = append(b.units, uint16(ch)) }
func (b *jsonTextBuffer) Write(data []byte)   { b.WriteString(string(data)) }
func (b *jsonTextBuffer) Len() int            { return len(b.units) }
func (b *jsonTextBuffer) Truncate(length int) { b.units = b.units[:length] }
func (b *jsonTextBuffer) Value() String       { return StringFromUTF16(b.units) }
func (b *jsonTextBuffer) Bytes() []byte {
	var out []byte
	for i := 0; i < len(b.units); i++ {
		ch := b.units[i]
		if ch >= 0xd800 && ch <= 0xdbff && i+1 < len(b.units) && b.units[i+1] >= 0xdc00 && b.units[i+1] <= 0xdfff {
			out = utf8.AppendRune(out, utf16.DecodeRune(rune(ch), rune(b.units[i+1])))
			i++
		} else if ch >= 0xd800 && ch <= 0xdfff {
			out = append(out, '\\', 'u', hex[ch>>12], hex[ch>>8&15], hex[ch>>4&15], hex[ch&15])
		} else {
			out = utf8.AppendRune(out, rune(ch))
		}
	}
	return out
}
