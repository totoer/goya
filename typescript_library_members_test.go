package goya

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/totoer/goya/unistring"
)

// Check every named declaration/overload in these families, including optional
// profiles, against runtime members. Behavioral TS fixtures complement this
// structural audit; merely passing a smoke fixture does not establish parity.
func TestTypeScriptCollectionPromiseErrorResourceMemberInventory(t *testing.T) {
	data, err := os.ReadFile("docs/typescript-library-inventory.json")
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Libraries []struct {
			Name         string
			References   []string
			External     bool
			Declarations []struct{ Name, Kind, Symbol string }
		}
	}
	if err := json.Unmarshal(data, &inventory); err != nil {
		t.Fatal(err)
	}
	r := New()
	owners := map[string]*Object{
		"Array": r.getArrayPrototype(), "ReadonlyArray": r.getArrayPrototype(), "ArrayConstructor": r.getArray(),
		"Map": r.getMapPrototype(), "ReadonlyMap": r.getMapPrototype(), "MapConstructor": r.getMap(),
		"Set": r.getSetPrototype(), "ReadonlySet": r.getSetPrototype(), "SetConstructor": r.getSet(),
		"WeakMap": r.getWeakMapPrototype(), "WeakMapConstructor": r.getWeakMap(), "WeakSet": r.getWeakSetPrototype(), "WeakSetConstructor": r.getWeakSet(),
		"WeakRef": r.getWeakRefPrototype(), "WeakRefConstructor": r.getWeakRef(), "FinalizationRegistry": r.getFinalizationRegistryPrototype(), "FinalizationRegistryConstructor": r.getFinalizationRegistry(),
		"Atomics":           r.getAtomics(),
		"Intl":              r.getIntl(),
		"SharedArrayBuffer": r.getSharedArrayBufferPrototype(), "SharedArrayBufferConstructor": r.getSharedArrayBuffer(),
		"ArrayBuffer": r.getArrayBufferPrototype(), "ArrayBufferConstructor": r.getArrayBuffer(), "DataView": r.getDataViewPrototype(), "DataViewConstructor": r.getDataView(),
		"IteratorObject": r.getIteratorPrototype(), "IteratorConstructor": r.getIteratorCtor(),
		"Promise": r.getPromisePrototype(), "PromiseConstructor": r.getPromise(),
		"Error": r.getErrorPrototype(), "ErrorConstructor": r.getError(), "EvalErrorConstructor": r.getEvalError(), "RangeErrorConstructor": r.getRangeError(), "ReferenceErrorConstructor": r.getReferenceError(), "SyntaxErrorConstructor": r.getSyntaxError(), "TypeErrorConstructor": r.getTypeError(), "URIErrorConstructor": r.getURIError(), "AggregateErrorConstructor": r.getAggregateError(), "SuppressedErrorConstructor": r.getSuppressedError(),
		"DisposableStack": r.getDisposableStack(false).Get("prototype").(*Object), "DisposableStackConstructor": r.getDisposableStack(false), "AsyncDisposableStack": r.getDisposableStack(true).Get("prototype").(*Object), "AsyncDisposableStackConstructor": r.getDisposableStack(true),
	}
	for name, constructor := range map[string]*Object{"Int8Array": r.getInt8Array(), "Uint8Array": r.getUint8Array(), "Uint8ClampedArray": r.getUint8ClampedArray(), "Int16Array": r.getInt16Array(), "Uint16Array": r.getUint16Array(), "Int32Array": r.getInt32Array(), "Uint32Array": r.getUint32Array(), "Float16Array": r.getFloat16Array(), "Float32Array": r.getFloat32Array(), "Float64Array": r.getFloat64Array(), "BigInt64Array": r.getBigInt64Array(), "BigUint64Array": r.getBigUint64Array()} {
		owners[name] = constructor.Get("prototype").(*Object)
		owners[name+"Constructor"] = constructor
	}

	for name, object := range map[string]*Object{
		"Object": r.global.ObjectPrototype, "ObjectConstructor": r.getObject(),
		"Boolean": r.getBooleanPrototype(), "BooleanConstructor": r.getBoolean(),
		"Number": r.getNumberPrototype(), "NumberConstructor": r.getNumber(),
		"BigInt": r.getBigIntPrototype(), "BigIntConstructor": r.getBigInt(),
		"String": r.getStringPrototype(), "StringConstructor": r.getString(),
		"Symbol": r.getSymbolPrototype(), "SymbolConstructor": r.getSymbol(),
		"Date": r.getDatePrototype(), "DateConstructor": r.getDate(),
		"Math": r.getMath(), "JSON": r.getJSON(), "Reflect": r.getReflect(), "ProxyConstructor": r.getProxy(),
		"FunctionConstructor": r.getFunction(), "CallableFunction": r.getFunctionPrototype(), "NewableFunction": r.getFunctionPrototype(),
		"ConcatArray": r.getArrayPrototype(), "ArrayIterator": r.getArrayIteratorPrototype(), "StringIterator": r.getStringIteratorPrototype(), "MapIterator": r.getMapIteratorPrototype(), "SetIterator": r.getSetIteratorPrototype(), "RegExpStringIterator": r.getRegExpStringIteratorPrototype(),
		"Generator": r.getGeneratorPrototype(), "GeneratorFunction": r.getGeneratorFunction(), "GeneratorFunctionConstructor": r.getGeneratorFunction(),
		"AsyncGenerator": r.getTSAsyncGeneratorPrototype(), "AsyncGeneratorFunction": r.getTSAsyncGeneratorFunction(), "AsyncGeneratorFunctionConstructor": r.getTSAsyncGeneratorFunction(),
	} {
		owners[name] = object
	}
	function, err := r.RunString("function mark(_v: Function, c: ClassDecoratorContext) { c.metadata!.flag = true; } @mark class Example {} Example;")
	if err != nil {
		t.Fatal(err)
	}
	owners["Function"] = function.(*Object)
	for name, source := range map[string]string{
		"GeneratorFunction":      "(function* example(){yield 1})",
		"AsyncGeneratorFunction": "(async function* example(){yield 1})",
		"IArguments":             "(function example(value: unknown){return arguments})(1)",
		"TemplateStringsArray":   "((strings:TemplateStringsArray)=>strings)`x`",
	} {
		value, err := r.RunString(source)
		if err != nil {
			t.Fatal(err)
		}
		owners[name] = value.(*Object)
	}

	intlConstructors := make(map[string]*Object)
	for _, family := range []struct {
		name        string
		constructor *Object
	}{{"Intl.DateTimeFormat", r.getIntlDateTimeFormat()}, {"Intl.Collator", r.getIntlCollator()}, {"Intl.NumberFormat", r.getIntlNumberFormat()}, {"Intl.PluralRules", r.getIntlPluralRules()}, {"Intl.RelativeTimeFormat", r.getIntlRelativeTimeFormat()}} {
		instance, err := r.New(family.constructor)
		if err != nil {
			t.Fatal(err)
		}
		intlConstructors[family.name] = family.constructor
		owners[family.name] = instance
		owners[family.name+"Constructor"] = family.constructor
	}

	for _, family := range []struct {
		name        string
		constructor *Object
		args        []Value
	}{
		{"Intl.Locale", r.getIntlLocale(), []Value{asciiString("en")}},
		{"Intl.DisplayNames", r.getIntlDisplayNames(), []Value{asciiString("en"), r.intlResult("type", "language")}},
		{"Intl.ListFormat", r.getIntlListFormat(), nil},
		{"Intl.Segmenter", r.getIntlSegmenter(), nil},
		{"Intl.DurationFormat", r.getIntlDurationFormat(), nil},
	} {
		instance, err := r.New(family.constructor, family.args...)
		if err != nil {
			t.Fatal(err)
		}
		owners[family.name] = instance
		owners[family.name+"Constructor"] = family.constructor
		intlConstructors[family.name] = family.constructor
	}

	segmenter, err := r.New(r.getIntlSegmenter())
	if err != nil {
		t.Fatal(err)
	}
	segment, _ := AssertFunction(segmenter.Get("segment"))
	segments, err := segment(segmenter, asciiString("test"))
	if err != nil {
		t.Fatal(err)
	}
	owners["Intl.Segments"] = segments.(*Object)
	iterator, _ := AssertFunction(segments.(*Object).GetSymbol(SymIterator))
	iter, err := iterator(segments)
	if err != nil {
		t.Fatal(err)
	}
	owners["Intl.SegmentIterator"] = iter.(*Object)

	regexp, err := r.New(r.getRegExp(), asciiString("(?<name>a)"), asciiString("d"))
	if err != nil {
		t.Fatal(err)
	}
	owners["RegExp"] = regexp
	owners["RegExpConstructor"] = r.getRegExp()
	execute, _ := AssertFunction(regexp.Get("exec"))
	match, err := execute(regexp, asciiString("a"))
	if err != nil {
		t.Fatal(err)
	}
	owners["RegExpExecArray"] = match.(*Object)
	owners["RegExpMatchArray"] = match.(*Object)
	owners["RegExpIndicesArray"] = match.(*Object).Get("indices").(*Object)
	ownerNames := make([]string, 0, len(owners))
	for name := range owners {
		ownerNames = append(ownerNames, name)
	}
	sort.Slice(ownerNames, func(i, j int) bool {
		if len(ownerNames[i]) != len(ownerNames[j]) {
			return len(ownerNames[i]) > len(ownerNames[j])
		}
		return ownerNames[i] < ownerNames[j]
	})
	symbols := map[string]*Symbol{"iterator": SymIterator, "asyncIterator": SymAsyncIterator, "toPrimitive": SymToPrimitive, "hasInstance": SymHasInstance, "metadata": SymMetadata, "match": SymMatch, "matchAll": SymMatchAll, "replace": SymReplace, "search": SymSearch, "split": SymSplit, "toStringTag": SymToStringTag, "species": SymSpecies, "unscopables": SymUnscopables, "dispose": SymDispose, "asyncDispose": SymAsyncDispose}
	references := map[string][]string{}
	for _, library := range inventory.Libraries {
		references[library.Name] = library.References
	}
	mandatory := map[string]bool{}
	var include func(string)
	include = func(name string) {
		if mandatory[name] {
			return
		}
		mandatory[name] = true
		for _, ref := range references[name] {
			include("lib." + ref + ".d.ts")
		}
	}
	include("lib.es2025.d.ts")
	var temporalProfile func(string, map[string]bool) bool
	temporalProfile = func(name string, visited map[string]bool) bool {
		if name == "lib.esnext.temporal.d.ts" {
			return true
		}
		if visited[name] {
			return false
		}
		visited[name] = true
		for _, ref := range references[name] {
			if temporalProfile("lib."+ref+".d.ts", visited) {
				return true
			}
		}
		return false
	}
	seen := make(map[string]bool)
	for _, library := range inventory.Libraries {
		if library.External || !mandatory[library.Name] && temporalProfile(library.Name, map[string]bool{}) {
			continue
		}
		for _, declaration := range library.Declarations {
			if declaration.Kind == "KindInterfaceDeclaration" || declaration.Kind == "KindTypeAliasDeclaration" {
				continue
			}
			if mandatory[library.Name] && !strings.Contains(declaration.Name, ".") && (declaration.Kind == "KindVariableDeclaration" || declaration.Kind == "KindFunctionDeclaration" || declaration.Kind == "KindModuleDeclaration") {
				value := r.Get(declaration.Name)
				if value == nil || IsUndefined(value) {
					t.Errorf("missing standard global %s", declaration.Name)
				}
				if declaration.Kind == "KindFunctionDeclaration" {
					if _, ok := AssertFunction(value); !ok {
						t.Errorf("standard global is not callable: %s", declaration.Name)
					}
				}
			}
			var name string
			var object *Object
			for _, owner := range ownerNames {
				if suffix, ok := strings.CutPrefix(declaration.Name, owner+"."); ok {
					name = suffix
					object = owners[owner]
					if constructor := intlConstructors[owner]; constructor != nil && (name == "<construct>" || name == "<call>" || name == "supportedLocalesOf" || name == "prototype") {
						object = constructor
					}
					break
				}
			}
			if object == nil || seen[declaration.Name] {
				continue
			}
			// Nested signatures describe inline option bags, not properties of
			// the constructor or its prototype (e.g. fromBase64.alphabet).
			if strings.Contains(name, ".") {
				continue
			}
			seen[declaration.Name] = true
			if name == "<index>" || name == "cause" || name == "stack" {
				continue
			}

			// Upstream GeneratorFunction interfaces expose structural construct
			// signatures although ECMAScript generator function instances are
			// non-constructable. Audit their actual required runtime behavior.
			if declaration.Name == "GeneratorFunction.<construct>" || declaration.Name == "AsyncGeneratorFunction.<construct>" {
				if object.self.assertConstructor() != nil {
					t.Errorf("generator function is constructable: %s", declaration.Name)
				}
				continue
			}
			if name == "<construct>" {
				if object.self.assertConstructor() == nil {
					t.Errorf("%s: %s is not constructable", library.Name, declaration.Name)
				}
				continue
			}
			if name == "<call>" {
				if _, ok := object.self.assertCallable(); !ok {
					t.Errorf("%s: %s is not callable", library.Name, declaration.Name)
				}
				continue
			}
			var value Value
			present := false
			if symbol := symbols[declaration.Symbol]; symbol != nil {
				present = object.self.hasPropertySym(symbol)
				if declaration.Kind == "KindMethodSignature" || declaration.Kind == "KindFunctionDeclaration" {
					value = object.GetSymbol(symbol)
				}
			} else {
				present = object.self.hasPropertyStr(unistring.NewFromString(name))
				if declaration.Kind == "KindMethodSignature" || declaration.Kind == "KindFunctionDeclaration" {
					value = object.Get(name)
				}
			}
			if !present {
				t.Errorf("%s: missing %s", library.Name, declaration.Name)
				continue
			}
			if declaration.Kind == "KindMethodSignature" || declaration.Kind == "KindFunctionDeclaration" {
				if _, ok := AssertFunction(value); !ok {
					t.Errorf("%s: %s is not a runtime method", library.Name, declaration.Name)
				}
			}
		}
	}
	if len(seen) < 150 {
		t.Fatalf("inventory audit did not cover expected families: %d", len(seen))
	}
	t.Logf("audited %d distinct declared members and signatures", len(seen))
}
