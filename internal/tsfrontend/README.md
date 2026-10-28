# TypeScript frontend integration

This directory contains the pinned source baseline and integration tooling for
the native TypeScript interpreter. The verified language baseline is TypeScript
7.0.2 with an ES2025 runtime profile. `manifest.json` pins the source, profile
and verification toolchain; [the verification record](../../docs/typescript-release.md)
records the gates.

## Provenance

`manifest.json` records the stable TypeScript version, original source repository,
release tag and commit, source archive checksum, published npm package integrity,
asset checksums, and local adaptations. The 7.0.2 release was originally tagged in
`microsoft/typescript-go`, even though its npm repository metadata names
`microsoft/TypeScript`. Resolve this relationship from release metadata when
updating; do not substitute a repository's current `main` branch.

The upstream license is retained in `LICENSE.typescript`. Imported code also
retains upstream `LICENSE`, `NOTICE.txt`, and per-file copyright notices.

## Reproduce or verify the import

Use Go 1.27.2 for byte-for-byte regeneration; application builds require Go 1.27
or newer. Download the **exact source archive
URL in the manifest**, then run from the repository root:

```sh
CGO_ENABLED=0 go run ./internal/tsfrontend/cmd/importupstream \
  -archive /path/to/typescript-source.tar.gz

CGO_ENABLED=0 go run ./internal/tsfrontend/cmd/importupstream \
  -archive /path/to/typescript-source.tar.gz -check

CGO_ENABLED=0 go build ./internal/tsfrontend/upstream/...
go test ./internal/tsfrontend/cmd/importupstream
```

The importer checks the source archive, generated assets and source-adaptation
digests before writing. Roots include compiler/bundled/parser/checker/ast and the
native ls/project/lsp packages. It follows Go import declarations to collect their transitive
internal packages, excludes test files and `ignore`-tagged generators, retains
OS-specific implementations, and includes the bundled standard declarations.
Neither the import nor the build invokes npm, Node.js, or code generators.
Application builds use the committed tree and need no source archive or network.

Go import literals are relocated from
`github.com/microsoft/typescript-go/internal/` to
`github.com/totoer/goya/internal/tsfrontend/upstream/`. Modified files carry a
notice. Other string literals are preserved by relocation. Separate, explicitly
pinned adaptations are applied before relocation; their exact source hashes and
single-match edits are recorded in `cmd/importupstream/patches/*.json` and their
checksums in `manifest.json`. A changed upstream body or mismatching adaptation
fails import rather than silently changing the patch target. `upstream/files.lock.json` records
both original and imported file digests. `-check` verifies file contents and
rejects unexpected files without writing. An update does not silently remove
stale or locally modified source files: review those explicitly.

The expanded import contains 65 packages and 540 files at this baseline. Embedded
language-service/server options disable npm/ATA, parent-process watchdog hooks,
system telemetry, native filesystem watchers and external profiling/API-session
commands. The host supplies all source updates. Native source import is not a
claim of editor feature parity: the public facade and remaining fixes/refactors
are tracked by [the LSP inventory](../../docs/typescript-lsp.md).

The embedded adaptation also provides the shared command-line normalizer for
configured/inferred editor projects and serializes background work registration
with queue shutdown. The public workspace adapter owns cancellation and waits
for registered background work before releasing a retired generation.

The adaptation exposes ephemeral completion context/member builders, completes
import-statement/type-only import handling and prepares auto-imports for the
selected project. Auxiliary export-detail programs parse only the frozen virtual
filesystem and retain normalized reference configurations. TLS presentation and
opaque result ownership live in the public facade/editor adapter; compiler ASTs
and native completion metadata never enter the public API.

Hover/signature adaptations preserve the reference Markdown/JSDoc rendering,
overload selection/counts and optional-parameter presentation. Hover expands
checked parameter types while completion/signature help retain declaration
annotations. Symbol links are generated from declaration starts and mapped to
the embedding's frozen resource URIs by the public facade.

Auto-import exclusion patterns share the runtime's pinned ECMAScript grammar
through `regexpgrammar` and its pure-Go regexp engines through `editorregex`.
The adaptation's bounded regex cache retains only compiled matchers and pattern
text. A matching budget failure unwinds the editor query and becomes a typed
request error at the facade; it cannot silently produce partial completions.

Reference-query adaptations retain the initial declaration identity across
related projects and distinguish local aliases from exported symbols when
filtering definition locations. Highlight presentation follows the pinned TLS
kind mapping and tag-name ranges; Unicode whitespace is decoded as runes when
combining `else if`. Request-local declaration keys contain only detached names
and positions, and disappear when the search finishes.

Dependency notices and exact module versions, including the native project
system's `github.com/mackerelio/go-osstat` dependency, are recorded in
`go.mod`/`go.sum` and `docs/typescript-dependency-notices.json`. Embedded sessions
disable the system telemetry path that uses OS memory statistics.

## Update procedure

1. Check the official `typescript` npm `latest` metadata and corresponding stable
   GitHub release. Reject prerelease versions and verify the source tag, commit,
   source version, package integrity, and asset provenance agree.
2. Update the baseline manifest, licenses, asset digests, and the recorded local
   patch set. Review upstream changes before importing a new snapshot.
3. Run the import and its read-only verification. Review the changed file lock,
   original notices, import relocations, and dependency versions.
4. Update the language/type/runtime/standard-library compatibility inventory and
   add or revise the affected conformance fixtures. Include the new version in
   analysis cache keys and compiled-program identities.
5. Run importer, frontend, registry, backend, module, runtime, and embedding
   checks, including `CGO_ENABLED=0` builds without external compiler tools.

Development snapshots may have incomplete compatibility. A release must not
claim full support until all required entries in the compatibility inventory and
the OpenSpec change's integration gates are complete. A checker-only success is
not evidence of runtime or standard-library compatibility. Do not make missing
runtime members disappear by deleting their standard type declarations.

## Environment boundary

The standard declarations are compiler assets, not host capabilities. The default
language profile must exclude DOM, WebWorker, ScriptHost, and Node declarations.
User sources, package metadata, external declarations, and module bindings come
from the Go application. The frontend must not fall back to an OS filesystem,
spawn a compiler process, run a content-mapper executable, or execute host API
functions during checking.

`editorpattern` adapts the pinned oracle TypeScript 6.0.3 pattern matcher, with its
Apache-2.0 notice, UTF-16 word/case semantics and the original matching unit cases.
The embedded symbols profile preserves navigation-tree declaration spans and
merging, drops outline aliases, and uses reference symbol kinds and UI ordering.
Temporary symbol-to-node maps live only inside one leased query. Workspace
symbol indexing never mutates the compiler's named-declaration cache.

The selection profile adapts the pinned TypeScript 6 smart-selection algorithm
with ephemeral SyntaxLists and tokens. It does not change native parent links or
put tokens with synthetic parents in the source-file token cache; repeated-query
retention has a regression test. It preserves reference EOF line-index ranges,
list/bookend grouping and JSX attribute folding bounds.

The semantic-token profile uses the native classifier with TS6's inclusive range
intersections, fixed 2020 type/modifier legend and UTF-16 relative encoding. Client
legend filters do not silently renumber tokens. The call-hierarchy adaptation
propagates request cancellation into checker lookups/collection, retains the
reference's call-site multiplicity and first-encounter outgoing order, and emits
deprecation tags. Public hierarchy handles retain only detached source targets.

The inlay-hint profile preserves TS6 interactive part boundaries and omits links
on synthesized parameter/property names while retaining type-reference links.
Plain hints use a separate single-line printer and a request-local emit context
for tuple layout. No source AST flags are changed by presentation preferences.

## Owned execution profile

The upstream assets remain unchanged. `execution_profile.go` masks the standard
eval declaration and adds symbol-aware diagnostics for direct source-code
constructors. Inventory generation applies the same overlay, so its library
hashes describe the effective declarations rather than unmodified asset bytes;
manifest and files.lock.json continue to pin the raw imported baseline.

ModuleRoots and literal imports prepare a closed graph in one resolver snapshot.
Only detached code, import rules and source/contract metadata cross into Program;
Analysis no longer retains a callback for dynamic compilation.
