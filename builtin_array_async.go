package goya

// Array.fromAsync uses an intrinsic promise and sequential continuations. The
// async iterator path preserves yielded values; the sync adapter and array-like
// path await their values before mapping.
type arrayFromAsyncState struct {
	r                *Runtime
	cap              *promiseCapability
	iterator         *iteratorRecord
	array, arrayLike *Object
	mapper           func(FunctionCall) Value
	thisArg          Value
	index, length    int64
}

func (r *Runtime) array_fromAsync(call FunctionCall) Value {
	s := &arrayFromAsyncState{r: r, cap: r.newPromiseCapability(r.getPromise()), thisArg: call.Argument(2)}
	s.cap.try(func() {
		if mapper := call.Argument(1); !IsUndefined(mapper) {
			s.mapper = r.toCallable(mapper)
		}
		items := call.Argument(0)
		if method := toMethod(r.getV(items, SymAsyncIterator)); method != nil {
			s.iterator = r.getIterator(items, method)
		} else if method := toMethod(r.getV(items, SymIterator)); method != nil {
			s.iterator = r.createAsyncFromSyncIterator(r.getIterator(items, method))
		}
		var ctor func([]Value, *Object) *Object
		if o, ok := call.This.(*Object); ok {
			ctor = o.self.assertConstructor()
		}
		if s.iterator != nil {
			if ctor != nil {
				s.array = ctor(nil, nil)
			} else {
				s.array = r.newArrayValues(nil)
			}
		} else {
			s.arrayLike = items.ToObject(r)
			s.length = toLength(s.arrayLike.Get("length"))
			if ctor != nil {
				s.array = ctor([]Value{intToValue(s.length)}, nil)
			} else {
				s.array = r.builtin_newArray([]Value{intToValue(s.length)}, r.getArrayPrototype())
			}
		}
		s.advance()
	})
	return s.cap.promise
}

func (s *arrayFromAsyncState) finish() {
	s.array.self.setOwnStr("length", intToValue(s.index), true)
	s.cap.resolve(s.array)
}

func (s *arrayFromAsyncState) advance() {
	s.cap.try(func() {
		if s.iterator == nil {
			if s.index >= s.length {
				s.finish()
				return
			}
			value := nilSafe(s.arrayLike.GetIdx(s.index))
			s.r.tsThen(value, func(value Value) { s.consume(value) }, func(reason Value) { s.cap.reject(reason) })
			return
		}
		if s.index >= 1<<53-1 {
			s.closeThrow(s.r.NewTypeError("Array.fromAsync result exceeds maximum safe length"))
			return
		}
		if s.iterator.next == nil {
			panic(s.r.NewTypeError("iterator.next is not a function"))
		}
		next := s.iterator.next(FunctionCall{This: s.iterator.iterator})
		s.r.tsThen(next, func(value Value) {
			s.cap.try(func() {
				result := s.r.toObject(value)
				if iteratorComplete(result) {
					s.finish()
					return
				}
				s.consume(iteratorValue(result))
			})
		}, func(reason Value) { s.cap.reject(reason) })
	})
}

func (s *arrayFromAsyncState) consume(value Value) {
	if s.mapper == nil {
		s.append(value)
		return
	}
	if ex := s.r.vm.try(func() {
		mapped := s.mapper(FunctionCall{This: s.thisArg, Arguments: []Value{value, intToValue(s.index)}})
		s.r.tsThen(mapped, func(value Value) { s.append(value) }, func(reason Value) { s.closeThrow(reason) })
	}); ex != nil {
		s.closeThrow(ex.val)
	}
}

func (s *arrayFromAsyncState) append(value Value) {
	if ex := s.r.vm.try(func() { createDataPropertyOrThrow(s.array, intToValue(s.index), value) }); ex != nil {
		s.closeThrow(ex.val)
		return
	}
	s.index++
	s.advance()
}

func (s *arrayFromAsyncState) closeThrow(reason Value) {
	if s.iterator == nil {
		s.cap.reject(reason)
		return
	}
	if ex := s.r.vm.try(func() {
		method := toMethod(s.iterator.iterator.self.getStr("return", nil))
		if method == nil {
			s.cap.reject(reason)
			return
		}
		value := method(FunctionCall{This: s.iterator.iterator})
		// The original throwing completion wins over any cleanup failure.
		s.r.tsThen(value, func(Value) { s.cap.reject(reason) }, func(Value) { s.cap.reject(reason) })
	}); ex != nil {
		s.cap.reject(reason)
	}
}
