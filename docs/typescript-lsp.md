# Embedded TypeScript language service

The embedded LSP change is in progress. Feature entries remain pending until
their complete parity coverage is recorded in the inventory. The reference is
`typescript-language-server` **6.0.2** with TypeScript **6.0.3**; the embedded
compiler remains the project's pinned TypeScript **7.0.2**. Language-version
differences do not excuse a missing editor operation.

## Public session ownership

`lsp.NewSession(ctx, lsp.SessionOptions{...})` creates an application-owned session
from `goya.Project`, a frozen `goya.Environment` and compiler options. It captures
the resolver once, copies files/options/entry points and never binds or executes
host functions. `Snapshot` returns detached inputs suitable for `goya.Check` or
`goya.Compile`; snapshot mutations cannot change session state.

The application calls `Close`, which is idempotent and releases that session's
state. The parent context controls session lifetime. Closing one session does
not close other sessions or any application streams. Direct errors support
`errors.As` with `*lsp.Error`, stable `ErrorCode` values and `errors.Is` for
`lsp.ErrClosed`/`lsp.ErrContentModified`; resolver failures retain their cause.
Public editor DTOs live in `lsp/protocol`, without upstream AST/checker types.

`OpenDocument`, `ChangeDocument`, `SaveDocument` and `CloseDocument` take canonical
virtual names such as `/main.ts`. Changes use `protocol.ContentChange` with
zero-based UTF-16 ranges, applied sequentially and atomically. Versions must
increase; invalid notifications leave both text and version unchanged.

`UpdateProject` explicitly captures a new base catalog, calling its resolver once.
Open overlays take precedence over that catalog; closing a document reveals the
latest base version. Save records the event without writing or changing the host
base. Supply the accepted saved contents through `UpdateProject`. Source reads
and snapshot queries never invoke the resolver again.

`ProjectInfo(ctx, virtualName)` reports the selected configured/inferred project,
its non-library source files and configuration diagnostics. The native project
system discovers `tsconfig.json`, `extends`, project references and package
metadata exclusively in the supplied catalog. A file's own configured project
is discovered even when an importing project already includes that file through
a project-reference redirect. Missing virtual configuration files produce
TypeScript diagnostics rather than an operating-system lookup.

## Document identity and positions

`SessionOptions.DocumentURIs` maps canonical virtual names to client URIs. Use it
for custom schemes, Windows drive paths and UNC file URIs; the compiler catalog
continues to use slash-separated virtual names. Unmapped sources use escaped
`file:///...` URIs. `DocumentURI` and `DocumentName` provide both directions.
Mappings are detached before resolver callbacks. Equivalent percent encodings
cannot assign one URI to two sources, including collisions with default file
URIs. Unknown custom/Windows URIs are rejected instead of read from the OS.

`UpdateDocumentURIs` atomically replaces mappings after host-side changes and
invalidates old results without renaming any source. `ReadDocument(ctx, uri)`
returns a `VirtualDocument` containing URI, virtual name,
text, generation, optional open-document version and `ReadOnly`. Host declarations
use the reserved `goya-host:` scheme; compiler libraries use `goya-lib:`. These
schemes cannot be reassigned by mappings. The wire request is
`goya/readDocument` with `{ "uri": "..." }` and the same result DTO. Reading
declarations requires no disk writes, resolver calls, binding or execution.

Public editor ranges use zero-based UTF-16 coordinates. `OffsetAt` converts them
to UTF-8 byte offsets; `PositionAt` converts original byte boundaries back. Emoji
consume two UTF-16 units. CRLF, LF and CR separate lines; U+2028/U+2029 remain
ordinary UTF-16 source units under the agreed coordinate exception. A position beyond a line's text is clamped to its end; a position
inside a surrogate pair or an offset inside a UTF-8 rune is rejected. Compiler
diagnostics retain their existing one-based line/column and UTF-8 offset contract;
subtract one from line and column when adapting them to editor positions.

## Deferred results and proposed edits

`ResultHandle` is opaque and bound to a session generation. Source, config,
environment and option changes invalidate all deferred metadata. An old handle
returns `ErrContentModified`; an evicted handle returns `ErrResultExpired`.
Handles from another session are rejected. `SessionOptions.ResultCacheLimits`
bounds retained detached result metadata (default 64 entries / 4 MiB of accounted
metadata). Either zero disables retention; oversized results immediately expire.
The cache never retains AST/checker/program pointers.

Editor actions will return `BoundEdits` with an opaque handle and a detached
`protocol.WorkspaceEdit`. `ValidateEdits` checks the proposal immediately before
the host applies it. Open-document edits carry the captured version. Altering a
proposal, using it after invalidation or editing frozen host/library declarations
is rejected. Workspace edits support ordered text changes and create/rename/delete
resource operations; computing or validating one never mutates files or session
text. Report accepted changes through the ordinary host/document operations.
Feature-specific resolve/action methods remain pending in the parity inventory.

## Cancellation and request ownership

Each editor request holds immutable generation inputs and a native checker lease.
State updates do not wait for a running checker; a newer generation uses a separate
workspace. Operations on one generation serialize through a cancellable gate.
Request cancellation releases queued work without damaging subsequent requests.
Application resolver callbacks run outside the state lock and may read/update the
session. Native background work uses the session lifetime, independently of a
single request's context.

`MaxConcurrentRequests` defaults to 8 (configurable 1–128). It bounds simultaneously
held checker generations; callers waiting for admission can cancel. `MaxSourceBytes`
defaults to 16 MiB per captured base/effective catalog, counting source names/text
and host declarations, excluding immutable compiler assets. Oversized updates
are rejected atomically. Retired workspaces are released after their last request;
the session retains at most one idle current workspace.

`Events()` returns a bounded stream of `EventInvalidated` values (generation plus
kind). `EventBuffer` defaults to 32 (maximum 4096). When a consumer falls behind,
older invalidations coalesce and the latest generation remains observable. Updates
never wait for event consumers. `WatchDiagnostics` provides independent diagnostic
subscriptions; it does not consume this invalidation stream.

`Close` cancels active/queued requests before waiting for state, waits for active
leases and native background tasks, clears retained metadata and closes events.
Concurrent/repeated calls wait for the same shutdown. It does not close application
streams or other sessions. The reproducible native adaptation also fixes work
registration racing with background-queue shutdown.

## Host-supplied LSP transport

`lsp.Serve(ctx, reader, writer, lsp.ServerOptions{Session: session})` handles
Content-Length framing and initialize/initialized/shutdown/exit. The session and
streams remain owned by the application. Omitting `Session` constructs a
connection-owned session from `ServerOptions.SessionOptions` and closes it when
Serve returns. A shutdown response contains JSON null; exit returns to the host,
with `ErrExitWithoutShutdown` for an ungraceful protocol exit. Neither path exits
the process or closes application IO.

Framing accepts fragmented/multiple messages and preserves string and integer
request IDs without floating-point conversion. Headers are bounded to 16 KiB and
bodies to `MaxFrameBytes` (default 16 MiB); malformed/truncated framing returns a
`ProtocolError`. Initialize advertises UTF-16 and only the operations currently
implemented by the public wire facade.

For cancellation during blocked IO, provide readers/writers implementing both
the ordinary IO interface and `ContextReader.ReadContext` or
`ContextWriter.WriteContext`. These calls receive Serve's context. With ordinary
blocking IO, cancellation is checked between operations and the application must
unblock its own outstanding read/write when ending the connection. Serve creates
no hidden goroutine to abandon a blocked read and never closes foreign resources
to interrupt IO. Optional logs go only to the explicitly supplied `Logger`.

The wire facade advertises incremental document sync and dispatches
`textDocument/didOpen`, `didChange`, `didSave` and `didClose` to the same public
session methods. Multiple changes are sequential/atomic, stale versions are
rejected and optional save text does not replace the host base. Synchronization
methods sent as requests are rejected before mutation. Invalid notifications are
reported through the supplied logger.

`RefreshProject` explicitly reuses the last detached project descriptor and calls
its host resolver once. `NotifyFilesChanged` uses that operation for validated
file events; the wire facade accepts watched/create/rename/delete notifications.
The resolver supplies accepted new contents. With no resolver, file events return
an explicit error: the application supplies a new catalog through `UpdateProject`.
Neither direct nor wire events load OS paths or perform file/resource operations.
Open overlays retain precedence. A slow refresh superseded by a newer base update
returns `ErrContentModified` without replacing that base.

Request dispatch tracks exact IDs, supports `$/cancelRequest`, bounds active work
and separately bounds completed responses waiting for IO. Connection limits use
`ServerOptions.MaxConcurrentRequests` (default 8) and `MaxClientRequests` (default
16), each configurable from 1 to 128. Overload returns an explicit error instead
of starting unbounded work. Shutdown cancels/waits for active request workers.
The reader continues accepting responses to server-initiated client requests.
Cancellation releases pending-client state; late responses are ignored/logged.

Errors map consistently: invalid parameters to `-32602`, unknown methods to
`-32601`, cancellation to `-32800`, and stale/expired results to `-32801`. Invalid
JSON in a correctly framed body receives a parse error and the next frame remains
usable; broken framing returns `ProtocolError`. UTF-8/Content-Type are validated.
`ResponseError` preserves errors returned by a connected client. All optional
application logs use `Logger` and never raw protocol output.

`ClientHooks` supplies typed apply-edit and message-selection callbacks with
per-interaction `ClientCapabilities`. `Session.ApplyEdits` validates a
`BoundEdits` proposal, adapts text-only edits for clients without
`documentChanges`, checks resource/annotation capabilities and invokes the hook
outside session locks/checker leases. Unsupported resource operations fail before
calling the client; they are never silently removed. Rejection is represented by
`ApplyWorkspaceEditResult.Applied == false`; cancellation remains a context error.

The wire adapter derives capabilities from initialize and implements these hooks
using `workspace/applyEdit` / `window/showMessageRequest`. Client replies and null
selections are preserved. Even an accepted edit does not change the server catalog:
the application/client must report accepted contents with document/host events.
Callbacks may make those updates directly, without deadlocking session state.

## Configured snapshots

`Project.ConfigFile` explicitly selects a virtual configuration, including custom
config names. `ProjectSnapshot(ctx, virtualName)` exports the selected configured
project with this field, current overlays, host options and frozen environment;
pass those inputs to `goya.Check` or `goya.Compile`. `Snapshot` instead preserves
the config selection originally supplied by the host. An empty `ConfigFile` in
`Check`/`Compile` keeps the original entry-point behavior without config discovery.

Session project descriptors and exported snapshots preserve detached
`Project.ModuleRoots` alongside startup entry points. Pass the exported roots
through to Check/Compile when a computed import selects a prepared plugin,
including referenced sources whose output files do not exist. Editor analysis
does not execute or load modules into a Runtime.

The editor uses the same execution-profile library overlay as compilation:
eval is absent, and direct intrinsic source constructors produce goya 1114 in
both direct diagnostics and LSP reports. Editor diagnostic filters do not weaken
Check/Compile or the runtime constructor guards. Object methods named eval and
user functions named Function follow normal TypeScript rules.

Host compiler options override config options; absent settings use the shared
strict/ES2025/ESNext/bundler defaults without DOM or Node types. Explicit host
entry points override the selected config's roots. Referenced projects retain
their own roots and resolve unbuilt declaration outputs to their original TS
sources. Compilation checks referenced implementations before execution and uses
original source identities for static and dynamic imports. Configs, inherited
configs, references and package metadata are read exclusively from the catalog.
Referenced implementation documents select their own configured checker even
when the host explicitly selected an importing root config. Their effective
snapshots use the referenced config's roots, including custom config names;
explicit entry points from the importing project are not copied into them.

`UpdateEnvironment` and `UpdateCompilerOptions` advance the session generation.
The next request uses the replacement environment/options; older detached
snapshots retain their original inputs. Environment declarations are included
even when a config has an explicit `files` list. Compilation cache identity
includes the complete captured config catalog and environment schema identity.

## Direct editor operations and examples

`Hover(ctx, name, position, options)` is available directly and as
`textDocument/hover`. Its `HoverResult` carries generation, optional open-document
version and a public `protocol.Hover`. It uses the original UTF-16 range and
Markdown/JSDoc from the pinned language service. `HoverOptions.SupportsVerbosity`
enables the reference `canIncreaseVerbosityLevel` extension and `VerbosityLevel`;
wire clients opt in with initialization option `supportsHoverVerbosity`.
`EditorOptions.Preferences.MaximumHoverLength` (wire preference
`maximumHoverLength`) controls truncation; the default is 500. Verbosity levels
expand aliases recursively, with negative levels clamped to zero. Documentation
preserves JSDoc tags, examples, parameter descriptions and Markdown links;
declaration links use the request's virtual URI mapping, including host/library
resources.
Requests capture inputs before asynchronous dispatch, so an intervening edit
cannot substitute a new program into an older request.

`SignatureHelp(ctx, name, position, options)` and `textDocument/signatureHelp`
return the same `protocol.SignatureHelp`: overloads, parameter documentation,
active signature and active parameter. `SignatureHelpResult` also carries the
captured generation and optional document version. When no call applies, help
is nil. Optional `SignatureHelpRequestOptions.Context` supports invoked,
character and content-change triggers. On retrigger, a previous selected
signature is retained when its label is still present. The context and result
are detached DTOs. As in TLS, labels are strings and documentation is Markdown,
including when a client advertises offset labels or plaintext documentation.
The advertised trigger characters are `(`, `,`, `<`; the retrigger is `)`.
These operations inspect frozen Go contracts without binding or calling Go.

`Definition`, `TypeDefinition`, `Implementation` and `SourceDefinition` take
`(ctx, name, position)` and return a `NavigationResult` with the captured
generation, optional request-document version and detached `Targets` location
links. `Locations()` returns a separate slice of target selection locations.
No matching symbol produces an empty list. Targets preserve original UTF-16
positions, including declaration-map redirects and referenced project sources.
Implementation searches the active program, including source redirects, as TLS
does. Source definition excludes ambient declarations and unresolved aliases.

The wire methods are `textDocument/definition`, `textDocument/typeDefinition`
and `textDocument/implementation`. Definition honors `definition.linkSupport`;
the other two return ordinary locations, following the reference server even
when their clients advertise link support. Source definition uses
`workspace/executeCommand` with command `_typescript.goToSourceDefinition` and
arguments `[documentURI, position]`; that compatibility command requires an open
document. Direct queries can also inspect closed catalog files.

Host and standard-library targets use read-only `goya-host` / `goya-lib` URIs.
Use `ReadDocument` or `goya/readDocument` to read them in memory. Target data
contains no compiler objects, and navigation never binds or executes Go.

`References(ctx, name, position, options)` returns detached locations from related
projects with generation and optional document version. Nil `ReferenceOptions`
includes declarations. An explicit `IncludeDeclaration: false` follows TLS's
definition identity: searches started at a use can retain declarations, while
searches started at a declaration distinguish that symbol from imported aliases.
The same behavior is exposed by `textDocument/references` and its required
`context.includeDeclaration` field. Open overlays participate in every related
project; closing an overlay restores the base source.

`DocumentHighlights(ctx, name, position)` / `textDocument/documentHighlight`
return spans in the requested document. The highlight kinds match TLS v6.0.2:
semantic spans, including writes and declarations, use Read; syntactic spans use
Text except a combined `else if` span, which uses Read. Matching JSX tag pairs
select the tag names. These choices preserve the reference presentation rather
than exposing a separate access classifier. Results carry the captured generation
and optional document version and remain independent of caller mutations.

`DocumentSymbols(ctx, name)` returns an owned hierarchical outline with captured
generation, optional version and document URI. It also supports closed catalog
files and frozen declarations. `DocumentSymbolResult.Flat()` produces a detached
depth-first fallback with immediate container names. The wire method
`textDocument/documentSymbol` uses the client's
`hierarchicalDocumentSymbolSupport` capability and requires an open buffer.

`WorkspaceSymbols(ctx, query)` / `workspace/symbol` search the captured virtual
project tree, including closed files in configured and referenced projects.
Results use TLS's declaration ranges, symbol kinds, English UI ordering and
TypeScript 6 pattern matching (prefixes, word boundaries, camel case and dotted
containers). A host-selected config controls the search even when an unrelated
catalog file sorts first. Overlay updates and close events produce new generations;
old read-only results continue to describe their captured source and URI mapping.
No symbol query retains ASTs or executes Go bindings.

`FoldingRanges(ctx, name)`, `SelectionRanges(ctx, name, positions)` and
`LinkedEditingRanges(ctx, name, position)` return captured generation/version
metadata and detached range DTOs. The SDK accepts closed catalog files. Selection
preserves the input order and returns an owned parent chain for each position;
linked editing returns nil when no matching JSX tag pair is available.

The corresponding wire methods are `textDocument/foldingRange`,
`textDocument/selectionRange` and `textDocument/linkedEditingRange`. As in TLS,
selection/linked editing return null for closed buffers; folding reports a
request error. Folding is line based, independent of optional client range limits
or kind filters, with TLS's closing-brace adjustment and end-region comment filter.
Selection uses the pinned TypeScript 6 smart-selection grouping, including imports,
parameters, mapped types, template expressions and multi-line bookends. EOF uses
the stable UTF-16 end of the original source: a final newline places EOF at the
start of the following line. This agreed parity exception avoids reproducing
TLS ScriptVersionCache history. Input positions
still follow the facade's UTF-16 validation and line-end clamping rules.
All operations inspect unsaved text and never edit documents.

`PrepareCallHierarchy(ctx, name, position)` returns captured metadata and callable
items. `IncomingCalls(ctx, item)` and `OutgoingCalls(ctx, item)` return callers or
callees with source ranges; nested items can be passed to subsequent calls. The
SDK accepts closed catalog files; wire prepare requires an open document and
returns null otherwise. Unknown symbols have no prepared items; an eligible item
without calls has an empty call list. Incoming searches follow the selected
project, matching TLS. Source-file items use the virtual file name and directory.

`CallHierarchyItem.Data` identifies a bounded, generation-bound batch. Keep this
opaque data when passing an item back. Editing presentation fields cannot redirect
a query. Source/configuration/environment changes invalidate old items, foreign
session handles fail, and evicted batches report an expired result. Returned DTOs
are owned by the caller; retained targets contain only names and positions, with
no ASTs. Queries support cancellation and never invoke Go bindings.

`CodeLenses(ctx, name)` returns owned, generation/version-bound lenses for
closed or open catalog documents. `ResolveCodeLens(ctx, lens)` returns the
resolved lens and a typed `NavigationContinuation` with URI, position and target
locations. Applications can use these targets directly. Resolution never applies
edits or invokes an editor callback or Go binding. Keep `lens.Data` opaque;
stale, foreign and evicted handles fail explicitly. Presentation mutations cannot
change the retained target. One bounded metadata batch covers an enumeration.

Lenses are disabled by default. Host options are
`EditorOptions.ReferencesCodeLens` (`Enabled`, `ShowOnAllFunctions`) and
`EditorOptions.ImplementationsCodeLens` (`Enabled`), each an optional settings
block with optional booleans. Dynamic settings use
`typescript.referencesCodeLens.enabled`,
`typescript.referencesCodeLens.showOnAllFunctions` and
`typescript.implementationsCodeLens.enabled`. `Configure` clones and replaces
these workspace blocks; an omitted block restores the disabled defaults.
Reference visibility follows TLS exports/classes/members, with an explicit
option for local functions. Implementation lenses cover interfaces and abstract
classes/members. Implementations precede references; overloads share a lens.

Wire `textDocument/codeLens` requires an open buffer and a
`textDocument.codeLens` capability. No interactive editor extension is required.
`codeLens/resolve` returns `editor.action.showReferences` plus its URI, position
and locations, or an empty command with the same arguments when no targets exist.
Refresh is gated by `workspace.codeLens.refreshSupport`; inlay and lens refresh
requests are coalesced and serialized, including with a single client request
slot. Shutdown cancels unanswered refresh requests.

`FormatDocument(ctx, name, options)` and `FormatRange(ctx, name, range, options)`
share `Formatting(ctx, name, optionalRange, options)`. They return captured
generation/version metadata, owned `TextEdits` and a version-bound `BoundEdits`
proposal when changes exist. They accept closed catalog files. Validate proposals
before applying them to a detached copy or delegating application to the host;
formatting never changes the session or filesystem. Read-only host/library edits
are rejected. Cancellation and source/configuration changes invalidate deferred
editing work.

`protocol.FormatCodeSettings` owns all 27 fields from pinned TypeScript 6.0.3.
Set workspace defaults through `EditorOptions.Format` or dynamic
`typescript.format`; request `protocol.FormattingOptions` can override individual
fields. Request tab size and space conversion supply defaults only when explicit
`indentSize`/`convertTabsToSpaces` are absent. Newline defaults to LF. Optional
booleans preserve explicit false values. `IndentStyle` accepts None/Block/Smart;
the pinned formatter produces identical whole-document results for those three
variants, which are recorded separately. Semicolons support ignore/insert/remove.
Standard final-newline options have the pinned TLS behavior; they do not add a
separate postprocessing pass. Formatting sizes must be nonnegative, tab size must
be positive, and sizes above 65536 report a request-budget error.

A typed `SessionOptions.FormattingOptionsProvider` can return optional document
`TabSize`/`InsertSpaces` values when an SDK operation needs document defaults.
Return nil when unavailable. Callbacks run outside state locks and checker
leases, can reenter the session, and must honor cancellation. Explicit formatting
requests already contain their options and bypass this provider.

With `workspace.configuration` support, completion/resolve, hover and rename
obtain the same document defaults via `workspace/configuration` with
`{scopeUri: URI, section: "formattingOptions"}`. Only tab size and insert-spaces
fields are consumed. Per-connection results are cached by URI, bounded to 256
entries, and applied to a request-local language-service value. Matching TLS,
these cached document defaults survive close/reopen and ordinary configuration
notifications; workspace format settings themselves always follow the current
session generation. Without a provider/configuration result, workspace settings
and language defaults apply. Client errors and cancellation propagate.

Wire `textDocument/formatting` requires an open buffer and returns the TLS
request error for a closed document; `textDocument/rangeFormatting` returns an
empty list for a closed buffer. Both return exact ordered text edits over the
original source. Golden fixtures cover each format field's explicit variants,
tabs/spaces, CRLF, TSX, selection ranges and per-document completion snippets.
The inventory also indexes the two valid format fields omitted from the TLS
configuration guide directly from the pinned TypeScript interface.

`SemanticTokens(ctx, name, range)` returns generation/version metadata and
`Tokens.Data`, encoded as relative groups of five unsigned integers: line delta,
UTF-16 character delta, UTF-16 length, type index and modifier mask. A nil range
requests the full document; an optional range is copied before execution. The
SDK also accepts closed catalog files. `SemanticTokensLegend()` returns an owned
copy of the fixed TLS 6.0.2 legend; its final type is `member` and its modifiers
are declaration/static/async/readonly/defaultLibrary/local.

The wire methods are `textDocument/semanticTokens/full` and
`textDocument/semanticTokens/range`. Closed buffers and token-free documents
produce `{ "data": [] }`. As in TLS, optional client type/modifier filters do not
change the fixed legend or its data indices. New overlays are classified in
their own source generation, and the wire adapter cancels superseded token
responses. Tokens never mutate sources or execute Go bindings.

`InlayHints(ctx, name, range)` returns generation/version metadata and owned
`protocol.InlayHint` DTOs. Labels are either strings or arrays of label parts;
interactive parts can contain source locations, including frozen Go and bundled
library targets readable through `ReadDocument`. Enum-value hints have no kind,
and type/parameter padding follows TLS. Hints contain no edits or resolve handles
and never bind or execute Go.

`EditorPreferences` exposes all eight `includeInlay…` settings plus
`interactiveInlayHints` (true by default). Hint families default to disabled;
parameter-name modes are `none`, `literals` and `all`. The two matching-name
settings control parameter/variable suppression. Interactive labels preserve
reference part boundaries and links; plain labels use the compiler's single-line
printer, including tuple spacing and object-member semicolons.

Dynamic `WorkspaceConfiguration.TypeScript.InlayHints` uses optional
`InlayHintSettings` overrides over initialization preferences. `Configure` replaces
workspace settings: omitting that block clears its overrides and restores the
initialization values. Both configuration and returned label/location DTOs are
copied. The wire method is `textDocument/inlayHint`; a closed buffer produces a
reference error message and an empty array. Clients advertising
`workspace.inlayHint.refreshSupport` receive a refresh request when that workspace
block changes. Refreshes coalesce to one pending request, leave the protocol
reader available and cancel on shutdown if the client does not answer.

`PrepareRename(ctx, name, position)` returns the rename span and captured
metadata, or a typed `RenameUnavailableError` with the compiler's reason.
`Rename(ctx, name, position, newName)` returns an optional `BoundEdits` proposal;
an ineligible symbol produces no proposal. Use `ValidateEdits` immediately before
applying a proposal, report accepted text changes as document events, and use
`Check` on a detached catalog when the host needs to validate the result. No
rename operation mutates sources or writes files. Stale proposals are rejected.
The SDK can query closed catalog files; the compatible wire methods
`textDocument/prepareRename` and `textDocument/rename` require open buffers.

`providePrefixAndSuffixTextForRename` defaults to true: a local import rename can
insert `value as next`, and shorthand properties preserve their external names.
False follows the reference's broader symbol rename behavior.
`allowRenameOfImportPath` defaults to true and controls module-specifier spans;
these are text proposals only. File/directory operations are separate host actions.
Frozen environment and bundled library declarations cannot be edited; any
proposal touching a frozen target fails in full with `ErrReadOnly`. Preparation
checks the same boundary. Standard-library symbols also retain the compiler's
rename eligibility checks.

As in TLS v6.0.2, new names are substituted verbatim, including empty names,
reserved words and invalid identifiers. Such proposals need not pass `Check`.
Prepare-rename failures expose the reference JSON-RPC error message, while an
ordinary rename of an ineligible element returns null.

`Diagnostics(ctx, name, options)` returns syntax, semantic and suggestion
diagnostics with generation, URI and optional document version. Numeric codes,
messages and original ranges are preserved; source is `typescript`, with related
locations and unnecessary/deprecated tags. `DiagnosticOptions.Capabilities` can
limit optional metadata; nil requests full metadata. The wire facade also offers
standard `textDocument/diagnostic` full reports.

`WatchDiagnostics(ctx, options)` checks open TypeScript documents whenever their
effective generation changes. Its `Events()` channel carries typed result, clear,
progress (begin/end, including cancellation), and error events. Results carry the
generation and open-document version. Consume the channel until cancellation or
call `Close`; session shutdown cancels and waits for subscriptions too. A slow
consumer backpressures this channel without blocking document updates. Each
subscription has at most two checking jobs, coalesces source invalidations, and
discards superseded results. `MaxDiagnosticSubscriptions` defaults to 8 (1–128).

After `initialized`, the wire facade publishes diagnostics automatically, including
for clients that omit optional publish-diagnostics capabilities. Version numbers,
related information and tags follow the client's capabilities. Closing a document
or changing its URI clears its previous publication; eager-clear clears changed
documents before rechecking. A generation check after obtaining the protocol
writer prevents queued obsolete results from replacing a newer publication.
Clients advertising `window.workDoneProgress` receive optional checking progress;
a missing progress-create response does not stall diagnostics. `shutdown` stops
publication without closing an application-owned session.

`SessionOptions.EditorOptions` and `UpdateEditorOptions` contain editor-only
`DiagnosticSettings`: ignored codes, disabled suggestions/validation and eager
clear. Updates create a new generation and invalidate old handles. These values
are detached in snapshots and do not enter compiler options. Hiding an error in
the editor leaves `Check` diagnostics and the mandatory `Compile` gate intact.
`Configure` and wire `workspace/didChangeConfiguration` replace dynamic
`diagnostics.ignoredCodes` / `diagnostics.eagerClear` settings over defaults;
initialization `preferences.disableSuggestions` is also supported.

`Completion(ctx, name, position, options)` and `ResolveCompletion(ctx, item)` share
the same implementation as `textDocument/completion` / `completionItem/resolve`.
Their results carry generation and optional document version. The completion
result contains a `protocol.CompletionList`; resolution returns one typed `Item`.
`CompletionRequestOptions` supplies trigger context and optional
`CompletionCapabilities`; nil capabilities request the complete direct DTO.
Snippet, commit-character, label-detail and insert/replace presentation follows
the individual caller's capabilities.

Keep each item's opaque `Data` unchanged for resolution. One bounded retained
batch owns all items from a list. It contains detached DTO bytes, never ASTs or
compiler pointers. Resolution rejects foreign, stale, invalid and evicted tokens
and uses the originally issued item, independently of mutable client display
fields. Resolving a completion does not apply its edits or execute a Go binding.

`EditorOptions.Preferences` uses typed TypeScript preference names. Optional
booleans distinguish explicit false from defaults, including module/import
completions, optional-chain completions, class/object method snippets, insert text,
snippet text, label details and JSDoc return-template generation. JSX attribute
style and quote preference use the TypeScript string values. Wire initialization
accepts these in `initializationOptions.preferences`. Dynamic
`completions.completeFunctionCalls` is available through `Configure` and
`workspace/didChangeConfiguration`; resolved call snippets handle required,
optional and rest parameters and preserve an existing argument list.

Auto-import indexes use configured/inferred projects, open overlays, frozen host
modules and virtual package metadata. The captured open-file set is restored
before checking a generation, including other buffers that have not been queried.
Preparing an index preserves an explicitly selected or referenced config. Closing
an overlay or replacing `Environment` invalidates both the index and old tokens.

Resolved auto-import items carry the exported type/JSDoc and ordinary
`AdditionalTextEdits`. They can add/update imports, qualify an existing namespace
or promote a type-only import. An unfinished import uses one complete replacement
and can include a snippet. The host owns application and snippet expansion; apply
edits to its current text and report accepted contents through document events.
Completion is rejected for frozen host/library documents.

Auto-import preferences include module-specifier preference/ending, quote style,
type-only imports, excluded file globs and excluded specifier regexes.
`includePackageJsonAutoImports` accepts `auto`, `on` and `off`; already loaded
declarations remain available in all modes. Proactive indexing follows supplied
dependency metadata and `auto` has the reference limit of ten typed dependencies.
No package installation or filesystem fallback is involved. Completion lists use
the reference English UI ordering with numeric comparison; opaque data alone is
normalized in the shared fixtures, while item order, type text and edits remain
exact.

Specifier exclusions use ECMAScript patterns, including lookarounds,
backreferences and `/pattern/iu` notation. As in TypeScript, only `i` and `u`
flags in that notation affect matching; invalid patterns are ignored. The
compiler and editor share the pinned regular-expression grammar. Backtracking
matches have a 50 ms budget. Exhaustion aborts the whole request with
`ErrRequestLimit` (`request_limit`, wire error `-32803`); it does not return a
partial completion list. The process cache holds at most 1,000 compiled patterns
with at most 1 MiB of pattern text; larger patterns are not cached. Its entries
contain no editor snapshots or compiler objects.

The currently exposed preference variants are covered by the shared direct/wire
fixtures linked individually in `docs/typescript-lsp-parity.json`:

| Preference | Recorded variants |
| --- | --- |
| `includeCompletionsForModuleExports` | default enabled, disabled |
| `includeCompletionsForImportStatements` | default enabled, disabled; snippet/plain-text clients |
| `includeAutomaticOptionalChainCompletions` | default enabled, disabled |
| `includeCompletionsWithClassMemberSnippets` | default enabled, disabled |
| `includeCompletionsWithObjectLiteralMethodSnippets` | default enabled, disabled |
| `includeCompletionsWithInsertText` | default enabled, disabled for members, class/object snippets and TSX |
| `includeCompletionsWithSnippetText` | default enabled, disabled for class/object/import snippets and TSX |
| `useLabelDetailsInCompletionEntries` | default enabled, disabled; client fallback |
| `jsxAttributeCompletionStyle` | `auto`, `braces`, `none` |
| `quotePreference` | `auto`, `single`, `double`, including existing single-quoted imports |
| `importModuleSpecifierPreference` | `shortest`, `relative`, `non-relative`, `project-relative`, with path aliases |
| `importModuleSpecifierEnding` | `auto`, `minimal`, `index`, `js` |
| `includePackageJsonAutoImports` | `auto`, `on`, `off`; loaded declarations and proactive providers; dependency threshold |
| `preferTypeOnlyAutoImports` | default disabled, enabled |
| `autoImportFileExcludePatterns` | empty/default, matching glob |
| `autoImportSpecifierExcludeRegexes` | empty/default, plain patterns, `i`/`u`, lookahead/lookbehind, backreference, invalid patterns |
| `maximumHoverLength` | default 500, explicit 70 with truncation |
| `disableSuggestions` | default disabled, enabled; configured checking errors remain |
| `generateReturnInDocTemplate` | default enabled, disabled; reference completion has no whole-comment template in either case |

Dynamic `completions.completeFunctionCalls` also has required/optional/rest,
existing-call and minimal-client fixtures. `diagnostics.ignoredCodes` fixtures
preserve the compiler gate, and deterministic direct/wire stream tests cover
`diagnostics.eagerClear`. Public preferences require a recorded explicit variant
in both inventory evidence lists; the inventory test verifies those files are
accepted by the same runner used for direct and wire execution.

Run the direct completion example without an editor or external compiler:

```sh
go run ./examples/typescript-lsp-completion
```

It selects and resolves `append`, applies its completion/import edits to a
caller-owned buffer, reports version 2, queries signature help and diagnostics,
then compiles the same effective snapshot. Expected output:

```text
completion=append imports=1
signature=append(value: number): number diagnostics=0 compiled=true errors=false
```

Other editor operations remain pending in the inventory. A runnable direct API
example is:

```sh
CGO_ENABLED=0 go run ./examples/typescript-lsp-session
```

The example registers a typed `add` function, obtains an inferred config selection
from an in-memory catalog, exports snapshots for `Check`, edits an unsaved
dependency and refreshes the resolver base. The error remains while the overlay
is open, then disappears when close reveals the refreshed numeric value. It also
reads the host declaration through its reserved URI. Expected output includes:

```text
initial: generation=1 config=/workspace/tsconfig.json errors=false
overlay: generation=2 config=/workspace/tsconfig.json errors=true
refreshed base with overlay: generation=3 config=/workspace/tsconfig.json errors=true
closed overlay: generation=4 config=/workspace/tsconfig.json errors=false
hover available=true
host declaration: readOnly=true
resolver calls=2 host calls=0
```

The editor owns native programs independently of checked compilation
analyses. Member completion remains available in unfinished TS and TSX with
syntax errors. Those programs retain their diagnostics and cannot be passed to
the execution backend; `Compile` still requires a successful checking gate.

`examples/typescript-lsp-serve` passes memory buffers containing a complete
initialize/initialized/shutdown/exit transcript to `Serve` and decodes its replies.
It starts no process, editor extension or socket listener:

```sh
CGO_ENABLED=0 go run ./examples/typescript-lsp-serve
```

Its output reports `server=goya encoding=utf-16 hover=true` and `responses=2`.

## Offline compatibility inventory

`testdata/lsp/reference.json` records the pinned source interface index, source
file hashes, reference versions, commits and archive checksums.
`docs/typescript-lsp-parity.json` maps every reference entry to pending or verified
implementation, direct test and protocol test evidence. Source action providers,
quick-fix registrations and refactor registrations are tracked individually.
Only the agreed JavaScript/Node/tsserver infrastructure exclusions are recorded
as excluded. A missing native implementation is pending work.

```sh
CGO_ENABLED=0 go run ./internal/tsfrontend/cmd/lspparity -check
CGO_ENABLED=0 go test ./internal/tsfrontend/cmd/lspparity
```

The separate release gate requires every applicable entry to be verified; it
intentionally fails while implementation is incomplete:

```sh
CGO_ENABLED=0 go run ./internal/tsfrontend/cmd/lspparity -check -release
```

These commands read committed files and require neither Node.js nor downloaded
reference tools. Test evidence paths are relative to the repository; verified
entries require implementation, direct-call and protocol evidence. Passing the
inventory validator alone does not establish behavioral compatibility: the
referenced fixture suites must pass as well.

## Reproduce the source index

Obtain these source archives explicitly during development:

- TLS: `https://codeload.github.com/typescript-language-server/typescript-language-server/tar.gz/refs/tags/v6.0.2`
- TypeScript: `https://codeload.github.com/microsoft/TypeScript/tar.gz/refs/tags/v6.0.3`

Run generation into separate output paths and review the result. The generator
checks each archive SHA-256 against the pins before reading its source members,
retains per-file hashes and refuses to overwrite existing coverage. It does not
download packages or run external tools.

```sh
CGO_ENABLED=0 go run ./internal/tsfrontend/cmd/lspparity \
  -tls /path/to/tls-v6.0.2.tar.gz \
  -typescript /path/to/typescript-v6.0.3.tar.gz \
  -reference /tmp/lsp-reference.json -out /tmp/lsp-parity.json
```

The index includes all registered TLS connection handlers, editor commands and
notifications, declared source action kinds, initialization and dynamic settings,
default TypeScript user preferences, code-fix/refactor registrations and static
refactor kinds. Provider files are source-linked so applicability and generated
action variants can be checked when their behavioral fixtures are added.

## Recorded behavioral fixtures

`internal/lspfixture` loads strict JSON fixtures and replays ordered notifications
and calls against a supplied endpoint. `internal/lspfixture/embedded` provides
direct-call and in-memory wire adapters using only the public API. Both execute
every committed fixture with captured capabilities; hover additionally runs with
minimal and no-snippet/no-location-link capabilities. Cases with different output
use separately captured initialization variants. Project files, declarations,
selected config, entry points,
compiler options and optional initialization capabilities/options are explicit
inputs. `{{ROOT_URI}}` is the only placeholder;
result comparison preserves array order, ranges, edits and integer precision.
Fixtures with a supplied conventional config use its root set without a host
entry-point override, so the oracle and embedded service receive the same
project. Explicit/custom host config selection is also tested directly.

`testdata/lsp/hover-number-after-edit.json` was captured from the actual pinned
TLS/TypeScript packages. It opens a typed document, queries hover, replaces the
unsaved text and queries the changed type. The fixture records the SHA-256 of
both npm package archives. Runner unit tests exercise the recorded response tape,
ordering, mismatch detection, malformed fixtures and cancellation. The embedded
suite compares actual direct/wire hover results with this oracle before/after
the edit. `hover-verbosity-after-edit.json` was separately captured with
`supportsHoverVerbosity`, retaining its explicit false expansion flags. Passing
these fixtures does not establish completion of the remaining editor inventory.

`hover-alias-verbosity.json` checks recursive alias expansion at levels 0/1/2
and the expansion flag. JSDoc fixtures cover virtual declaration links, tags,
captions, defaults and author formatting. Signature fixtures cover overloaded
and variadic calls, type arguments, trigger suppression, retrigger selection,
empty parameter lists and client capability variants. The host overload fixture
is also replayed with an actual frozen `HostBinding`; its declarations must
exactly match the recorded oracle inputs, and both adapter and binding call
counts must stay zero.

Diagnostic fixtures cover host-call errors, ignored codes, syntax recovery,
suggestions, deprecated/unnecessary tags, related locations and Unicode/CRLF.
TLS 6 pushes diagnostics, while the embedding also supports pull reports:
`oracle_method: textDocument/publishDiagnostics` explicitly records that capture
adapter. It waits for the configured settling interval and wraps the entire
captured array in a full report without reordering or discarding any fields.
Ordinary tests do not wait or launch an oracle.

`diagnostics-related-tags.json` and `diagnostics-disable-suggestions.json` compare
editor reports to the exact TS 6 oracle, including the argument-error head and
related locations. The editor compatibility layer restores that presentation;
`goya.Check` retains the native TS 7.0.2 missing-property code 2741. Fixtures pin
that final compiler gate difference and its source evidence. A compiler upgrade
requires review; no editor result array or operation is replaced.

For an explicit development-only capture, first obtain and extract the pinned
TLS 6.0.2 and TypeScript 6.0.3 npm package archives into separate temporary
directories. No installation is performed by the capture script. Provide a
fixture template whose request steps omit `expected`; all other fields use the
committed fixture format. Run:

```sh
python3 internal/lspfixture/capture.py \
  --server /path/to/tls/package/lib/cli.mjs \
  --tsserver /path/to/typescript/package/lib/tsserver.js \
  --server-package /path/to/typescript-language-server-6.0.2.tgz \
  --typescript-package /path/to/typescript-6.0.3.tgz \
  --fixture /path/to/input.json --out /tmp/captured-fixture.json
```

The script checks package versions, uses a temporary workspace, disables
automatic typing acquisition, launches only the explicitly supplied development
oracle with one semantic server (`useSyntaxServer: never`, also used by TLS's
own tests), records full results and shuts it down. This avoids speculative
startup responses from a syntax server before external declarations are loaded.
It refuses to overwrite an
existing output. Review captured changes before copying them into `testdata/lsp`.
The temporary workspace uses a canonical physical path so package/declaration-map
realpaths share its `ROOT_URI` identity. The pinned oracle's standard-library URI
prefix is explicitly translated to `goya-lib:///`; target ranges, ordering and
all other fields are preserved. Tests that install an actual frozen Go provider
likewise translate only its known oracle declaration-file identities to their
reserved host URIs, then verify the same recorded targets and read their text.
Offline runner checks need no Node or oracle packages:

```sh
CGO_ENABLED=0 go test ./internal/lspfixture/...
```

Rename fixtures preserve exact `WorkspaceEdit.changes` URI keys and edit array
order. Fixture ROOT_URI expansion applies to object keys and values. A request
can record an exclusive `expected_error` JSON-RPC object; both adapters compare
its full code/message/data. The capture tool accepts an error only when the seed
explicitly contains `expected_error`. The rename Check test applies each valid
proposal independently to a detached captured catalog, including source overlays
and related configured projects. Invalid-name fixtures retain TLS's verbatim
substitution behavior.

Recorded range/hierarchy fixtures explicitly opt into `source_eof` for the agreed
EOF coordinate exception. Raw `expected` oracle responses and package provenance
remain unchanged. The runner replays fixture inputs to project only an exact
legacy EOF endpoint to the current source end; it does not normalize other
positions or actual results. Folding omits end characters, so its opt-in requires
an explicit oracle outlining text span, result index and source SHA-256, validated
against the current text. The incomplete TSX span was recorded with TypeScript
6.0.3 `OutliningElementsCollector.collectElements`. This is not an exemption from
implementing any editor feature.

LSP positions count UTF-16 units and use CR, LF or CRLF as line separators.
U+2028 and U+2029 remain ordinary source units. This explicitly agreed coordinate
exception avoids the mixed compiler/document line codecs of TLS 6.0.2. The
`unicode_source_coordinates` fixture opt-in preserves raw oracle responses and
records TS6 navigation-tree source spans plus a source SHA-256; only ranges and
navigation-command positions are projected from those spans. Other fields,
counts, ordering and text are compared unchanged. This profile rejects unrelated
methods, sources without these separators, mismatched hashes, invalid spans and
unrecorded target ranges.

Unicode coordinate metadata was captured from the pinned TypeScript 6.0.3
`LanguageService.getNavigationTree(file)`: each lens records its node's `text`,
`kind`, `kindModifiers`, first `spans` entry and optional `nameSpan` before any
conversion to protocol line/character. The SHA-256 covers the UTF-8 source text.
Recapturing changed source requires regenerating these spans; the loader rejects
stale hashes. The oracle output in `expected` is retained unchanged.

Recorded fixture transcripts keep a 45-second normal-test budget. Race builds
allow 90 seconds for both endpoints of one transcript; golden comparisons stay
exact. Run race packages with `-p 1` on hosts where concurrent instrumented native
checkers compete for memory. This timeout is a test harness allowance, not a
service performance guarantee.

Visual and formatting handlers have direct and in-memory wire evidence linked
individually in the parity inventory: fixed semantic-token legend/full/range,
all nine inlay preferences and workspace overrides, reference/implementation
lenses and all 27 formatting settings. Provider declarations alone never satisfy
the inventory. The Unicode coordinate projections retain original oracle data
and validate independent source evidence; formatter edits and completion snippets
otherwise remain byte-for-byte comparisons after URI/opaque-handle translation.

## Code actions and deferred fixes

`Session.CodeActions(ctx, name, range, context, options)` returns owned
`protocol.CodeAction` values plus a parallel `Proposals` slice of optional
`BoundEdits`. `Session.ResolveCodeAction` computes a deferred combined fix and
returns both its complete action and a separately owned, validated edit proposal.
Neither method applies edits or executes commands. Proposed text changes preserve
provider order, exact text and current open-document versions.

The common dispatch preserves `context.only` (including the distinction between
omitted and empty), `triggerKind`, preferred flags, disabled reasons, diagnostics
and client diagnostic codes using either the numeric or string LSP alternative.
As in TLS, quick fixes are considered independently of `context.only`. Refactor
and source-action providers are tracked separately in the inventory while their
implementation continues. A closed-document wire request returns an empty list;
the direct SDK also supports catalog documents without an editor overlay.

Actions following a document change compute current diagnostics before using a
possibly stale client list. Once diagnostics have been explicitly requested or
an action enumeration has completed, client-supplied diagnostics are preserved,
deduplicated by code/range with the last duplicate's payload. Formatting callbacks
execute outside locks and checker leases, including deferred combined fixes.
Basic wire diagnostics preserve related locations, matching TLS independently
of the optional related-information capability.

`CodeActionRequestOptions.Capabilities` controls lazy fix-all availability;
the direct API defaults to resolve support for edits. A wire resolver copies only
the properties advertised in `textDocument.codeAction.resolveSupport.properties`.
Disabled actions without a continuation remain unchanged. Issued opaque data
belongs to one session and generation and uses the bounded result store:
foreign/malformed, stale and evicted handles produce the existing typed errors.
Resolve uses retained metadata; changing client presentation fields cannot select
a different provider or operation. SDK callers obtain the complete owned result.

The fixtures preserve raw TLS command payloads, including one-based TypeScript
line/offset arguments. `{{ROOT_PATH}}` translates only the development oracle's
temporary project directory to the decoded file-URI root; `{{ROOT_URI}}` keeps its
existing URI role. `params_from_code_action` forwards the actual issued action
to resolve. Only server-private `data` identity is normalized; edit order, text,
diagnostics, command arguments, preferred/disabled fields and result counts
remain exact.

The quick-fix bridge also preserves multiple target files in the provider's
first-change order. The initial converted providers include const-to-let,
bigint literals, index-signature element access (including optional chains and
quote preferences), extends-interface conversion, and the native missing-interface
implementation provider. Their individual and combined proposals have recorded
direct/wire comparisons and are checked in copied catalogs. All 70 applicable
quick-fix registrations now have provider-level evidence; the two agreed
infrastructure exclusions remain explicit in the inventory.

Foreign const declarations follow the reference's caller-text token traversal.
Recorded cases retain an empty result, a caller-file proposal that leaves the
original assignment error, and a tokenization rejection with no partial edits.
Fix-all over two symbols in one const declaration likewise preserves the
reference's overlapping-change rejection. Dedicated direct/wire tests verify
those typed errors, continued operation and unchanged source catalogs.

`Session.ApplicableRefactors` enumerates owned provider/action groups with kind,
invocation and interactive-action filters. `Session.ApplyRefactor` selects an
issued group/action by index and handle; changing returned descriptions cannot
redirect that selection. Source, configuration and environment changes invalidate
the handle, and eviction produces `ErrResultExpired`. Disabled choices fail with
`CodeInvalidEdit`. `Session.RefactorEdits` instead explicitly computes a named
operation against the current snapshot. All three use the same refactor engine
as stream requests.

`RefactorEditResult` contains optional `BoundEdits` and a typed `RenameContinuation`
with a URI and UTF-16 position in the prospective changed document. The host
accepts/rejects edits and reports accepted document events before requesting a
subsequent rename. `CodeActionResult.Refactors` provides parallel owned selections
for deferred refactor commands. No method edits the session catalog itself.

Refactor transcripts retain reference command names and one-based file ranges.
`record_client_requests` checks explicit client-request methods and full payloads,
including `workspace/applyEdit`; a refusal returns without committing source
changes. Braces and arrow/function conversion transcripts cover kind-prefix
quirks, automatic/invoked selection, disabled reasons and multiple choices.
An inapplicable source operation has a separate raw oracle error artifact;
direct/wire checks reject its entire proposal and verify subsequent requests.

Extract-type choices preserve selected union/intersection constituents, captured
type parameters and the reference's interface applicability rules. Action ranges
in the SDK use stable zero-based LSP source positions. Alias/interface proposals
include a rename continuation in the changed text; recorded accepted and rejected
client flows compare the complete apply-edit and rename request payloads.
Fixture `initialization.client_responses` acknowledges specified requests without
applying source changes; only subsequent host/document events change the catalog.

Value/function extraction offers each applicable enclosing function, class,
namespace and file scope. Analysis preserves the reference's rules for control
flow, captured reads/writes, exposed declarations, type parameters, qualified
names and imported parameter types. Exact-text transcripts cover async/generator
wrappers, contextual function signatures, comments, TSX and list insertions.
Both extraction kinds return a rename continuation; SDK tests apply host changes
before renaming and verify stale selections, owned ranges and stable Unicode
coordinates. Some reference proposals still leave errors, including loss of a
literal contextual type or an out-of-scope value. Their complete remaining codes
are recorded after an independent check with the pinned TypeScript.

Further refactor transcripts cover inferred return annotations (including
overloads and type predicates), getter/setter generation, inline variables,
combined overload signatures, template strings and optional chains. The accessor
refactor reuses the same generator as accessor quick fixes; its continuation
retains parameter-property and quoted-name rename locations. Repeated concurrent
SDK proposals check stable printing and continuations over the borrowed program.

Recorded reference bugs remain explicit: inline across files can delete a caller
span and leave TS2304; optional-chain conversion of a parenthesized access can
leave TS1005; an untyped parameter remains TS7006 after a return annotation.
Copied-catalog checks retain those exact remaining codes, independently checked
with the pinned TypeScript. An anonymous static class accessor operation has a
separate reference error artifact and returns a typed rejection without edits.

Import/export conversions update consumers through the same virtual program:
named/default imports, namespace accesses, re-exports and import types. Named
imports needed by re-exports are retained; name conflicts use the reference's
file-level names. JSDoc `@import` refactors inside TypeScript preserve reference
proposals even when the resulting binding remains unresolved. The ordinary
compiler's removed-option gate remains unchanged for the explicitly recorded
`allowSyntheticDefaultImports=false` case.

Parameter conversion supports functions, const arrows/expressions, methods,
contextual method signatures and constructors. It rewrites validated calls,
preserves `this`, optional/default/rest parameters and comments, and returns no
proposal for unsupported reference groups such as callback/alias usages. Source
extra-argument failures reject the complete operation; a separate fixture retains
the reference's invalid bare-arrow result and the existing Check parser gate.

Move selections preserve overload groups and offer new-file or explicit target
operations. `RefactorArguments.TargetFile` uses a virtual catalog name; hosts can
predeclare URI mappings for prospective resources. The SDK returns `CreateFile`
and text edits as one generation-bound plan, and `ApplyEdits` requires a client
that accepts those resource operations. Acceptance alone does not change the
catalog. The host supplies the resulting files through ordinary update events.

For compatibility, `_typescript.applyRefactoring` projects the validated SDK plan
to the reference's legacy `changes` payload, including its implicit creation of
new documents. This projection does not create files or use operating-system IO.
Invalid or out-of-program targets preserve `NotApplicableReason` in the SDK;
the command returns the reference's protocol error without applying any subset.
A virtual config with a `files` array receives the reference's proposed addition.

Move transcripts compare imports/exports, consumer aliases, namespace references,
re-exports, prologues, comments and TSX/MTS/CTS/declaration files. Copied-catalog
checks explicitly record reference bugs, such as an out-of-scope alias, conflicting
namespace names or an extensionless import emitted into a new MTS file. Separate
raw oracle failures cover overlapping statement groups and require-call aliases;
typed rejections leave the catalog unchanged and allow subsequent requests.

EOF edit projections also cover recorded `workspace/applyEdit` payloads. Each
annotated client edit identifies its request, URI and edit index and carries an
independent pinned TypeScript span and source SHA-256. Raw oracle payloads stay
intact; text, order, labels and other fields remain exact. Invalid metadata and
non-EOF range changes are rejected by fixture validation.

Further converted quick fixes cover unknown conversions for non-overlapping
types (both assertion syntaxes), object property assignment, semicolon-to-comma
repairs, missing `new`, missing module `typeof`, and accidental getter calls.
Fix-all metadata follows each provider: a getter-call fix carries no combined
continuation. Their fixtures retain no-result cases, including an invalid array
form for which TLS offers no comma fix, and positive edits are checked separately.

Additional converted providers add async modifiers and Promise return annotations
(including contextually typed arrows), remove unnecessary await while preserving
constructor parentheses, add missing call parentheses, qualify instance/static/
private member references, and remove unused labels without losing multiline
indentation. Their individual and combined fixes have positive/negative fixtures
and detached-catalog checks. Private reference fixtures isolate the requested
expression range; the unused-declaration provider has its own complete
recorded variants and detached-catalog checks.

The agreed EOF projection also covers exact zero-width code-action insertions.
Its `source_eof.code_action_edits` records the action/document/edit indices,
target URI, independently captured TypeScript 6.0.3 UTF-16 source span and source
SHA-256, plus the corresponding command file index when present. Only an exact
legacy EOF endpoint may project to the canonical source end, in both WorkspaceEdit
and one-based quick-fix command coordinates. Text, order, diagnostics, preferred
status and every other field remain unchanged. The empty-export spans were
recorded directly with the pinned `getCodeFixesAtPosition` API; raw oracle results
remain in each fixture.

The source-derived inventory additionally includes the quick-fix manager's
`_typescript.applyCodeActionCommand` and `_typescript.applyFixAllCodeAction`,
the `_typescript.rename` client request and the `editor.action.showReferences`
client command. Client continuations are tracked separately from server commands.
Generation preserved all prior 279 reference rows and their evidence and added
only these four rows, for 283 total. Unimplemented command flows remain pending.

The converted type/JSX/scope providers also repair qualified type names using
indexed access, add `declare` to inherited properties, escape invalid JSX text
using either expression containers or HTML entities, add empty exports for
top-level await/await-using/for-await modules, and insert missing constructor super
calls. Editor newline and quoting settings apply to inserted code; shared native
helpers are checked against completion and formatting fixtures as well.

Override fixes preserve the shared `fixOverrideModifier` name while selecting
the add/remove fix ID, compiler diagnostic group and per-diagnostic descriptions.
Fixtures cover visibility/static/abstract modifiers, accessors, constructor
parameter properties, decorators, mixed fix IDs and fix-all ordering. They also
preserve TLS's diagnostic rematerialization and Set deduplication behavior: the
cache identity check can offer a combined fix when only one displayed diagnostic
has that particular code, provided the compiler exposes its fix ID.

Decorator fixes call uninvoked factories or parenthesize invalid decorator
expressions. Async-return fixes use the native checker's existing awaited-type
algorithm and type serializer, retaining aliases/unions in Promise annotations.
NaN fixes use the compiler's matching diagnostic and related source span to
preserve the chosen operand, comparison polarity and suggestion title; no fix is
offered when that compiler suggestion is absent. Each provider has exact
individual/combined direct/wire fixtures and copied-catalog Check coverage.

Inherited abstract member fixes reuse the native member builder with instantiated
base types, preserving protected modifiers, generics, constructor placement and
required type imports. Unreachable code fixes remove whole dead branches or
statement ranges while retaining the reference trivia boundaries. Nameless
signature parameters gain indexed names and retain rest modifiers and type
references; malformed array syntax retains the reference no-result behavior.
Individual and combined edits are checked on detached catalogs.

Mapped-type fixes convert mistaken computed type literals and literal/generic
index signatures, retaining readonly flags, interface bases, type parameters and
other members. Import syntax fixes split mixed default/named type-only imports
and convert CommonJS namespace/import-equals declarations to default imports,
including quote preferences and retained attributes. The embedded suggestion
pass supplies the corresponding default-import diagnostic over virtual modules.
Fixtures compare individual and combined edits through both interfaces, then
check corrected copies without mutating session documents.

Unused-identifier fixes retain separate delete, underscore-prefix, import-delete
and infer-to-unknown continuations. Deletion checks provided arguments,
overridden methods and callback parameter positions; writes, overloads,
destructuring side effects and parameter JSDoc are retained or changed according
to the selected fix. The editor adapter reproduces the pinned TypeScript 6
unused diagnostic ranges/codes/grouping, independently of Check/Compile's native
diagnostics. Four combined fix IDs and individual actions have exact SDK/LSP
fixtures and detached-catalog Check coverage.

Source actions require an explicit `context.only` and completed diagnostics for
the current generation. The diagnostic stream used by push clients also makes
these actions available; generation changes invalidate that readiness. Direct
hosts can call `Diagnostics` before requesting them. Parent source kinds are
matched by intersection, and source actions precede quick fixes.

`source.fixAll.ts` matches the reference's selective policy: interface
implementation and async-function fixes are individual; unreachable-code fixes
are combined. `source.removeUnused.ts` chooses the first applicable unused
identifier fix, including its conservative declaration/callback safeguards.
`source.addMissingImports.ts` uses the same import provider and project roots as
completion. A no-error action has no edit; an unrelated diagnostic can produce
an explicit empty `documentChanges` array. Eager source proposals carry the same
generation/version/read-only checks as other edits and never apply themselves.

Import operations are also available as typed calls: `OrganizeImports` accepts
`OrganizeImportsAll`, `OrganizeImportsSortAndCombine`, or
`OrganizeImportsRemoveUnused`; `SortImports` and `RemoveUnusedImports` select the
latter modes. `AddMissingImports` computes current diagnostics internally and
works on a closed catalog. All return `ImportEditsResult` with an optional
`BoundEdits`, without changing files. Explicit entry points remain the roots
when inferred native projects rebuild, including during auto-import preparation.

The `source.organizeImports.ts` code action follows TLS's non-destructive
`SortAndCombine` policy, as does `source.sortImports.ts`;
`source.removeUnusedImports.ts` removes unused bindings without reordering.
Parent filters and repeated overlapping filters retain reference action order.
Identical replacements are omitted. Aliases, type-only ordering, attributes,
side-effect imports, re-exports and ambient modules have exact reference fixtures.

Import sorting exposes the reference organize-import preferences. For
`OrganizeImportsIgnoreCase`, typed callers select `OrganizeImportsCaseAuto`,
`OrganizeImportsCaseSensitive`, or `OrganizeImportsCaseInsensitive`; their JSON
forms remain `"auto"`, `false`, and `true`. `OrganizeImportsCaseFirst` similarly
uses `OrganizeImportsCaseDefault`, `OrganizeImportsUpperFirst`, or
`OrganizeImportsLowerFirst`. Locale, collation, numeric/accent handling and type
order apply to the same native operation. Unicode comparers are request-owned,
use the existing pinned pure-Go x/text dependency, and handle locale expansions;
ordinal sorting uses UTF-16 and JavaScript lowercase rules. Completion auto-import
visibility does not disable explicit import fixes or add-missing-import actions.

`FileRenameEdits(ctx, []FileRename{{OldName: "/lib", NewName: "/renamed"}})`
computes file or directory move proposals over the captured virtual catalog.
Its `FileRenameEditsResult` contains the generation and optional `BoundEdits`.
The edits update imports, exports, dynamic/type imports, reference directives
and applicable configured-project paths. Loaded related projects participate;
shared files retain the first project's edits. Batch pairs retain input order
and refer to the same original snapshot.

`workspace/willRenameFiles` exposes this operation as a text-only `changes` map,
including an explicit empty map when no edits are needed. Neither interface
performs the move or sends `workspace/applyEdit`. The host applies the text
changes and intended moves to its own catalog, then supplies the new base and
URI mappings/document events. `ValidateEdits` checks the original generation
and touched open-document versions before applying a proposal. Library and host
declaration moves or edits are rejected.

Typed callers supply virtual names directly. Wire callers using an explicit
foreign URI namespace must register mappings for proposed destination names
(including directory names) before requesting the edits. A future destination
may be registered before it exists in the catalog. The server does not infer
virtual identities from opaque host resource identifiers.

Missing-const fixes cover assignments, comma expressions, destructuring and
for-in/of initializers, with checker-based safeguards for existing bindings.
Tuple syntax fixes move optional/rest modifiers to their labels, including the
reference's empty proposal for a stale diagnostic. Constructor fixes move an
existing `super()` before `this` access, retaining nested-block trivia and
avoiding direct access inside its own arguments. Promise fixes add `<void>` or
extend existing type arguments with `| void`, preserving required parentheses.
Individual and combined proposals have exact direct/wire fixtures and detached
Check coverage.

JSDoc-style type fixes handle nullable/non-nullable prefixes and postfixes,
JSDoc type syntax, annotated function signatures and both combined fix IDs.
The pinned nullable alternatives preserve TypeScript's union/type display rules.
Type-only export fixes use semantic diagnostics to split mixed value/type
re-exports, retain aliases, and avoid duplicate work in combined requests.

Copied-catalog edit tests inspect the error diagnostics returned by `Check` as
well as its Go error. They require no remaining errors unless a fixture records
an exact `edited_compiler_error_codes` list with an
`edited_compiler_error_reason`. This records reference behavior without changing
any oracle edit: the original using fixture lacks disposable library types;
source-remove-unused can delete a parameter used by a dependent caller; a paths
rename can omit the required relative prefix in a config substitution. Separate
valid-library and outside-config-directory cases require clean Check results.

The same copied-catalog checks retain exact reference diagnostics for an
`extends any` proposal at a return-keyword span and an external constraint alias
inserted without its import. Useful local/template constraints and contextual
async conversions separately require clean Check results. These are reference
edit semantics, not coordinate projections or skipped result comparisons.

Strict class initialization fixes offer undefined-type, definite-assignment and
initializer variants, with separate combined IDs. Initializers follow the
reference's literal/union/array/class selection and constructor/abstract-class
safeguards. Request-owned printer flags retain one-line type literals and avoid
repeating property comments; shared compiler nodes are not mutated.

Configuration fixes edit the supplied virtual `Project.ConfigFile`: they can
insert `compilerOptions` or individual settings, retain JSON comments and omit
identical replacements. JSX enablement proposes `react`; module/target proposals
remain separate and can be composed against the same original config. The SDK
composition test validates both proposals, checks the combined detached project
and verifies that the live catalog is unchanged. Inferred projects do not gain
an invented configuration file.

A target-only proposal retains the existing module kind, so CommonJS can still
reject top-level await. The copied-catalog checks record that exact remaining
reference error. An explicit removed `moduleResolution=node10` setting retains
the native TypeScript 7.0.2 compiler gate diagnostic, with pinned source evidence;
its oracle action and edit are still compared unchanged.

Oracle capture retains the explicit source entry points and also roots every
catalog `.d.ts`/`.d.mts`/`.d.cts`, matching the embedded host. File-operation
fixtures that change declaration roots supply their `tsconfig.json` explicitly,
so every proposed config edit belongs to the captured virtual catalog.

Spelling fixes cover names, properties and optional access, namespace/import
members, private names, override members and JSX attributes. Checker suggestions
stay under a request lease; combined fixes preserve the reference provider's
scope and action order, including adjacent missing-member proposals. Module
filename arguments in module diagnostics use unambiguous client file mappings;
user string literal types and opaque-resource module IDs are preserved.

Copied-catalog checks retain two reference spelling behaviors: changing an
import's token leaves its old local uses unresolved, and converting optional
access to a nonidentifier property can remove optional chaining. Exact remaining
diagnostics are recorded and independently checked with the pinned oracle;
ordinary optional access and corrected import declarations require clean Check
results.

The source-derived inventory retains `fixCannotFindModule` as an excluded
registration: the pinned provider emits only `install package` commands and
`installTypesPackage` continuations, with no text edits. This follows the
existing npm/automatic-typing-acquisition boundary; it does not remove the
registration from the inventory or relax the release gate for editor fixes.

A quick-fix candidate that would modify a frozen host/library declaration is
omitted without hiding safe fixes for the user's source. Fix-all resolution
still validates every target and rejects a proposal that includes frozen edits.
The frozen Go binding test checks that a spelling fix remains available and no
binding is called. Module diagnostics keep virtual IDs for opaque/non-file URI
mappings; ordinary file URI spellings share their canonical file display path.

Missing-member fixes cover class/interface/type-literal properties and methods,
enum members, called and contextually typed function declarations, namespace
exports, object properties and required JSX attributes. Each combined fix ID is
covered separately. Method imports target the declaration file; a closed target
may reuse prepared export indexes without a specifier memo. The memo is optional
and paths still derive from the same frozen virtual program. Object defaults
follow reference literal/union/array/class/function rules and quote preferences;
recursive expansion has cancellation and finite depth/work limits.

The exact reference proposals can retain strict-property-initialization errors
when a field is assigned outside a constructor, or a type error when an empty
string initializes a prefixed template literal type. Detached checks record the
exact independently verified compiler codes. Constructor-assignment and ordinary
object/JSX/function cases separately require a clean Check. The local-JSX-namespace
scope fixture retains its pre-existing missing-global-JSX error; the catalog
JSX declaration variant checks cleanly. Ambient namespace suggestions and missing
object argument/satisfies diagnostic heads use pinned editor presentation,
without changing Check/Compile diagnostics.

Type-only import fixes preserve aliases, mixed value/type use, inline type
specifiers and declaration-level default/named splitting. Whole-import
alternatives follow symbol-reference checks, and repeated equivalent candidates
retain the pinned server's last-action ordering. Exact-optional-property fixes
find assignment targets through variables, calls and nested properties, extending
all exact optional property types with undefined; declaration-file targets retain
the reference guard. The recorded scenarios publish diagnostics and then send
that diagnostic context, as ordinary clients do.

The pinned TS 6 type-only fix-all can reject a proposal with overlapping edits
when it combines a whole import replacement and one of its specifiers. The
embedding rejects the same invalid proposal with typed CodeInvalidEdit and an
LSP internal-error response, returns no partial edit, preserves the catalog and
continues serving requests. Direct and wire recovery are tested; successful
combined fixes use separate import declarations and require a clean detached
Check. Native tracker overlap assertions use a specific internal error type;
unrelated native panics are not swallowed.

Require-to-import fixes include top-level const suggestions, identifier and object
bindings, aliases, template-literal module names and combined fixes. Resolution-mode
fixes cover import declarations and import types, retain attributes and quotes,
and choose import or require by comparing both resolution targets in the frozen
catalog, including conditional package exports. Node16 positive cases and NodeNext
no-action cases preserve the pinned compiler's require-of-ESM distinction.

Detached Check records the pinned require fix's default-import proposal for an
ES module without a default export (1192). CommonJS declaration targets require
a clean Check separately. The explicit allowSyntheticDefaultImports=false variant
preserves the native TypeScript 7.0.2 removed-option gate, with pinned evidence,
while its reference editor proposal remains unchanged.

Invalid namespace-import fixes cover callable and constructable CommonJS namespaces,
assignability locations, parenthesized expressions and declaration-name safeguards.
They offer a default import, import-equals for CommonJS, or synthetic default-member
access when applicable. Both registrations retain their reference diagnostic groups;
complete action arrays include adjacent default-import suggestions. The quoted
module expression in an existing import is preserved even when the requested quote
preference differs. Selected alternatives and copied-catalog Check are recorded
through both APIs.

Non-exported local import fixes add an export modifier or update the last
applicable export declaration, including isolated-module type specifiers and
multi-variable declarations. Combined fixes group value and type exports per
module in reference order. Edits target catalog sources, preserve closed-file
versions, and reject library targets. Recorded direct/wire cases apply the
proposals to detached catalogs and require a clean Check.

Missing-parameter fixes support required and optional variants, middle-list
insertion, optional/default parameters, arrows, methods, function expressions and
compatible overload declarations. Names and widened types follow argument
expressions; imported parameter types add imports to the edited declaration file.
Both combined IDs and selected alternatives are recorded, including callback,
object/array types and parameter trivia. Identical tracker replacements are
omitted as in TypeScript 6, so a combined request does not rewrite an unrelated
overload implementation. Its unfixable overload-call diagnostic is retained with
an independently checked compiler expectation; ordinary combined cases check
cleanly and all edits remain detached.

Missing-await fixes offer declaration-site and use-site proposals, use semantic
related information to establish applicability, and preserve the reference's
initializer-reference safeguards. Combined fixes track declaration symbols,
prefer eligible initializers and complete remaining operands when necessary.
Coverage includes properties, calls/new, arithmetic, argument types, iterations,
for-await conversion, ASI guards and selected alternatives. Inline loop bodies
retain their layout through request-owned printer flags. Missing-property
argument diagnostics retain the pinned assignability head for any argument
expression; compiler gate diagnostics remain native.

Two exact reference proposals retain errors after editing: a partial
initializer-only binary fix leaves another Promise operand (2365), and inserting
await into a synchronous function leaves its async-context error (1308). The
fixtures record independently checked compiler codes. Use-site, async-function
and complete combined cases separately require a clean detached Check.

JSDoc type migration preserves the documentation and inserts TypeScript types
for variables, properties, function parameters and return values. Template
parameters include their constraints and defaults; unparenthesized arrows gain
parentheses in the same edit order as the reference. The migration follows the
reference's nearest-comment and parameter-name/position attachment rules,
including the restriction on parenthesized `@type`/`@satisfies` ownership.
Closure function types, nullable/optional/variadic forms, primitive aliases and
index signatures are converted with the pinned reference's traversal rules.
Lazy TS/TSX documentation parsing recognizes Closure functions and bare `?`
without changing runtime source parsing. Suggestions and fix-all use the same
eligibility predicate. Direct/wire fixtures compare edits and command metadata;
the edited copies are independently checked without mutating the session.

JSDoc typedef migration provides type aliases and interfaces with nested property
signatures, optional properties, adjacent tags, text comments and combined edits.
It retains the reference child-range search for overlapping documentation and
modifier trivia, including the reference's two-comment navigation behavior.
Request-owned deep clones retain source ranges so an inline object type stays
inline. Fixtures cover Unicode comments, CRLF, several tags in one comment and
separate documentation blocks. Applying the reference's `*` or bare `?` alias
leaves errors 8020 or 1110 respectively; these are independently verified TS 6
results recorded in the copied-catalog checks.

Some typedef forms cause the pinned TypeScript 6 formatter to reject the whole
proposal (`Token end is child end`): Closure functions, nullable/non-nullable,
optional and variadic JSDoc nodes, including nested forms. Raw oracle failures
are retained under `testdata/lsp/oracle-errors/`. The embedded API returns
`invalid_edit`, and the wire adapter reports an internal-error response without
partial edits. The regression test verifies both paths, continued operation and
an unchanged catalog. These error artifacts keep the raw oracle stack; they
are inspected by the dedicated test rather than rewritten into response fixtures.

Return-value fixes check the proposed expression against the declared or
contextual return type before offering edits. They support expression statements,
label-shaped object literals and nested blocks, including contextual call
arguments, JSX attributes, generic functions and async signatures. Actions add
return statements, remove arrow-body braces or parenthesize object literals;
each fix ID has combined-edit fixtures. Comments are copied to request-owned
nodes while the original expression's trivia is suppressed in the request's
emit context. No shared AST or source catalog is modified. One comma fixture
retains the reference proposal's pre-existing side-effect-free operand error
2695; the effectful-comma variant passes a clean Check.

Unmatched JSDoc parameter fixes delete the selected tag or rename it to the first
undocumented identifier parameter, with grouped deletion fix-all. Documentation
is reconstructed as separate deletion/insertion edits, preserving descriptions,
links and applicable neighboring tags with the reference printer's layout.
Assignment-hosted function documentation uses the reference attachment rules for
editor diagnostics; Check/Compile retain native diagnostics. Structured JSDoc
printing is confined to editor-created documentation nodes and its type-printing
context. Runtime source comments continue through the native comment path.

The fixtures also retain reference quirks: several visibility/deprecation tags
emit empty lines, while a comment attached directly before an arrow can cause
its proposed deletion and insertion to overlap. The raw error is recorded in
`testdata/lsp/oracle-errors/fix-unmatched-param-arrow-own-error.json`; direct/wire
tests require invalid-edit rejection, recovery and an unchanged session.

Accessor override fixes share the property-to-accessor generator for class
fields, constructor parameter properties and object properties. They preserve
accessibility, static/readonly behavior, quoted names, optional-property types,
constructor writes and file-level backing-name collision rules. A conflict may
edit the base declaration in a closed file. Generated names and modifiers are
owned by each edit, so formatter position assignment cannot affect another
getter/setter. Fix-all uses the reference's per-diagnostic proposals.

The copied checks retain two reference outcomes: underscore-backed fields can
leave a private-member inheritance error (2415), and constructor parameter
renaming can leave lexical references unresolved (2663). Both are independently
verified with TS 6; ordinary field and parameter-property variants check clean.
The pinned cross-file fix-all asserts in `pushRaw`; its raw failure is retained
under `testdata/lsp/oracle-errors/`, with direct/wire tests for invalid-edit
rejection, recovery and an unchanged catalog.


Decorator-metadata fixes offer namespace/default imports and `import type` as
appropriate. The shared import conversion qualifies references throughout the
file, expands shorthand properties and retains a separate named import for
re-exports. Namespace names follow the reference UTF-16 identifier and full
uppercase rules, with collision checks in each reference scope. Type-only
conversion preserves imports of values and avoids the invalid combination of
a default and named type-only import. The reference provides no fix-all action.

The recorded proposals include reference outcomes that do not fully repair the
project: type re-exports can retain error 1205, string-named imports can produce
a namespace-as-type error 2709, and export-assignment/metadata cases can retain
1203/1272. Their copied-catalog checks record the complete remaining error codes,
independently verified with TS 6. Ordinary namespace/default/type-only variants
have clean checks.


Constructor-function conversion covers declarations and variable statements,
static function/arrow members, quoted and numeric names, comments and member
order. Editor adapters translate native binary-assignment expando symbols into
the reference access-expression/method view without modifying bound symbols.
Original modifier tokens preserve the reference comment behavior through the
request-owned change writer. TypeScript's function prototype assignments retain
the reference behavior and can leave error 2339 after conversion; the copied
checks record that outcome. The native compiler's diagnostics remain intact,
while the editor treats a function's built-in prototype as initialized, matching
TS 6; genuine class property initialization errors remain visible.

Diagnostic 80002 is normally a JavaScript suggestion. The provider is also
callable for a client-supplied TypeScript diagnostic, as in TLS, without adding
that JavaScript suggestion to TypeScript editor or compiler diagnostics.

Promise-chain conversion supports `then`, `catch` and `finally`, inline or named
callbacks, destructuring, nested chains, explicit type arguments and the
reference's branch applicability rules. Editor suggestion 80006 follows the
reference eligibility and nested-callback rules. Semantic queries use source
nodes; renamed edit trees belong to the request. The change writer preserves an
absent embedded statement during position assignment, so an omitted `else`
remains absent when formatting the proposal.

Code-action resolve caches follow TLS: a new enumeration expires selectors from
previous enumerations, even without a source change. Resolving such a selector
returns the issued unresolved action without edits. Source-generation changes
and retention eviction retain their typed stale/expired errors. Async conversion
keeps its reference success flag with the enumeration and serializes its fix-all
resolves with cancellation; no provider state is shared between sessions. A
failed conversion can leave later fix-all proposals as `async` modifiers only,
and repeated resolves can differ just as in the reference. These sequences have
raw direct/wire fixtures. Catch/finally continuation fixtures also preserve the
reference's `never` annotation and its remaining assignment errors (2322),
independently checked with TS 6; ordinary conversions have clean copied checks.

CommonJS conversion handles whole-file exports, export objects, functions and
classes, `require` imports and re-exports, alias chains, local-name conflicts,
and import-equals consumers of a newly introduced default export. Diagnostic
80001 is a JavaScript suggestion; TypeScript clients can explicitly request its
provider with a supplied diagnostic, as in TLS. It has no fix-all variant.
Import-use replacements operate on request-owned edit trees. Repeated and
concurrent requests keep the original catalog and semantic tree unchanged.

Recorded proposals preserve TS 6 outcomes even when the proposed source retains
errors: a named import can collide with a newly generated export, destructuring
can request an unavailable default export, and empty destructuring or generator
name insertion can produce invalid syntax. Each affected fixture records the
complete error-code list from `Check`, including pre-existing errors in helper
files. Proposals are also checked independently with TS 6. When a proposal has
syntax errors, `Check` retains its existing syntax-first behavior; fixture notes
identify additional semantic diagnostics from the independent TS 6 check. These results do not change compiler diagnostics or
silently repair a reference proposal.

Inference from usage covers variables, members, parameters, rest parameters,
accessors and function receivers. It combines call sites and contextual uses
across project files, structural properties and signatures, index uses and
applicable string, number, Array and Promise members. Accessible exported types
can add imports. Its synthetic types preserve property encounter order and
write through a request-owned print context; inference never rewrites the source
semantic tree. TypeScript and TSX fixtures cover single proposals and fix-all,
including JSDoc annotation alongside inference. Repeated concurrent queries also
check unchanged source diagnostics.

Implicit-this fixes offer arrow conversion when a function shadows an outer
receiver. Named function expressions that reference themselves retain the
reference's rejection. Receiver inference remains a separate action; anonymous
returned functions can have no usable references. Related information for an
anonymous function points at its first token, including an async modifier. At
the same source position, computed editor suggestions precede checker inference
suggestions, preserving the JSDoc/inference action order.

These proposals retain reference outcomes such as a typed but uninitialized
class property (2564), required parameters in calls with fewer arguments (2554),
a recursive return type (7023), or an anonymous receiver that fix-all cannot
infer (2683). Converting an inline function declaration to an arrow variable can
also omit a required separating semicolon (1005). Affected fixtures record the
complete copied-project error codes and independently checked TS 6 outcomes.

Isolated declaration fixes preserve the reference's annotation, inline
`satisfies`/assertion and extraction variants, including breadth-first nested
binding extraction, expando properties, class heritage and importable inferred
types. Type minimization uses cached generic constraints and the reference's
function-symbol rules; diagnostic titles truncate by UTF-16 units. Printing uses
an action-owned context, including the reference's single-line behavior after a
long title, and never changes source nodes.

Declaration diagnostics use a fresh editor-only traversal that records the
emitted signature surface and distinguishes expression inference from signature
fallback. This preserves TypeScript 6 return-boundary diagnostics for nested,
async, generator and contextually typed functions while leaving ordinary
`Check`/`Compile` diagnostics and declaration caches unchanged. Fixtures also
apply proposals to detached catalogs and record remaining errors independently
confirmed with the pinned TypeScript package. A quoted destructuring property
whose reference formatter throws rejects the whole proposal with a typed error;
a subsequent query remains usable.

Missing-import quick fixes preserve the reference's value/type/namespace lookup,
existing namespace qualification, default/named binding merges, type-only
promotion and ESM/CommonJS/UMD import forms. Equivalent candidates follow program
file order. Existing module-specifier spelling takes precedence over recomputed
paths; promotion rewrites a resolved TypeScript extension to its output extension
when the compiler options require it. Comments and individual edit ranges are
preserved, including the reference's insertion after a commented final specifier.

`export =` candidates include constructor statics, `prototype`, namespace exports
and function properties. Their qualification uses the resolved export's original
name across aliases. Indexing retains detached strings and identifiers, without
storing a checker in a deferred result. Fixtures for removed TypeScript 6 options
retain their editor behavior; detached `Check` expectations explicitly record the
ordinary TypeScript 7 configuration gate. Other fixtures use NodeNext and CTS
sources to verify applicable proposals with a clean compiler check.

Совместимые команды `_typescript.organizeImports` (command path и необязательные
`mode`/`skipDestructiveCodeActions`), `_typescript.applyRenameFile`
(`{sourceUri,targetUri}`) и `_typescript.applyCompletionCodeAction`
(`[filePath,codeActions]`) вызывают соответственно `OrganizeImports`,
`FileRenameEdits` и `CompletionCodeActionEdits`. Команды возвращают `null` и
передают legacy `changes` в `workspace/applyEdit`; organize imports требует
открытого документа, явный `mode` имеет приоритет. Rename-file предлагает только
изменения ссылок, а хост отдельно подтверждает перемещение через обычные события.
Completion выбирает только первое действие; пустое первое действие не запускает
следующее. Его публичные DTO сохраняют однобазовые UTF-16 locations эталона и
caller-visible command paths. Операция проверяет диапазоны по текущему снимку,
возвращает owned `BoundEdits` и отвергает внешние tsserver commands целиком до
клиентского callback. Подтверждение `applyEdit` само по себе не меняет каталог.

`_typescript.selectRefactoring` и `editor.action.showReferences` исполняет клиент:
сервер возвращает их исходные аргументы, а прямой API — `RefactorChoice` и
`NavigationContinuation`. После принятого refactor edit сервер отправляет
`_typescript.rename`; прямой API возвращает prospective `RenameContinuation`.
Выданные команды refactoring проверяют исходный выбор и generation, включая
изменение настроек между подготовкой и запуском, и ограничены лимитами retention.
Для самостоятельного вычисления на текущем снимке предназначен `RefactorEdits`.

Quick-fix follow-ups `_typescript.applyCodeActionCommand` (результат `true` при
отсутствии commands) и `_typescript.applyFixAllCodeAction` (результат `null`) не
применяют текст повторно. Прямой аналог — `CodeActionCommands`. Все применимые
embedded providers возвращают текстовые правки; Node/npm/tsserver commands
отвергаются до callback, в том числе внутри completion action. Запросы
`typescript.tsserverRequest` и `_typescript.configurePlugin` получают method-not-found;
они отсутствуют в capabilities. Сервер не предоставляет restart/log/install
команды внешнего tsserver.

Initialization `locale` управляет локализацией редакторских диагностик, а
`completionDisableFilterText` — completion rendering, включая optional members.
`hostInfo` сохраняет имя встраивающего хоста в `EditorOptions`; оно не меняет
редакторские результаты и не включает telemetry. `displayPartsForJSDoc=false`
сохраняет исходные inline link tags вместо rich links. `disableLineTextInReferences`
передаётся в backend; публичные references в обоих вариантах состоят из LSP
locations. `excludeLibrarySymbolsInNavTo` управляет библиотечными workspace symbols,
а `provideRefactorNotApplicableReason` — выдачей недоступных действий.
После `initialized` отправляется `$/typescriptVersion` с `source: "bundled"` и
версией из `goya.TypeScriptVersion()`.

`WorkspaceConfiguration.ImplicitProjectConfiguration` задаёт module/target,
legacy decorators и strict-флаги только для inferred projects. `Configure`
заменяет этот блок, а отсутствие блока восстанавливает defaults эталона.
Настройки из виртуального config-файла и явно переданные host compiler options
имеют приоритет. `ProjectSnapshot` экспортирует effective inferred options для
последующего `Check`/`Compile`. В TS 6 отсутствующий `strict` означает `true`;
TLS не передаёт false strict-флаги, поэтому false наследует это значение, пока
хост явно не задаст соответствующий compiler option. Fixtures проверяют обе
ветви, смену target/module/decorators и configured project без изменений.
Target-dependent editor grammar для private members и auto-accessors сохраняет
диагностики TS 6; обязательный compiler gate продолжает использовать native checker.

`allowIncompleteCompletions` поддерживает reference memo module specifiers,
лимиты 100 новых разрешений и 1000 cache-only попыток в выдаче, повторные
completion-запросы и resolve отложенного элемента. При legacy resolution false
не добавляет предварительные specifier details; import statement completion,
package exports и specifier-exclude filters требуют полной выдачи независимо
от этого флага. Подготовка native кандидатов остаётся eager; эти лимиты описывают
reference presentation/cache contract, а не гарантированное время вычисления.
Кеш принадлежит виртуальному workspace и освобождается вместе с ним; смена
поколения создаёт отдельный кеш, не перенося compiler objects между сессиями.

`lazyConfiguredProjectsFromExternalProject` сохраняется и передаётся в backend.
Этот флаг управляет созданием configured projects из tsserver external projects;
в поддерживаемых configured/inferred host projects оба значения дают одинаковые
редакторские результаты, как в эталоне. Fixtures проверяют оба значения на
project references и inferred overlays. Флаг не открывает external projects и
не включает исключённый passthrough `typescript.tsserverRequest`.

Validation commands, offline embedding results and workload measurements are
recorded in [typescript-lsp-validation.md](typescript-lsp-validation.md).

Move-to-existing-file uses the actual source EOF for statement-free targets,
including empty files, whitespace and comments. TLS/TS6 reports `EOF+1` for these
insertions. This agreed coordinate exception preserves edit text and ordering.
Fixtures retain the raw client request and independently recorded TS6 span,
`SourceFile.end`, zero statement count and source SHA-256. The projector checks
whitespace/comments independently, requires that exact target of `Move to file`,
and rejects other offsets, replacements, commands or targets. BOM input also has
an ordinary raw-parity case: the disk oracle strips its BOM, so the returned
position already equals the supplied source EOF and no projection is enabled.

A host can select a configured project and explicitly override an empty `files`
list with entry points. TS6's language-service API fails in `addNewFileToTsconfig`
when moving to a new file in that shape; the embedded API rejects the complete
proposal with a typed invalid-edit error. Its independently captured API failure
is distinguished from ordinary TLS transcripts, which would infer another project.
No resource or partial text proposal escapes this failure.
