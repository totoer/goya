package goya

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"
)

// hostTypeGraph captures reflection and name mapping once. Runtime conversion
// never consults a live mapper or rediscovers fields independently of checking.
type hostTypeGraph struct {
	nodes        []*hostType
	root         *hostType
	prefix       string
	declarations string
	key          string
}

type hostType struct {
	id                         int
	typeOf                     reflect.Type
	kind                       reflect.Kind
	elem                       *hostType
	length                     int
	fields                     []hostField
	methods                    []hostMethod
	inputs, outputs            []*hostType
	variadic, throws, readonly bool
	viewSchema                 string
	numberMode                 string
	numberBits                 int
	optional                   bool
}

type hostField struct {
	name  string
	index []int
	typ   *hostType
}
type hostMethod struct {
	name  string
	index int
	typ   *hostType
}
type hostTypeKey struct {
	typ            reflect.Type
	view, receiver bool
}

func describeHostType(t reflect.Type, mapper FieldNameMapper, mutable bool, numbers NumericPolicy) (*hostTypeGraph, error) {
	g := &hostTypeGraph{}
	cache := make(map[hostTypeKey]*hostType)
	var build func(reflect.Type, bool, bool) (*hostType, error)
	build = func(t reflect.Type, view, receiver bool) (*hostType, error) {
		key := hostTypeKey{t, view, receiver}
		if found := cache[key]; found != nil {
			return found, nil
		}
		n := &hostType{id: len(g.nodes), typeOf: t, readonly: view && !mutable}
		g.nodes = append(g.nodes, n)
		cache[key] = n
		if t == nil {
			return n, nil
		}
		n.kind = t.Kind()
		if t.Kind() == reflect.Struct && t.NumField() == 2 && t.Field(0).Name == "Value" && t.Field(1).Name == "Present" && t.Implements(reflect.TypeFor[hostOptionalType]()) {
			n.optional = true
			element := reflect.Zero(t).Interface().(hostOptionalType).hostOptionalElement()
			var err error
			n.elem, err = build(element, false, false)
			return n, err
		}
		switch n.kind {
		case reflect.Bool, reflect.String, reflect.Float64, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		case reflect.Float32:
			if numbers.Float32 == "" {
				return nil, fmt.Errorf("float32 requires an explicit precision/rounding policy")
			}
			n.numberMode = string(numbers.Float32)
		case reflect.Int, reflect.Uint, reflect.Int64, reflect.Uint64, reflect.Uintptr:
			if numbers.Integers == "" {
				return nil, fmt.Errorf("%s requires an explicit bigint or safe-number policy", t)
			}
			n.numberBits = t.Bits()
			if n.kind == reflect.Int || n.kind == reflect.Uint || n.kind == reflect.Uintptr {
				if numbers.IntBits == 0 || numbers.IntBits > t.Bits() {
					return nil, fmt.Errorf("%s requires explicit IntBits within this Go target's %d-bit range", t, t.Bits())
				}
				n.numberBits = numbers.IntBits
			}
			n.numberMode = string(numbers.Integers)
		case reflect.Ptr:
			var err error
			n.elem, err = build(t.Elem(), true, false)
			if err != nil {
				return nil, err
			}
		case reflect.Array, reflect.Slice, reflect.Map:
			if n.kind == reflect.Map && t.Key().Kind() != reflect.String {
				return nil, fmt.Errorf("map %s requires an explicit string-key conversion contract", t)
			}
			if n.kind == reflect.Array {
				n.length = t.Len()
			}
			var err error
			n.elem, err = build(t.Elem(), false, false)
			if err != nil {
				return nil, err
			}
		case reflect.Struct:
			for index := 0; index < t.NumField(); index++ {
				field := t.Field(index)
				if !field.IsExported() {
					continue
				}
				name := field.Name
				if mapper != nil {
					name = mapper.FieldName(t, field)
				}
				if name == "" {
					continue
				}
				if !utf8.ValidString(name) {
					return nil, fmt.Errorf("invalid mapped field name for %s.%s", t, field.Name)
				}
				child, err := build(field.Type, false, false)
				if err != nil {
					return nil, fmt.Errorf("%s.%s: %w", t, field.Name, err)
				}
				n.fields = append(n.fields, hostField{name, slices.Clone(field.Index), child})
			}
			slices.SortFunc(n.fields, func(a, b hostField) int { return strings.Compare(a.name, b.name) })
		case reflect.Func:
			start := 0
			if receiver {
				start = 1
			}
			n.variadic = t.IsVariadic()
			for index := start; index < t.NumIn(); index++ {
				parameter := t.In(index)
				if n.variadic && index == t.NumIn()-1 {
					parameter = parameter.Elem()
				}
				child, err := build(parameter, false, false)
				if err != nil {
					return nil, err
				}
				n.inputs = append(n.inputs, child)
			}
			optional := false
			for i, p := range n.inputs {
				if n.variadic && i == len(n.inputs)-1 {
					break
				}
				if optional && !p.optional {
					return nil, fmt.Errorf("required argument follows Optional argument in %s", t)
				}
				optional = optional || p.optional
			}
			count := t.NumOut()
			if count > 0 && t.Out(count-1) == reflectTypeError {
				n.throws = true
				count--
			}
			for index := 0; index < count; index++ {
				child, err := build(t.Out(index), false, false)
				if err != nil {
					return nil, err
				}
				n.outputs = append(n.outputs, child)
			}
		default:
			return nil, fmt.Errorf("Go type %s requires an explicit conversion contract", t)
		}
		if n.kind == reflect.Struct || n.kind == reflect.Ptr {
			for index := 0; index < t.NumMethod(); index++ {
				method := t.Method(index)
				name := method.Name
				if mapper != nil {
					name = mapper.MethodName(t, method)
				}
				if name == "" {
					continue
				}
				if !utf8.ValidString(name) {
					return nil, fmt.Errorf("invalid mapped method name for %s.%s", t, method.Name)
				}
				child, err := build(method.Type, false, true)
				if err != nil {
					return nil, fmt.Errorf("method %s.%s: %w", t, method.Name, err)
				}
				n.methods = append(n.methods, hostMethod{name, index, child})
			}
			slices.SortFunc(n.methods, func(a, b hostMethod) int { return strings.Compare(a.name, b.name) })
			seen := make(map[string]bool)
			fields := n.fields
			if n.kind == reflect.Ptr && n.elem.kind == reflect.Struct {
				fields = n.elem.fields
			}
			for _, field := range fields {
				if seen[field.name] {
					return nil, fmt.Errorf("duplicate mapped field %q in %s", field.name, t)
				}
				seen[field.name] = true
			}
			for _, method := range n.methods {
				if seen[method.name] {
					return nil, fmt.Errorf("mapped method conflicts with member %q in %s", method.name, t)
				}
				seen[method.name] = true
			}
		}
		return n, nil
	}
	var err error
	g.root, err = build(t, false, false)
	if err != nil {
		return nil, err
	}
	// Serialize conversion rules, rather than Go type names or addresses.
	type member struct {
		Name  string
		Type  int
		Index []int
	}
	type node struct {
		Kind                       string
		Elem, Length               int
		Fields, Methods            []member
		Inputs, Outputs            []int
		Variadic, Throws, Readonly bool
		NumberMode                 string
		NumberBits                 int
		Optional                   bool
	}
	nodes := make([]node, len(g.nodes))
	for i, n := range g.nodes {
		x := node{Kind: n.kind.String(), Elem: -1, Length: n.length, Variadic: n.variadic, Throws: n.throws, Readonly: n.readonly, NumberMode: n.numberMode, NumberBits: n.numberBits, Optional: n.optional}
		if n.elem != nil {
			x.Elem = n.elem.id
		}
		for _, f := range n.fields {
			x.Fields = append(x.Fields, member{f.name, f.typ.id, f.index})
		}
		for _, m := range n.methods {
			x.Methods = append(x.Methods, member{m.name, m.typ.id, nil})
		}
		for _, p := range n.inputs {
			x.Inputs = append(x.Inputs, p.id)
		}
		for _, p := range n.outputs {
			x.Outputs = append(x.Outputs, p.id)
		}
		nodes[i] = x
	}
	encoded, _ := json.Marshal(nodes)
	digest := sha256.Sum256(encoded)
	g.key = fmt.Sprintf("reflect:v1:%x", digest)
	g.prefix = fmt.Sprintf("__goya_host_%x_", digest[:16])
	var declarations strings.Builder
	for _, n := range g.nodes {
		fmt.Fprintf(&declarations, "type %s = %s;\n", g.name(n), g.definition(n))
	}
	g.declarations = declarations.String()
	for _, n := range g.nodes {
		if n.kind == reflect.Ptr {
			n.viewSchema = hostViewSchema(n)
		}
	}
	return g, nil
}

// A returned pointer and the same pointer registered directly must share a view
// when their reachable contracts match, even in different descriptor graphs.
func hostViewSchema(root *hostType) string {
	seen := make(map[*hostType]int)
	var visit func(*hostType) any
	visit = func(n *hostType) any {
		if id, ok := seen[n]; ok {
			return id
		}
		id := len(seen)
		seen[n] = id
		members := []any{id, n.kind.String(), n.length, n.readonly, n.variadic, n.throws, n.numberMode, n.numberBits, n.optional}
		if n.elem != nil {
			members = append(members, visit(n.elem))
		} else {
			members = append(members, nil)
		}
		for _, f := range n.fields {
			members = append(members, []any{"field", f.name, visit(f.typ)})
		}
		for _, m := range n.methods {
			members = append(members, []any{"method", m.name, visit(m.typ)})
		}
		for _, p := range n.inputs {
			members = append(members, []any{"in", visit(p)})
		}
		for _, p := range n.outputs {
			members = append(members, []any{"out", visit(p)})
		}
		return members
	}
	data, _ := json.Marshal(visit(root))
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x", digest)
}

func (g *hostTypeGraph) name(n *hostType) string { return fmt.Sprintf("%s%d", g.prefix, n.id) }

func (g *hostTypeGraph) definition(n *hostType) string {
	if n.optional {
		return g.name(n.elem) + " | undefined"
	}
	switch n.kind {
	case reflect.Invalid:
		return "null"
	case reflect.Bool:
		return "boolean"
	case reflect.String:
		return "string"
	case reflect.Float64, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return "number"
	case reflect.Float32:
		return "number"
	case reflect.Int, reflect.Uint, reflect.Int64, reflect.Uint64, reflect.Uintptr:
		if n.numberMode == string(IntegerBigInt) {
			return "bigint"
		}
		return "number"
	case reflect.Ptr:
		if n.elem.kind == reflect.Struct && !n.elem.optional {
			return "(" + g.objectDefinition(n.elem.fields, n.methods, n.elem.readonly) + ") | null"
		}
		return g.name(n.elem) + " | null"
	case reflect.Struct:
		return g.objectDefinition(n.fields, n.methods, n.readonly)
	case reflect.Slice:
		return "Array<" + g.name(n.elem) + "> | null"
	case reflect.Array:
		members := make([]string, n.length)
		for i := range members {
			members[i] = g.name(n.elem)
		}
		return "[" + strings.Join(members, ",") + "]"
	case reflect.Map:
		return "{ [key: string]: " + g.name(n.elem) + " } | null"
	case reflect.Func:
		parameters := make([]string, len(n.inputs))
		for i, p := range n.inputs {
			parameters[i] = fmt.Sprintf("arg%d: %s", i, g.name(p))
			if p.optional {
				parameters[i] = fmt.Sprintf("arg%d?: %s", i, g.name(p))
			}
			if n.variadic && i == len(n.inputs)-1 {
				parameters[i] = fmt.Sprintf("...arg%d: %s[]", i, g.name(p))
			}
		}
		result := "void"
		if len(n.outputs) == 1 {
			result = g.name(n.outputs[0])
		} else if len(n.outputs) > 1 {
			members := make([]string, len(n.outputs))
			for i, p := range n.outputs {
				members[i] = g.name(p)
			}
			result = "[" + strings.Join(members, ",") + "]"
		}
		return "(" + strings.Join(parameters, ",") + ") => " + result
	}
	panic("unhandled host type")
}

func (g *hostTypeGraph) objectDefinition(fields []hostField, methods []hostMethod, readonly bool) string {
	var b strings.Builder
	b.WriteByte('{')
	for _, f := range fields {
		if readonly {
			b.WriteString("readonly ")
		}
		optional := ""
		if f.typ.optional {
			optional = "?"
		}
		fmt.Fprintf(&b, "%s%s: %s;", tsQuote(f.name), optional, g.name(f.typ))
	}
	for _, m := range methods {
		fmt.Fprintf(&b, "readonly %s: %s;", tsQuote(m.name), g.name(m.typ))
	}
	b.WriteByte('}')
	return b.String()
}
