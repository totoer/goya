# Internationalization data and implementation

Intl is a language intrinsic. Its locale data is embedded in the application;
it does not consult the operating system's locale database or download data.
The TypeScript declarations remain the pinned upstream declarations. Work on
the Intl constructors is tracked individually in compatibility tasks 8.14–8.18;
their declarations do not establish runtime completeness.

## Locale data

`internal/cldrdata` embeds selected documents from official CLDR JSON 49.0.0,
commit `a4b67247b446fd51b475991017a2481911210c60`. The package's manifest records
the official archive digest, every selected source digest and the generated
compressed data digest. The unmodified Unicode V3 license accompanies the data.
The CLDR release is described by the
[Unicode release notes](https://cldr.unicode.org/downloads/cldr-49).

The data locales are en, en-GB, de, fr, ru, sr, sr-Latn, ar, ja, zh, zh-Hant,
hi, es, tr, ko, pt, pt-PT, it, nl, sv, pl, uk, he, id and th. The additional
available locale en-US uses en data. The deterministic default locale is en-US.
Requested locales undergo structural validation and canonicalization before
matching. Unknown, well-formed locale identifiers remain valid identifiers.
Lookup progressively removes subtags to find an available locale; best fit uses
the same matching policy. A request without a match falls back to the default.
`supportedLocalesOf` returns matching requests, preserving relevant identifiers.

Locale identifiers use the Unicode BCP 47 grammar: canonical casing, aliases,
variant validation, sorted extensions and first occurrence of Unicode keywords.
Pinned supplemental data supplies aliases and likely subtags. Returned arrays,
resolved options and locale records do not expose mutable data shared by runtimes.

To regenerate or verify the data, obtain the archive URL recorded in the manifest
and run these commands with the supported Go toolchain:

```sh
CGO_ENABLED=0 go run ./internal/cldrdata/cmd/importcldr -archive /path/to/cldr-49.0.0.tar.gz
CGO_ENABLED=0 go run ./internal/cldrdata/cmd/importcldr -archive /path/to/cldr-49.0.0.tar.gz -check
```

For byte-for-byte regeneration use Go 1.27.2, recorded in the CLDR manifest
(`GOTOOLCHAIN=go1.27.2`). Both compressed and uncompressed data digests are recorded;
Go releases can produce different gzip bytes for identical locale documents.

The importer refuses an archive with a different checksum. It performs no network
access, rejects duplicate documents and requires the expected locale documents.
The runtime reads only the embedded generated file.

## Collation

Collator uses the pure-Go collation implementation in pinned x/text v0.43.0.
That package embeds Unicode 6.2 collation tables and CLDR 23 tailoring tables;
these are distinct from the CLDR 49 formatting documents. It handles the
available locales' default order and the backed German phonebook and Chinese
stroke/pinyin specializations. Normalization, sensitivity, numeric comparison,
punctuation handling and case ordering are applied within each runtime.
Unicode 17 locale casing is applied before case-insensitive comparisons, so newly
assigned cased scripts retain sensitivity/caseFirst behavior alongside older
collation weights.
Unsupported well-formed collation requests follow locale negotiation.

The compare getter caches a bound function per instance. Calling that function
does not require a receiver. Other prototype operations require an actual branded
instance; wrapping one in Proxy does not transfer its internal slots.

## Numbers and plural operands

Number formatting uses decimal integer arithmetic for quantization. BigInt input
retains its full integer precision. Numeric strings retain exact decimal values
after the specification's overflow/underflow classification. Number input uses
its standard decimal representation. Negative zero remains distinguishable.

Rounding supports the nine rounding modes, permitted increments, fraction and
significant digit limits, precision priority and trailing-zero display. Formatting
uses embedded digits, separators, grouping patterns, currency metadata, unit
patterns and compact patterns. Plural operands retain visible fractional zeros
and exact integer residues. Compact notation shifts operands by its exponent
before evaluating the pinned cardinal rules.

Numeric range output uses the locale's range and approximation patterns. Range
parts identify startRange, endRange and shared portions. Repeated endpoint text
uses the approximation pattern. The range policy keeps each endpoint's affixes.

Number and BigInt toLocaleString, array and typed-array locale formatting, and
string localeCompare use the same native formatters. Locale casing uses pure-Go
Unicode casing rules, including Turkish/Azerbaijani and Lithuanian behavior,
and preserves isolated UTF-16 surrogates.

## Plural rules and relative time

PluralRules supplies cardinal and ordinal categories, visible-digit-sensitive
rounding and per-locale category arrays. Number inputs retain their exact binary64
mathematical value while rounding for plural selection. The category rules and
range mappings come from the pinned CLDR supplemental documents.

RelativeTimeFormat supports year, quarter, month, week, day, hour, minute and
second (including plural unit spellings), long/short/narrow styles, numeric and
auto phrases, and numbered parts with a singular unit identifier. A negative
zero selects the past pattern in numeric mode. Automatic phrases such as yesterday
and next year are data-driven; their parts consist of a literal.

The pinned TypeScript declarations do not yet include PluralRules.selectRange or
its newer rounding fields. Native behavior for these standard operations is
covered through explicit type assertions in the tests; upstream declarations
remain unmodified.

## Clock, timezones and calendars

Date and Intl share a timezone setting per Runtime. The default is UTC.
Go can call `SetTimeZone("Europe/Belgrade")` or `SetTimeZone("+05:30")` before
execution. The clock comes from the same SetTimeSource callback used by Date.
A formatter captures its resolved timezone when constructed. Changing a runtime's
setting does not mutate an existing formatter or another runtime.

Explicit zone identifiers resolve only within the embedded IANA 2026e catalog.
`internal/tzdata/manifest.json` pins the checksum of the Python Software
Foundation's tzdata 2026.5 wheel and each selected TZif source. This wheel is an
archive of compiled data: importing and using it requires no Python interpreter,
zic, C compiler or Cgo. The Go importer retains the distribution licenses and
reproduces its selected archive and metadata with `-check`.

```sh
CGO_ENABLED=0 go run ./internal/tzdata/cmd/importtz -wheel /path/to/tzdata-2026.5.whl
CGO_ENABLED=0 go run ./internal/tzdata/cmd/importtz -wheel /path/to/tzdata-2026.5.whl -check
```

Named lookup is case-insensitive. Historical aliases resolve to canonical names;
country-specific entries from zone.tab remain primary identifiers. UTC/GMT aliases
normalize to UTC. Offset identifiers normalize to signed HH:mm. No named lookup
consults OS timezone files. The catalog includes IANA 2026e's recent changes to
Canadian zones and Morocco, verified by independent offset tests.

The available calendars are gregory and iso8601. Both use Gregorian civil date
fields. Other well-formed calendar requests participate in locale negotiation
and use the supported default if unavailable. Styles and component patterns come
from the selected CLDR Gregorian documents, including grammatical month context,
localized day periods, time zone names and interval fallback patterns.

DateTimeFormat exposes bound format, formatToParts, formatRange,
formatRangeToParts and resolvedOptions. Component matching preserves the locale's
numeric/name pattern categories and grammatical month context. The basic matcher
uses the standard component penalties; best fit also preserves numeric date
categories. Hour cycles, flexible day periods, fractional seconds, styles and
numbering systems use the embedded data. Range formatting uses the CLDR interval
fallback pattern; indistinguishable formatted endpoints share their parts.

Date.prototype locale methods snapshot the time value before reading options.
An invalid Date returns Invalid Date without consulting those options. The same
runtime timezone controls Date construction, parsing without an offset, local
getters/setters, Go Date-object exports and Intl's default timezone.
ExportTo(time.Time) from text retains an explicitly written offset. Ordinary Date formatting
has a deterministic UTC default; old Go code that changed the process-wide
time.Local should configure the runtime explicitly with SetTimeZone.

## Locale objects, display names and lists

Locale retains a canonical identifier with its Unicode and private extensions.
Language, script, region and Unicode-key options override the corresponding tag
fields. Compound aliases match variant subsets independently of variant order.
Maximize/minimize use pinned likely-subtag data and return fresh intrinsic Locale
objects. Locale arguments use their internal identifier even if toString changes.
The locale information methods return detached arrays/records for calendars,
collations, hour cycles, numbering systems, text direction, week conventions and
explicit-region time zones. Calendar/collation lists reflect the runtime's
available implementations; an explicit Unicode key remains an identifier.

DisplayNames supports language, region, script, currency, calendar and dateTimeField
codes, long/short/narrow styles, dialect/standard language naming and code/none
fallback. It validates each code family before lookup. Dialect names can consume
language/script/region combinations; remaining qualifiers use the locale display
pattern. Names come from the same embedded CLDR documents as other formatters.

ListFormat accepts an iterable of strings, including an empty or omitted list.
It implements conjunction, disjunction and unit lists in all three styles,
including CLDR's contextual Spanish conjunctions and Hebrew prefixes. Parts retain
original elements and distinguish literal separators. Non-string elements throw
without coercion and close the iterator. Formatting retains isolated UTF-16
surrogates and expands right-associated patterns in linear space and time.

The pinned declarations omit some Locale information fields and restrict
getCanonicalLocales arguments more tightly than the runtime standard. Tests use
explicit assertions for these operations while retaining upstream declarations.

## Segmentation and durations

Segmenter exposes grapheme, word and sentence boundaries, Segments.containing and
independent Segment Iterators inheriting the standard Iterator prototype. Indices
are UTF-16 code-unit offsets; output preserves the original string, including
isolated surrogates. The implementation uses default UAX #29 Unicode 17 boundaries
from pure-Go uax29 v2.7.0, commit b03477d1fbba89df95a6b55da0a222b2e2228610.
This boundary policy applies to every available locale. Word-like classification
uses the pinned Unicode 17 letter and number categories. It does not consult an
OS segmenter or load external dictionaries.

The runtime's boundary adapter passes the official GraphemeBreakTest (766 cases),
WordBreakTest (1944 cases) and SentenceBreakTest (512 cases). The original test
files and category data reproduce from the checksum-pinned official UCD archive:

```sh
CGO_ENABLED=0 go run ./internal/unicode17/cmd/importucd -archive /path/to/UCD.zip -check
```

The Unicode V3 data license and uax29's MIT license are retained in
internal/unicode17. Source and generated checksums are in its manifest.

DurationFormat accepts integral, consistently signed duration records with the
standard calendar-field and normalized-second limits. It reads fields in the
specified order and validates exact integer nanoseconds. Long/short/narrow/digital
styles support per-unit styles and display criteria, localized numeric separators,
numbering systems and up to nine fractional digits. Fractional combination and
truncation use decimal integer arithmetic, including unsafe binary64 integers.
Durations remain unbalanced; seconds above 59 are not silently converted.

Only the first displayed component receives the sign. Explicit zero components
can carry a negative sign when smaller fields are negative. Parts identify the
singular unit on each number-format part; list and digital separators are literals
without a unit. Native NumberFormat and ListFormat helpers retain their intrinsic
behavior when application code replaces properties on Intl.
