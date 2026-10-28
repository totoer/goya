package goya

import "github.com/totoer/goya/unistring"

// A strongly connected component has one externally visible completion. Its
// internal DFS edges wait for descendants, while ancestor back edges do not.
// Thus a cycle can make progress without exposing an early successful member
// when another member later rejects its initialization.
type moduleComponent struct {
	members   []*moduleInstance
	cap       *promiseCapability
	async     bool
	remaining int
	failed    bool
}

func (r *Runtime) internalModulePromise() *promiseCapability {
	cap := r.newPromiseCapability(r.getPromise())
	cap.promise.self.(*Promise).handled = true
	return cap
}

func (r *Runtime) prepareModuleComponents(roots []*moduleInstance) {
	var all []*moduleInstance
	seen := map[*moduleInstance]bool{}
	var gather func(*moduleInstance)
	gather = func(m *moduleInstance) {
		if seen[m] {
			return
		}
		seen[m] = true
		all = append(all, m)
		for _, d := range m.dependencies {
			gather(d)
		}
	}
	for _, root := range roots {
		gather(root)
	}
	for _, m := range all {
		if m.component == nil {
			m.plan = moduleEvaluationPlan(m)
		}
	}
	indices, low := map[*moduleInstance]int{}, map[*moduleInstance]int{}
	onStack := map[*moduleInstance]bool{}
	var stack []*moduleInstance
	var components []*moduleComponent
	index := 0
	var visit func(*moduleInstance)
	visit = func(m *moduleInstance) {
		if m.component != nil {
			return
		}
		index++
		indices[m] = index
		low[m] = index
		stack = append(stack, m)
		onStack[m] = true
		for _, d := range m.plan {
			if d.component != nil {
				continue
			}
			if indices[d] == 0 {
				visit(d)
				low[m] = min(low[m], low[d])
			} else if onStack[d] {
				low[m] = min(low[m], indices[d])
			}
		}
		if low[m] == indices[m] {
			c := &moduleComponent{}
			for {
				n := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[n] = false
				n.component = c
				c.members = append(c.members, n)
				if n.state != moduleEvaluated {
					c.remaining++
				}
				if n.code != nil && n.code.async {
					c.async = true
				}
				if n == m {
					break
				}
			}
			components = append(components, c)
		}
	}
	for _, root := range all {
		if indices[root] == 0 {
			visit(root)
		}
	}
	// Tarjan produces dependency components before their users.
	for _, c := range components {
		for _, m := range c.members {
			for _, d := range m.plan {
				if d.component != c && d.component.async {
					c.async = true
				}
			}
		}
		if c.async {
			c.cap = r.internalModulePromise()
			for _, m := range c.members {
				m.evaluation = r.internalModulePromise()
			}
		}
	}
}

func (r *Runtime) finishModule(m *moduleInstance) {
	if m.component.failed {
		return
	}
	m.state = moduleEvaluated
	if m.code != nil && m.code.commonJS {
		value := nilSafe(m.moduleObject.Get("exports"))
		for name := range m.exports {
			if name == "default" {
				m.cjsNamespaceValues[name] = value
			} else {
				m.cjsNamespaceValues[name] = nilSafe(r.getVStr(value, unistring.NewFromString(name)))
			}
		}
	}
	if m.evaluation != nil {
		m.evaluation.resolve(_undefined)
	}
	m.component.remaining--
	if m.component.remaining == 0 && m.component.cap != nil {
		m.component.cap.resolve(_undefined)
	}
}

func (r *Runtime) failModule(m *moduleInstance, reason Value) {
	c := m.component
	if c.failed {
		return
	}
	c.failed = true
	for _, member := range c.members {
		member.state = moduleFailed
		member.failure = reason
		if member.evaluation != nil {
			member.evaluation.reject(reason)
		}
	}
	if c.cap != nil {
		c.cap.reject(reason)
	}
}

func (r *Runtime) resumeModule(m *moduleInstance, value Value, rejected bool) {
	if m.component.failed {
		return
	}
	ex := r.vm.try(func() {
		g, ok := m.iterator.self.(*generatorObject)
		if !ok {
			panic(r.NewTypeError("invalid internal module continuation"))
		}
		var result Value
		var kind resultType
		var exception *Exception
		if rejected {
			result, kind, exception = g.gen.nextThrow(value)
		} else {
			result, kind, exception = g.gen.next(value)
		}
		if exception != nil {
			panic(exception)
		}
		switch kind {
		case resultNormal:
			g.state = genStateCompleted
			r.finishModule(m)
		case resultAwait:
			if m.evaluation == nil {
				panic(r.NewTypeError("unclassified async module continuation"))
			}
			r.tsThen(result, func(v Value) { r.resumeModule(m, v, false) }, func(v Value) { r.resumeModule(m, v, true) })
		default:
			panic(r.NewTypeError("unexpected module suspension"))
		}
	})
	if ex != nil {
		r.failModule(m, ex.val)
		if m.evaluation == nil {
			panic(ex)
		}
	}
}

func (r *Runtime) startModuleBody(m *moduleInstance) Value {
	if m.component.failed {
		return m.namespace
	}
	if !m.code.module {
		result, err := r.runCheckedOperations(m.code.code)
		if err != nil {
			panic(err)
		}
		r.finishModule(m)
		return result
	}
	r.resumeModule(m, nil, false)
	return m.evaluationValue()
}

func (r *Runtime) evaluateModule(m *moduleInstance, ancestors map[*moduleInstance]bool) (result Value) {
	if m.interruption != nil {
		panic(m.interruption)
	}
	if m.state == moduleFailed {
		if m.evaluation != nil {
			return m.namespace
		}
		panic(m.failure)
	}
	if m.state == moduleEvaluated || m.state == moduleEvaluating {
		return m.evaluationValue()
	}
	m.state = moduleEvaluating
	m.visiting = true
	defer func() { m.visiting = false }()
	ancestors[m] = true
	defer delete(ancestors, m)
	ex := r.vm.try(func() {
		var pending []*promiseCapability
		for _, d := range m.plan {
			if ancestors[d] {
				continue
			}
			r.evaluateModule(d, ancestors)
			var cap *promiseCapability
			if d.component == m.component {
				cap = d.evaluation
			} else {
				cap = d.component.cap
			}
			if cap != nil {
				promise := cap.promise.self.(*Promise)
				switch promise.state {
				case PromiseStateFulfilled:
					continue
				case PromiseStateRejected:
					panic(promise.result)
				default:
					pending = append(pending, cap)
				}
			}
		}
		if len(pending) == 0 {
			result = r.startModuleBody(m)
			return
		}
		left := len(pending)
		for _, cap := range pending {
			r.tsThen(cap.promise, func(Value) {
				left--
				if left == 0 && !m.component.failed {
					r.startModuleBody(m)
				}
			}, func(reason Value) { r.failModule(m, reason) })
		}
		result = m.namespace
	})
	if ex != nil {
		r.failModule(m, ex.val)
		if m.evaluation == nil {
			panic(ex)
		}
	}
	return
}

func (r *Runtime) evaluateModuleRoots(roots []*moduleInstance, completionOnly bool) Value {
	result := Value(_undefined)
	var pending []*promiseCapability
	for _, root := range roots {
		result = r.evaluateModule(root, map[*moduleInstance]bool{})
		if root.component != nil && root.component.cap != nil {
			pending = append(pending, root.component.cap)
		}
	}
	if len(pending) == 0 {
		if completionOnly {
			return _undefined
		}
		return result
	}
	cap := r.newPromiseCapability(r.getPromise())
	left := len(pending)
	for _, p := range pending {
		r.tsThen(p.promise, func(Value) {
			left--
			if left == 0 {
				if completionOnly {
					cap.resolve(_undefined)
				} else {
					cap.resolve(result)
				}
			}
		}, cap.reject)
	}
	return cap.promise
}
