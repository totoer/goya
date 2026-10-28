# Runtime environment and ownership

The standard profile uses the pinned ES2025 language declarations, with strict
TypeScript checking. DOM, WebWorker, ScriptHost and Node declarations are absent
by default. Compiler assets are embedded. Project and package resolution read
only the in-memory catalog captured from the Go application.

The [compatibility inventory](typescript-compatibility.md) records the verified
language and standard-library families, including Intl and modern RegExp.
Selecting additional declarations does not install runtime objects. References
to unavailable standard globals, such as Temporal from `esnext`, fail compilation
with diagnostic goya 1104 before a Program is created.

The profile has no built-in eval or source-string function construction. Intrinsic
Function/AsyncFunction/GeneratorFunction/AsyncGeneratorFunction calls and
construction reject with EvalError, including prototype and alias paths.
Recognized direct calls are rejected at compile time with goya 1114. Ordinary
functions, closures, invocation methods and function prototypes remain usable.

`import()` only selects prepared modules from the Program. Unknown computed
targets reject with TypeError without invoking a resolver or frontend.
Compilation and changes to the allowed set belong to the Go application's API.
The typed registry reserves the global name eval; object methods such as
api.eval() are allowed. Explicit host callbacks retain the capabilities granted
by their application, and memory/CPU isolation remains a separate concern.

## External APIs

A new Runtime installs no console, process, require, readFile, load, networking
or timer API. Register a typed function, object or host module with Registry,
freeze an Environment, and bind a compatible Environment before RunProgram.
Compile and Check use that Environment's declarations. Additional unused
registrations are allowed; missing or changed required schemas fail preflight
before user initializers. Declaration-only APIs, including an explicitly selected
DOM library, do not supply implementations.

The Go CLI reads its selected input and snapshots a project directory. This is
application input handling: the source catalog never exposes an OS file-reading
function to the executed code. A package specifier resolves to supplied metadata
and TS sources or a registered host module, without loading Node's require.

Date and Math remain language intrinsics. The Go application may use SetTimeSource
and SetRandSource to control their clock and randomness. SetTimeZone selects the
shared Date/Intl timezone from embedded IANA data or a fixed minute offset. These callbacks belong
to the application's runtime configuration, not a script-accessible OS API.

Date calendar components remain Numbers during normalization and are clipped only
after computing the timestamp and applying the local offset. Large components
cannot wrap Go integers; cancellation between finite components is preserved.
Setters snapshot their original timestamp before argument conversions, execute
all required conversions in order, and preserve ordinary conversion side effects.
TimeClip accepts the inclusive range of ±8,640,000,000,000,000 milliseconds and
truncates fractions toward zero. Expanded ISO years use a sign and six digits.

Local Date operations use the Runtime's configured timezone. New runtimes use
UTC; Go selects an embedded IANA zone or fixed offset with SetTimeZone. Repeated local
times choose the earlier instant; skipped times use the offset before the
transition. Parsing a local date-time follows the same rule. Conversion applies
the offset at the normalized result, which can differ from the original Date's
offset. Tests include half-hour DST transitions, a skipped day and local values
at both TimeClip boundaries. These follow the
[calendar and timezone algorithms](https://tc39.es/ecma262/multipage/numbers-and-dates.html#sec-makeday).

## Goroutines and asynchronous work

One goroutine owns a Runtime while it is executing. Runtime methods, script
objects, host views, callbacks and promise resolvers must be used on that owning
goroutine, or while the runtime is idle. Independently created runtimes may run
concurrently and share an immutable Program. Manifest returns detached data;
source-position caches and shared checked analysis have their own synchronization.

Each Runtime owns its module namespace objects, initialized modules, failure
cache, promise jobs and host view identities. Repeated imports of one canonical
module reuse that instance within the Runtime. A changed code/profile identity
for an already loaded canonical module rejects the new graph before effects.
Different runtimes have separate script state. Go objects intentionally shared
by the application keep their Go identity and require the application's own
synchronization when mutable; use separate implementations for separate host state.

Promise jobs drain before the outer execution call returns. The engine creates
no timer, worker, I/O service or automatic event loop. An application that performs
asynchronous I/O must schedule its callback or promise resolution back onto the
runtime's owner. RunProgram returns a promise for an async module graph; the
application should inspect its fulfillment or rejection. The CLI reports a
pending graph as an error because it has no application event loop.

Array.fromAsync is implemented as native Go promise continuations. The pinned
frontend declares it in `lib.esnext.array`, so select `lib: ["esnext"]` to check
its use. Processing is sequential. Array-like and sync-iterator values are
awaited; an async iterator's value is preserved unless the mapper awaits it.
Mapper or target-property failures await iterator cleanup and preserve the
original error. Its
[standard algorithm](https://tc39.es/ecma262/multipage/indexed-collections.html#sec-array.fromasync)
is exercised by typescript_array_async_test.go.

DisposableStack and AsyncDisposableStack also have native Go implementations.
Select `lib: ["esnext"]` for their pinned declarations. They capture disposal
methods when registered, dispose in reverse order, transfer ownership through
move, reject additions after disposal, and fold later cleanup failures into
SuppressedError. Async disposal waits sequentially; synchronous fallback results
are ignored. Both classes support using/await using and preserve their own brands
when subclassed. Their
[standard contracts](https://tc39.es/ecma262/multipage/control-abstraction-objects.html#sec-disposablestack-objects)
are exercised by typescript_disposable_stack_test.go.

Float16Array, DataView.getFloat16/setFloat16 and Math.f16round use IEEE binary16
rounding directly from binary64, with nearest/ties-to-even rounding, subnormals,
NaN, infinities and signed zero. There is no float32 intermediate. The largest
finite magnitude is 65504; the overflow midpoint is 65520. A magnitude of 2^-25
ties to zero. ArrayBuffer storage uses native byte order; DataView has its usual
explicit endian option. Float16Array.Export returns a copied []float64 because
Go has no native binary16 scalar; the values remain exact. Raw storage is available
through its ArrayBuffer. See the
[rounding algorithm](https://tc39.es/ecma262/multipage/numbers-and-dates.html#sec-math.f16round).
Element brands and Number/BigInt compatibility use private storage identity,
including empty arrays and species/subclasses, rather than public constructor names.

ArrayBuffer accepts the maxByteLength constructor option. Resizable buffers can
shrink or grow up to that maximum; regrown storage is zeroed. An omitted length
on a resizable-buffer TypedArray or DataView constructor creates a tracking view.
Explicit lengths stay fixed. A fixed view becomes out of bounds when any part
of it is removed and recovers its original extent when the buffer grows again.
Tracking typed arrays round their length down to a whole number of elements.
Out-of-bounds typed-array length/byteLength/byteOffset are zero and indexed reads
return undefined; methods that validate the view throw. DataView length/offset
getters and operations throw for an out-of-bounds view. The buffer property
continues to expose the original buffer.

Transfer detaches the original buffer and all its views. The transfer method
preserves resizability and the maximum; transferToFixedLength produces a fixed
buffer. Slice copies the still-available prefix after species/coercion effects.
Typed-array callbacks use their initial iteration length, while individual reads
observe the current buffer. Copying and filling revalidate and clamp according to
their algorithms. Sorting snapshots values before comparisons and writes back
the still-valid prefix. A resizable-buffer view cannot be made non-extensible.

Go ArrayBuffer.Bytes and ordinary typed-array exports may expose a backing slice.
Reacquire such slices after resize or transfer; previously returned Go slices do
not track replacement storage. These operations belong to the Runtime owner and
do not permit concurrent access. Controlled sharing uses the independent
[SharedArrayBuffer wrapper](typescript-shared-memory.md). The 39 checked scenarios and adapted regressions in
typescript_array_buffer_resize_test.go follow the
[buffer algorithms](https://tc39.es/ecma262/multipage/structured-data.html#sec-arraybuffer-objects)
and [typed-array algorithms](https://tc39.es/ecma262/multipage/indexed-collections.html#sec-typedarray-objects).

JSON parsing operates on UTF-16 code units, preserving unpaired surrogates in
strings and keys. It accepts the JSON grammar, retains signed zero, rounds numeric
values to binary64 and produces infinities on numeric overflow. Duplicate object
properties use their last value; `__proto__` is an ordinary own data property.

Revivers receive a third, plain context object. Its source property contains the
original primitive token, including quotes/escapes where applicable. The property
is absent for objects and for values changed by an earlier reviver invocation.
JSON.rawJSON validates primitive JSON text, creates a frozen null-prototype object,
and preserves that text in stringify. JSON.isRawJSON tests the private brand;
proxies and copies do not inherit it. Stringify preserves UTF-16 in replacer keys,
raw text and its ten-code-unit indentation limit. Go MarshalJSON exports escape
lone surrogates to retain their value in UTF-8 JSON bytes.

The pinned TypeScript declarations currently expose only the two-argument
reviver and omit rawJSON/isRawJSON. These runtime extensions use application-owned
declaration augmentations or explicit assertions. A callback with an optional
third parameter is accepted by the stable signature. The behavioral tests in
typescript_standard_json_test.go check these cases without modifying upstream
declarations. The parser and source-context behavior follow the
[JSON algorithms](https://tc39.es/ecma262/multipage/structured-data.html#sec-json.parse).

## Weak references and garbage collection

WeakRef uses Go weak pointers for objects and non-registered symbols. Creating a
reference or successfully calling deref retains the target until the current
script or promise job ends. Each job releases its own kept targets before the
next job starts. A registered Symbol.for value remains an invalid weak target
when the Go application passes it between runtimes. WeakMap and WeakSet apply the
same rule. Well-known symbols are valid targets and remain intrinsically live.

After a job ends, deref may return undefined if Go has collected the target.
Collection timing is unspecified; a successful deref does not promise survival
into a later job. There is no script-accessible GC function. Behavioral and
Go-driven GC checks are in typescript_weakref_test.go and follow the
[WeakRef contract](https://tc39.es/ecma262/multipage/managing-memory.html#sec-weak-ref-objects).

FinalizationRegistry keeps registered held values strongly and targets and
unregister tokens weakly. A Go cleanup callback only appends a weak registry
identity and registration ID to a synchronized queue. It does not call script or
retain the registry and its held values. Unregister removes all matching cells,
including notifications already queued for those registrations.

The application calls Runtime.RunJobs while the runtime is idle to deliver
pending cleanup callbacks and drain their promise continuations. Callbacks run
on that calling goroutine with an undefined receiver and the held value. A
callback exception is returned to Go; later queued callbacks remain pending.
RunJobs supports interruption and rejects recursive calls from executing code.
It creates no event loop, forces no collection, and supplies no script GC API.
Cleanup timing and eventual delivery are not guaranteed: keep important resource
release in explicit disposal or Go application lifecycle code. See
typescript_finalization_registry_test.go and the
[FinalizationRegistry algorithms](https://tc39.es/ecma262/multipage/managing-memory.html#sec-finalization-registry-objects).

## Interruption

Interrupt may be called from another goroutine. Execution terminates with an
InterruptedError carrying the application's value; TS catch clauses cannot
consume it. This applies to synchronous loops, module initialization, top-level
await continuations and chains of promise callbacks. A running Go host function
must cooperate with application cancellation itself; Interrupt takes effect when
control returns to VM execution.
The job drain also checks interruption between native continuations, including
chains that execute no script bytecode.

Array.prototype.flat and flatMap check interruption during their native traversal,
including skipped holes. Traversal uses an explicit stack bounded by
SetMaxCallStackSize (including active VM calls) and a maximum of 10000 nested
arrays. Exceeding either bound returns an uncatchable StackOverflowError to Go;
cyclic arrays cannot overflow the Go stack through flattening.

Abrupt exit discards queued jobs and clears the interrupt flag. Unfinished module
instances record the interruption and cannot later be treated as initialized or
resumed by a pending host promise. Their canonical cache entries keep the same
host-side error. Modules whose initialization already completed keep their state.
The Runtime can execute a new independent checked program afterwards; create a
fresh Runtime to restart an interrupted graph. Drop its unfinished promises.

## Errors and source positions

Check and Compile return structured diagnostics with upstream TypeScript codes,
source names, UTF-8 byte offsets, one-based UTF-16 columns and related locations.
Convenience Go APIs return CompilationError when checking fails. Eval and the
function constructors expose a catchable SyntaxError named
TypeScriptCompilationError with plain diagnostic objects; they use the checked
frontend and do not call the old JavaScript parser.

Runtime exceptions retain original TS source positions through class,
decorator, async and module lowering. StackFrame.Position reports UTF-16 columns
for checked TypeScript. Internal module factories and helper operations do not
replace source names with generated JavaScript files.

Behavioral checks are in typescript_interrupt_test.go, typescript_stack_test.go,
typescript_isolation_test.go and the module/host tests. Numeric, UTF-16 and object
operation regressions also run through the checked entry point in
typescript_vm_regression_test.go.
