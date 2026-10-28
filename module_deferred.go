package goya

func gatherAsyncModules(m *moduleInstance, seen map[*moduleInstance]bool) []*moduleInstance {
	if seen[m] || m.code == nil || m.visiting || m.component != nil && (m.component.remaining == 0 || m.component.failed) {
		return nil
	}
	seen[m] = true
	if m.code.async {
		return []*moduleInstance{m}
	}
	var result []*moduleInstance
	for i, d := range m.dependencies {
		if m.code.dependencies[i].Phase == "require" {
			continue
		}
		result = append(result, gatherAsyncModules(d, seen)...)
	}
	return result
}

func moduleEvaluationPlan(m *moduleInstance) []*moduleInstance {
	if m.code == nil {
		return nil
	}
	var result []*moduleInstance
	seen := map[*moduleInstance]bool{}
	for i, d := range m.dependencies {
		phase := m.code.dependencies[i].Phase
		if phase == "require" {
			continue
		}
		additional := []*moduleInstance{d}
		if phase == "defer" {
			additional = gatherAsyncModules(d, map[*moduleInstance]bool{})
		}
		for _, n := range additional {
			if !seen[n] {
				seen[n] = true
				result = append(result, n)
			}
		}
	}
	return result
}

func readyForSynchronousModule(m *moduleInstance, seen map[*moduleInstance]bool) bool {
	if m.code == nil || seen[m] {
		return true
	}
	seen[m] = true
	if m.component != nil && (m.component.remaining == 0 || m.component.failed) {
		return true
	}
	if m.state == moduleEvaluating || m.code.async {
		return false
	}
	for i, d := range m.dependencies {
		if m.code.dependencies[i].Phase == "require" {
			continue
		}
		if !readyForSynchronousModule(d, seen) {
			return false
		}
	}
	return true
}

func (r *Runtime) evaluateModuleSynchronously(m *moduleInstance) {
	if !readyForSynchronousModule(m, map[*moduleInstance]bool{}) {
		panic(r.NewTypeError("deferred module is not ready for synchronous evaluation"))
	}
	r.evaluateModule(m, map[*moduleInstance]bool{})
	if m.state == moduleFailed {
		panic(m.failure)
	}
	if m.component != nil && m.component.cap != nil {
		promise := m.component.cap.promise.self.(*Promise)
		if promise.state == PromiseStateRejected {
			panic(promise.result)
		}
		if promise.state == PromiseStatePending {
			panic(r.NewTypeError("deferred module did not complete synchronously"))
		}
	}
}

func (r *Runtime) deferredNamespace(m *moduleInstance) *Object {
	if m.deferredNamespace == nil {
		cells := make(map[string]*moduleCell, len(m.exports))
		for name, cell := range m.exports {
			if name != "then" {
				cells[name] = cell
			}
		}
		o := r.newModuleNamespace(cells)
		n := o.self.(*moduleNamespaceObject)
		n.deferred = m
		n.symValues.set(SymToStringTag, valueProp(asciiString("Deferred Module"), false, false, false))
		m.deferredNamespace = o
	}
	return m.deferredNamespace
}
