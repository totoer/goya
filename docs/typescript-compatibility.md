# TypeScript compatibility inventory

Baseline: TypeScript 7.0.2, pinned by `internal/tsfrontend/manifest.json`.
Verified execution profile: ES2025 language intrinsics, strict checking by
default, and external APIs supplied explicitly by Go. Language, module, member
audits and behavioral fixtures pass through the checked compiler and VM.
The dated commands and provenance are recorded in `typescript-release.md`.

`typescript-library-inventory.json` records every language declaration and overload
in the pinned `.d.ts` assets, with file digests, owning families, and original source ranges. Regenerate
it with `go run ./internal/tsfrontend/cmd/inventory`; use `-check` to detect drift.
The inventory retains external library names and digests for provenance, while
excluding their members from language-runtime obligations. External libraries are
not part of the default environment. Compiler availability is distinct from
runtime implementation.

## Language and type system

| Family | Current project evidence | Implementation target | Conformance input |
| --- | --- | --- | --- |
| Type annotations, inference, assignability | Native checking facade implemented | `internal/tsfrontend`, official checker | `internal/tsfrontend/project_test.go` |
| Generics, conditional/mapped/template literal types | Differential checking passed against pinned upstream | Official checker, no custom type system | `testdata/typescript/types.json`, `reference_test.go` |
| Recursive types, overloads and narrowing | Differential codes/ranges and ordered-root overload tests passed | Official binder/checker | `testdata/typescript/types.json`, `reference_test.go` |
| Structural host types | Frozen descriptors, recursive types, mapper, adapters and conversion policies implemented | Shared registry descriptor and `.d.ts` generation | `host_*_test.go`, host-module preflight/execution tests |
| TSX and JSX checking | Native checking and JSX factory/runtime lowering implemented | Official parser/checker plus JSX lowering | Runtime fixture `jsx`, `typescript_jsx_test.go` |
| Diagnostics, options and strict defaults | Native type/syntax/config/import diagnostics and strict defaults verified | Frontend facade and structured diagnostics | `project_test.go`; Unicode host-call case |
| Source snapshots | Read-only in-memory projects and declaration/environment snapshots implemented | Frozen project and environment | `files_test.go`, snapshot identity tests |

The differential harness compares diagnostic codes, severity and byte ranges
with direct calls into the checksum-verified pinned upstream frontend. Merely
importing upstream source is not by itself a passing differential test suite.
The machine inventory classifies all 351 upstream syntax kinds with implementation
owners and behavioral fixture references. It also records 13 features encoded by
flags, modifiers or compiler options rather than separate node kinds. Regeneration
fails for an unaccounted non-token kind. Checker/recovery-only nodes, JSDoc and
excluded JS-only nodes have explicit classifications; they are not executable
fallbacks. Inventory ownership does not mark the final runtime/library gate done.

## Execution families

Inputs and expected observable results are in `testdata/typescript/runtime.json`.
They are TypeScript projects, including module graphs and per-case options.
All 43 cases within the execution profile name their `execution_verification`
harness and pass. The remaining Temporal case describes an optional unavailable
proposal library; its runtime use is rejected during compilation.

Basic script execution is additionally verified by
`TestTypeScriptExecutionOperations` in `typescript_compile_test.go`: numeric and
string literals, operators, short circuiting, object/array operations, templates,
loops, switch/try/finally, closures, hoisting, loop captures, TDZ, destructuring,
defaults/rest/spread, assertions, optional chains and side-effect order.
`typescript_internal_test.go` covers the checked handoff and immutable code;
`internal/tsfrontend/ir_test.go` covers source ranges and type-only erasure.
`typescript_syntax_test.go` also checks normalization across ES2016–ESNext,
including computed methods, auto-accessors, logical assignment, optional receiver
capture and an explicitly non-strict, warning-suppressed with statement.
See `typescript-backend.md` for the fixture-extension procedure.

| Family | Existing implementation candidates / gap | Target tasks | Fixture IDs |
| --- | --- | --- | --- |
| Expressions, control flow, closures | `compiler*.go`, `vm.go`, `func.go` | 4.2–4.4 | `expressions`, `closures` |
| Enums and namespaces | Native runtime normalization, const-enum policy and merging tested | 5.1–5.2 | `enum`, `namespace` |
| Parameter properties and classes | Fields, initialization order, static blocks, private brands and inheritance tested | 5.2–5.3 | `parameter-properties`, `private-fields`, `class-initialization` |
| Decorators | Standard/legacy lowering, native helpers and registered metadata hooks tested | 5.4–5.5 | `standard-decorators`, `legacy-decorators` |
| JSX execution | Classic, preserve/react-native normalization and registered automatic runtimes tested | 5.6 | `jsx` |
| Async functions, generators and async iteration | Checked AST normalization, native Go helpers, VM continuations; ES2016–ESNext behavioral tests passed | 5.7 | `async`, `generators`, `typescript_async_test.go` |
| Explicit resource management | Native disposal helpers, standard stack classes, suppression, iterator disposal and await-using completion tested; family member audit passed | 5.8, 8.3, 8.22 | `resource-management`, `typescript_disposal_test.go`, `typescript_disposable_stack_test.go` |
| ESM cycles and live bindings | Private module records, live cells, TDZ, namespace descriptors and cached initialization tested | 6.2–6.3 | `module-live-bindings`, `typescript_modules_test.go` |
| Import/export assignment | Official AST transform and private linker require, CommonJS cycles, ESM interop and preserve mode tested | 6.5 | `module-import-assignment`, `typescript_commonjs_test.go` |
| Top-level await and dynamic import | Async dependency/cycle completion, prepared dynamic targets and canonical instance reuse tested | 6.4, 6.7 | `async`, `dynamic-import`, `typescript_module_async_test.go`, `typescript_dynamic_test.go` |
| Runtime source-string compilation | Intentionally unavailable: eval absent; all intrinsic code constructors reject with EvalError; Go-side compilation remains available | Execution profile | `eval`, `function-constructor` negative fixtures, `typescript_eval_test.go`, `typescript_function_constructor_test.go` |
| Deferred module evaluation | Deferred namespace operations, eager async subgraphs, cycles, failures, re-exports and import.defer tested | 6.9 | `deferred-import`, `typescript_deferred_test.go` |
| Import attributes and JSON modules | Strict JSON analysis, direct AST lowering, static/dynamic/deferred attributes and explicit catalog policy tested | 6.10 | `import-attributes`, `typescript_json_test.go` |
| Module metadata | Requires a module-owned metadata object; additional host properties are explicit API | 6.2 | `import-meta` |

## Standard library families

Every non-external declaration in the machine inventory belongs to these work
families, including inherited members and overloads. Runtime fixture coverage
below is a starting point: it does not replace member-by-member review. Optional
`esnext` profiles also need their declared runtime members before those profiles
can be advertised as implemented.

| Family / declaration-name prefix | Existing Goya candidate / known gap | Target | Fixture ID |
| --- | --- | --- | --- |
| Object, Function, Reflect, Proxy | `builtin_object.go`, `builtin_function.go`, `builtin_reflect.go`, `builtin_proxy.go` | 8.1, 8.4 | `object`, `proxy-reflect` |
| Boolean, Number, Math | `builtin_boolean.go`, `builtin_number.go`, `builtin_math.go` | 8.1, 8.4 | `number-math` |
| String | `builtin_string.go`, Unicode string representations | 8.1, 8.4 | `string` |
| Symbol and well-known symbols | Native symbols, registry/well-known symbols and disposal protocol fixtures passed | 5.8, 8.3 | `symbol`, `resource-management` |
| Array and array constructors | Array methods, sequential fromAsync, constructors/cleanup, sparse arrays, stable sorting and native locale formatting tested; member audit passed | 8.2, 8.19 | `array`, `typescript_array_async_test.go` |
| Map, WeakMap and grouping | `builtin_map.go`, `builtin_weakmap.go` | 8.2 | `collection`, `object` |
| Set and WeakSet | `builtin_set.go`, `builtin_weakset.go` | 8.2 | `set` |
| Iterator, Iterable and generator interfaces | Native iterator helpers, generator/async protocols, closure and disposal behavior tested; member audit passed | 8.2 | `iterator`, `generators` |
| Promise and asynchronous iteration | Combinators, withResolvers/try, async propagation and job scheduling checked; declaration-member audit passed | 8.3 | `promise`, `async`, `generators`, `typescript_collections_test.go` |
| Error and AggregateError | Cause, AggregateError, SuppressedError and Error.isError checked; constructor/member audit passed | 8.3 | `error`, `typescript_collections_test.go` |
| Date | Calendar normalization, full TimeClip bounds, coercion order, expanded ISO years and local folds/gaps tested; native DateTimeFormat locale integration, shared UTC/IANA runtime configuration and Date locale methods tested | 8.4, 8.15, 8.24 | `date`, `typescript_date_calendar_test.go` |
| RegExp | Indices, Unicode sets and string properties, local modifiers, duplicate groups, capture lifetime and all declared regexp members implemented and tested | 8.4, 8.23 | `regexp` |
| JSON | Native UTF-16 parser, strict grammar, overflow/signed zero, reviver source context and raw JSON tested | 8.4, 8.21 | `json`, `typescript_standard_json_test.go` |
| BigInt | `builtin_bigint.go` | 8.4 | `bigint` |
| ArrayBuffer, DataView and numeric typed arrays | Resizing, tracking/fixed views, recovery, transfer, conversion side effects and all 12 element kinds tested; declaration-member audit passed | 8.4, 8.20 | `typed-array`, `typescript_array_buffer_resize_test.go` |
| Float16Array and half-precision operations | Native binary16 storage, DataView operations and direct binary64 rounding tested; all 65,536 representations checked | 8.9 | `float16`, `float16_test.go`, `typescript_float16_test.go` |
| SharedArrayBuffer and Atomics | Native atomic shared storage, growth, all views and explicit Go agents passed functional/race tests; FIFO wait/notify, async owner delivery, interruption and agent-GC behavior passed functional/race checks | 8.10–8.11 | `shared-memory` |
| WeakRef | Native weak object/symbol targets, per-job retention and Go-driven GC tests passed | 8.12 | `weak-reference`, `typescript_weakref_test.go` |
| FinalizationRegistry | Native weak targets/tokens, strong held values, cancellation and owner-side RunJobs cleanup tested, including Go GC and callback failures | 8.13 | `finalization`, `typescript_finalization_registry_test.go` |
| Intl | Embedded CLDR 49 data, canonical locales, Collator, NumberFormat, DateTimeFormat, PluralRules, RelativeTimeFormat, Locale, DisplayNames, ListFormat, Segmenter and DurationFormat implemented and tested; all mandatory Intl constructors have behavioral fixtures | 8.14–8.18 | `intl` |
| DisposableStack, AsyncDisposableStack, disposal protocols | Native stack brands/state, use/adopt/defer/move, disposal order and suppression tested | 5.8, 8.3, 8.22 | `resource-management`, `typescript_disposable_stack_test.go` |
| Temporal (`esnext` profile, outside ES2025) | Native type analysis available; runtime use rejected at compilation with goya 1104 | Optional runtime profile unavailable | `temporal`, `TestTypeScriptUnsupportedStandardProfileFailsCompilation` |
| Pure type utilities, decorator context types and generic protocol interfaces | No runtime object required for erased types | Official checker, 5.4–5.8 | Type fixtures and corresponding runtime family |
| Global URI helpers (`global-functions`) | `builtin_global.go` | 8.1, 8.4 | `global-functions` |

DOM, WebWorker, ScriptHost and Node API are outside the default runtime. Explicit
application declarations remain possible; declarations alone never create host
bindings. No filesystem/network/process loader is installed implicitly.

`TestTypeScriptCollectionPromiseErrorResourceMemberInventory` audits 1316 distinct
declared members/signatures across core objects, functions, generators, primitives,
Date, Math, JSON, Reflect, Proxy, collections, promises, errors, weak references,
disposal stacks, buffers, Atomics, views, Collator, NumberFormat, DateTimeFormat, PluralRules, RelativeTimeFormat, Locale, DisplayNames, ListFormat, Segmenter, Segments, SegmentIterator, DurationFormat and RegExp/match interfaces, including implemented optional profiles. The audit also checks every top-level
runtime global in the complete ES2025 library reference closure. Computed names
use their original `Symbol` expression recorded in the machine inventory. Runtime members
are checked against native objects; inline option/result-bag fields and index
signatures are classified as type structure. Instance-only optional Error fields
are covered by behavioral tests. Structural presence alone does not establish conformance.
The upstream GeneratorFunction/AsyncGeneratorFunction interfaces contain structural
construct signatures; generator function instances remain non-constructable under
ECMAScript semantics. Those signatures are classified explicitly in the audit.
No bundled declaration is removed or modified to fit runtime capabilities.
Selecting `esnext` does not claim that every proposal library is executable;
Compile rejects references to unavailable standard globals before producing a Program.
Array and typed-array locale formatting forward locale/options to native Number/BigInt formatters; locale behavior is covered by the Intl fixtures.

## Completion gate

For each library declaration and overload: classify whether it is erased or has
a runtime obligation, associate its owning family and fixture, and verify its
actual behavior. Add dedicated tasks for missing families before implementation;
do not close broad tasks merely because the smoke fixture passes. Record known
gaps as unfinished work, not compatibility exceptions. The overall change stays
in development until syntax, type, transform, module, standard-library and
pure-Go embedding gates all pass.

The execution profile excludes string-code generation and runtime source loading.
The owned declaration overlay removes eval while retaining Function object types.
Computed imports require prepared targets; lazy plugins must declare their host
requirements before startup. Legacy eval/code-constructor fixtures are skipped
explicitly rather than counted as conformance passes. Go callbacks remain
application-granted capabilities, so this profile is not process isolation.
