package goya

import "strconv"

// These deprecated properties remain in the pinned TypeScript declarations.
// State belongs to the realm; failed matches do not replace it.
func (r *Runtime) installRegExpStatics(constructor *Object) {
	for i := range r.regexpStatics {
		r.regexpStatics[i] = stringEmpty
	}
	names := []struct {
		name  string
		index int
	}{{"input", 0}, {"$_", 0}, {"lastMatch", 1}, {"$&", 1}, {"lastParen", 2}, {"$+", 2}, {"leftContext", 3}, {"$" + string(rune(96)), 3}, {"rightContext", 4}, {"$'", 4}}
	for i := 1; i <= 9; i++ {
		names = append(names, struct {
			name  string
			index int
		}{"$" + strconv.Itoa(i), i + 4})
	}
	for _, entry := range names {
		descriptor := PropertyDescriptor{Configurable: FLAG_TRUE}
		descriptor.Getter = r.newNativeFunc(func(call FunctionCall) Value {
			if call.This != constructor {
				panic(r.NewTypeError("incompatible RegExp static receiver"))
			}
			value := r.regexpStatics[entry.index]
			if value == nil {
				panic(r.NewTypeError("RegExp legacy match state is unavailable"))
			}
			return value
		}, newStringValue("get "+entry.name).string(), 0)
		if entry.index == 0 {
			descriptor.Setter = r.newNativeFunc(func(call FunctionCall) Value {
				if call.This != constructor {
					panic(r.NewTypeError("incompatible RegExp static receiver"))
				}
				r.regexpStatics[0] = call.Argument(0).toString()
				return _undefined
			}, newStringValue("set "+entry.name).string(), 1)
		}
		constructor.self.defineOwnPropertyStr(newStringValue(entry.name).string(), descriptor, true)
	}
}
func (regexp *regexpObject) updateStatics(input String, result regexpResult) {
	if len(result.indexes) == 0 {
		return
	}
	r := regexp.val.runtime
	if !regexp.legacyFeatures {
		clear(r.regexpStatics[:])
		return
	}
	for i := range r.regexpStatics {
		r.regexpStatics[i] = stringEmpty
	}
	start, end := result.indexes[0], result.indexes[1]
	r.regexpStatics[0] = input
	r.regexpStatics[1] = input.Substring(start, end)
	r.regexpStatics[3] = input.Substring(0, start)
	r.regexpStatics[4] = input.Substring(end, input.Length())
	count := len(result.indexes) / 2
	for index := 1; index < count; index++ {
		offset := index * 2
		value := stringEmpty
		if result.indexes[offset] >= 0 {
			value = input.Substring(result.indexes[offset], result.indexes[offset+1])
		}
		if index <= 9 {
			r.regexpStatics[index+4] = value
		}
		if index == count-1 {
			r.regexpStatics[2] = value
		}
	}
}
