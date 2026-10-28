package goya

import "github.com/totoer/goya/internal/tsfrontend"

type loadDynamicImport struct {
	info     *typeScriptProgram
	from     string
	deferred bool
}

func (op *loadDynamicImport) exec(vm *vm) {
	vm.push(vm.r.newNativeFunc(func(c FunctionCall) Value { return vm.r.dynamicImport(op.info, op.from, c, op.deferred) }, "import", 1))
	vm.pc++
}

func (r *Runtime) preparedDynamicModule(info *typeScriptProgram, from, specifier string) (p *Program, target string, host bool) {
	if info == nil {
		panic(r.NewTypeError("dynamic import requires a prepared program"))
	}
	entry, ok := tsfrontend.LookupPreparedImport(info.imports[from], info.importRules[from], from, specifier)
	if !ok {
		panic(r.NewTypeError("module %s is not prepared for this program", specifier))
	}
	if entry.Host {
		return nil, entry.ID, true
	}
	if info.units[entry.ID] == nil {
		panic(r.NewTypeError("missing prepared module %s", entry.ID))
	}
	copy := *info
	copy.roots = []string{entry.ID}
	copy.scriptEntry = false
	return &Program{typescript: &copy}, entry.ID, false
}

func (r *Runtime) dynamicHostNamespace(specifier string) *Object {
	key := "host:" + specifier
	if instance := r.moduleInstances[key]; instance != nil {
		return instance.namespace
	}
	values, ok := r.hostModules[specifier]
	if !ok {
		panic(r.NewTypeError("missing registered host module %s", specifier))
	}
	cells := make(map[string]*moduleCell, len(values))
	for name, value := range values {
		cells[name] = &moduleCell{name: name, getter: func(FunctionCall) Value { return value }}
	}
	namespace := r.newModuleNamespace(cells)
	if r.moduleInstances == nil {
		r.moduleInstances = make(map[string]*moduleInstance)
	}
	r.moduleInstances[key] = &moduleInstance{namespace: namespace, exports: cells, state: moduleEvaluated}
	return namespace
}

func (r *Runtime) dynamicImport(info *typeScriptProgram, from string, c FunctionCall, deferred bool) Value {
	cap := r.newPromiseCapability(r.getPromise())
	cap.try(func() {
		specifier := c.Argument(0).toString().String()
		attributes := make(map[string]string)
		options := c.Argument(1)
		if !IsUndefined(options) {
			o, ok := options.(*Object)
			if !ok {
				panic(r.NewTypeError("dynamic import options must be an object"))
			}
			if attrs := nilSafe(o.Get("with")); !IsUndefined(attrs) {
				a, ok := attrs.(*Object)
				if !ok {
					panic(r.NewTypeError("import attributes must be an object"))
				}
				var entries []struct {
					name  string
					value Value
				}
				for item, next := iterateEnumerableStringProperties(a)(); next != nil; item, next = next() {
					entries = append(entries, struct {
						name  string
						value Value
					}{item.name.String(), nilSafe(item.value)})
				}
				for _, entry := range entries {
					value, ok := entry.value.(String)
					if !ok {
						panic(r.NewTypeError("import attribute values must be strings"))
					}
					attributes[entry.name] = value.String()
				}
				for _, entry := range entries {
					if entry.name != "type" || attributes[entry.name] != "json" {
						panic(r.NewTypeError("unsupported import attribute %s: %s", entry.name, attributes[entry.name]))
					}
				}
			}
		}
		r.enqueuePromiseJob(func() {
			cap.try(func() {
				phase := moduleResolutionImport
				if deferred {
					phase = moduleResolutionDefer
				}
				key := preparedResolutionKey(info, from, specifier, phase, attributes)
				if target := r.dynamicResolutions.lookup(key); target != "" {
					m := r.moduleInstances[target]
					if m != nil {
						if message := tsfrontend.ValidateImportAttributes(attributes, m.code != nil && m.code.json, false); message != "" {
							panic(r.NewTypeError("%s", message))
						}
						r.finishDynamicImport(m, deferred, cap)
						return
					}
				}
				p, target, host := r.preparedDynamicModule(info, from, specifier)
				if host {
					if message := tsfrontend.ValidateImportAttributes(attributes, false, false); message != "" {
						panic(r.NewTypeError("%s", message))
					}
					ns := r.dynamicHostNamespace(target)
					r.retainModuleResolution(key, "host:"+target)
					if deferred {
						cap.resolve(r.deferredNamespace(r.moduleInstances["host:"+target]))
					} else {
						cap.resolve(ns)
					}
					return
				}
				if message := tsfrontend.ValidateImportAttributes(attributes, p.typescript.units[target].json, false); message != "" {
					panic(r.NewTypeError("%s", message))
				}
				if err := r.preflightTypeScript(p); err != nil {
					panic(r.NewTypeError("%s", err.Error()))
				}
				value, err := r.runModuleGraph(p, true, deferred)
				if m := r.moduleInstances[target]; m != nil && m.code.fingerprint == p.typescript.units[target].fingerprint {
					r.retainModuleResolution(key, target)
				}
				if err != nil {
					if ex, ok := err.(*Exception); ok {
						panic(ex)
					}
					if isUncatchableException(err) {
						panic(err)
					}
					panic(r.NewTypeError("%s", err.Error()))
				}
				if promise, ok := value.(*Object); ok {
					if _, ok := promise.self.(*Promise); ok {
						r.tsThen(promise, func(Value) {
							if deferred {
								cap.resolve(r.deferredNamespace(r.moduleInstances[target]))
							} else {
								cap.resolve(r.moduleInstances[target].namespace)
							}
						}, cap.reject)
						return
					}
				}
				if deferred {
					cap.resolve(r.deferredNamespace(r.moduleInstances[target]))
				} else {
					cap.resolve(r.moduleInstances[target].namespace)
				}
			})
		})
	})
	return cap.promise
}

func (r *Runtime) finishDynamicImport(m *moduleInstance, deferred bool, cap *promiseCapability) {
	roots := []*moduleInstance{m}
	if deferred {
		roots = gatherAsyncModules(m, map[*moduleInstance]bool{})
	}
	value := r.evaluateModuleRoots(roots, true)
	resolve := func(Value) {
		if deferred {
			cap.resolve(r.deferredNamespace(m))
		} else {
			cap.resolve(m.namespace)
		}
	}
	if o, ok := value.(*Object); ok {
		if _, ok := o.self.(*Promise); ok {
			r.tsThen(o, resolve, cap.reject)
			return
		}
	}
	resolve(_undefined)
}
