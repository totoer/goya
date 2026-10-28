# Typed Go APIs in TypeScript

The embedding API uses one registry for declarations and runtime implementations.
The checked execution path is `Compile`; `CompileProject` is an alias. The
language version and standard environment are pinned; see the compatibility
matrix and verification record for the precise execution profile.

See [language configuration and checked examples](typescript-language.md) for
TS/JSX/decorator options and `examples/typescript-language`.

```go
registry := goya.NewRegistry()
err := registry.Register("add", func(a, b float64) float64 { return a + b })
if err != nil { return err }
environment, err := registry.Freeze(ctx)
if err != nil { return err }

project := goya.Project{Files: map[string]string{
    "/main.ts": `add(1, 2);`,
}}
program, diagnostics, err := goya.Compile(ctx, project, environment, nil)
if err != nil { return err }
if program == nil { /* present diagnostics and stop */ }

runtime := goya.New()
if err := environment.Bind(runtime); err != nil { return err }
result, err := runtime.RunProgram(program)
```

`add("1", 2)` produces TypeScript diagnostic 2345 and no program. Passing a
string through `any` still fails the actual-value check before Go is called.
`Check(ctx, project, environment, options)` performs the same analysis without a
VM, adapter binding, or Go function calls.

Set `Project.ConfigFile` to a virtual catalog name to share `extends` and project
references with an editor session. Configs and their dependencies must be in
`Files` or the resolver snapshot. Host compiler options take precedence; explicit
entry points select the executable roots. Empty `ConfigFile` keeps the existing
selection rules. See [checking configuration](typescript-checking.md) and the
[editor snapshot API](typescript-lsp.md) for the complete input contract.

Editor sources can have client URIs independent of their virtual compiler names.
Supply `SessionOptions.DocumentURIs` for custom schemes or Windows file URIs and
use the session's bidirectional mapping. Editor positions are zero-based UTF-16;
`lsp.PositionAt`/`lsp.OffsetAt` convert original UTF-8 offsets without changing
source text. `ReadDocument` provides read-only host/library navigation targets
directly from memory. See the [document identity contract](typescript-lsp.md#document-identity-and-positions).

`examples/typescript-lifecycle` checks and compiles one in-memory project and
runs the same Program in two compatible environments with different Go values.
Each Runtime owns its module instances and script state. `Check` is optional
before `Compile`; Compile always performs the checking gate itself.

`RunString` and `RunScript` check a single source using the Environment already
bound to the Runtime. The default virtual name is `/script.ts`; an explicit name
must be a TypeScript path. Imports need the project form of Compile. These
convenience methods return `*CompilationError` with the full diagnostics when
checking fails. `MustCompile(ctx, project, environment, options)` panics with that
error instead. The old `Compile(name, source, strict)` and `MustCompile` signatures,
`Parse`, and `CompileAST` entry points have been removed. A zero Program or a
Program without checked compilation metadata cannot be executed. Runtime.Set
alone does not supply a checked host declaration: use Registry and Environment.

`Program.Manifest()` returns detached metadata: the pinned frontend version,
options/profile identity, captured source hashes, entry points and required host
schemas. Go values and function addresses do not enter this identity. Bytecode
retains source maps to the original TS text. A `BindingCompatibilityError`
indicates a failed startup preflight; no user initializer has run.

The bounded analysis cache lets Check and Compile reuse the same checked
snapshot. Its default limits are two analyses and 16 MiB of captured source text.
`SetCompilationCacheLimits(entries, maxSourceBytes)` clears and configures the
cache; either zero disables retention. Resolvers are captured for each request,
and source/declaration content, options, baseline and registry schemas participate
in invalidation. Cached compiler data contains no VM state or Go implementations.

### Preparing lazy modules

Provide additional computed-import targets in `Project.ModuleRoots`. These are
virtual source paths or exact declared host-module names; they are checked and
compiled together with the entry graph but are not started merely by being
prepared. Use explicit entry points when plugins must stay lazy:

```go
project := goya.Project{
    EntryPoints: []string{"/main.ts"},
    ModuleRoots: []string{"/plugins/fast.ts", "/plugins/safe.ts"},
    Files: map[string]string{
        "/main.ts": `const mode: string = "fast";
            export const result = (async () => {
                const m = await import("./plugins/" + mode + ".js");
                return m.value;
            })();`,
        "/plugins/fast.ts": `export const value = 42;`,
        "/plugins/safe.ts": `export const value = 43;`,
    },
}
program, diagnostics, err := goya.Compile(ctx, project, environment, nil)
```

Handle `err` and error diagnostics before binding a compatible environment and
calling `RunProgram`. Static and string-literal dynamic imports are prepared
automatically, including their dependencies. A file's presence in the source
catalog alone does not authorize a computed import. Type-only dependencies do
not grant execution rights. Unknown computed imports reject with `TypeError`.
Import does not call the resolver again or compile new source text. Updating
sources or adding targets requires a new Go-side `Compile` call. A root also
selected by the normal startup rules still starts normally; `ModuleRoots` does
not override `EntryPoints` or configured/inferred startup selection.

All host requirements of the prepared set are checked before any user
initializer, including requirements of plugins not selected in that run. An
incompatible lazy plugin therefore blocks startup. Extra registrations do not
grant additional imports.

The profile has no built-in `eval`. The typed registry reserves the global name
`eval`; methods such as `api.eval()` remain ordinary methods. All four families
of built-in function constructors reject string compilation with `EvalError`,
including calls reached through `.constructor`, aliases or `Reflect.construct`.
Their object model, `Function.prototype`, ordinary functions and invocation
methods remain available. Direct recognized constructor calls also receive
profile diagnostic goya 1114 from `Check`/`Compile`; indirect calls are guarded
at runtime before internal argument-to-string coercion. There is no opt-in mode.

Go-side `Check`, `Compile`, `RunString` and `RunScript` still accept sources and
apply the mandatory TypeScript gate. A host callback that deliberately exposes
compilation is a capability granted by the embedding application itself.

`Freeze` copies descriptor maps and declaration sources, validates the generated
types and computes a canonical schema digest. Registration order and Go function
addresses do not affect equivalent schemas. The snapshot freezes the contract;
Go implementations can still maintain their own application state. Later builder
additions create a different environment. Registered globals are immutable data
properties. An environment binds once per runtime; binding the same instance
again is idempotent.

Before executing instructions, the runtime checks required global and host-module
schemas, conversion policies and implementations. Different Go values of the
required schemas are accepted, as are additional unused registrations or module
exports. A missing or changed contract rejects startup before initializers run.
For execution, pass the registry environment at compilation as well as binding a
compatible environment at runtime. Declaration sources alone are useful for
independent type checking and do not supply conversion policies or implementations.

## Reflection and mapping

Automatic reflection supports bool, string, float64, fixed-width 8/16/32-bit
integers, ordinary functions, structures, arrays, slices, string-keyed maps,
methods, typed callbacks and recursive pointers. Unsupported interfaces and
ambiguous representations require an explicit adapter; there is no fallback to
`any`.

```go
type Account struct {
    Name string `ts:"name"`
    Balance int32 `ts:"balance"`
}
func (a *Account) Credit(amount int32) int32 {
    a.Balance += amount
    return a.Balance
}

registry.SetFieldNameMapper(goya.TagFieldNameMapper("ts", true))
err := registry.Register("account", &Account{Name: "example", Balance: 10})
```

The same captured field/method names drive declarations and runtime conversion.
Mapper changes after registration cannot rename existing members. Empty mapped
names hide members, and unexported fields are hidden. Embedded exported Go fields
are represented by their own field name rather than flattening promoted fields.
Pointers are nullable. TS uses `account!.credit(2)` and reads `account!.balance`.

Struct values, arrays, ordinary slices and maps are copies. Copies preserve cycles
within one conversion. Pointer-to-struct values are live views with readonly
fields by default; registered methods can perform their declared Go operations.
Use `HostBinding{Value: pointer, Mutable: true}` to allow checked writes through a
view. While a view remains strongly reachable from Go or TypeScript, returning the
same Go pointer with a matching contract in that runtime returns the same view.
Views belong to one runtime and keep their original Go source alive, including
structs without exposed members. Replacing a pointer in a host field does not
change the source of a previously returned view. Plain TS objects are converted
and validated when a Go parameter expects a struct, collection or pointer.
Passing a matching host view back to Go returns its original Go pointer.

The runtime's identity index holds views weakly. Once no strong references remain,
Go GC can collect a view and its source data that have no other owners, including
unreachable cycles. If the host still owns the source, returning it after the old
view has been collected may create a new view. Dead index entries are cleaned up
automatically, even while the runtime is idle, without new calls or `RunJobs`.
Collection and cleanup are asynchronous, have no guaranteed deadline, and need
not finish after a single Go GC. No per-view release call is required. Resource
disposal remains the host application's responsibility.

Extracted methods and accessors keep their Go receiver alive for as long as they
are used, even if the view itself is collected. Bridge-created functions and
callbacks retain the data needed by their callable, but do not retain unrelated
arguments or sibling results through completed conversions. Data deliberately
captured by Go functions, TypeScript closures, or custom host adapters remain
subject to those owners' lifetimes.

`[]T` and `map[string]T` are nullable, so nil and empty collections remain
different. Arrays use fixed-length tuples. Function arity is checked: missing
required or excess non-rest arguments fail; rest elements are individually
validated. Multiple Go results form a TS tuple in the same order. A trailing
`error` is omitted from the success type and throws a standard language Error
when non-nil, without exposing the original Go object.

`Optional[T]` distinguishes omitted/undefined arguments from supplied zero values.
Optional arguments must follow required arguments and precede any rest parameter.
`Optional[*T]` additionally distinguishes present null from missing. An optional
result has type `T | undefined`. A zero-result function returns undefined.

Strings must cross the Go boundary losslessly: invalid UTF-8 Go strings and
ill-formed UTF-16 TS strings are rejected, including strings in results and map
keys. Standard TS strings and surrogate pairs remain supported inside the VM.

## Numeric policies

Fixed-width 8/16/32-bit integers use `number`; actual inputs must be finite,
integral and in range. Negative zero is rejected for Go integers because its sign
cannot be retained. Number conversions never coerce a string or object.

```go
binding := goya.HostBinding{
    Value: func(value uint64) uint64 { return value },
    Numbers: goya.NumericPolicy{Integers: goya.IntegerBigInt},
}
err := registry.Register("identity", binding)
```

`int64` and `uint64` require `IntegerBigInt` or `IntegerSafeNumber`. Bigint checks
the Go range and preserves exact 64-bit values. Safe-number accepts only integers
between -9007199254740991 and 9007199254740991, also checking the Go range. Results
are validated too; an oversized Go result never silently rounds.

For `int`, `uint` and `uintptr`, also set `IntBits: 32` or `64`. This fixes the
declared range independently of Go's native width. Registration rejects a
contract wider than the current target can represent. Numeric choices apply
through the registered type graph and participate in schema compatibility.

`float32` requires `Float32Exact` or `Float32Round`. Exact conversion rejects
inexact finite inputs; round explicitly uses Go's float32 rounding. Both reject
finite overflow to infinity. Float64 accepts the language's number values,
including infinities, NaN and signed zero.

## Explicit adapters

For dynamic objects, low-level callbacks or interfaces, provide both a TS type
and an adapter with a stable conversion schema ID. Additional caller-owned type
definitions can be supplied with `DeclareTypes("/api.d.ts", declarations)`.
Freeze validates those definitions together with generated declarations.

```go
adapter := goya.HostAdapterFuncs{
    ID: "strict-number-increment/v1",
    BindValue: func(r *goya.Runtime, _ any) (goya.Value, error) {
        return r.ToValue(func(call goya.FunctionCall) goya.Value {
            if len(call.Arguments) != 1 || !goya.IsNumber(call.Argument(0)) {
                panic(r.NewTypeError("expected one number"))
            }
            return r.ToValue(call.Argument(0).ToFloat() + 1)
        }), nil
    },
}
err := registry.Register("increment", goya.HostBinding{
    TypeScript: "(value: number) => number",
    Adapter: adapter,
})
```

An adapter is trusted application conversion code. It must validate actual
arguments, property writes and results even when `any` or assertions passed the
checker. Its schema ID covers those rules, not a Go object address. Bind rejects
a changed schema ID, nil result or object from another runtime. Implementations
and their semantic promises remain the application's responsibility.

## Host modules

```go
err := registry.RegisterModule("host:math", map[string]any{
    "double": func(value float64) float64 { return 2 * value },
})
// TS: import {double} from "host:math";
```

Host-module values use the same descriptors and boundary checks as globals.

`examples/typescript-modules` combines a registered host module with an in-memory
resolver, virtual package exports, static and dynamic imports and top-level await:

```sh
go run ./examples/typescript-modules
```

It prints `same module: true; value: 42; loaded once: 1; isolated: true`.
The source catalog is supplied by a `ResolverFunc`; virtual `/node_modules/`
metadata is just application-provided text. Neither the compiler nor linker
looks for packages on disk. The example verifies that console, process, timers,
load/readFile and a global require are absent from the script's environment.

`Check` and `CompileProject` each capture their own immutable resolver snapshot.
A Program retains prepared code and a frozen import catalog, not a compiler
Analysis or the resolver callback. Both static and dynamic imports keep using
that code after the application changes its catalog. A computed target must
belong to the prepared closure or `ModuleRoots`; unknown targets reject without
requesting another snapshot. Instances, including pending or failed
initialization, remain cached per Runtime. Compile a new Program from Go to
change code or permissions. Canonical modules with compatible code and import permissions can still be
shared by programs in one Runtime. A cached loader with different permissions
rejects startup before effects; its old authority is never inherited silently.

Default exports and string-named exports are supported. Module namespace values
are kept separate from global bindings. Imports are live, namespace data
descriptors follow ESM rules, and initialization state belongs to each runtime.
An ambient `.d.ts` can pass `Check` without an implementation; executing a value
import requires a registered compatible implementation before graph evaluation.
Type-only imports have no runtime dependency.

Run the combined function/object/module and exact-integer example with:

```sh
CGO_ENABLED=0 go run ./examples/typescript-host
```

It prints `example:84:12` and confirms the exact bigint plus the Go balance.
No console, filesystem, process, network, timers or Node loader is registered
inside TypeScript automatically. The example's output is produced by Go.

## Standalone verification

`examples/typescript-lifecycle` runs Check, Compile and RunProgram using source
strings and typed Go registrations, then executes one Program in two compatible
runtimes. Build and run it without a tool search path:

```sh
CGO_ENABLED=0 GOPROXY=off go build -o /tmp/goya-ts-standalone ./examples/typescript-lifecycle
PATH=/nonexistent /tmp/goya-ts-standalone
```

The expected output is `41` followed by `42`. The macOS/arm64 verification used
Go 1.27.2; `go version -m` confirmed `CGO_ENABLED=0`. It also passed under an OS
sandbox denying network access, process creation, execution of other binaries,
and reads from the project, unpacked compiler sources, module cache and build
cache. The binary includes its compiler and standard declarations. Resolver
isolation is separately checked by `TestFrozenFilesDoNotReadHostFilesystem`.

