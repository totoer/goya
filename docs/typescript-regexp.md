# Native regular expressions

RegExp patterns execute in Go. Capturing patterns and Unicode sets use pinned
regexp2 v2.10.1 (commit a6ef7b6f88fff76c0c7d4f5fe832535de8cfda11).
The simple-pattern fast path uses Go's regular-expression engine. Go 1.27 or
newer is required so the standard Unicode categories, scripts and simple case
folding include Unicode 17. No external regexp executable or ICU is loaded.

Backtracking matching polls Runtime.Interrupt inside the interpreter, including
when collecting subsequent matches. Cancellation returns the usual uncatchable
InterruptedError with its original value and script stack. Each backtracking
operation also has a one-second execution budget; collecting multiple matches
shares that deadline. Exhausting the budget raises a catchable RangeError.
The runner hooks are per call, so compiled expressions shared by runtimes do not
share cancellation state. The executor sources are retained in
[internal/regexp2](../internal/regexp2/README.md) to add these hooks; syntax and
Unicode data still use the pinned module above.

The supported flags are d, g, i, m, s, u, v and y. The u and v modes are mutually
exclusive. flags reads the corresponding public properties in standard order.
hasIndices and unicodeSets are branded getters. A v instance reports unicode
as false while matching and empty-match advancement use full Unicode semantics.
Source and toString retain the original pattern.

The d flag adds an indices array to each successful exec/matchAll result. Every
participating capture has a fresh [start, end] pair in UTF-16 code-unit offsets.
Unmatched captures are undefined. Named indices use a null-prototype groups
object and reference the same pairs as the numbered entries.

Unicode sets implement nested unions, intersections, subtraction, ranges,
complements, character escapes and q string disjunctions. Strings match longest
first; empty strings and isolated UTF-16 surrogates retain their meaning. In
case-insensitive v mode, operands are folded before set operations and complement.
This differs deliberately from the complement order of u property escapes.
Local i/m/s modifiers affect only their group, including v set folding.

All seven Unicode binary properties of strings are embedded from official Emoji
17 data: Basic_Emoji, Emoji_Keycap_Sequence, RGI_Emoji_Modifier_Sequence,
RGI_Emoji_Flag_Sequence, RGI_Emoji_Tag_Sequence, RGI_Emoji_ZWJ_Sequence and RGI_Emoji.
The latter contains 3953 sequences. Tests match every published sequence for
each property. Source URLs/digests and generated digest are recorded in
internal/unicode17/emoji-manifest.json. The retained Unicode V3 license covers
these data. Regenerate or check from the two exact source files:

    CGO_ENABLED=0 go run ./internal/unicode17/cmd/importemoji -sources /path/to/emoji17 -check

The pinned native TypeScript scanner checks regexp grammar, including local
modifiers, duplicate flags and mutually exclusive duplicate named groups.
Constructor patterns retain Annex B identity escapes, literal slashes and line
terminators. Unicode property repertoire validation for constructors uses the
runtime data; the TypeScript 7.0.2 scanner's alias catalog predates some Unicode
17 scripts. Its original diagnostics for regexp literals remain unchanged.

Captures inside repetitions are cleared before each new iteration. Duplicate
named groups in mutually exclusive alternatives retain distinct numbered entries;
groups, indices.groups, replacement strings and replacement callbacks select the
participating capture. Lookahead captures may extend beyond a later capture.

The pinned declarations also contain deprecated RegExp static match properties.
They use realm-local state, initially empty strings. A successful intrinsic match
updates input/$_, lastMatch/$&, lastParen/$+, contexts and $1 through $9; failure
retains the preceding state. input is settable with string coercion. Other fields
are computed accessors. Getters require the intrinsic constructor receiver.
A proper subclass match invalidates this deprecated state until a subsequent
intrinsic match; compile requires an intrinsic same-realm instance. These legacy
policies follow the [TC39 legacy features draft](https://github.com/tc39/proposal-regexp-legacy-features).
Deleting the configurable accessors does not affect ordinary matching.

Behavioral fixtures cover the checked public APIs, all seven sequence catalogs,
modern syntax, invalid patterns, Unicode advancement and realm-local static state.
The declaration-member audit checks RegExp, its constructor and all match/indices
result interfaces against native objects.

