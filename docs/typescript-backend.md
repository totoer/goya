# Developing the checked TypeScript backend

The developing execution path is `Compile` in `typescript_compile.go`.
It captures and checks a project, calls `Analysis.Lower`, then passes the checked
execution IR to bytecode generation. It never prints intermediate JavaScript or
feeds generated text to the legacy parser. Static ESM linking, cycles and live
bindings run through private module factories. Remaining module features and
runtime transforms are tracked in OpenSpec; unsupported lowering operations
return an error diagnostic and no `Program`.

## Boundaries

`internal/tsfrontend/project.go` owns the immutable virtual source catalog,
compiler options, host declarations and pinned upstream program. `Analyze`
returns a private checked certificate only after all required diagnostics have
passed. A zero or failed analysis cannot be lowered.

`internal/tsfrontend/ir.go` defines the internal handoff. `ExecutionIR` has no
constructor accepting a caller's AST. Its `Visit` method hands private operation
trees to the trusted bytecode backend. The first operation vocabulary reuses
the execution nodes in `ast/`; this is independent of the old parser. Public
embedding signatures expose no upstream AST, checker or symbol types.

`lower.go`, `lower_expr.go` and `lower_functions.go` convert the checked native
AST directly. The pinned upstream type eraser and import-elision transformer
run on private emit contexts; their results are walked as nodes, never printed.
The upstream tree stays behind the facade. Lowering of one analysis is serialized
because upstream emit/reference information can be lazy. Repeated lowering
produces separate operation trees with the same checked identity.

Every recorded operation has a range in the original TypeScript snapshot.
`IRModule.SourceRange` preserves exact byte offsets and UTF-16 source columns,
including where an execution node's compact position representation is less
precise. Each operation tree's `file.File` contains the original TS text. VM source
maps therefore retain TypeScript filenames and lines after erasing type syntax.

`compileCheckedIR` in the root package performs instruction selection with
`compiler*.go`. It accepts the internal checked handoff, and returns no bytecode
when lowering or instruction selection reports an error. The resulting `Program`
owns code and source-map data; mutating or releasing an earlier IR tree does not
change it. VM state and host objects belong to each `Runtime` separately.

The program records required global host schemas discovered through checked
symbols. `RunProgram` preflights those schemas and frozen runtime values before
instructions execute. Distinct Go implementations of one schema are permitted,
as are additional unused registrations. Executable host-module imports also
record the required per-export schemas and are checked before initialization.

Async functions use VM continuations. Async generators normalize directly from
native AST to generators and private Go promise/iterator helpers, with no emitted
helper text. Their functions and instances retain async-generator prototypes and
non-constructibility. `typescript_async_test.go` exercises ES2016 through ESNext:
serialized next/throw/return requests, awaiting yields and returns, delegation,
missing throw methods, finally, lexical super access, receiver/arguments, and
for-await closure. Lowered async functions and arrows retain async prototypes.

`module_async.go` groups cyclic dependencies into strongly connected components.
An importer outside a component waits for the whole component, so a successful
member cannot hide a later initialization rejection in its cycle. Inside a cycle,
DFS descendants are awaited and ancestor back edges remain live bindings.
Independent dependencies start in source order before waiting for completion.
The internal generator directly suspends on VM await operations, including
top-level for-await loops. No source rewrite to text is involved.

`RunProgram` returns a namespace for a synchronous ESM graph. For a graph with
top-level await, it returns a Promise whose fulfilled result is that namespace;
initialization errors reject it. Promise jobs drain at the outer runtime boundary.
The application can resume a pending host callback on the runtime's owning
goroutine. Repeated runs share initialization, including pending and failed
states. `typescript_module_async_test.go` covers these cases and async cycles.

Explicit resource management uses native `__addDisposableResource` and
`__disposeResources` operations, the standard disposal symbols, SuppressedError,
and iterator disposal methods. Resource methods are captured once on acquisition;
disposal reverses acquisition order and preserves abrupt completions. Async
disposal awaits each resource, including the required turn for null resources,
and ignores return values from synchronous fallback methods. Tests cover script,
function, loop, generator and top-level module scopes. The pinned frontend puts
these types in `esnext.disposable`; opt in with `lib: ["es2025",
"esnext.disposable"]`. The separate DisposableStack / AsyncDisposableStack
library work remains in task 8.3; this is not a claim of full optional-lib support.

CommonJS-format files use the official native module AST transformer. Generated
require/exports/module value references become private factory parameters; names
of object properties and original user identifiers retain their meaning. All
static dependencies are checked before compilation, but synchronous require
edges evaluate at their statement positions. Cycles see the existing partial
exports object. Export assignment can return an object, function or primitive.
The linker also supports import assignment in ESM files and preserve mode.

CommonJS entry points return their exports value. ESM importers receive a
namespace with a default referencing that value and captured named exports;
CommonJS importers use the live exports object, including native TS import-star,
default and re-export helpers. Requiring a graph with top-level await throws a
TypeError. The runtime installs no global require, exports or module object.
An original source reference to a declared require still needs a registered Go
binding. `typescript_commonjs_test.go` tests cycles, ordering, both interop
directions, export assignments, internal aliases, extension rewriting and host
preflight. The pinned TS 7 frontend rejects removed options such as
`esModuleInterop: false`; the backend does not reinterpret rejected settings.

Preparation closes over startup roots, `Project.ModuleRoots`, static imports
and literal dynamic imports. Type-only dependencies are checked but do not
receive execution rights. The checked IR hands the backend separate startup
roots, executable units, namespace host contracts and frozen import decisions.
The backend collects host requirements from the entire prepared graph before
startup, including unselected lazy plugins.

Dynamic import only looks up a prepared target. Relative/output-style paths,
package aliases, paths mappings and project-reference redirects are captured
while the native resolver is available. Runtime lookup carries normalized
aliases and finite target/existence decisions, without source/configuration
JSON, checker state or application callbacks. Unknown computed targets reject
with TypeError. Import does not ask for a fresh snapshot, parse or compile code.
Successful, pending and failed instances are cached per Runtime. Promise jobs,
linking and lazy evaluation retain their existing protocols.

Module fingerprints include original source, concrete IR operations, dependencies,
exports, compiler profile and per-module import permissions. Reusing a cached
loader with different import permissions is rejected before user effects. They exclude entry-point order, unrelated catalog
files and Go addresses. A new graph that conflicts with an existing canonical
module is rejected before its initializers run. Preflight still verifies required
Go schemas before using cached modules. `typescript_dynamic_test.go` exercises
this boundary, static/dynamic sharing, host modules, async loading, rejection
caching, microtask order and CommonJS-target import normalization. Promise jobs
have one outer drain; nested module loading cannot drain later jobs early.

Deferred evaluation follows the [TC39 deferred-import algorithms](https://tc39.es/proposal-defer-import-eval/).
Static defer imports still load, check and link their complete graphs. Their
asynchronous transitive dependencies execute eagerly; synchronous work waits for
namespace access. Evaluation plans replace defer edges with those eager async
dependencies before computing cycles. Already fulfilled async dependencies do
not force later synchronous initialization into another promise turn.

A deferred namespace is a distinct, cached exotic object. String property reads,
membership, descriptors, deletion and enumeration trigger synchronous evaluation;
symbol reads and the excluded `then` key do not. Assignment always fails without
evaluation. Deferred namespaces expose the `Deferred Module` tag and rethrow
cached initialization errors on subsequent accesses. Readiness checks reject
reentrant/pending execution before effects. `import.defer()` checks and links the
new graph, awaits eager async dependencies and returns its deferred namespace.
The pinned checker controls which module options accept this syntax.

JSON source modules are strictly validated during analysis, including Check.
Their native JSON AST lowers to ordinary object/array/primitive operations and a
default export. Object keys use data-property semantics, including `__proto__`;
UTF-16 strings, negative zero and number overflow follow VM value semantics.
There is no JSON-to-JavaScript text generation or call through a replaceable
global JSON.parse. JSON data belongs to each module instance and runtime.

Import attributes retain source ranges through IR and bytecode metadata. The
explicit-catalog loader accepts `type: "json"` for JSON and rejects unknown
attributes, mismatched types and ESM JSON requests missing that attribute.
CommonJS require returns the JSON value without an ESM attribute. Dynamic import
captures all enumerable string attribute values in order before validating their
types/support, then includes the captured attributes in its request-cache key.
Malformed JSON produces compile diagnostics before executable code is returned.

The syntax audit is reproducible with `cmd/inventory -check`. All 351 pinned kinds
have an explicit execution/erasure/structural/internal owner, and flag-based
features have a separate inventory. `typescript_syntax_test.go` verifies optional
receiver capture, short circuiting, logical assignment, computed method order,
auto-accessors, template newline/escape handling and explicitly non-strict with.
The ordinary unknown-node diagnostic remains a separate invariant. The owned
execution overlay removes eval and constructor code generation; the standard
library families retain their individual compatibility gates.

## Adding a backend fixture

1. Add a named TypeScript case to `TestTypeScriptExecutionOperations` in
   `typescript_compile_test.go`. It should end with a boolean expression testing
   observable behavior. `compileTypeScript` calls the checked API; the test runs
   its bytecode in a new VM and requires `true`.
2. For ordering behavior, record effects in a TS array and assert the full order.
   For static errors, use `CompileProject` directly and require diagnostics plus
   a nil program. For unknown lowering nodes, assert the `goya` diagnostic code,
   original file and range instead of accepting partial code.
3. Add module-graph/transform cases to `testdata/typescript/runtime.json` with
   explicit roots, files, options and observable expectations. Those fixtures
   remain marked unverified until they run through the linker and execution
   harness; passing their type checks alone does not verify runtime behavior.
4. Update the corresponding family in `typescript-compatibility.md`. Keep the
   release gate open for other members of that family that have not passed.
5. Run `CGO_ENABLED=0 go test . ./internal/tsfrontend`. The checked-handoff,
   immutable-code, type-erasure, source-location and concurrency regressions run
   alongside the execution cases.

To add a new operation, extend the lowering switch and its fixtures together.
The default branch must remain a structured diagnostic. Do not silently erase
an unrecognized executable node, bypass checking, or add a generated-text
fallback. Source importing and generated-asset updates follow
`internal/tsfrontend/README.md` and its manifest.

## Migration status

Public Compile, MustCompile, RunString and RunScript use the checked frontend.
The old public Parse/CompileAST entry points have been removed. Runtime source
compilation through eval and Function constructors has also been removed. A
Program retains bytecode, source positions/text, contracts and frozen module
lookup data; frontend analyses remain only in the bounded compilation cache or
editor sessions. Historical VM operation tests use isolated test helpers.
Fixtures requiring string compilation are explicitly skipped, with public
rejection coverage in typescript_eval_test.go and
typescript_function_constructor_test.go. Test262 module exclusions still apply;
these tests do not establish full ECMAScript compatibility.
Full support remains gated on the complete runtime and standard-library matrix.

## Prepared module allocation measurement

`BenchmarkPreparedPluginPackage` compares preparing a main module plus two lazy
plugins with repeatedly importing the already initialized selected plugin. The
analysis cache is disabled; the repeated-import benchmark verifies that resolver
calls do not increase. On Go 1.27.0, darwin/arm64, Apple M1 Pro, 100 iterations:

| Operation | Time/op | Bytes/op | Allocations/op |
|---|---:|---:|---:|
| Compile package | 20.61 ms | 13,836,862 | 68,749 |
| Repeated import | 5.37 us | 5,042 | 77 |

These are local measurements, not resource limits or cross-machine guarantees.
Reproduce with `go test . -run '^$' -bench '^BenchmarkPreparedPluginPackage$'
-benchtime=100x -benchmem`.
