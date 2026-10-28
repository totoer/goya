package goya

import (
	"reflect"
	"runtime"
	"testing"
	"time"
	"weak"
)

func waitHostGC(t *testing.T, collected func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		runtime.GC()
		if collected() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("host objects or bookkeeping retained after GC")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestHostViewCacheIdentity(t *testing.T) {
	cache := &hostViewCache{}
	object := New().NewObject()
	key := hostViewKey{reflect.TypeFor[*struct{ Name string }](), 1, "readonly"}
	cache.put(key, object)
	if cache.get(key) != object {
		t.Fatal("live cache entry lost its identity")
	}
	otherType, otherContract := key, key
	otherType.typ = reflect.TypeFor[*struct{ Other string }]()
	otherContract.schema = "mutable"
	if cache.get(otherType) != nil || cache.get(otherContract) != nil {
		t.Fatal("cache merged distinct Go types or contracts")
	}
	runtime.KeepAlive(object)
}

func putTransientHostView(cache *hostViewCache, key hostViewKey) weak.Pointer[Object] {
	object := New().NewObject()
	cache.put(key, object)
	return weak.Make(object)
}

func TestHostViewCacheDoesNotRetainObject(t *testing.T) {
	cache := &hostViewCache{}
	key := hostViewKey{reflect.TypeFor[*struct{ Name string }](), 1, "readonly"}
	ref := putTransientHostView(cache, key)
	waitHostGC(t, func() bool { return ref.Value() == nil })
	if cache.get(key) != nil {
		t.Fatal("dead cache entry returned an object")
	}
	runtime.KeepAlive(cache)
}

func TestHostViewCacheDelayedCleanup(t *testing.T) {
	cache := &hostViewCache{}
	key := hostViewKey{reflect.TypeFor[*struct{ Name string }](), 1, "readonly"}
	old := putTransientHostView(cache, key)
	waitHostGC(t, func() bool { return old.Value() == nil })
	object := New().NewObject()
	cache.put(key, object)
	cleanupHostView(hostViewCleanup{cache, key, old})
	if cache.get(key) != object {
		t.Fatal("old cleanup deleted the replacement view")
	}
	cleanupHostView(hostViewCleanup{cache, key, weak.Make(object)})
	cache.mu.Lock()
	empty := cache.objects == nil
	cache.mu.Unlock()
	if !empty {
		t.Fatal("empty cache retained its map")
	}
	runtime.KeepAlive(object)
}

type hostGCSource struct {
	Name    string
	Next    *hostGCSource
	payload *[4096]byte
	owner   *Object
}

func (s *hostGCSource) Label() string { return s.Name }

type hostGCRefs struct {
	view    weak.Pointer[Object]
	source  weak.Pointer[hostGCSource]
	payload weak.Pointer[[4096]byte]
}

func (refs hostGCRefs) collected() bool {
	return refs.view.Value() == nil && refs.source.Value() == nil && refs.payload.Value() == nil
}

func bindHostGC(t *testing.T, r *Runtime, value any, mutable bool) Value {
	t.Helper()
	g, err := describeHostType(reflect.TypeOf(value), nil, mutable, NumericPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := newHostConversion(r, g).bind(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func callHostGC(t *testing.T, function Value, args ...Value) Value {
	t.Helper()
	call, ok := AssertFunction(function)
	if !ok {
		t.Fatal("host value is not callable")
	}
	result, err := call(_undefined, args...)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func hostCacheSize(cache *hostViewCache) int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return len(cache.objects)
}

func discardHostResults(t *testing.T, r *Runtime, count int) []hostGCRefs {
	refs := make([]hostGCRefs, 0, count)
	factory := bindHostGC(t, r, func() *hostGCSource {
		source := &hostGCSource{Name: "transient", payload: new([4096]byte)}
		refs = append(refs, hostGCRefs{source: weak.Make(source), payload: weak.Make(source.payload)})
		return source
	}, false)
	for i := range count {
		view := callHostGC(t, factory).(*Object)
		refs[i].view = weak.Make(view)
	}
	return refs
}

func TestHostViewDiscardedResultsCollectedWhileIdle(t *testing.T) {
	r := New()
	refs := discardHostResults(t, r, 1000)
	waitHostGC(t, func() bool {
		for _, ref := range refs {
			if !ref.collected() {
				return false
			}
		}
		return hostCacheSize(r.hostViews) == 0
	})
	runtime.KeepAlive(r)
}

func exerciseHostViewsDuringGC(t *testing.T, r *Runtime) []hostGCRefs {
	stop, done, started := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		runtime.GC()
		close(started)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	defer func() { close(stop); <-done }()
	<-started
	var refs []hostGCRefs
	for range 8 {
		refs = append(refs, discardHostResults(t, r, 128)...)
	}
	return refs
}

func TestHostViewConversionsDuringCleanup(t *testing.T) {
	r := New()
	view, source := newHostGCView(t, r)
	refs := exerciseHostViewsDuringGC(t, r)
	waitHostGC(t, func() bool {
		for _, ref := range refs {
			if !ref.collected() {
				return false
			}
		}
		return hostCacheSize(r.hostViews) == 1
	})
	assertLiveHostGCView(t, r, view, source)
	runtime.KeepAlive(view)
}

func newHostGCView(t *testing.T, r *Runtime) (*Object, hostGCRefs) {
	source := &hostGCSource{Name: "live", payload: new([4096]byte)}
	source.Next = source
	view := bindHostGC(t, r, source, true).(*Object)
	return view, hostGCRefs{weak.Make(view), weak.Make(source), weak.Make(source.payload)}
}

func assertLiveHostGCView(t *testing.T, r *Runtime, view *Object, refs hostGCRefs) {
	t.Helper()
	runtime.GC()
	if refs.source.Value() == nil || refs.payload.Value() == nil || view.Get("Name").String() != "live" || view.Get("Next") != view {
		t.Fatal("live view lost its source or recursive identity")
	}
	identity := bindHostGC(t, r, func(source *hostGCSource) *hostGCSource { return source }, true)
	if callHostGC(t, identity, view) != view || callHostGC(t, view.Get("Label")).String() != "live" {
		t.Fatal("live view lost its round trip or method receiver")
	}
	if err := view.Set("Name", "updated"); err != nil || refs.source.Value().Name != "updated" {
		t.Fatalf("live mutable view did not update its source: %v", err)
	}
}

func TestHostViewGoRootProtectsSource(t *testing.T) {
	r := New()
	view, refs := newHostGCView(t, r)
	assertLiveHostGCView(t, r, view, refs)
	runtime.KeepAlive(view)
}

func scriptRootedHostView(t *testing.T, r *Runtime) hostGCRefs {
	view, refs := newHostGCView(t, r)
	if err := r.Set("makeView", bindHostGC(t, r, func() *hostGCSource { return refs.source.Value() }, true)); err != nil {
		t.Fatal(err)
	}
	// Only the script's global property will own the view after this returns.
	if _, err := r.RunString(`const host = globalThis as any; host.heldView = host.makeView(); undefined;`); err != nil {
		t.Fatal(err)
	}
	if r.Get("heldView") != view {
		t.Fatal("script received a different view")
	}
	return refs
}

func TestHostViewScriptRootProtectsSource(t *testing.T) {
	r := New()
	refs := scriptRootedHostView(t, r)
	runtime.GC()
	view := r.Get("heldView").(*Object)
	assertLiveHostGCView(t, r, view, refs)
	runtime.KeepAlive(r)
}

func weakHostViewForSource(t *testing.T, r *Runtime, source *hostGCSource) weak.Pointer[Object] {
	return weak.Make(bindHostGC(t, r, source, false).(*Object))
}

func TestHostViewCollectedWithHostOwnedSource(t *testing.T) {
	r := New()
	source := &hostGCSource{Name: "host owned", payload: new([4096]byte)}
	ref := weakHostViewForSource(t, r, source)
	waitHostGC(t, func() bool { return ref.Value() == nil && hostCacheSize(r.hostViews) == 0 })
	view := bindHostGC(t, r, source, false).(*Object)
	if weak.Make(view) == ref || view.Get("Name").String() != "host owned" || source.payload == nil {
		t.Fatal("new view did not preserve the host-owned source")
	}
	runtime.KeepAlive(source)
	runtime.KeepAlive(r)
}

func cyclicHostView(t *testing.T, r *Runtime, mixed bool) hostGCRefs {
	view, refs := newHostGCView(t, r)
	if mixed {
		refs.source.Value().owner = view
		if err := view.Set("self", view); err != nil {
			t.Fatal(err)
		}
	}
	return refs
}

func TestHostViewUnreachableCyclesCollected(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "Go cycle", true: "Go and JS cycle"}[mixed], func(t *testing.T) {
			r := New()
			refs := cyclicHostView(t, r, mixed)
			waitHostGC(t, func() bool { return refs.collected() && hostCacheSize(r.hostViews) == 0 })
			runtime.KeepAlive(r)
		})
	}
}

func abandonedHostRuntime(t *testing.T) (weak.Pointer[Runtime], hostGCRefs, *hostViewCache) {
	r := New()
	refs := cyclicHostView(t, r, true)
	return weak.Make(r), refs, r.hostViews
}

func TestHostViewCacheDoesNotRetainRuntime(t *testing.T) {
	ref, objects, cache := abandonedHostRuntime(t)
	waitHostGC(t, func() bool { return ref.Value() == nil && objects.collected() && hostCacheSize(cache) == 0 })
	runtime.KeepAlive(cache)
}

func TestHostViewContractsAndRealms(t *testing.T) {
	type contractSource struct {
		Title string `ts:"title"`
	}
	type otherSource contractSource
	source := &contractSource{Title: "original"}
	r := New()
	readonly := bindHostGC(t, r, source, false).(*Object)
	mutable := bindHostGC(t, r, source, true).(*Object)
	if readonly == mutable || readonly.Set("Title", "forbidden") == nil {
		t.Fatal("readonly and mutable contracts were merged")
	}
	if err := mutable.Set("Title", "updated"); err != nil || readonly.Get("Title").String() != "updated" {
		t.Fatalf("mutable view did not update shared source: %v", err)
	}
	g, err := describeHostType(reflect.TypeOf(source), TagFieldNameMapper("ts", false), false, NumericPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := newHostConversion(r, g).bind(source)
	if err != nil || mapped == readonly || mapped.(*Object).Get("title").String() != "updated" || mapped.(*Object).Get("Title") != nil {
		t.Fatalf("mapper contracts were merged: %v", err)
	}
	// These pointers have the same address and view schema, but different types.
	other := bindHostGC(t, r, (*otherSource)(source), false).(*Object)
	if other == readonly {
		t.Fatal("views with distinct Go pointer types were merged")
	}
	second := New()
	if bindHostGC(t, second, source, false) == readonly {
		t.Fatal("different runtimes shared a view")
	}
	accept := bindHostGC(t, second, func(*contractSource) bool { return true }, false)
	call, _ := AssertFunction(accept)
	if _, err := call(_undefined, readonly); err == nil {
		t.Fatal("host boundary accepted a view from another runtime")
	}
	runtime.KeepAlive(readonly)
	runtime.KeepAlive(mapped)
	runtime.KeepAlive(other)
}

func hostViewWithDiscardedSibling(t *testing.T, r *Runtime) (*Object, weak.Pointer[Object], hostGCRefs) {
	var refs hostGCRefs
	factory := bindHostGC(t, r, func() (*hostGCSource, []*hostGCSource) {
		kept := &hostGCSource{Name: "kept"}
		discarded := &hostGCSource{Name: "discarded", payload: new([4096]byte)}
		refs.source, refs.payload = weak.Make(discarded), weak.Make(discarded.payload)
		return kept, []*hostGCSource{discarded}
	}, false)
	tuple := callHostGC(t, factory).(*Object)
	view := tuple.Get("0").(*Object)
	array := tuple.Get("1").(*Object)
	refs.view = weak.Make(array.Get("0").(*Object))
	return view, weak.Make(array), refs
}

func TestHostViewDoesNotRetainSiblingResults(t *testing.T) {
	r := New()
	view, array, refs := hostViewWithDiscardedSibling(t, r)
	waitHostGC(t, func() bool { return array.Value() == nil && refs.collected() })
	if view.Get("Name").String() != "kept" || callHostGC(t, view.Get("Label")).String() != "kept" {
		t.Fatal("retained view lost its receiver")
	}
	runtime.KeepAlive(view)
}

type hostArgumentRefs struct {
	array  weak.Pointer[Object]
	object weak.Pointer[Object]
	copy   weak.Pointer[hostGCSource]
}

func (refs hostArgumentRefs) collected() bool {
	return refs.array.Value() == nil && refs.object.Value() == nil && refs.copy.Value() == nil
}

func hostGCArgument(t *testing.T, r *Runtime) (*Object, hostArgumentRefs) {
	object := r.NewObject()
	if err := object.Set("Name", "argument"); err != nil {
		t.Fatal(err)
	}
	if err := object.Set("Next", Null()); err != nil {
		t.Fatal(err)
	}
	array := r.NewArray(object)
	return array, hostArgumentRefs{array: weak.Make(array), object: weak.Make(object)}
}

func returnedHostFunction(t *testing.T, r *Runtime) (Value, hostArgumentRefs) {
	array, refs := hostGCArgument(t, r)
	factory := bindHostGC(t, r, func(args []*hostGCSource) func() string {
		refs.copy = weak.Make(args[0])
		return func() string { return "returned" }
	}, false)
	function := callHostGC(t, factory, array)
	return function, refs
}

func TestHostReturnedFunctionDoesNotRetainArguments(t *testing.T) {
	r := New()
	function, refs := returnedHostFunction(t, r)
	waitHostGC(t, refs.collected)
	if callHostGC(t, function).String() != "returned" {
		t.Fatal("returned function is no longer callable")
	}
	runtime.KeepAlive(function)
}

func retainedHostCallback(t *testing.T, r *Runtime) (func() string, hostArgumentRefs) {
	array, refs := hostGCArgument(t, r)
	var saved func() string
	save := bindHostGC(t, r, func(args []*hostGCSource, callback func() string) {
		refs.copy = weak.Make(args[0])
		saved = callback
	}, false)
	callback, err := r.RunString(`() => "callback";`)
	if err != nil {
		t.Fatal(err)
	}
	callHostGC(t, save, array, callback)
	return saved, refs
}

func TestHostCallbackDoesNotRetainArguments(t *testing.T) {
	r := New()
	callback, refs := retainedHostCallback(t, r)
	waitHostGC(t, refs.collected)
	if callback() != "callback" {
		t.Fatal("saved callback is no longer callable")
	}
	runtime.KeepAlive(callback)
}

func retainedHostMember(t *testing.T, r *Runtime, kind string) (Value, hostGCRefs, hostArgumentRefs) {
	array, args := hostGCArgument(t, r)
	var refs hostGCRefs
	factory := bindHostGC(t, r, func(sources []*hostGCSource) *hostGCSource {
		args.copy = weak.Make(sources[0])
		source := &hostGCSource{Name: "receiver", payload: new([4096]byte)}
		refs.source, refs.payload = weak.Make(source), weak.Make(source.payload)
		return source
	}, true)
	view := callHostGC(t, factory, array).(*Object)
	refs.view = weak.Make(view)
	var member Value
	switch kind {
	case "method":
		member = view.Get("Label")
	case "getter":
		member = view.self.getOwnPropStr("Name").(*valueProperty).getterFunc
	case "setter":
		member = view.self.getOwnPropStr("Name").(*valueProperty).setterFunc
	}
	return member, refs, args
}

func exerciseRetainedHostMember(t *testing.T, r *Runtime, kind string) hostGCRefs {
	member, refs, args := retainedHostMember(t, r, kind)
	waitHostGC(t, func() bool { return args.collected() && refs.view.Value() == nil && hostCacheSize(r.hostViews) == 0 })
	if refs.source.Value() == nil || refs.payload.Value() == nil {
		t.Fatal("extracted member lost its source")
	}
	if kind == "setter" {
		callHostGC(t, member, r.ToValue("changed"))
		if refs.source.Value().Name != "changed" {
			t.Fatal("extracted setter did not update its receiver")
		}
	} else if callHostGC(t, member).String() != "receiver" {
		t.Fatal("extracted member used the wrong receiver")
	}
	runtime.KeepAlive(member)
	return refs
}

func TestHostExtractedMembersKeepOnlyReceiver(t *testing.T) {
	for _, kind := range []string{"method", "getter", "setter"} {
		t.Run(kind, func(t *testing.T) {
			r := New()
			refs := exerciseRetainedHostMember(t, r, kind)
			waitHostGC(t, refs.collected)
			runtime.KeepAlive(r)
		})
	}
}
