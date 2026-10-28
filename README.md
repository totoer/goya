# goya — Native TypeScript interpreter in Go

goya is a TypeScript-only interpreter in Go, derived from [Goja](https://github.com/dop251/goja).
Its Go module is `github.com/totoer/goya`.
Parsing, type checking, compilation and execution run inside the Go process with
`CGO_ENABLED=0`. Runtime operation requires no Node.js, external `tsc`, child
process, network connection or compiler download. Go 1.27 or newer is required.

The pinned language version is TypeScript **7.0.2**, with an **ES2025** standard
environment. The checked compiler, runtime and embedding compatibility gates
have passed. Optional proposal libraries have separate availability; Temporal
from `esnext` is checked but cannot execute. See the
[compatibility inventory](docs/typescript-compatibility.md) and
[verification record](docs/typescript-release.md). This is a breaking replacement
of the former JavaScript API.

JavaScript input and the former JavaScript compilation API are not retained.
The execution profile has no built-in `eval` or function construction from
strings. Only the Go application compiles sources. `import()` selects prepared
modules; it never loads or compiles a new source during execution.

## Embed a typed Go API

Register an API once to provide both its TypeScript contract and implementation:

```go
import (
    "context"

    "github.com/totoer/goya"
)

ctx := context.Background()
registry := goya.NewRegistry()
if err := registry.Register("add", func(a, b float64) float64 {
    return a + b
}); err != nil {
    return err
}
environment, err := registry.Freeze(ctx)
if err != nil { return err }

project := goya.Project{Files: map[string]string{
    "/main.ts": `const answer: number = add(20, 22); answer;`,
}}
program, diagnostics, err := goya.Compile(ctx, project, environment, nil)
if err != nil { return err }
if program == nil { return &goya.CompilationError{Diagnostics: diagnostics} }

runtime := goya.New()
if err := environment.Bind(runtime); err != nil { return err }
value, err := runtime.RunProgram(program)
// value.ToInteger() == 42
```

`Check(ctx, project, environment, options)` exposes the same type analysis without
creating a VM or calling Go implementations. Compile always checks. Syntax,
type, declaration, configuration or backend errors produce diagnostics and no
Program. Before execution, RunProgram verifies the required host schemas; missing
or incompatible bindings reject startup before user initializers run.

`RunString` and `RunScript` also check TypeScript, using the bound Environment.
`MustCompile` takes the same arguments as Compile. The old Compile signature,
Parse, CompileAST and SetParserOptions APIs have been removed. See the
[embedding and migration guide](docs/typescript-embedding.md) for reflection,
explicit adapters, numeric conversion policies and diagnostics.

## Embedded language service

`lsp.NewSession` provides editor operations directly over a virtual `Project` and
frozen `Environment`. `lsp.Serve` exposes the same core through LSP on streams
supplied by the application, for VS Code, Neovim and other clients. The pure-Go
service handles incomplete TS/TSX without running the VM or an external tsserver;
the host owns applying edits and reporting subsequent document changes.

Editor compatibility is verified against typescript-language-server 6.0.2 for the
supported TypeScript/TSX profile. See the [SDK and LSP guide](docs/typescript-lsp.md),
[direct session example](examples/typescript-lsp-session/main.go),
[stream example](examples/typescript-lsp-serve/main.go), and
[validation record](docs/typescript-lsp-validation.md).

## Projects and external capabilities

Project paths are virtual identifiers. Supply TS/TSX files, declarations and
package metadata through Project.Files or an application SourceResolver. The
engine never searches the operating system filesystem for a module. Checked AST
lowers directly to VM operations, without generated JavaScript text or a second
JavaScript parser.

Static and dynamic imports, live bindings, module cycles, top-level await,
CommonJS interop, deferred imports and explicit JSON modules use the internal
linker. Supply extra computed-import targets through `Project.ModuleRoots`;
static and literal dynamic dependencies are included automatically. Registered
host modules share the typed binding contract. A Program can
be reused across compatible runtimes; each runtime owns its module state.

The default environment provides language intrinsics and no DOM, Node, console,
process, global require, filesystem, networking, load/readFile or timers. External
capabilities come from the Go application's registry. A JSX factory/runtime or
metadata hook is also supplied explicitly. See [language options](docs/typescript-language.md).
The [runtime guide](docs/typescript-runtime.md) covers ownership, promise jobs,
interruption and source-level exceptions.

## CLI

```sh
CGO_ENABLED=0 go build -o /tmp/goya-ts ./goya
printf 'const answer: number = 42; answer;\n' | /tmp/goya-ts -check -
printf 'const answer: number = 42; answer;\n' | /tmp/goya-ts -
/tmp/goya-ts -check -project-root ./project ./project/main.ts
/tmp/goya-ts -project-root ./project ./project/main.ts
```

For a file entry point, the Go CLI snapshots its containing directory, or the
explicit `-project-root`, into a virtual source catalog. It reads TypeScript and
JSON files, skips hidden subdirectories and symlinks, and exposes no file API to
scripts. Imports cannot leave the supplied catalog. Stdin is a single virtual
`/stdin.ts`. Checking errors print structured source locations and exit nonzero.
The CLI runs without implicit external registrations. A pending module promise
requires an application event loop and is reported as an error by this CLI.

Runnable embedding examples:

```sh
CGO_ENABLED=0 go run ./examples/typescript-check
CGO_ENABLED=0 go run ./examples/typescript-host
CGO_ENABLED=0 go run ./examples/typescript-language
CGO_ENABLED=0 go run ./examples/typescript-modules
CGO_ENABLED=0 go run ./examples/typescript-lifecycle
```

## Development and provenance

```sh
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go run ./internal/tsfrontend/cmd/inventory -check
```

The [backend guide](docs/typescript-backend.md) describes the checked-analysis
boundary, IR, module runtime and conformance fixtures. Historical VM tests retain
test-only helpers for original operation regressions; those helpers are absent
from production builds.

The official TypeScript Go sources, declarations and generated assets are pinned
with release provenance and checksums in
[manifest.json](internal/tsfrontend/manifest.json). Import/update instructions and
license notices are in [the frontend guide](internal/tsfrontend/README.md).
The Goja base is [MIT licensed](LICENSE); vendored TypeScript code retains its
[Apache 2.0 license](internal/tsfrontend/LICENSE.typescript) and upstream notices.
Intl data retains its [Unicode V3 license](internal/cldrdata/LICENSE), with
[CLDR provenance and generation instructions](docs/typescript-intl.md).
Embedded [IANA timezone data](internal/tzdata/manifest.json) retains the
[distribution notice](internal/tzdata/LICENSE) and [Apache license](internal/tzdata/LICENSE_APACHE).
Unicode 17 segmentation/category data retains its [Unicode V3 license](internal/unicode17/LICENSE);
pure-Go uax29 retains its [MIT license](internal/unicode17/LICENSE.uax29).
Other Go dependencies and their versions are recorded in go.mod and go.sum.

Dependency license texts and their checksums are retained in the
[third-party notices](docs/typescript-third-party-notices.md).
