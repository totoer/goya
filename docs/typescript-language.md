# Language configuration and examples

The checked frontend is pinned to TypeScript 7.0.2. The default runtime profile
uses the unmodified ES2025 language declarations.
The compiler accepts native compilerOptions names, validates them with the pinned
frontend and reports removed or invalid settings through structured diagnostics.

| Setting | Interpreter behavior |
| --- | --- |
| `strict` | Defaults to true. Explicit type-system settings are honored; the checking stage always runs. |
| `target` | Defaults to ES2025. Requested target participates in checking and normalization. ESNext proposals requiring execution support are normalized for the VM. |
| `module`, `moduleResolution` | Default to ESNext/bundler. Native rules resolve the supplied virtual catalog. ESM and CommonJS formats, including preserve and NodeNext interop, use the internal linker. No Node globals are installed. |
| `useDefineForClassFields` | Controls field definition versus assignment semantics. Original profile defaults are retained during internal async normalization. |
| `preserveConstEnums`, `isolatedModules` | Control const-enum preservation/inlining according to native checking and transforms. |
| `experimentalDecorators` | Selects legacy decorators. With the default setting, standard decorators use context/addInitializer semantics. |
| `emitDecoratorMetadata` | Uses native serialization decisions with legacy decorators. A metadata hook must be supplied explicitly; no external reflection package is loaded. |
| `jsx: react` | Calls the configured classic `jsxFactory` and optional `jsxFragmentFactory`. |
| `jsx: preserve` or `react-native` | JSX is checked in that mode, then normalized to the configured classic factory for execution. The interpreter produces no intermediate JSX/JS file. |
| `jsx: react-jsx` or `react-jsxdev` | Uses the explicitly supplied `jsxImportSource` runtime modules (`jsx`, `jsxs`, `Fragment` or `jsxDEV`). No React/DOM implementation is bundled. |
| `resolveJsonModule` | Enables JSON types/resolution; JSON source must be in the provided catalog. ESM requests use `type: "json"`. |
| `lib`, `types` | Default to ES2025 language declarations and no implicit external type packages. Additional declarations do not install runtime implementations. See the compatibility inventory before selecting optional libraries. |
| `noEmit`, output paths and source-map emission settings | Validated as native options; the embedded compiler writes no output files. VM positions refer to original TS sources. |
| `importHelpers`, `noEmitHelpers` | Native checking still applies. Executable helpers remain private Go operations; textual emission choices install no external helper runtime. |

Options that skip checking (`noCheck`, skip-lib checks, `noResolve`, `allowJs`,
`listFilesOnly`) are rejected when enabled. Native removed settings, including
`alwaysStrict: false` and `esModuleInterop: false`, remain errors. Suppression
directives and explicit any/assertions retain TypeScript's own meaning; they do
not bypass checks of actual values crossing registered Go API boundaries.

Run six complete checked examples with:

```sh
CGO_ENABLED=0 go run ./examples/typescript-language
```

The example executes an enum/namespace/generic parameter-property program,
standard method decorator, legacy metadata routed through a registered Go hook,
an async generator with top-level for-await, resource disposal and a registered
JSX factory. Every case runs Check and compilation before binding a fresh runtime.
The metadata hook is also checked for absence of compilation-time calls. All six
cases print true, followed by `metadata hooks: 1`.

Resource-management declarations in the pinned release are in
`lib: ["es2025", "esnext.disposable"]`. Their disposal primitives and using forms
and stack classes are implemented and covered by checked execution fixtures.
More detailed transform and ordering fixtures are in `typescript_*_test.go`.
