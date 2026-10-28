package goya_test

import (
	"testing"

	"github.com/totoer/goya"
)

type hostRecursiveMap map[string]hostRecursiveMap
type hostRecursiveSlice []hostRecursiveSlice
type hostInputNode struct{ Next *hostInputNode }

func TestHostCollectionCyclesAndInputCopies(t *testing.T) {
	mapping := make(hostRecursiveMap)
	mapping["self"] = mapping
	slice := make(hostRecursiveSlice, 1)
	slice[0] = slice
	registry := goya.NewRegistry()
	registerHost(t, registry, "mapping", mapping)
	registerHost(t, registry, "slice", slice)
	registerHost(t, registry, "mapCycle", func(v hostRecursiveMap) bool {
		v["changed"] = nil
		return v["self"]["changed"] == nil && len(v["self"]) == 2
	})
	registerHost(t, registry, "sliceCycle", func(v hostRecursiveSlice) bool { v[0][0] = nil; return v[0] == nil })
	registerHost(t, registry, "pointerCycle", func(v *hostInputNode) bool { return v != nil && v.Next == v })
	environment := freezeHost(t, registry)
	checkHost(t, environment, `const m = mapping?.self; const s = slice?.[0]; mapCycle(mapping); sliceCycle(slice);`, false)
	r := goya.New()
	if err := environment.Bind(r); err != nil {
		t.Fatal(err)
	}
	m := hostObject(t, r.Get("mapping"))
	s := hostObject(t, r.Get("slice"))
	if m.Get("self") != m || s.Get("0") != s {
		t.Fatal("output copy lost cycles")
	}
	if !hostCall(t, r, r.Get("mapCycle"), m).ToBoolean() || !hostCall(t, r, r.Get("sliceCycle"), s).ToBoolean() {
		t.Fatal("input copy lost cycles")
	}
	if m.Get("changed") != nil || s.Get("0") != s || len(mapping) != 1 || slice[0] == nil {
		t.Fatalf("input copy modified original object: added=%#v sameSlice=%v mapLength=%d sliceNil=%v", m.Get("changed"), s.Get("0") == s, len(mapping), slice[0] == nil)
	}
	plain := r.NewObject()
	if err := plain.Set("Next", plain); err != nil {
		t.Fatal(err)
	}
	if !hostCall(t, r, r.Get("pointerCycle"), plain).ToBoolean() {
		t.Fatal("plain TS object pointer cycle lost")
	}
}

func TestHostViewStateIsRuntimeLocal(t *testing.T) {
	node := &hostInputNode{}
	node.Next = node
	registry := goya.NewRegistry()
	registerHost(t, registry, "node", node)
	registerHost(t, registry, "same", func(v *hostInputNode) bool { return v == node })
	environment := freezeHost(t, registry)
	a, b := goya.New(), goya.New()
	if err := environment.Bind(a); err != nil {
		t.Fatal(err)
	}
	if err := environment.Bind(b); err != nil {
		t.Fatal(err)
	}
	if a.Get("node") == b.Get("node") {
		t.Fatal("runtime object shared across realms")
	}
	if !hostCall(t, a, a.Get("same"), a.Get("node")).ToBoolean() {
		t.Fatal("passing a host view lost its pointer identity")
	}
	if err := hostObject(t, a.Get("node")).Set("Next", goya.Null()); err == nil || node.Next != node {
		t.Fatal("readonly pointer field was modified")
	}
}
