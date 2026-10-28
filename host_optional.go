package goya

import "reflect"

// Optional distinguishes an omitted/undefined argument from a supplied Go zero
// value. Optional parameters must follow required parameters, before any rest.
// A supplied null remains present when T itself accepts null.
type Optional[T any] struct {
	Value   T
	Present bool
}

type hostOptionalType interface{ hostOptionalElement() reflect.Type }

func (Optional[T]) hostOptionalElement() reflect.Type { return reflect.TypeFor[T]() }
