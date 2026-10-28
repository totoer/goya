package goya

import (
	"reflect"
	"runtime"
	"sync"
	"weak"
)

// The address is only an identity key; it is never converted back to a pointer.
// A live view owns the source, preventing address reuse while it is observable.
type hostViewKey struct {
	typ     reflect.Type
	pointer uintptr
	schema  string
}

// hostViewCache is also accessed by GC cleanups. It must not own a Runtime,
// a source pointer, or a view through any strong reference.
type hostViewCache struct {
	mu      sync.Mutex
	objects map[hostViewKey]weak.Pointer[Object]
}

func (c *hostViewCache) get(key hostViewKey) *Object {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.objects[key].Value()
}

func (c *hostViewCache) put(key hostViewKey, object *Object) {
	identity := weak.Make(object)
	c.mu.Lock()
	if c.objects == nil {
		c.objects = make(map[hostViewKey]weak.Pointer[Object])
	}
	c.objects[key] = identity
	c.mu.Unlock()
	runtime.AddCleanup(object, cleanupHostView, hostViewCleanup{c, key, identity})
	runtime.KeepAlive(object)
}

type hostViewCleanup struct {
	cache    *hostViewCache
	key      hostViewKey
	identity weak.Pointer[Object]
}

// This runs on a GC goroutine and touches only the independent cache.
func cleanupHostView(arg hostViewCleanup) {
	c := arg.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.objects[arg.key] == arg.identity {
		delete(c.objects, arg.key)
		if len(c.objects) == 0 {
			c.objects = nil
		}
	}
}

// hostViewObject owns its source independently of its exposed members.
// In particular, a struct without exported fields must still round trip to
// the original Go pointer.
type hostViewObject struct {
	baseObject
	source reflect.Value
}

func (r *Runtime) newHostView(source reflect.Value) *Object {
	object := &Object{runtime: r}
	view := &hostViewObject{
		baseObject: baseObject{val: object, prototype: r.global.ObjectPrototype, class: classObject, extensible: true},
		// Snapshot the pointer, rather than retaining an addressable field whose
		// value the host could subsequently replace.
		source: reflect.ValueOf(source.Interface()),
	}
	object.self = view
	view.init()
	return object
}
