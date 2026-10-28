package lsp

import (
	"unicode/utf8"

	"github.com/totoer/goya/lsp/protocol"
)

// OffsetAt converts zero-based UTF-16 coordinates to an original UTF-8 offset.
// Lines use CR, LF or CRLF; U+2028/U+2029 are ordinary source units.
// Characters past the line end are clamped; split surrogate pairs are rejected.
func OffsetAt(text string, p protocol.Position) (int, error) {
	if !utf8.ValidString(text) {
		return 0, &Error{Code: CodeInvalidDocument, Message: "document is not valid UTF-8"}
	}
	start, end := 0, 0
	for line := uint32(0); ; line++ {
		var next int
		end, next = lineEnd(text, start)
		if line == p.Line {
			break
		}
		if next == end {
			return 0, &Error{Code: CodeInvalidDocument, Message: "line is outside the document"}
		}
		start = next
	}
	units := uint32(0)
	for offset := start; offset < end; {
		if units == p.Character {
			return offset, nil
		}
		r, size := utf8.DecodeRuneInString(text[offset:end])
		if r == utf8.RuneError && size == 1 {
			return 0, &Error{Code: CodeInvalidDocument, Message: "document is not valid UTF-8"}
		}
		width := uint32(1)
		if r > 0xFFFF {
			width = 2
		}
		if units+width > p.Character {
			return 0, &Error{Code: CodeInvalidDocument, Message: "position splits a UTF-16 surrogate pair"}
		}
		units += width
		offset += size
	}
	// LSP characters beyond the line length are clamped to the line end.
	return end, nil
}

func positionOffset(text string, p protocol.Position) (int, error) { return OffsetAt(text, p) }

// PositionAt converts an original UTF-8 byte boundary to zero-based UTF-16.
// Offsets inside a newline sequence map to the preceding line's end.
func PositionAt(text string, offset int) (protocol.Position, error) {
	if offset < 0 || offset > len(text) || !utf8.ValidString(text) || (offset < len(text) && !utf8.RuneStart(text[offset])) {
		return protocol.Position{}, &Error{Code: CodeInvalidDocument, Message: "invalid UTF-8 byte offset"}
	}
	start := 0
	line := uint32(0)
	for {
		end, next := lineEnd(text, start)
		if offset < next || next == end {
			character := uint32(0)
			for _, r := range text[start:min(offset, end)] {
				character++
				if r > 0xffff {
					character++
				}
			}
			return protocol.Position{Line: line, Character: character}, nil
		}
		start = next
		line++
	}
}

func lineEnd(text string, start int) (end, next int) {
	for offset := start; offset < len(text); {
		r, size := utf8.DecodeRuneInString(text[offset:])
		if r == '\r' || r == '\n' {
			next = offset + size
			if r == '\r' && next < len(text) && text[next] == '\n' {
				next++
			}
			return offset, next
		}
		offset += size
	}
	return len(text), len(text)
}
