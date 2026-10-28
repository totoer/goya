package goya

import (
	"github.com/totoer/goya/internal/tsfrontend"
	"strings"
)

type moduleState uint8

const (
	moduleAllocated moduleState = iota
	moduleInstantiated
	moduleEvaluating
	moduleEvaluated
	moduleFailed
)

type moduleInstance struct {
	code               *compiledModule
	namespace          *Object
	metadata           *Object
	locals             map[string]*moduleCell
	exports            map[string]*moduleCell
	dependencies       []*moduleInstance
	iterator           *Object
	state              moduleState
	failure            any
	interruption       error
	reflectLookup      Value
	evaluation         *promiseCapability
	component          *moduleComponent
	moduleObject       *Object
	cjsExports         *Object
	cjsNamespaceValues map[string]Value
	requireNamespace   *Object
	checking           *typeScriptProgram
	deferredNamespace  *Object
	plan               []*moduleInstance
	visiting           bool
}

func (r *Runtime) moduleCall(function Value, this Value, args ...Value) Value {
	object, ok := function.(*Object)
	if !ok {
		panic(r.NewTypeError("module operation is not callable"))
	}
	call, ok := object.self.assertCallable()
	if !ok {
		panic(r.NewTypeError("module operation is not callable"))
	}
	return call(FunctionCall{This: this, Arguments: args})
}

func (r *Runtime) runModuleGraph(p *Program, completionOnly ...bool) (result Value, err error) {
	if r.moduleInstances == nil {
		r.moduleInstances = make(map[string]*moduleInstance)
	}
	info := p.typescript
	for id, unit := range info.units {
		if existing := r.moduleInstances[id]; existing != nil && existing.interruption != nil {
			return nil, existing.interruption
		}
		if existing := r.moduleInstances[id]; existing != nil && existing.code != nil && existing.code.fingerprint != unit.fingerprint {
			return nil, &BindingCompatibilityError{Name: id, Reason: "loaded module has different checked code, compiler profile or import permissions"}
		}
	}
	var records []*moduleInstance
	var createdKeys []string
	linked := false
	var allocate func(string) *moduleInstance
	allocate = func(id string) *moduleInstance {
		key := id
		if existing := r.moduleInstances[key]; existing != nil {
			return existing
		}
		instance := &moduleInstance{locals: make(map[string]*moduleCell), exports: make(map[string]*moduleCell)}
		instance.checking = info
		instance.metadata = r.NewObject()
		if err := instance.metadata.SetPrototype(nil); err != nil {
			panic(err)
		}
		r.moduleInstances[key] = instance
		createdKeys = append(createdKeys, key)
		records = append(records, instance)
		if strings.HasPrefix(id, "host:") {
			specifier := strings.TrimPrefix(id, "host:")
			values, ok := r.hostModules[specifier]
			if !ok {
				panic(r.NewTypeError("missing host module %s", specifier))
			}
			for name, value := range values {
				instance.exports[name] = &moduleCell{name: name, getter: func(FunctionCall) Value { return value }}
			}
			instance.namespace = r.newModuleNamespace(instance.exports)
			instance.state = moduleEvaluated
			return instance
		}
		instance.code = info.units[id]
		if instance.code == nil {
			panic(r.NewTypeError("missing checked source module %s", id))
		}
		instance.cjsExports = r.NewObject()
		instance.moduleObject = r.NewObject()
		r.tsSet(instance.moduleObject, "exports", instance.cjsExports)
		if instance.code.commonJS {
			instance.cjsNamespaceValues = make(map[string]Value)
		}
		localBindings := make(map[string]*moduleCell)
		for _, export := range instance.code.exports {
			if !export.star && export.from < 0 {
				if instance.code.commonJS {
					name := export.name
					instance.locals[name] = &moduleCell{name: name, getter: func(FunctionCall) Value { return nilSafe(instance.cjsNamespaceValues[name]) }}
					continue
				}
				cell := localBindings[export.localName]
				if cell == nil {
					cell = &moduleCell{name: export.name}
					localBindings[export.localName] = cell
				}
				instance.locals[export.name] = cell
			}
		}
		for _, dependency := range instance.code.dependencies {
			if dependency.Phase != "evaluation" && dependency.Phase != "require" && dependency.Phase != "defer" {
				panic(r.NewTypeError("module phase %s is not implemented", dependency.Phase))
			}
			target := info.units[dependency.ID]
			if message := tsfrontend.ValidateImportAttributes(dependency.Attributes, target != nil && target.json, dependency.Phase == "require"); message != "" {
				panic(r.NewTypeError("%s", message))
			}
			instance.dependencies = append(instance.dependencies, allocate(dependency.ID))
		}
		return instance
	}
	// Names can circulate through export-star cycles. Gather candidates first;
	// ResolveExport below removes ambiguous or unresolvable candidates.
	var names func(*moduleInstance, map[*moduleInstance]bool) map[string]bool
	names = func(instance *moduleInstance, seen map[*moduleInstance]bool) map[string]bool {
		result := make(map[string]bool)
		if seen[instance] {
			return result
		}
		seen[instance] = true
		if instance.code == nil {
			for name := range instance.exports {
				result[name] = true
			}
			return result
		}
		for _, export := range instance.code.exports {
			if !export.star {
				result[export.name] = true
				continue
			}
			for name := range names(instance.dependencies[export.from], seen) {
				if name != "default" {
					result[name] = true
				}
			}
		}
		return result
	}
	type exportVisit struct {
		module *moduleInstance
		name   string
	}
	ambiguous := &moduleCell{}
	var resolve func(*moduleInstance, string, map[exportVisit]bool) *moduleCell
	resolve = func(instance *moduleInstance, name string, seen map[exportVisit]bool) *moduleCell {
		if instance.code == nil {
			return instance.exports[name]
		}
		visit := exportVisit{instance, name}
		if seen[visit] {
			return nil
		}
		seen[visit] = true
		defer delete(seen, visit)
		for _, export := range instance.code.exports {
			if export.star || export.name != name {
				continue
			}
			if export.from < 0 {
				return instance.locals[name]
			}
			dependency := instance.dependencies[export.from]
			if export.namespace {
				if cell := instance.locals[name]; cell != nil {
					return cell
				}
				cell := &moduleCell{name: name, getter: func(FunctionCall) Value {
					if instance.code.dependencies[export.from].Phase == "defer" {
						return r.deferredNamespace(dependency)
					}
					return dependency.namespace
				}}
				instance.locals[name] = cell
				return cell
			}
			return resolve(dependency, export.imported, seen)
		}
		if name == "default" {
			return nil
		}
		var candidate *moduleCell
		for _, export := range instance.code.exports {
			if !export.star {
				continue
			}
			cell := resolve(instance.dependencies[export.from], name, seen)
			if cell == ambiguous {
				return ambiguous
			}
			if cell == nil {
				continue
			}
			if candidate != nil && candidate != cell {
				return ambiguous
			}
			candidate = cell
		}
		return candidate
	}
	var instantiate func(*moduleInstance)
	instantiate = func(instance *moduleInstance) {
		if instance.state != moduleAllocated {
			return
		}
		if !instance.code.module {
			instance.state = moduleInstantiated
			return
		}
		factory, compileErr := r.runCheckedOperations(instance.code.code)
		if compileErr != nil {
			panic(compileErr)
		}
		register := r.newNativeFunc(func(call FunctionCall) Value {
			if IsUndefined(call.Argument(0)) {
				instance.reflectLookup = call.Argument(1)
				r.vm.stash.module = instance
				return _undefined
			}
			name := call.Argument(0).String()
			cell := instance.locals[name]
			object, ok := call.Argument(1).(*Object)
			if cell == nil || !ok {
				panic(r.NewTypeError("invalid internal export registration"))
			}
			getter, ok := object.self.assertCallable()
			if !ok {
				panic(r.NewTypeError("invalid internal export getter"))
			}
			cell.getter = getter
			return _undefined
		}, "register exports", 2)
		require := r.newNativeFunc(func(c FunctionCall) Value { return r.requireModule(instance, c.Argument(0).toString().String()) }, "", 1)
		arguments := []Value{register, instance.metadata, require, instance.cjsExports, instance.moduleObject}
		for i, dependency := range instance.dependencies {
			if instance.code.dependencies[i].Phase == "defer" {
				arguments = append(arguments, r.deferredNamespace(dependency))
			} else {
				arguments = append(arguments, dependency.namespace)
			}
		}
		iterator := r.moduleCall(factory, _undefined, arguments...).ToObject(r)
		instance.iterator = iterator
		first := r.moduleCall(iterator.Get("next"), iterator).ToObject(r)
		if first.Get("done").ToBoolean() {
			panic(r.NewTypeError("module factory failed to suspend after instantiation"))
		}
		instance.state = moduleInstantiated
	}
	err = r.runWrapped(func() {
		roots := make([]*moduleInstance, 0, len(info.roots))
		for _, id := range info.roots {
			roots = append(roots, allocate(id))
		}
		for _, instance := range records {
			if instance.namespace != nil {
				continue
			}
			for name := range names(instance, make(map[*moduleInstance]bool)) {
				if cell := resolve(instance, name, make(map[exportVisit]bool)); cell != nil && cell != ambiguous {
					instance.exports[name] = cell
				}
			}
			instance.namespace = r.newModuleNamespace(instance.exports)
		}
		for _, instance := range records {
			if instance.code == nil {
				continue
			}
			for _, export := range instance.code.exports {
				if !export.star && instance.exports[export.name] == nil {
					panic(r.NewTypeError("unresolvable module export %s", export.name))
				}
			}
			for index, dependency := range instance.code.dependencies {
				for _, name := range dependency.RequiredExports {
					if instance.dependencies[index].exports[name] == nil {
						panic(r.NewTypeError("module %s has no export %s", dependency.ID, name))
					}
				}
			}
		}
		for _, instance := range records {
			instantiate(instance)
		}
		linked = true
		r.prepareModuleComponents(roots)
		if len(completionOnly) > 1 && completionOnly[1] {
			var asyncRoots []*moduleInstance
			seen := map[*moduleInstance]bool{}
			for _, root := range roots {
				asyncRoots = append(asyncRoots, gatherAsyncModules(root, seen)...)
			}
			roots = asyncRoots
		}
		result = r.evaluateModuleRoots(roots, len(completionOnly) > 0 && completionOnly[0])
	})
	if err != nil {
		if !linked {
			for _, key := range createdKeys {
				delete(r.moduleInstances, key)
			}
		}
		return nil, err
	}
	return result, nil
}

// An interrupted initialization cannot be resumed: abrupt exit discards its
// queued continuation jobs. Keep its host-side error so canonical cache reuse
// cannot report a partially initialized module as successful or pending.
func (r *Runtime) abortModuleEvaluation(reason error) {
	for _, m := range r.moduleInstances {
		if m.state < moduleEvaluated {
			if m.component != nil {
				m.component.failed = true
			}
			m.interruption = reason
			m.state = moduleFailed
			m.failure = reason
		}
	}
}
