package goya

func (m *moduleInstance) evaluationValue() Value {
	if m.code != nil && m.code.commonJS {
		return nilSafe(m.moduleObject.Get("exports"))
	}
	return m.namespace
}

func (r *Runtime) requireModule(from *moduleInstance, specifier string) Value {
	for i, d := range from.code.dependencies {
		if d.Specifier != specifier {
			continue
		}
		target := from.dependencies[i]
		if target.code == nil {
			return target.namespace
		}
		if target.component.async {
			panic(r.NewTypeError("cannot synchronously require an async module graph: %s", specifier))
		}
		r.evaluateModule(target, map[*moduleInstance]bool{})
		if target.code.json {
			return target.exports["default"].get(r)
		}
		if target.code.commonJS {
			return nilSafe(target.moduleObject.Get("exports"))
		}
		if target.exports["default"] == nil || target.exports["__esModule"] != nil {
			return target.namespace
		}
		if target.requireNamespace == nil {
			cells := make(map[string]*moduleCell, len(target.exports)+1)
			for name, cell := range target.exports {
				cells[name] = cell
			}
			cells["__esModule"] = &moduleCell{name: "__esModule", getter: func(FunctionCall) Value { return valueTrue }}
			target.requireNamespace = r.newModuleNamespace(cells)
		}
		return target.requireNamespace
	}
	// A CommonJS-target import() is normalized by the official transformer to
	// a deferred private require. It selects only already prepared code.
	key := preparedResolutionKey(from.checking, from.code.id, specifier, moduleResolutionRequire, nil)
	if id := r.dynamicResolutions.lookup(key); id != "" {
		if m := r.moduleInstances[id]; m != nil {
			if m.code == nil {
				return m.namespace
			}
			if m.component.async {
				panic(r.NewTypeError("cannot synchronously require an async module graph: %s", specifier))
			}
			r.evaluateModule(m, map[*moduleInstance]bool{})
			if m.code.json {
				return m.exports["default"].get(r)
			}
			return m.evaluationValue()
		}
	}
	p, target, host := r.preparedDynamicModule(from.checking, from.code.id, specifier)
	if host {
		ns := r.dynamicHostNamespace(target)
		r.retainModuleResolution(key, "host:"+target)
		return ns
	}
	if checkedGraphHasAsync(p, target, map[string]bool{}) {
		panic(r.NewTypeError("cannot synchronously require an async module graph: %s", specifier))
	}
	if err := r.preflightTypeScript(p); err != nil {
		panic(r.NewTypeError("%s", err.Error()))
	}
	_, err := r.runModuleGraph(p, true)
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
	m := r.moduleInstances[target]
	if m.code.json {
		return m.exports["default"].get(r)
	}
	if m.component.async {
		panic(r.NewTypeError("cannot synchronously require an async module graph: %s", specifier))
	}
	if m.code.commonJS {
		return m.evaluationValue()
	}
	return m.namespace
}

func checkedGraphHasAsync(p *Program, id string, seen map[string]bool) bool {
	if seen[id] {
		return false
	}
	seen[id] = true
	u := p.typescript.units[id]
	if u == nil {
		return false
	}
	if u.async {
		return true
	}
	for _, d := range u.dependencies {
		if !d.Host && d.Phase != "require" && checkedGraphHasAsync(p, d.ID, seen) {
			return true
		}
	}
	return false
}
