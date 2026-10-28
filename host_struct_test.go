package goya_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/totoer/goya"
)

type hostNode struct {
	Name    string    `ts:"name"`
	Next    *hostNode `ts:"next"`
	Values  []int32   `ts:"values"`
	private string
}

func (n hostNode) Label() string       { return n.Name }
func (n *hostNode) Rename(name string) { n.Name = name }

func hostObject(t *testing.T, value goya.Value) *goya.Object {
	t.Helper()
	object, ok := value.(*goya.Object)
	if !ok {
		t.Fatalf("expected object, got %T", value)
	}
	return object
}
func hostCall(t *testing.T, r *goya.Runtime, value goya.Value, args ...goya.Value) goya.Value {
	t.Helper()
	function, ok := goya.AssertFunction(value)
	if !ok {
		t.Fatalf("expected function, got %T", value)
	}
	result, err := function(goya.Undefined(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHostStructMappedRecursiveView(t *testing.T) {
	node := &hostNode{Name: "first", Values: []int32{3, 4}, private: "hidden"}
	node.Next = node
	registry := goya.NewRegistry()
	registry.SetFieldNameMapper(goya.TagFieldNameMapper("ts", false))
	registerHost(t, registry, "node", node)
	registerHost(t, registry, "getNode", func() *hostNode { return node })
	registerHost(t, registry, "copy", *node)
	environment := freezeHost(t, registry)
	checkHost(t, environment, `if (node) { const a: string = node.name; const b: string | undefined = node.next?.name; node.Rename("ok"); const c: string = node.Label(); if (node.values) {const d: number = node.values[0];} } copy.name = "copy";`, false)
	checkHost(t, environment, `node!.name = "forbidden";`, true)
	checkHost(t, environment, `node!.Name; node!.private;`, true)
	r := goya.New()
	if err := environment.Bind(r); err != nil {
		t.Fatal(err)
	}
	object := hostObject(t, r.Get("node"))
	if object.Get("name").String() != "first" || object.Get("next") != object {
		t.Fatal("mapped fields or recursive identity lost")
	}
	if got := hostCall(t, r, r.Get("getNode")); got != object {
		t.Fatal("returned pointer has a different view")
	}
	if err := object.Set("name", "changed"); err == nil {
		t.Fatal("readonly view accepted write")
	}
	hostCall(t, r, object.Get("Rename"), r.ToValue("renamed"))
	if node.Name != "renamed" || object.Get("name").String() != "renamed" || hostCall(t, r, object.Get("Label")).String() != "renamed" {
		t.Fatal("method or live field uses a stale receiver")
	}
	copy := hostObject(t, r.Get("copy"))
	if err := copy.Set("name", "independent"); err != nil {
		t.Fatal(err)
	}
	if node.Name != "renamed" {
		t.Fatal("struct value writes reached host")
	}
	values := hostObject(t, object.Get("values"))
	if err := values.Set("0", 99); err != nil {
		t.Fatal(err)
	}
	if node.Values[0] != 3 {
		t.Fatal("slice was not copied")
	}
}

func TestHostStructMutableViewAndInput(t *testing.T) {
	type item struct {
		Title string `ts:"title"`
		Count int16  `ts:"count"`
	}
	value := &item{Title: "a", Count: 1}
	calls := 0
	registry := goya.NewRegistry()
	registry.SetFieldNameMapper(goya.TagFieldNameMapper("ts", false))
	registerHost(t, registry, "item", goya.HostBinding{Value: value, Mutable: true})
	registerHost(t, registry, "accept", func(v item) string { calls++; return v.Title })
	environment := freezeHost(t, registry)
	checkHost(t, environment, `item!.title = "b"; accept({title: "copy", count: 2});`, false)
	r := goya.New()
	if err := environment.Bind(r); err != nil {
		t.Fatal(err)
	}
	object := hostObject(t, r.Get("item"))
	if err := object.Set("title", "b"); err != nil {
		t.Fatal(err)
	}
	if value.Title != "b" {
		t.Fatal("mutable view does not write through")
	}
	if err := object.Set("count", 32768); err == nil || value.Count != 1 {
		t.Fatal("setter failed to validate range")
	}
	input := r.NewObject()
	if err := input.Set("title", "plain"); err != nil {
		t.Fatal(err)
	}
	if err := input.Set("count", 2); err != nil {
		t.Fatal(err)
	}
	if hostCall(t, r, r.Get("accept"), input).String() != "plain" || calls != 1 {
		t.Fatal("plain object conversion")
	}
	if err := input.Set("count", "2"); err != nil {
		t.Fatal(err)
	}
	accept, _ := goya.AssertFunction(r.Get("accept"))
	if _, err := accept(goya.Undefined(), input); err == nil || calls != 1 {
		t.Fatal("invalid property reached Go")
	}
}

func TestHostCollectionsAndNullability(t *testing.T) {
	registry := goya.NewRegistry()
	registerHost(t, registry, "tuple", [2]string{"a", "b"})
	registerHost(t, registry, "list", []int32{})
	registerHost(t, registry, "absent", []int32(nil))
	registerHost(t, registry, "dictionary", map[string]bool{"ready": true})
	registerHost(t, registry, "missingMap", map[string]bool(nil))
	registerHost(t, registry, "useTuple", func(v [2]string) string { return v[0] + v[1] })
	registerHost(t, registry, "useList", func(v []int32) bool { return v == nil })
	registerHost(t, registry, "useMap", func(v map[string]bool) bool { return v == nil })
	environment := freezeHost(t, registry)
	checkHost(t, environment, `const t: [string,string] = tuple; const n: number[] | null = list; const m: {[key:string]: boolean} | null = dictionary; useTuple(["a","b"]); useList(null); useMap({ready:true});`, false)
	checkHost(t, environment, `useTuple(["only"]); const n: number[] = absent;`, true)
	r := goya.New()
	if err := environment.Bind(r); err != nil {
		t.Fatal(err)
	}
	if !goya.IsNull(r.Get("absent")) || !goya.IsNull(r.Get("missingMap")) || goya.IsNull(r.Get("list")) {
		t.Fatal("nil and empty collections conflated")
	}
	if hostCall(t, r, r.Get("useTuple"), r.NewArray("a", "b")).String() != "ab" {
		t.Fatal("tuple conversion")
	}
	if !hostCall(t, r, r.Get("useList"), goya.Null()).ToBoolean() || hostCall(t, r, r.Get("useList"), r.NewArray()).ToBoolean() {
		t.Fatal("slice input nullability")
	}
	if !hostCall(t, r, r.Get("useMap"), goya.Null()).ToBoolean() || hostCall(t, r, r.Get("useMap"), r.NewObject()).ToBoolean() {
		t.Fatal("map input nullability")
	}
	function, _ := goya.AssertFunction(r.Get("useTuple"))
	if _, err := function(goya.Undefined(), r.NewArray("one")); err == nil {
		t.Fatal("wrong tuple length accepted")
	}
}

type mutableHostMapper struct{ prefix string }

func (m *mutableHostMapper) FieldName(_ reflect.Type, f reflect.StructField) string {
	return m.prefix + f.Name
}
func (m *mutableHostMapper) MethodName(_ reflect.Type, f reflect.Method) string {
	return m.prefix + f.Name
}
func TestHostMapperCapturedAtRegistration(t *testing.T) {
	mapper := &mutableHostMapper{prefix: "before"}
	registry := goya.NewRegistry()
	registry.SetFieldNameMapper(mapper)
	registerHost(t, registry, "value", struct{ Title string }{"title"})
	mapper.prefix = "after"
	environment := freezeHost(t, registry)
	checkHost(t, environment, `const a: string = value.beforeTitle;`, false)
	checkHost(t, environment, `value.afterTitle;`, true)
	r := goya.New()
	if err := environment.Bind(r); err != nil {
		t.Fatal(err)
	}
	if hostObject(t, r.Get("value")).Get("beforeTitle").String() != "title" {
		t.Fatal("runtime consulted changed mapper")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.Freeze(ctx); err == nil {
		t.Fatal("freeze ignored cancellation")
	}
}
