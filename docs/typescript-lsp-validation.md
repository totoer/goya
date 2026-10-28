# Embedded TypeScript LSP validation

Validation environment: 2026-10-28, Go 1.27.2, darwin/arm64. The imported frontend
is TypeScript 7.0.2, commit `2bd066d87f5bafd315be9f40889d0a60b9e58e0b`.
Committed oracle fixtures use TLS 6.0.2 with TypeScript 6.0.3; their package digests
and source adaptations are recorded in the parity inventory and source notices.

| Check | Result |
| --- | --- |
| `CGO_ENABLED=0 GOPROXY=off go test ./...` | Passed, including compiler/runtime regressions and the complete direct/wire fixture suite. |
| `CGO_ENABLED=0 GOPROXY=off go build ./...` | Passed. |
| `importupstream -archive <verified-archive> -check` | Passed: 65 packages, 540 files; generated import matches the pinned archive and local patches. |
| Public SDK, protocol, editor, fixture and inventory package tests | Passed. Every exposed preference has explicit pinned oracle evidence. |
| In-memory session, completion and stream-server example binaries | Passed with an empty environment/PATH from an empty working directory. |
| OpenSpec strict validation | Passed. |

The examples were built with CGO disabled, then executed with `env -i` and PATH
pointing to an empty directory. The session example reported two explicit resolver
calls and zero host-function calls, checked overlays/configuration, and read frozen
host declarations. The completion example produced an import, signature and a
checked program with no diagnostics. The stream example initialized UTF-16 and
returned hover over caller-provided streams. No Node, npm, tsc or tsserver executable
was available to those binaries. The tests consume committed fixtures without
installing or launching oracle tools; Node packages are development capture tools.

The first full test invocation had one fixture harness deadline failure in
`refactor-type-object`, with no golden mismatch. The shared normal transcript
budget was adjusted from 15 to 45 seconds and the full invocation passed. Its fresh
fixture package run took 176.323 seconds on this host. Race transcripts retain a
90-second budget. Harness timeouts are bounded test allowances, not a service SLA;
URI/opaque-handle projections and explicitly agreed source-coordinate projections
are the only comparison adaptations.

The complete race invocation passed. Package times were 26.536 seconds for the
SDK, 1.252 for protocol DTOs, 1.413 for the fixture runner, 597.727 for the complete
2316-fixture direct/wire corpus, and 3.078/1.441/5.199 for editor/importer/inventory
checks. There were no race reports. The reproduction command allows 30 minutes
for a whole instrumented package, independently of each transcript's 90-second
budget, so a large corpus is not tied to Go's default ten-minute package timeout.

`BenchmarkSessionRepeatedEditCancel` ran three independent 100-iteration samples
on an Apple M1 Pro (ten logical CPUs): 3.969, 4.220 and 3.418 ms/op; respectively
5,951,727 / 5,942,621 / 5,942,303 B/op and 16,115 / 16,103 / 16,101 allocations/op.
Each sample rebuilt the editor generation, queried hover, and rejected a cancelled
query with invalidation events intentionally unread. All ended with two queued
events and zero retained result bytes. B/op measures allocations rather than live
heap or RSS; this is a measured workload, not a service performance guarantee.

The repeated edit/cancel/close test separately exercised three sessions with 24
edits each, completion invalidation, cancelled-query recovery, four concurrent
Close callers, a two-event queue and result limits of four entries/8192 bytes.
It checked empty request slots, cleared private results and released source,
environment and workspace state after Close. Active/queued cancellation and
client-response backpressure tests also passed under race. Immutable process
library caches are distinct from retained per-session results.

The statement-free move insertion decision selects the actual source EOF. The
additional pinned transcripts cover empty/whitespace/comment/LF/CRLF/BOM targets
and emoji with Unicode separators. The strict fixture projector preserves raw
oracle payloads, source hashes and independently recorded source shape/spans;
negative tests reject broader projection. SDK, wire and detached-catalog checks
passed. The host-selected empty-config failure is recorded separately through
the pinned TypeScript language-service API and rejects the complete proposal.

Final post-decision validation passed:

| Check | Result |
| --- | --- |
| Full `CGO_ENABLED=0 GOPROXY=off go test ./...` | Passed; runtime/compiler package 26.436 seconds, complete 2332-fixture direct/wire package 142.204 seconds. |
| Affected SDK/refactor race checks | Passed in 6.053 seconds. |
| Strict EOF fixture projector race checks | Passed in 1.297 seconds. |
| All move transcripts and detached-catalog checks under race | Passed in 23.156 seconds. |
| Pure-Go build and importer `-check` | Passed after the EOF change; 65 packages, 540 files. |
| `lspparity -check -release` | Passed: all 283 inventory entries validated, with no pending applicable methods, preferences or providers. |

The earlier complete race run covers the full baseline corpus; the affected race
rerun includes every new statement-free-target case and existing move variant.
The source-span exception changes only the insertion position, preserving the
reference edit text/order and the host-owned application contract.

To reproduce the offline checks after dependencies and the verified upstream
archive have been supplied:

```sh
CGO_ENABLED=0 GOPROXY=off go test ./...
CGO_ENABLED=0 GOPROXY=off go build ./...
CGO_ENABLED=0 GOPROXY=off go run ./internal/tsfrontend/cmd/importupstream \
  -archive <verified-typescript-source-archive> -check
CGO_ENABLED=1 GOPROXY=off go test -race -timeout 30m -p 1 ./lsp ./lsp/protocol \
  ./internal/lspfixture/... ./internal/tsfrontend/editor \
  ./internal/tsfrontend/cmd/importupstream ./internal/tsfrontend/cmd/lspparity
CGO_ENABLED=0 GOPROXY=off go test ./lsp -run '^$' \
  -bench BenchmarkSessionRepeatedEditCancel -benchtime=100x -benchmem -count=3
```

CGO is enabled only for Go's race detector on its supported host configuration;
production builds and ordinary tests require no C toolchain.

## Execution-profile delta: no source-string compilation

The owned standard-library overlay removes global eval and its documentation.
Recorded upstream oracle responses remain intact. Reviewed completion steps
explicitly annotate `execution_profile_omit_global_eval_completion`: only the
reference expectation for embedded endpoints omits that single standard-global
item. Actual responses are never filtered. All remaining items, ordering, edits
and incomplete flags remain exact, and resolve indices are adjusted to select
the same originally recorded item. Fixture-integrity tests reject ambiguous or
non-global omissions and detect an endpoint that accidentally offers eval.

Direct and wire diagnostics for recognized source constructors additionally
match compiler goya 1114. This is an intentional execution-profile difference,
not a change to the pinned upstream oracle or a claim of complete ECMAScript
compatibility. Session snapshots preserve ModuleRoots for Go-side compilation.
