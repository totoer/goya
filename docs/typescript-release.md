# TypeScript verification record

The native interpreter targets TypeScript 7.0.2 with the unmodified ES2025
standard declarations, strict checking by default and explicitly registered Go
capabilities. This is a breaking replacement of the former JavaScript API.
The source change is prepared for review; no release or archive is published by
these checks.

Stable baseline was rechecked on 2026-10-26 against
[the official latest package metadata](https://registry.npmjs.org/typescript/latest):
version 7.0.2, gitHead `2bd066d87f5bafd315be9f40889d0a60b9e58e0b`.
The source archive, package integrity, original asset hashes, local relocation
hashes and licenses are pinned in `internal/tsfrontend/manifest.json` and
`internal/tsfrontend/upstream/files.lock.json`. Native compiler source bodies and
standard declarations are preserved; source relocation is mechanical.

The subsequent embedded-LSP change expands the source closure and adds separately
pinned source adaptations for embedded language-service operation. The compiler
verification recorded below predates that integration; it does not establish
editor parity. Current LSP implementation coverage and checks are tracked in
[the LSP inventory guide](typescript-lsp.md).

Verification used Go 1.27.2 on macOS/arm64. Runtime builds require Go 1.27 or newer.
Compiler and data regeneration uses the recorded toolchain because Go formatting
and compression can change between releases. Checks use provided local archives;
application operation never downloads compiler or locale data.

Artifact digests and machine-readable results are in
[typescript-verification.json](typescript-verification.json).

## Compatibility gates

- Native differential diagnostics: fixture codes, severity and source ranges,
  including explicit root ordering and Unicode positions, match the pinned
  checker called directly.
- Syntax inventory: 351 kinds and 13 features have explicit implementation or
  erasure classifications; unknown executable kinds fail compilation.
- Runtime catalog: 43 TypeScript projects execute with their expected exports,
  covering transforms, module graphs and standard-library families. The optional
  Temporal case passes native analysis and is rejected at compilation with goya
  1104. All bundled declarations remain available to independent type analysis.
- Standard-library audit: 1317 distinct members/signatures and the complete
  ES2025 top-level global reference closure are classified and checked against
  native objects. Family-specific behavioral suites supplement structural checks.
- Unicode: all 65,536 binary16 representations; 3222 official Unicode 17 break
  cases; all 3953 RGI emoji sequences and the seven string-property catalogs.
- Full `CGO_ENABLED=0 go test ./...`, including CLI, importer and adapted VM
  regressions; race checks for parallel Program/module/host isolation,
  concurrent standard-library use, shared buffers, Atomics wait/notify,
  frontend reuse and immutable locale/timezone data.
- Standalone Check → Compile → Run: an executable built with `CGO_ENABLED=0`
  returns 41 and 42 in two compatible runtimes, with PATH absent. It also passes
  an OS sandbox denying network access, fork, execution of other binaries and
  reads from the source tree, compiler sources and module/build caches.
- Frontend import, declaration/syntax inventory, CLDR, IANA, Unicode category/
  segmentation and emoji importers reproduce their checked-in outputs.

These are explicit compatibility checks, rather than an exhaustive proof of all
possible programs. TypeScript's `any`, assertions and trusted declarations retain
their standard limitations. Go boundary conversion checks actual argument values.

## Reproduce the checks

Use the Go 1.27.2 toolchain and the exact archives identified by the corresponding
manifests. Compiler/data import commands perform no network access themselves.

```sh
CGO_ENABLED=0 go test ./... -count=1
CGO_ENABLED=0 go run ./internal/tsfrontend/cmd/importupstream -archive /path/to/typescript-source.tar.gz -check
CGO_ENABLED=0 go run ./internal/tsfrontend/cmd/inventory -check
CGO_ENABLED=0 go run ./internal/cldrdata/cmd/importcldr -archive /path/to/cldr-49.0.0.tar.gz -check
CGO_ENABLED=0 go run ./internal/tzdata/cmd/importtz -wheel /path/to/tzdata-2026.5.whl -check
CGO_ENABLED=0 go run ./internal/unicode17/cmd/importucd -archive /path/to/UCD.zip -check
CGO_ENABLED=0 go run ./internal/unicode17/cmd/importemoji -sources /path/to/emoji-17 -check
GOMAXPROCS=4 CGO_ENABLED=1 go test -race -p 2 . ./internal/tsfrontend ./internal/cldrdata ./internal/tzdata ./internal/unicode17 -run 'TestTypeScript.*(Parallel|Concurr|Isolation)|Test.*(Atomics|SharedBuffer)|TestContentBasedAnalysisReuse|TestAnalysisReuseCapturesCurrentResolver|TestIndependentLocaleReaders|TestCanonicalLocaleIdentifiers|TestEveryPrimaryZoneLoadsFromEmbeddedData|TestCurrentIANA2026eTransitions|TestPinnedCategories' -count=1
```

The complete profile includes modern RegExp, all ten mandatory Intl constructors,
resizable and shared buffers, half precision, weak references, managed cleanup
and resource disposal. Locale availability, collation tables, segmentation and
calendar/timezone policies are specified in `typescript-intl.md`;
`typescript-regexp.md` records the regular-expression data and semantics.
Temporal from the optional `esnext` profile has no runtime implementation;
requesting its standard global prevents compilation before any user effect.
DOM, Node, networking, filesystem, process and timers are application capabilities,
with no automatic bindings.

Asset licenses remain alongside their manifests. The exact notices of all twelve
Go module dependencies are preserved in
[third-party notices](typescript-third-party-notices.md), with byte digests in
`typescript-dependency-notices.json`.
