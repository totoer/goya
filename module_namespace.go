package goya

import (
	"slices"

	"github.com/totoer/goya/unistring"
)

type moduleCell struct {
	name   string
	getter func(FunctionCall) Value
}

func (c *moduleCell) get(r *Runtime) Value {
	if c.getter == nil {
		panic(r.newError(r.getReferenceError(), "Cannot access export '"+c.name+"' before initialization"))
	}
	return c.getter(FunctionCall{This: _undefined})
}

// Module namespace properties are live data descriptors. They cannot be
// assigned, despite the mandated writable:true descriptor, and have null proto.
type moduleNamespaceObject struct {
	baseObject
	cells    map[unistring.String]*moduleCell
	deferred *moduleInstance
}

func (n *moduleNamespaceObject) ensureEvaluation() {
	if n.deferred != nil {
		n.val.runtime.evaluateModuleSynchronously(n.deferred)
	}
}
func (n *moduleNamespaceObject) symbolLike(name unistring.String) bool {
	return n.deferred != nil && name == "then"
}

func (r *Runtime) newModuleNamespace(cells map[string]*moduleCell) *Object {
	object := &Object{runtime: r}
	namespace := &moduleNamespaceObject{cells: make(map[unistring.String]*moduleCell)}
	namespace.val = object
	namespace.class = classObject
	namespace.extensible = true
	object.self = namespace
	namespace.init()
	names := sortedKeys(cells)
	// Module namespace ordering is UTF-16 lexicographic, including numeric keys.
	slices.SortFunc(names, func(a, b string) int { return int(newStringValue(a).CompareTo(newStringValue(b))) })
	for _, name := range names {
		key := unistring.NewFromString(name)
		namespace.cells[key] = cells[name]
		namespace._putProp(key, _undefined, true, true, false)
	}
	namespace._putSym(SymToStringTag, valueProp(newStringValue("Module"), false, false, false))
	namespace.prototype = nil
	namespace.extensible = false
	return object
}
func (n *moduleNamespaceObject) getStr(name unistring.String, _ Value) Value {
	if n.symbolLike(name) {
		return nil
	}
	n.ensureEvaluation()
	if cell := n.cells[name]; cell != nil {
		return cell.get(n.val.runtime)
	}
	return nil
}
func (n *moduleNamespaceObject) getOwnPropStr(name unistring.String) Value {
	if n.symbolLike(name) {
		return nil
	}
	n.ensureEvaluation()
	if cell := n.cells[name]; cell != nil {
		return valueProp(cell.get(n.val.runtime), true, true, false)
	}
	return nil
}
func (n *moduleNamespaceObject) hasAndGetStr(name unistring.String, receiver Value) (Value, bool) {
	if n.symbolLike(name) {
		return nil, false
	}
	n.ensureEvaluation()
	if _, ok := n.cells[name]; ok {
		return n.getStr(name, receiver), true
	}
	return nil, false
}
func (n *moduleNamespaceObject) setOwnStr(name unistring.String, _ Value, throw bool) bool {
	n.val.runtime.typeErrorResult(throw, "Cannot assign to module namespace property '%s'", name)
	return false
}
func (n *moduleNamespaceObject) setForeignStr(name unistring.String, val, receiver Value, throw bool) (bool, bool) {
	return n.setOwnStr(name, val, throw), true
}
func (n *moduleNamespaceObject) setForeignIdx(name valueInt, val, receiver Value, throw bool) (bool, bool) {
	return n.setOwnStr(name.string(), val, throw), true
}
func (n *moduleNamespaceObject) setForeignSym(name *Symbol, val, receiver Value, throw bool) (bool, bool) {
	n.val.runtime.typeErrorResult(throw, "Cannot assign to module namespace symbol property")
	return false, true
}
func (n *moduleNamespaceObject) deleteStr(name unistring.String, throw bool) bool {
	if n.symbolLike(name) {
		return true
	}
	n.ensureEvaluation()
	if _, ok := n.cells[name]; ok {
		n.val.runtime.typeErrorResult(throw, "Cannot delete module namespace property '%s'", name)
		return false
	}
	return true
}
func (n *moduleNamespaceObject) defineOwnPropertyStr(name unistring.String, descriptor PropertyDescriptor, throw bool) bool {
	if !n.symbolLike(name) {
		n.ensureEvaluation()
	}
	cell := n.cells[name]
	if cell != nil && descriptor.Getter == nil && descriptor.Setter == nil && descriptor.Configurable != FLAG_TRUE && descriptor.Enumerable != FLAG_FALSE && descriptor.Writable != FLAG_FALSE {
		value := cell.get(n.val.runtime)
		if descriptor.Value == nil || descriptor.Value.SameAs(value) {
			return true
		}
	}
	n.val.runtime.typeErrorResult(throw, "Cannot redefine module namespace property '%s'", name)
	return false
}
func (n *moduleNamespaceObject) stringKeys(_ bool, accum []Value) []Value {
	n.ensureEvaluation()
	for _, name := range n.propNames {
		accum = append(accum, stringValueFromRaw(name))
	}
	return accum
}
func (n *moduleNamespaceObject) iterateStringKeys() iterNextFunc {
	n.ensureEvaluation()
	index := 0
	var next iterNextFunc
	next = func() (propIterItem, iterNextFunc) {
		if index >= len(n.propNames) {
			return propIterItem{}, nil
		}
		name := n.propNames[index]
		index++
		return propIterItem{name: stringValueFromRaw(name), enumerable: _ENUM_TRUE}, next
	}
	return next
}

func (n *moduleNamespaceObject) hasOwnPropertyStr(name unistring.String) bool {
	if n.symbolLike(name) {
		return false
	}
	n.ensureEvaluation()
	_, exists := n.cells[name]
	return exists
}
func (n *moduleNamespaceObject) symbols(all bool, accum []Value) []Value {
	n.ensureEvaluation()
	return n.baseObject.symbols(all, accum)
}
func (n *moduleNamespaceObject) iterateSymbols() iterNextFunc {
	n.ensureEvaluation()
	return n.baseObject.iterateSymbols()
}
