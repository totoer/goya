package goya

import (
	js_ast "github.com/totoer/goya/ast"
	"github.com/totoer/goya/parser"
	"regexp"
	"sync"
	"testing"
	"time"
)

var legacySourceCompilationCall = regexp.MustCompile(`(?:^|[^\w.$])(?:eval\s*[\(\)]|(?:Function|AsyncFunction|GeneratorFunction|AsyncGeneratorFunction)\s*\()`)

func skipSourceCompilationFixture(t testing.TB, source string) {
	t.Helper()
	if legacySourceCompilationCall.MatchString(source) {
		t.Skip("legacy string-compilation fixture is outside the execution profile; public rejection is tested separately")
	}
}

var legacyParserOptions sync.Map

// Historical VM regressions may construct execution operations through the
// legacy parser. These helpers exist only in tests, outside the public checked
// compilation API. They do not establish JavaScript API compatibility.
// Legacy Date arithmetic fixtures explicitly select the application's Go
// location. Production runtimes use their own configured UTC/IANA timezone.
func newLocalVMFixtureRuntime() *Runtime {
	r := New()
	r.timeZone = time.Local
	if name, _, ok := intlResolveTimeZone(time.Local.String()); ok {
		r.timeZoneName = name
	}
	return r
}

func compileVMFixture(name, source string, strict bool) (*Program, error) {
	return compileUncheckedSource(name, source, strict)
}
func mustCompileVMFixture(name, source string, strict bool) *Program {
	p, err := compileVMFixture(name, source, strict)
	if err != nil {
		panic(err)
	}
	return p
}

func (r *Runtime) runVMString(source string) (Value, error) {
	return r.runVMScript("", source)
}
func (r *Runtime) runVMScript(name, source string) (Value, error) {
	p, err := r.compile(name, source, false, true, nil)
	if err != nil {
		return nil, err
	}
	return r.runVMProgram(p)
}
func (r *Runtime) runVMProgram(p *Program) (Value, error) {
	return r.runCheckedOperations(p)
}

func compileUncheckedSource(name, src string, strict bool) (*Program, error) {
	return compile(name, src, strict, true, nil)
}

func parseUncheckedSource(name, src string, options ...parser.Option) (prg *js_ast.Program, err error) {
	prg, err1 := parser.ParseFile(nil, name, src, 0, options...)
	if err1 != nil {
		// FIXME offset
		err = &CompilerSyntaxError{
			CompilerError: CompilerError{
				Message: err1.Error(),
			},
		}
	}
	return
}

func compile(name, src string, strict, inGlobal bool, evalVm *vm, parserOptions ...parser.Option) (p *Program, err error) {
	prg, err := parseUncheckedSource(name, src, parserOptions...)
	if err != nil {
		return
	}

	return compileAST(prg, strict, inGlobal)
}

func (r *Runtime) compile(name, src string, strict, inGlobal bool, evalVm *vm) (p *Program, err error) {
	var options []parser.Option
	if value, ok := legacyParserOptions.Load(r); ok {
		options = value.([]parser.Option)
	}
	p, err = compile(name, src, strict, inGlobal, evalVm, options...)
	if err != nil {
		switch x1 := err.(type) {
		case *CompilerSyntaxError:
			err = &Exception{
				val: r.builtin_new(r.getSyntaxError(), []Value{newStringValue(x1.Error())}),
			}
		case *CompilerReferenceError:
			err = &Exception{
				val: r.newError(r.getReferenceError(), x1.Message),
			} // TODO proper message
		}
	}
	return
}

// SetParserOptions sets parser options to be used by RunString, RunScript and eval() within the code.
func (r *Runtime) SetParserOptions(opts ...parser.Option) {
	legacyParserOptions.Store(r, opts)
}
