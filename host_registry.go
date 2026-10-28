package goya

import (
	stdcontext "context"
	"crypto/sha256"
	hexencoding "encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/totoer/goya/internal/tsfrontend"
	"github.com/totoer/goya/unistring"
)

// HostAdapter supplies a runtime value for an explicit TypeScript contract.
// Bind is never called by Check or Registry.Freeze. Implementations must enforce
// their declared argument, property and result conversions at runtime boundaries.
// SchemaID identifies those conversion rules and must remain stable after freeze.
type HostAdapter interface {
	SchemaID() string
	Bind(*Runtime, any) (Value, error)
}

// HostAdapterFuncs is a function-based explicit adapter. ID describes the
// contract's conversion rules, including all runtime argument/result checks.
type HostAdapterFuncs struct {
	ID        string
	BindValue func(*Runtime, any) (Value, error)
}

func (a HostAdapterFuncs) SchemaID() string { return a.ID }
func (a HostAdapterFuncs) Bind(r *Runtime, value any) (Value, error) {
	if a.BindValue == nil {
		return nil, fmt.Errorf("host adapter has no BindValue function")
	}
	return a.BindValue(r, value)
}

// HostBinding specifies the explicit contract for a value reflection cannot
// describe. Register and RegisterModule also accept ordinary Go values directly.
type HostBinding struct {
	Value      any
	TypeScript string
	Adapter    HostAdapter
	// Mutable enables writes through pointer views. Copies remain independent.
	Mutable bool
	Numbers NumericPolicy
}

type hostDescriptor struct {
	TypeScript string `json:"type"`
	Converter  string `json:"converter"`
	Schema     string `json:"schema"`
	value      any
	adapter    HostAdapter
	types      *hostTypeGraph
}

// Registry builds a host API. Freeze copies its descriptors and declarations;
// later additions to the builder do not change previously frozen environments.
type Registry struct {
	globals      map[string]hostDescriptor
	modules      map[string]map[string]hostDescriptor
	declarations map[string]string
	mapper       FieldNameMapper
}

// SetFieldNameMapper selects the mapper captured by subsequent registrations.
// Existing descriptors and frozen environments retain their mapped names.
func (b *Registry) SetFieldNameMapper(mapper FieldNameMapper) { b.mapper = mapper }

func NewRegistry() *Registry {
	return &Registry{globals: make(map[string]hostDescriptor), modules: make(map[string]map[string]hostDescriptor), declarations: make(map[string]string)}
}

func (b *Registry) Register(name string, value any) error {
	if name == "eval" {
		return fmt.Errorf("host global name %q is reserved by the execution profile", name)
	}
	if !tsfrontend.IsIdentifier(name) {
		return fmt.Errorf("invalid host global name %q", name)
	}
	if b.globals == nil {
		b.globals = make(map[string]hostDescriptor)
	}
	if _, exists := b.globals[name]; exists {
		return fmt.Errorf("host global already registered: %s", name)
	}
	descriptor, err := describeHost(value, b.mapper)
	if err != nil {
		return fmt.Errorf("register %s: %w", name, err)
	}
	b.globals[name] = descriptor
	return nil
}

func (b *Registry) RegisterModule(specifier string, exports map[string]any) error {
	if specifier == "" || !utf8.ValidString(specifier) || strings.ContainsRune(specifier, 0) || strings.HasPrefix(specifier, ".") || strings.HasPrefix(specifier, "/") {
		return fmt.Errorf("host module specifier must be a non-relative name: %q", specifier)
	}
	if b.modules == nil {
		b.modules = make(map[string]map[string]hostDescriptor)
	}
	if _, exists := b.modules[specifier]; exists {
		return fmt.Errorf("host module already registered: %s", specifier)
	}
	descriptors := make(map[string]hostDescriptor, len(exports))
	for name, value := range exports {
		if name == "" || !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
			return fmt.Errorf("invalid export name in %s", specifier)
		}
		descriptor, err := describeHost(value, b.mapper)
		if err != nil {
			return fmt.Errorf("register %s export %s: %w", specifier, name, err)
		}
		descriptors[name] = descriptor
	}
	b.modules[specifier] = descriptors
	return nil
}

// DeclareTypes adds caller-owned declaration source needed by explicit host
// contracts. Definitions are checked when freezing and again in each project.
func (b *Registry) DeclareTypes(name, source string) error {
	if !(strings.HasSuffix(name, ".d.ts") || strings.HasSuffix(name, ".d.mts") || strings.HasSuffix(name, ".d.cts")) {
		return fmt.Errorf("host type source must be a declaration file: %s", name)
	}
	if _, err := tsfrontend.CanonicalSourceName(name); err != nil {
		return err
	}
	if b.declarations == nil {
		b.declarations = make(map[string]string)
	}
	canonical, _ := tsfrontend.CanonicalSourceName(name)
	if canonical == "/.goya/host-api.d.ts" {
		return fmt.Errorf("reserved host declaration path: %s", name)
	}
	if _, exists := b.declarations[canonical]; exists {
		return fmt.Errorf("duplicate declaration source: %s", canonical)
	}
	b.declarations[canonical] = source
	return nil
}

// TypeCheckError is returned when generated or explicitly supplied host types
// fail TypeScript validation. Diagnostics remain available without text parsing.
type TypeCheckError struct{ Diagnostics []Diagnostic }

func (e *TypeCheckError) Error() string {
	for _, d := range e.Diagnostics {
		if d.Severity == "error" {
			return fmt.Sprintf("%s %d: %s", d.Namespace, d.Code, d.Message)
		}
	}
	return "TypeScript type checking failed"
}

func (b *Registry) Freeze(ctx stdcontext.Context, options ...CompileOptions) (*Environment, error) {
	if len(options) > 1 {
		return nil, fmt.Errorf("Freeze accepts at most one compiler-options map")
	}
	environment := &Environment{
		globals: maps.Clone(b.globals), modules: make(map[string]map[string]hostDescriptor, len(b.modules)),
		declarations: maps.Clone(b.declarations),
	}
	if environment.globals == nil {
		environment.globals = make(map[string]hostDescriptor)
	}
	if environment.declarations == nil {
		environment.declarations = make(map[string]string)
	}
	for name, exports := range b.modules {
		environment.modules[name] = maps.Clone(exports)
	}
	var source strings.Builder
	source.WriteString("// Generated host declarations. No runtime implementation is implied.\n")
	types := make(map[string]string)
	collect := func(d hostDescriptor) {
		if d.types != nil {
			types[d.types.key] = d.types.declarations
		}
	}
	for _, d := range environment.globals {
		collect(d)
	}
	for _, exports := range environment.modules {
		for _, d := range exports {
			collect(d)
		}
	}
	for _, key := range sortedKeys(types) {
		source.WriteString(types[key])
	}
	for _, name := range sortedKeys(environment.globals) {
		fmt.Fprintf(&source, "declare const %s: %s;\n", name, environment.globals[name].TypeScript)
	}
	for _, specifier := range sortedKeys(environment.modules) {
		exports := environment.modules[specifier]
		fmt.Fprintf(&source, "declare module %s {\n", tsQuote(specifier))
		names := sortedKeys(exports)
		for index, name := range names {
			fmt.Fprintf(&source, "const __goya_export_%d: %s;\n", index, exports[name].TypeScript)
		}
		source.WriteString("export { ")
		for index, name := range names {
			if index > 0 {
				source.WriteString(", ")
			}
			fmt.Fprintf(&source, "__goya_export_%d as %s", index, tsQuote(name))
		}
		source.WriteString(" };\n}\n")
	}
	environment.declarations["/.goya/host-api.d.ts"] = source.String()
	var selected CompileOptions
	if len(options) > 0 {
		selected = options[0]
	}
	analysis, diagnostics, err := tsfrontend.Analyze(ctx, Project{Files: environment.declarations}, nil, selected)
	if err != nil {
		return nil, err
	}
	if HasTypeErrors(diagnostics) {
		return nil, &TypeCheckError{Diagnostics: diagnostics}
	}
	typeDigests, err := analysis.BindingTypeDigests(ctx, "/.goya/host-api.d.ts")
	if err != nil {
		return nil, err
	}
	closeSchema := func(descriptor hostDescriptor, key string) (hostDescriptor, error) {
		digest, ok := typeDigests[key]
		if !ok {
			return descriptor, fmt.Errorf("missing checked binding schema: %s", key)
		}
		sum := sha256.Sum256([]byte(descriptor.Schema + ":" + digest))
		descriptor.Schema = hexencoding.EncodeToString(sum[:])
		return descriptor, nil
	}
	for name, descriptor := range environment.globals {
		descriptor, err = closeSchema(descriptor, tsfrontend.GlobalBindingKey(name))
		if err != nil {
			return nil, err
		}
		environment.globals[name] = descriptor
	}
	for specifier, exports := range environment.modules {
		for name, descriptor := range exports {
			descriptor, err = closeSchema(descriptor, tsfrontend.ModuleBindingKey(specifier, name))
			if err != nil {
				return nil, err
			}
			exports[name] = descriptor
		}
	}
	data, err := json.Marshal(struct {
		Globals      map[string]hostDescriptor            `json:"globals"`
		Modules      map[string]map[string]hostDescriptor `json:"modules"`
		Declarations map[string]string                    `json:"declarations"`
	}{environment.globals, environment.modules, environment.declarations})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	environment.schemaDigest = hexencoding.EncodeToString(digest[:])
	return environment, nil
}

func (e *Environment) SchemaID() string {
	if e == nil {
		return ""
	}
	return e.schemaDigest
}

func (e *Environment) TypeDeclarations() map[string]string {
	if e == nil {
		return nil
	}
	return maps.Clone(e.declarations)
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func tsQuote(text string) string { value, _ := json.Marshal(text); return string(value) }

func describeHost(value any, mapper FieldNameMapper) (hostDescriptor, error) {
	definition, explicit := value.(HostBinding)
	if !explicit {
		definition = HostBinding{Value: value}
	}
	descriptor := hostDescriptor{value: definition.Value, adapter: definition.Adapter}
	if err := definition.Numbers.validate(); err != nil {
		return descriptor, err
	}
	if definition.TypeScript != "" || definition.Adapter != nil {
		if definition.Mutable {
			return descriptor, fmt.Errorf("explicit adapters define their own mutability")
		}
		if definition.Numbers != (NumericPolicy{}) {
			return descriptor, fmt.Errorf("explicit adapters define their own numeric policies")
		}
		if strings.TrimSpace(definition.TypeScript) == "" || definition.Adapter == nil {
			return descriptor, fmt.Errorf("explicit TypeScript types require both a contract and an adapter")
		}
		adapterValue := reflect.ValueOf(definition.Adapter)
		if adapterValue.Kind() == reflect.Ptr && adapterValue.IsNil() {
			return descriptor, fmt.Errorf("host adapter is nil")
		}
		id := definition.Adapter.SchemaID()
		if id == "" {
			return descriptor, fmt.Errorf("host adapter must have a stable non-empty SchemaID")
		}
		descriptor.TypeScript = definition.TypeScript
		descriptor.Converter = "adapter:" + id
	} else {
		if _, runtimeValue := definition.Value.(Value); runtimeValue {
			return descriptor, fmt.Errorf("runtime values require an explicit per-runtime adapter")
		}
		types, err := describeHostType(reflect.TypeOf(definition.Value), mapper, definition.Mutable, definition.Numbers)
		if err != nil {
			return descriptor, err
		}
		if value := reflect.ValueOf(definition.Value); value.IsValid() && value.Kind() == reflect.Func && value.IsNil() {
			return descriptor, fmt.Errorf("host function is nil")
		}
		if value := reflect.ValueOf(definition.Value); value.IsValid() && value.Kind() == reflect.String && !utf8.ValidString(value.String()) {
			return descriptor, fmt.Errorf("host string is not valid UTF-8")
		}
		descriptor.TypeScript = types.name(types.root)
		descriptor.Converter = types.key
		descriptor.types = types
	}
	encoded, _ := json.Marshal(struct{ Type, Converter string }{descriptor.TypeScript, descriptor.Converter})
	digest := sha256.Sum256(encoded)
	descriptor.Schema = hexencoding.EncodeToString(digest[:])
	return descriptor, nil
}

// Bind installs the frozen implementations into a runtime. Type checking does
// not call this method. Module values are kept separate from global bindings.
func (e *Environment) Bind(r *Runtime) error {
	if e == nil || r == nil {
		return fmt.Errorf("binding requires an environment and runtime")
	}
	if r.hostEnvironment != nil {
		if r.hostEnvironment == e {
			return nil
		}
		return fmt.Errorf("runtime already has a host environment")
	}
	globals := make(map[string]Value, len(e.globals))
	modules := make(map[string]map[string]Value, len(e.modules))
	bind := func(name string, descriptor hostDescriptor) (Value, error) {
		if descriptor.adapter != nil {
			if "adapter:"+descriptor.adapter.SchemaID() != descriptor.Converter {
				return nil, fmt.Errorf("host adapter schema changed after freeze: %s", name)
			}
			value, err := descriptor.adapter.Bind(r, descriptor.value)
			if err != nil {
				return nil, err
			}
			if value == nil {
				return nil, fmt.Errorf("adapter returned nil for %s", name)
			}
			if object, ok := value.(*Object); ok && object.runtime != nil && object.runtime != r {
				return nil, fmt.Errorf("adapter returned a value from another runtime: %s", name)
			}
			return value, nil
		}
		return newHostConversion(r, descriptor.types).bind(descriptor.value)
	}
	for _, name := range sortedKeys(e.globals) {
		value, err := bind(name, e.globals[name])
		if err != nil {
			return err
		}
		globals[name] = value
	}
	for _, specifier := range sortedKeys(e.modules) {
		exports := make(map[string]Value, len(e.modules[specifier]))
		for _, name := range sortedKeys(e.modules[specifier]) {
			value, err := bind(name, e.modules[specifier][name])
			if err != nil {
				return err
			}
			exports[name] = value
		}
		modules[specifier] = exports
	}
	global := r.GlobalObject()
	if len(globals) > 0 && !global.self.isExtensible() {
		return fmt.Errorf("host globals require an extensible global object")
	}
	for _, name := range sortedKeys(globals) {
		if _, exists := r.global.stash.names[unistring.NewFromString(name)]; exists {
			return fmt.Errorf("host global conflicts with existing lexical binding: %s", name)
		}
		if global.self.getOwnPropStr(unistring.NewFromString(name)) != nil {
			return fmt.Errorf("host global conflicts with existing property: %s", name)
		}
	}
	for _, name := range sortedKeys(globals) {
		if err := global.DefineDataProperty(name, globals[name], FLAG_FALSE, FLAG_FALSE, FLAG_FALSE); err != nil {
			return err
		}
	}
	r.hostEnvironment = e
	r.hostModules = modules
	r.hostGlobals = globals
	return nil
}

func (r *Runtime) hostScalarArgument(value Value, t reflect.Type) reflect.Value {
	result := reflect.New(t).Elem()
	switch t.Kind() {
	case reflect.Bool:
		actual, ok := value.(valueBool)
		if !ok {
			panic(r.NewTypeError("expected boolean for %s", t))
		}
		result.SetBool(bool(actual))
	case reflect.String:
		actual, ok := value.(String)
		if !ok {
			panic(r.NewTypeError("expected string for %s", t))
		}
		for index := 0; index < actual.Length(); index++ {
			character := actual.CharAt(index)
			if isUTF16FirstSurrogate(character) {
				index++
				if index >= actual.Length() || !isUTF16SecondSurrogate(actual.CharAt(index)) {
					panic(r.NewTypeError("ill-formed UTF-16 cannot be converted to Go string"))
				}
			} else if isUTF16SecondSurrogate(character) {
				panic(r.NewTypeError("ill-formed UTF-16 cannot be converted to Go string"))
			}
		}
		result.SetString(actual.String())
	case reflect.Float64, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		switch value.(type) {
		case valueInt, valueFloat:
		default:
			panic(r.NewTypeError("expected number for %s", t))
		}
		number := value.ToFloat()
		if t.Kind() == reflect.Float64 {
			result.SetFloat(number)
			return result
		}
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number == 0 && math.Signbit(number) {
			panic(r.NewTypeError("expected an integer for %s", t))
		}
		if t.Kind() >= reflect.Uint && t.Kind() <= reflect.Uint64 {
			if number < 0 || number >= math.Ldexp(1, t.Bits()) {
				panic(r.NewTypeError("number is outside %s range", t))
			}
			result.SetUint(uint64(number))
		} else {
			limit := math.Ldexp(1, t.Bits()-1)
			if number < -limit || number >= limit {
				panic(r.NewTypeError("number is outside %s range", t))
			}
			result.SetInt(int64(number))
		}
	default:
		panic(r.NewTypeError("unsupported host argument type %s", t))
	}
	return result
}

func (r *Runtime) hostScalarValue(value reflect.Value) Value {
	if !value.IsValid() {
		return _null
	}
	switch value.Kind() {
	case reflect.Bool:
		return r.ToValue(value.Bool())
	case reflect.String:
		if !utf8.ValidString(value.String()) {
			panic(r.NewTypeError("ill-formed UTF-8 cannot be converted to TypeScript string"))
		}
		return r.ToValue(value.String())
	case reflect.Float64:
		return r.ToValue(value.Float())
	case reflect.Int8, reflect.Int16, reflect.Int32:
		return r.ToValue(value.Int())
	case reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return r.ToValue(value.Uint())
	default:
		panic(r.NewTypeError("unsupported host result type %s", value.Type()))
	}
}
