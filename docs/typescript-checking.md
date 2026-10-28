# Checking TypeScript from Go

The native TypeScript frontend is available through `goya.Check`. It checks a
virtual project and its static imports without constructing a runtime or
evaluating the script. Compile uses the same analysis before producing executable
code; this document describes the independent checking API.

```go
diagnostics, err := goya.Check(ctx, goya.Project{
    EntryPoints: []string{"main.ts"},
    Files: map[string]string{
        "main.ts": `const count: number = "hello";`,
    },
}, nil, nil)
if err != nil {
    // Resolver failures, cancellation, or another analysis operation failure.
    return err
}
if goya.HasTypeErrors(diagnostics) {
    // Present diagnostics to the caller and stop before execution.
}
```

The third argument is a frozen host `Environment`; nil exposes only the standard
language types and declarations supplied in the project. Build an environment
with `NewRegistry`, `Register`/`RegisterModule` and `Freeze`; frozen declarations
and runtime bindings are derived from the same descriptors.
The final argument is a `CompileOptions` map using TypeScript `compilerOptions`
names. Defaults select strict checking, the ES2025 language-library profile,
ESNext modules and bundler resolution. DOM and Node types are not added.

`noCheck`, `skipLibCheck`, `skipDefaultLibCheck`, `noResolve`, `allowJs` and `listFilesOnly`
cannot be enabled. Source-level TypeScript directives, explicit `any`, and type
assertions retain their upstream semantics. The analyzer emits no filesystem
outputs. A diagnostic of severity `error` indicates an invalid checked project;
type errors are returned in `diagnostics`, separately from `err`.

## Sources and resolution

`Files` is an in-memory catalog. `Resolver` optionally implements
`SourceResolver.Snapshot(context.Context)` and supplies an additional consistent
catalog. `ResolverFunc` adapts a Go function to that interface. It is called once
per analysis, before the frontend starts checking. Resolver failures are returned
as wrapped errors. The analyzer does not fall back to the operating system's
filesystem or package installation tools.

Names are slash-separated virtual paths; `main.ts` becomes `/main.ts`. Empty
names, reserved URI-like names, file/directory conflicts and duplicate canonical
names are rejected. Resolver and explicit files must not overlap. Entry points,
source contents, declarations and option values are captured for one immutable
analysis. A subsequent resolver change cannot alter that analysis. A new call
obtains a new snapshot. TypeScript module-resolution settings operate on this
virtual catalog, including explicitly supplied package metadata and normal `.js`
import specifiers that resolve to `.ts` sources.

Explicit entry-point order is preserved, including declaration ordering that
can affect overload resolution. When entry points are omitted, TS/TSX/MTS/CTS
sources in the catalog are roots in canonical path order.
Supplied declaration files are included for type analysis. When entry points are
provided, executable imports are discovered from those roots. Compiler assets
and standard declarations come from the embedded, pinned frontend.

`Project.ModuleRoots` adds source modules or exact host-module names to the
prepared set without selecting new startup roots. Its static and literal
dynamic dependency closure is checked in the same snapshot. Errors in an
unselected candidate still block compilation. Set explicit EntryPoints for lazy
plugins, since omitted entries retain the existing all-source root selection.
The new execution profile removes the standard eval declaration and reports
recognized intrinsic source constructors with goya 1114; ordinary TypeScript
strict rules and upstream diagnostic codes are unchanged.

`Project.ConfigFile` optionally selects a config by its virtual catalog name,
for example `/app/tsconfig.json`. The analyzer reads its `extends` and project
references from the same frozen catalog. Host `CompileOptions` override config
settings; missing settings use the defaults above. Explicit `EntryPoints`
override the selected config's roots; otherwise its `files`/`include` roots apply.
Supplied and environment declarations remain part of type analysis.

Project references can resolve declaration output names to original TypeScript
sources without generating build outputs. Referenced implementations are checked
as well as their exported signatures. Static/dynamic imports execute using the
original source module identities. Missing virtual configs produce diagnostics.
Leaving `ConfigFile` empty preserves the original behavior described above:
`Check` and `Compile` do not implicitly discover a catalog's tsconfig.

## Diagnostics

Each `Diagnostic` contains `Namespace`, `Code`, `Severity`, `Message`, an optional
source `File` and `Range`, and `Related` diagnostics. The `typescript` namespace
preserves upstream integer diagnostic codes. The `goya` namespace identifies
integration errors such as an unsupported checking configuration.

Source offsets are UTF-8 byte offsets. Line and column numbers are one-based;
columns count UTF-16 units, including surrogate pairs for supplementary Unicode
characters. Positions refer to original TypeScript source. Global and some
configuration errors have no source range.

## Run the example

With Go 1.27 or newer installed:

```sh
CGO_ENABLED=0 go run ./examples/typescript-check
```

The example resolves two modules from memory and reports diagnostic 2322 for an
incompatible imported value. It uses neither a runtime nor Node.js/external `tsc`.
`goya.TypeScriptVersion()` identifies the exact pinned language version.
The execution profile and compatibility evidence are recorded in
`typescript-compatibility.md` and `typescript-release.md`.
