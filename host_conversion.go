package goya

import (
	"fmt"
	"reflect"
	"strconv"
	"unicode/utf8"

	"github.com/totoer/goya/unistring"
)

type hostViewKey struct {
	pointer any
	schema  string
}
type hostCopyKey struct {
	typ              reflect.Type
	pointer          uintptr
	length, capacity int
}
type hostInputKey struct {
	object *Object
	typ    reflect.Type
}
type hostConversion struct {
	r       *Runtime
	g       *hostTypeGraph
	outputs map[hostCopyKey]Value
	inputs  map[hostInputKey]reflect.Value
}

func newHostConversion(r *Runtime, g *hostTypeGraph) *hostConversion {
	return &hostConversion{r: r, g: g, outputs: make(map[hostCopyKey]Value), inputs: make(map[hostInputKey]reflect.Value)}
}

func (c *hostConversion) function(name string, function reflect.Value, n *hostType) Value {
	fixed := len(n.inputs)
	if n.variadic {
		fixed--
	}
	minimum := fixed
	for i := 0; i < fixed; i++ {
		if n.inputs[i].optional {
			minimum = i
			break
		}
	}
	return c.r.newNativeFunc(func(call FunctionCall) Value {
		if len(call.Arguments) < minimum || (!n.variadic && len(call.Arguments) > fixed) {
			panic(c.r.NewTypeError("%s expects %d arguments (received %d)", name, minimum, len(call.Arguments)))
		}
		conversion := newHostConversion(c.r, c.g)
		arguments := make([]reflect.Value, len(n.inputs))
		for i := 0; i < fixed; i++ {
			arguments[i] = conversion.input(call.Argument(i), n.inputs[i])
		}
		var returned []reflect.Value
		if n.variadic {
			last := len(n.inputs) - 1
			count := len(call.Arguments) - fixed
			if count < 0 {
				count = 0
			}
			rest := reflect.MakeSlice(function.Type().In(last), count, count)
			for i := fixed; i < len(call.Arguments); i++ {
				rest.Index(i - fixed).Set(conversion.input(call.Arguments[i], n.inputs[last]))
			}
			arguments[last] = rest
			returned = function.CallSlice(arguments)
		} else {
			returned = function.Call(arguments)
		}
		if n.throws {
			last := returned[len(returned)-1]
			if !last.IsNil() {
				panic(c.r.newError(c.r.getError(), last.Interface().(error).Error()))
			}
			returned = returned[:len(returned)-1]
		}
		if len(returned) == 0 {
			return _undefined
		}
		if len(returned) == 1 {
			return conversion.output(returned[0], n.outputs[0])
		}
		values := make([]interface{}, len(returned))
		for i, v := range returned {
			values[i] = conversion.output(v, n.outputs[i])
		}
		return c.r.NewArray(values...)
	}, unistring.NewFromString(name), minimum)
}

func (c *hostConversion) input(value Value, n *hostType) reflect.Value {
	if value == nil {
		value = _undefined
	}
	if object, ok := value.(*Object); ok && object.runtime != nil && object.runtime != c.r {
		panic(c.r.NewTypeError("host argument belongs to another runtime"))
	}
	if n.optional {
		result := reflect.New(n.typeOf).Elem()
		if !IsUndefined(value) {
			result.FieldByName("Value").Set(c.input(value, n.elem))
			result.FieldByName("Present").SetBool(true)
		}
		return result
	}
	if n.kind == reflect.Ptr || n.kind == reflect.Slice || n.kind == reflect.Map {
		if IsNull(value) {
			return reflect.Zero(n.typeOf)
		}
	}
	switch n.kind {
	case reflect.Int, reflect.Uint, reflect.Int64, reflect.Uint64, reflect.Uintptr, reflect.Float32:
		return c.numberInput(value, n)
	case reflect.Bool, reflect.String, reflect.Float64, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return c.r.hostScalarArgument(value, n.typeOf)
	case reflect.Ptr:
		if object, ok := value.(*Object); ok {
			if view, exists := c.r.hostViewSources[object]; exists && view.Type() == n.typeOf {
				return view
			}
			key := hostInputKey{object, n.typeOf}
			if previous := c.inputs[key]; previous.IsValid() {
				return previous
			}
			result := reflect.New(n.typeOf.Elem())
			c.inputs[key] = result
			result.Elem().Set(c.input(value, n.elem))
			return result
		}
		result := reflect.New(n.typeOf.Elem())
		result.Elem().Set(c.input(value, n.elem))
		return result
	case reflect.Struct, reflect.Array, reflect.Slice, reflect.Map:
		object, ok := value.(*Object)
		if !ok {
			panic(c.r.NewTypeError("expected object for %s", n.typeOf))
		}
		key := hostInputKey{object, n.typeOf}
		if previous := c.inputs[key]; previous.IsValid() {
			return previous
		}
		switch n.kind {
		case reflect.Struct:
			result := reflect.New(n.typeOf).Elem()
			for _, f := range n.fields {
				result.FieldByIndex(f.index).Set(c.input(object.Get(f.name), f.typ))
			}
			return result
		case reflect.Array, reflect.Slice:
			if !isArray(object) {
				panic(c.r.NewTypeError("expected array for %s", n.typeOf))
			}
			length := object.Get("length").ToInteger()
			if length < 0 || uint64(length) > uint64(^uint(0)>>1) {
				panic(c.r.NewTypeError("array length exceeds Go range"))
			}
			var result reflect.Value
			if n.kind == reflect.Array {
				if length != int64(n.length) {
					panic(c.r.NewTypeError("expected tuple of length %d", n.length))
				}
				result = reflect.New(n.typeOf).Elem()
			} else {
				result = reflect.MakeSlice(n.typeOf, int(length), int(length))
				c.inputs[key] = result
			}
			for i := 0; i < int(length); i++ {
				result.Index(i).Set(c.input(object.GetIdx(int64(i)), n.elem))
			}
			return result
		case reflect.Map:
			result := reflect.MakeMap(n.typeOf)
			c.inputs[key] = result
			iterator := &enumerableIter{o: object, wrapped: object.self.iterateStringKeys()}
			for item, next := iterator.next(); next != nil; item, next = next() {
				nameValue := item.name
				if _, ok := nameValue.(String); !ok {
					nameValue = nameValue.ToString()
				}
				name := c.r.hostScalarArgument(nameValue, reflect.TypeFor[string]()).String()
				mapKey := reflect.New(n.typeOf.Key()).Elem()
				mapKey.SetString(name)
				result.SetMapIndex(mapKey, c.input(object.Get(name), n.elem))
			}
			return result
		}
	case reflect.Func:
		function, ok := AssertFunction(value)
		if !ok {
			panic(c.r.NewTypeError("expected callable for %s", n.typeOf))
		}
		return reflect.MakeFunc(n.typeOf, func(arguments []reflect.Value) []reflect.Value {
			conversion := newHostConversion(c.r, c.g)
			var values []Value
			for i, v := range arguments {
				if n.variadic && i == len(arguments)-1 {
					for j := 0; j < v.Len(); j++ {
						values = append(values, conversion.output(v.Index(j), n.inputs[i]))
					}
				} else {
					values = append(values, conversion.output(v, n.inputs[i]))
				}
			}
			result, err := function(_undefined, values...)
			if err != nil {
				if n.throws {
					returned := make([]reflect.Value, 0, len(n.outputs)+1)
					for _, t := range n.outputs {
						returned = append(returned, reflect.Zero(t.typeOf))
					}
					return append(returned, reflect.ValueOf(err).Convert(reflectTypeError))
				}
				panic(err)
			}
			returned := make([]reflect.Value, 0, len(n.outputs)+1)
			if len(n.outputs) == 1 {
				returned = append(returned, conversion.input(result, n.outputs[0]))
			} else if len(n.outputs) > 1 {
				object, ok := result.(*Object)
				if !ok || !isArray(object) || object.Get("length").ToInteger() != int64(len(n.outputs)) {
					panic(c.r.NewTypeError("callback must return a tuple of length %d", len(n.outputs)))
				}
				for i, t := range n.outputs {
					returned = append(returned, conversion.input(object.GetIdx(int64(i)), t))
				}
			}
			if n.throws {
				returned = append(returned, reflect.Zero(reflectTypeError))
			}
			return returned
		})
	}
	panic(c.r.NewTypeError("unsupported host input %s", n.typeOf))
}

func (c *hostConversion) output(value reflect.Value, n *hostType) Value {
	if n.optional {
		if !value.FieldByName("Present").Bool() {
			return _undefined
		}
		return c.output(value.FieldByName("Value"), n.elem)
	}
	switch n.kind {
	case reflect.Int, reflect.Uint, reflect.Int64, reflect.Uint64, reflect.Uintptr, reflect.Float32:
		return c.numberOutput(value, n)
	case reflect.Invalid:
		return _null
	case reflect.Bool, reflect.String, reflect.Float64, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return c.r.hostScalarValue(value)
	case reflect.Func:
		if value.IsNil() {
			panic(c.r.NewTypeError("nil host function violates callable contract"))
		}
		return c.function("host callback", value, n)
	case reflect.Ptr:
		if value.IsNil() {
			return _null
		}
		key := hostViewKey{value.Interface(), n.viewSchema}
		if object := c.r.hostViews[key]; object != nil {
			return object
		}
		if n.elem.kind != reflect.Struct || n.elem.optional {
			return c.output(value.Elem(), n.elem)
		}
		object := c.r.NewObject()
		if c.r.hostViews == nil {
			c.r.hostViews = make(map[hostViewKey]*Object)
			c.r.hostViewSources = make(map[*Object]reflect.Value)
		}
		c.r.hostViews[key] = object
		c.r.hostViewSources[object] = value
		c.objectMembers(object, value.Elem(), n.elem.fields, n.methods, n.elem.readonly, true, value)
		return object
	case reflect.Struct:
		object := c.r.NewObject()
		c.objectMembers(object, value, n.fields, n.methods, false, false, value)
		return object
	case reflect.Array, reflect.Slice:
		if n.kind == reflect.Slice && value.IsNil() {
			return _null
		}
		var key hostCopyKey
		if n.kind == reflect.Slice {
			key = hostCopyKey{value.Type(), value.Pointer(), value.Len(), value.Cap()}
			if previous := c.outputs[key]; previous != nil {
				return previous
			}
		}
		object := c.r.NewArray()
		if n.kind == reflect.Slice {
			c.outputs[key] = object
		}
		for i := 0; i < value.Len(); i++ {
			c.define(object, strconv.Itoa(i), c.output(value.Index(i), n.elem), true)
		}
		return object
	case reflect.Map:
		if value.IsNil() {
			return _null
		}
		key := hostCopyKey{typ: value.Type(), pointer: uintptr(value.UnsafePointer())}
		if previous := c.outputs[key]; previous != nil {
			return previous
		}
		object := c.r.NewObject()
		c.outputs[key] = object
		iterator := value.MapRange()
		for iterator.Next() {
			c.define(object, iterator.Key().String(), c.output(iterator.Value(), n.elem), true)
		}
		return object
	}
	panic(c.r.NewTypeError("unsupported host output %s", n.typeOf))
}

func (c *hostConversion) define(object *Object, name string, value Value, writable bool) {
	if !utf8.ValidString(name) {
		panic(c.r.NewTypeError("host property name is not valid UTF-8"))
	}
	flag := FLAG_FALSE
	if writable {
		flag = FLAG_TRUE
	}
	if err := object.DefineDataProperty(name, value, flag, FLAG_FALSE, FLAG_TRUE); err != nil {
		panic(err)
	}
}

func (c *hostConversion) objectMembers(object *Object, value reflect.Value, fields []hostField, methods []hostMethod, readonly, view bool, receiver reflect.Value) {
	for _, field := range fields {
		if !view {
			c.define(object, field.name, c.output(value.FieldByIndex(field.index), field.typ), true)
			continue
		}
		getter := c.r.newNativeFunc(func(FunctionCall) Value {
			return newHostConversion(c.r, c.g).output(value.FieldByIndex(field.index), field.typ)
		}, unistring.NewFromString("get "+field.name), 0)
		var setter Value = _undefined
		if !readonly {
			setter = c.r.newNativeFunc(func(call FunctionCall) Value {
				converted := newHostConversion(c.r, c.g).input(call.Argument(0), field.typ)
				value.FieldByIndex(field.index).Set(converted)
				return _undefined
			}, unistring.NewFromString("set "+field.name), 1)
		}
		if err := object.DefineAccessorProperty(field.name, getter, setter, FLAG_FALSE, FLAG_TRUE); err != nil {
			panic(err)
		}
	}
	for _, method := range methods {
		c.define(object, method.name, c.function(method.name, receiver.Method(method.index), method.typ), false)
	}
}

func (c *hostConversion) bind(value any) (result Value, err error) {
	if exception := c.r.Try(func() { result = c.output(reflect.ValueOf(value), c.g.root) }); exception != nil {
		return nil, fmt.Errorf("bind host value: %w", exception)
	}
	return result, nil
}
