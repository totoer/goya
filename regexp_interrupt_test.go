package goya

import (
	stdcontext "context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRegExpInterrupt(t *testing.T) {
	for name, source := range map[string]string{
		"test":                 `try { /^(a+)+(?=b)$/.test("a".repeat(32)); } catch (_) { throw new Error("caught interrupt"); }`,
		"exec unicode":         `/^(a+)+(?=b)$/u.exec("a".repeat(32));`,
		"match":                `"a".repeat(32).match(/^(a+)+(?=b)$/g);`,
		"replace":              `"a".repeat(32).replace(/^(a+)+(?=b)$/g, "x");`,
		"split":                `"a".repeat(32).split(/^(a+)+(?=b)$/);`,
		"match next":           `("z"+"a".repeat(32)).match(/z|(?<=z)(a+)+(?=b)$/g);`,
		"replace next unicode": `("ж"+"a".repeat(32)).replace(/ж|(?<=ж)(a+)+(?=b)$/gu, "x");`,
		"split next utf16":     `("ж"+"a".repeat(32)).split(/ж|(?<=ж)(a+)+(?=b)$/);`,
		"matchAll":             `[..."a".repeat(32).matchAll(/^(a+)+(?=b)$/g)];`,
		"search":               `"a".repeat(32).search(/^(a+)+(?=b)$/);`,
	} {
		t.Run(name, func(t *testing.T) {
			program := MustCompile(stdcontext.Background(), Project{Files: map[string]string{"/regexp.ts": source}}, nil, nil)
			r := New()
			reason := errors.New("cancel regexp")
			fired := make(chan struct{})
			start := time.Now()
			timer := time.AfterFunc(10*time.Millisecond, func() {
				r.Interrupt(reason)
				close(fired)
			})
			_, err := r.RunProgram(program)
			if timer.Stop() {
				t.Fatalf("match returned before cancellation: %v", err)
			}
			<-fired // Synchronize before clearing/reusing the runtime.
			elapsed := time.Since(start)
			var interrupted *InterruptedError
			if !errors.As(err, &interrupted) || interrupted.Value() != reason {
				t.Fatalf("expected InterruptedError with original value, got %v", err)
			}
			if len(interrupted.Stack()) == 0 {
				t.Fatal("interruption lost the script stack")
			}
			if elapsed > 500*time.Millisecond {
				t.Fatalf("interrupt took %v", elapsed)
			}
			t.Logf("interrupted in %v", elapsed)
			r.ClearInterrupt()
			value, err := r.RunString(`/(?=a)a/.test("a");`)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("reuse after interrupt: %v %v", value, err)
			}
		})
	}
}

func TestRegExpExecutionBudget(t *testing.T) {
	program := MustCompile(stdcontext.Background(), Project{Files: map[string]string{"/regexp.ts": `
		let timedOut = false;
		try { /^(a+)+(?=b)$/.test("a".repeat(32)); }
		catch (error) { timedOut = error instanceof RangeError; }
		timedOut && /(?=a)a/.test("a");
	`}}, nil, nil)
	start := time.Now()
	value, err := New().RunProgram(program)
	if err != nil || !value.ToBoolean() {
		t.Fatalf("expected recoverable RangeError on timeout: %v %v", value, err)
	}
	if elapsed := time.Since(start); elapsed < regexpMatchTimeout || elapsed > regexpMatchTimeout+time.Second {
		t.Fatalf("execution budget: %v", elapsed)
	}
}

func TestRegExpBudgetIncludesNextMatch(t *testing.T) {
	wrapper, err := compileRegexp2(`a`, false, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if wrapper.rx.MatchTimeout != regexpMatchTimeout {
		t.Fatalf("engine timeout is %v", wrapper.rx.MatchTimeout)
	}
	matcher := wrapper.matcher(nil)
	match, err := wrapper.rx.FindRunesMatchStartingAtWithInterrupt([]rune(strings.Repeat("a", 2)), 0, matcher.checkInterrupt)
	if err != nil || match == nil {
		t.Fatalf("first match: %v %v", match, err)
	}
	matcher.deadline = time.Now().Add(-time.Second)
	defer func() {
		if _, ok := recover().(rangeError); !ok {
			t.Fatal("next match reset the operation budget")
		}
	}()
	_, _ = wrapper.rx.FindNextMatchWithInterrupt(match, matcher.checkInterrupt)
}

func TestRegExpInterruptedUnicodeCache(t *testing.T) {
	wrapper, err := compileRegexp2(`(?=.)`, false, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	input := newStringValue("😀")
	if match, _, err := wrapper.matcher(nil).findUnicodeCached(input, 0, true); err != nil || match == nil {
		t.Fatalf("populate cache: %v %v", match, err)
	}
	matcher := wrapper.matcher(nil)
	matcher.deadline = time.Now().Add(-time.Second)
	func() {
		defer func() {
			if _, ok := recover().(rangeError); !ok {
				t.Fatal("expected interruption during split-surrogate match")
			}
		}()
		_, _, _ = matcher.findUnicodeCached(input, 1, true)
	}()
	if wrapper.cache.runes[0] != '😀' {
		t.Fatalf("interruption corrupted cached surrogate pair: %U", wrapper.cache.runes)
	}
}
