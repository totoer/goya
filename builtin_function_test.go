package goya

import (
	"testing"
)

func TestHashbangInFunctionConstructor(t *testing.T) {
	const SCRIPT = `
	assert.throws(SyntaxError, function() {
		new Function("#!")
	});
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestFunctionApplyNullArgArray(t *testing.T) {
	const SCRIPT = `
	assert.sameValue(0, (function() {return arguments.length}).apply(undefined, null))
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestFunctionToStringCallableProxy(t *testing.T) {
	const SCRIPT = `
	var handler = new Proxy({}, {
		get: function() { throw new Error("toString must not access proxy traps"); }
	});
	var targets = [() => 1, function() {}, Math.abs, (function() {}).bind(null), class {}];
	targets.forEach(function(target) {
		var proxy = new Proxy(target, handler);
		assert.sameValue(Function.prototype.toString.call(proxy), "function () { [native code] }");
		var nested = new Proxy(proxy, handler);
		assert.sameValue(Function.prototype.toString.call(nested), "function () { [native code] }");
	});
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestFunctionToStringRevokedProxy(t *testing.T) {
	const SCRIPT = `
	var handle = Proxy.revocable(() => 1, {});
	var outer = Proxy.revocable(handle.proxy, {});
	handle.revoke();
	assert.sameValue(Function.prototype.toString.call(handle.proxy), "function () { [native code] }");
	assert.sameValue(Function.prototype.toString.call(outer.proxy), "function () { [native code] }");
	outer.revoke();
	assert.sameValue(Function.prototype.toString.call(outer.proxy), "function () { [native code] }");
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestFunctionToStringNonCallableProxy(t *testing.T) {
	const SCRIPT = `
	var handle = Proxy.revocable({}, {});
	var outer = Proxy.revocable(handle.proxy, {});
	function check(proxy) {
		assert.throws(TypeError, function() {
			Function.prototype.toString.call(proxy);
		});
	}
	check(handle.proxy);
	check(outer.proxy);
	handle.revoke();
	check(handle.proxy);
	check(outer.proxy);
	outer.revoke();
	check(outer.proxy);
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestFunctionBindNegativeLength(t *testing.T) {
	const SCRIPT = `
	function target() {}
	Object.defineProperty(target, "length", {value: -5});
	// Zero bound arguments: max(-5 - 0, 0) === 0
	var b0 = target.bind({});
	assert.sameValue(0, b0.length, "zero bound args, integer length");

	// Additional bound arguments: max(-5 - 2, 0) === 0
	var b2 = target.bind({}, 1, 2);
	assert.sameValue(0, b2.length, "two bound args, integer length");

	// Fractional negative length is truncated toward zero before clamping.
	function target2() {}
	Object.defineProperty(target2, "length", {value: -5.7});
	var bf = target2.bind({});
	assert.sameValue(0, bf.length, "zero bound args, float length");
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}
