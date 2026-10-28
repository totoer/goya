package goya

import (
	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/clipperhouse/uax29/v2/sentences"
	"github.com/clipperhouse/uax29/v2/words"
	"github.com/totoer/goya/internal/unicode17"
)

type intlSegmenterObject struct {
	baseObject
	locale, granularity string
}
type intlSegmentSpan struct {
	start, end int
	wordLike   bool
}
type intlSegmentsObject struct {
	baseObject
	formatter *intlSegmenterObject
	input     String
	spans     []intlSegmentSpan
}
type intlSegmentIteratorObject struct {
	baseObject
	segments *intlSegmentsObject
	index    int
}
type intlBoundaryIterator interface {
	Next() bool
	Value() string
}

func (r *Runtime) intlSegmenterReceiver(value Value) *intlSegmenterObject {
	if object, ok := value.(*Object); ok {
		if segmenter, ok := object.self.(*intlSegmenterObject); ok {
			return segmenter
		}
	}
	panic(r.NewTypeError("incompatible Intl.Segmenter receiver"))
}
func (r *Runtime) intlSegmentsReceiver(value Value) *intlSegmentsObject {
	if object, ok := value.(*Object); ok {
		if segments, ok := object.self.(*intlSegmentsObject); ok {
			return segments
		}
	}
	panic(r.NewTypeError("incompatible Segments receiver"))
}
func (r *Runtime) newIntlSegmenter(args []Value, newTarget *Object) *Object {
	state := r.intlState()
	prototype := r.getPrototypeFromCtor(newTarget, state.constructors["Segmenter"], state.prototypes["Segmenter"])
	requested := r.intlCanonicalLocales(intlArgument(args, 0))
	options := r.intlOptions(intlArgument(args, 1), false)
	r.intlStringOption(options, "localeMatcher", []string{"lookup", "best fit"}, "best fit")
	resolved := intlResolveLocale(requested, nil, nil)
	granularity := r.intlStringOption(options, "granularity", []string{"grapheme", "word", "sentence"}, "grapheme")
	object := &Object{runtime: r}
	segmenter := &intlSegmenterObject{baseObject: baseObject{val: object, prototype: prototype, class: classObject, extensible: true}, locale: resolved.locale, granularity: granularity}
	object.self = segmenter
	segmenter.init()
	return object
}
func intlSegmentBoundaries(input String, granularity string, check func()) []intlSegmentSpan {
	// Only boundary calculation uses well-formed UTF-8. A lone surrogate maps to
	// U+FFFD (the same default UAX boundary class); all output slices use input.
	text := input.String()
	var iterator intlBoundaryIterator
	switch granularity {
	case "grapheme":
		iterator = graphemes.FromString(text)
	case "word":
		iterator = words.FromString(text)
	case "sentence":
		iterator = sentences.FromString(text)
	}
	var spans []intlSegmentSpan
	offset := 0
	for iterator.Next() {
		check()
		span := intlSegmentSpan{start: offset}
		for _, code := range iterator.Value() {
			if code > 0xffff {
				offset += 2
			} else {
				offset++
			}
			if granularity == "word" && unicode17.IsWordLike(code) {
				span.wordLike = true
			}
		}
		span.end = offset
		spans = append(spans, span)
	}
	return spans
}
func (segments *intlSegmentsObject) data(index int) *Object {
	span := segments.spans[index]
	r := segments.val.runtime
	object := r.intlResult("segment", segments.input.Substring(span.start, span.end), "index", span.start, "input", segments.input)
	if segments.formatter.granularity == "word" {
		createDataPropertyOrThrow(object, asciiString("isWordLike"), r.toBoolean(span.wordLike))
	}
	return object
}
func (r *Runtime) getIntlSegmentsPrototype() *Object {
	state := r.intlState()
	if prototype := state.prototypes["Segments"]; prototype != nil {
		return prototype
	}
	prototype := r.NewObject()
	state.prototypes["Segments"] = prototype
	prototype.self._putProp("containing", r.newNativeFunc(func(call FunctionCall) Value {
		segments := r.intlSegmentsReceiver(call.This)
		index := call.Argument(0).ToInteger()
		if index < 0 || index >= int64(segments.input.Length()) {
			return _undefined
		}
		low, high := 0, len(segments.spans)
		for low < high {
			middle := (low + high) / 2
			if segments.spans[middle].end <= int(index) {
				low = middle + 1
			} else {
				high = middle
			}
		}
		return segments.data(low)
	}, "containing", 1), true, false, true)
	prototype.self._putSym(SymIterator, valueProp(r.newNativeFunc(func(call FunctionCall) Value {
		segments := r.intlSegmentsReceiver(call.This)
		object := &Object{runtime: r}
		iterator := &intlSegmentIteratorObject{baseObject: baseObject{val: object, prototype: r.getIntlSegmentIteratorPrototype(), class: classObject, extensible: true}, segments: segments}
		object.self = iterator
		iterator.init()
		return object
	}, "[Symbol.iterator]", 0), true, false, true))
	return prototype
}
func (r *Runtime) getIntlSegmentIteratorPrototype() *Object {
	state := r.intlState()
	if prototype := state.prototypes["SegmentIterator"]; prototype != nil {
		return prototype
	}
	prototype := r.newBaseObject(r.getIteratorPrototype(), classObject).val
	state.prototypes["SegmentIterator"] = prototype
	prototype.self._putSym(SymToStringTag, valueProp(asciiString("Segmenter String Iterator"), false, false, true))
	prototype.self._putProp("next", r.newNativeFunc(func(call FunctionCall) Value {
		if object, ok := call.This.(*Object); ok {
			if iterator, ok := object.self.(*intlSegmentIteratorObject); ok {
				if iterator.index >= len(iterator.segments.spans) {
					return r.createIterResultObject(_undefined, true)
				}
				data := iterator.segments.data(iterator.index)
				iterator.index++
				return r.createIterResultObject(data, false)
			}
		}
		panic(r.NewTypeError("incompatible Segment Iterator receiver"))
	}, "next", 0), true, false, true)
	return prototype
}
func (r *Runtime) getIntlSegmenter() *Object {
	return r.intlConstructor("Segmenter", false, r.newIntlSegmenter, func(prototype *Object) {
		prototype.self._putProp("resolvedOptions", r.newNativeFunc(func(call FunctionCall) Value {
			segmenter := r.intlSegmenterReceiver(call.This)
			return r.intlResult("locale", segmenter.locale, "granularity", segmenter.granularity)
		}, "resolvedOptions", 0), true, false, true)
		prototype.self._putProp("segment", r.newNativeFunc(func(call FunctionCall) Value {
			segmenter := r.intlSegmenterReceiver(call.This)
			input := call.Argument(0).toString()
			object := &Object{runtime: r}
			segments := &intlSegmentsObject{baseObject: baseObject{val: object, prototype: r.getIntlSegmentsPrototype(), class: classObject, extensible: true}, formatter: segmenter, input: input, spans: intlSegmentBoundaries(input, segmenter.granularity, r.vm.checkInterrupt)}
			object.self = segments
			segments.init()
			return object
		}, "segment", 1), true, false, true)
	}, 0)
}
