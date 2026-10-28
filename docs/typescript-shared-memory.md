# Shared memory and Go agents

SharedArrayBuffer is a language intrinsic. The engine creates no script workers
or additional Runtime instances. Go decides which runtimes run concurrently and
which shared stores they receive. Each Runtime and its script objects still have
one owner goroutine. A compiled Program can be reused across those runtimes.

The Go wrapper is independent of a realm:

```go
buffer, err := goya.NewSharedArrayBuffer(4, 64) // growable; omit 64 for fixed
if err != nil { return err }
first := firstRuntime.ToValue(buffer)
second := secondRuntime.ToValue(buffer)
```

These are separate script objects with separate prototypes and ordinary own
properties. They share the byte store. Repeated conversion in a realm reuses a
still-live realm object; weak cache entries retain neither objects nor storage.
Exporting a script SharedArrayBuffer returns the transferable Go wrapper.
Ordinary ArrayBuffer wrappers continue to belong to their original runtime.

Register the wrapper with the standard TypeScript contract through an explicit
adapter. The complete example is in
[`examples/typescript-shared-memory/main.go`](../examples/typescript-shared-memory/main.go).
It compiles a counter once and explicitly starts four Go agents, each owning its
own Runtime and compatible binding. Their shared increments use Atomics.

SharedArrayBuffer supports byteLength, maxByteLength, growable, grow and slice.
Growth is monotonic, newly visible bytes are zero, and the configured maximum
does not change. Growth reserves the maximum backing storage during construction;
an allocation failure is an error. Omitted view lengths on growable stores track
growth; explicit view lengths stay fixed. Tracking arrays round down to whole
elements. Fixed shared views can be made non-extensible; tracking views cannot.
Shared stores cannot be detached or transferred as ordinary ArrayBuffers.

All shared accesses use Go atomic words, including ordinary typed-array reads
and writes. Subword writes use compare-and-swap, preserving adjacent elements.
Integer element reads/writes do not tear; DataView uses byte accesses and may
observe concurrent changes between bytes. Go ReadAt and WriteAt also use byte
operations and report bounds through the usual io errors. No shared backing slice
escapes to Go: typed-array and byte exports copy values. A copy of multiple
elements is not an atomic snapshot of the entire store.

The application may call the wrapper's ByteLength, MaxByteLength, Growable, Grow,
ReadAt and WriteAt concurrently with script agents. It must continue to synchronize
its other mutable Go objects. Shared storage does not permit simultaneous access
to the same Runtime from multiple goroutines.

The basic Atomics operations support signed/unsigned 8/16/32-bit integer arrays
and 64-bit BigInt arrays, including ordinary ArrayBuffers. Store returns the
coerced integer before storage-width wrapping. Read/modify/write operations return
the preceding element. All argument conversions complete before the final buffer
validation. Atomics.pause yields without exposing a timer or worker API.

Atomics.wait and waitAsync accept shared Int32Array and BigInt64Array views.
They compare and register under the storage's waiter lock. Wait suspends its Go
agent until notification, timeout or application Interrupt. Notify wakes matching
waiters in registration order, with an optional count; views with different
integer widths share a waiter list when their starting byte is the same.
Ordinary buffers support notify (returning zero), but cannot be waited on.

WaitAsync returns a synchronous not-equal/timed-out result when appropriate;
otherwise it returns a promise. A notification in the same agent resolves that
promise immediately, scheduling its reactions in the usual FIFO job queue.
Notifications from other agents and timeout callbacks enqueue only an identity
and a primitive result. They never access script values or enter the owner's VM.
The owner delivers those results when it next runs code or calls RunJobs while
idle. The application supplies any event loop; no script worker or timer API is
installed. Native timeout bookkeeping alone may use Go timers.

Interrupt wakes a blocking wait and returns the usual InterruptedError. It also
abandons that agent's outstanding asynchronous waits when execution unwinds.
Other native Go callbacks must still cooperate with cancellation themselves.
A dead Runtime does not remain alive through a shared waiter list: Go cleanup
removes its registrations. This cleanup, like other GC behavior, has no promised
delivery time.

The contracts are exercised by typescript_shared_buffer_test.go and
typescript_atomics_test.go and typescript_atomics_wait_test.go against the
[shared-buffer](https://tc39.es/ecma262/multipage/structured-data.html#sec-sharedarraybuffer-objects)
and [Atomics](https://tc39.es/ecma262/multipage/structured-data.html#sec-atomics-object)
algorithms. The final pure-Go and race verification is recorded in
`typescript-release.md`.
