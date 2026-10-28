# Tasks

## 1. Upstream baseline и матрица совместимости

- [x] 1.1 Определить актуальный стабильный TypeScript и соответствующий официальный source snapshot; записать версию, commit, release provenance, лицензии и хеши assets в manifest; проверить соответствие опубликованным stable metadata и отсутствие beta/nightly.
- [x] 1.2 Построить матрицу syntax, type system, runtime constructs и standard-library members выбранной версии; сопоставить её с текущим Goja; проверить, что каждое обязательное семейство имеет implementation target и conformance fixture, а крупные пробелы разбиты на отдельные checkbox-задачи до их реализации.
- [x] 1.3 Добавить воспроизводимое размещение транзитивно необходимых upstream Go-пакетов в `internal/tsfrontend/upstream/` с переписыванием импортов и отдельным patch set; проверить повторяемость source tree и сборку при `CGO_ENABLED=0` без запуска npm/Node.
- [x] 1.4 Встроить соответствующие `.d.ts` и generated assets в read-only compiler FS; проверить базовое разрешение стандартных типов без доступа к host FS и сети и отсутствие DOM/Node в default profile.
- [x] 1.5 Описать обновление upstream и правила маркировки development/release в документации; проверить воспроизведение import/update процедуры по manifest и правило запрета заявления полной поддержки при незакрытой матрице.

## 2. Project snapshots и независимая проверка типов

- [x] 2.1 Ввести Go-модель проекта, compile options, canonical source ID и resolver приложения; проверить анализ минимального проекта, предоставленного полностью из памяти.
- [x] 2.2 Материализовать immutable snapshot исходников, зависимостей, declarations и настроек; проверить, что изменение ответа resolver после анализа не меняет используемые при компиляции исходники.
- [x] 2.3 Реализовать frontend facade и `Check` без создания VM; проверить valid/invalid assignment, отсутствие вызовов host-функций и отсутствие исполняемого результата.
- [x] 2.4 Экспортировать structured diagnostics с upstream code, severity, диапазоном, именем файла и related locations; проверить ошибки синтаксиса, типов, конфигурации и импортов, включая Unicode-позиции.
- [x] 2.5 Установить strict defaults и отклонение no-check/skip-check options; проверить implicit-any, nullability, разрешённый явный `any`, TS suppression directives и одинаковые правила явных настроек.
- [x] 2.6 Добавить дифференциальные type fixtures для generics, recursive types, conditional/mapped/template literal types, overloads и narrowing; проверить совпадение кодов и диапазонов диагностики с зафиксированным эталонным stable frontend.
- [x] 2.7 Описать Go-пример проверки in-memory проекта и чтения diagnostics; проверить выполнение примера без runtime и внешних compiler tools.

## 3. Типизированный реестр Go API

- [x] 3.1 Реализовать registry builder, descriptor и freeze в Environment с каноническим schema digest; проверить неизменяемость окружения и одинаковый digest для эквивалентных контрактов независимо от порядка регистрации.
- [x] 3.2 Выводить объявления обычных Go-функций, bool/string/float64 и безопасных integer types из reflection; проверить корректный вызов `add(1, 2)` и compile error для `add("1", 2)`.
- [x] 3.3 Добавить описания структур, методов, arrays, slices, maps и рекурсивных указателей с общим mapper; проверить совпадение видимых имён, nullability, recursive declarations и доступности записи с runtime descriptor.
- [x] 3.4 Поддержать explicit TS contracts и converters для dynamic objects, низкоуровневых callbacks, interfaces и неоднозначных типов; проверить ошибки регистрации без schema и отсутствие неявного fallback в `any`.
- [x] 3.5 Реализовать числовые policies для platform-dependent и 64-bit integers и float32; проверить safe-integer boundaries, bigint roundtrip, переполнение, fractional integer input и явно выбранное округление.
- [x] 3.6 Реализовать bridge для rest/optional arguments, нескольких результатов и завершающего `error`; проверить arity, tuple order, nil/non-nil error и отсутствие прежнего silent truncation/zero-fill обязательных аргументов.
- [x] 3.7 Реализовать копирование значений с циклами и host views с явной mutability/identity; проверить передачу TS-object в Go, readonly fields, pointer identity и корректное различение nil и empty collections.
- [x] 3.8 Добавить проверки фактических значений на host boundary; проверить, что неверный аргумент через `any` или assertion отклоняется до вызова Go-функции, а нарушение объявленного return contract обнаруживается на выходе.
- [x] 3.9 Описать автоматическую регистрацию, explicit adapters и числовые policies в embedding-документации; проверить работающие примеры глобальной функции, объекта и host-модуля.

## 4. Checked AST, IR и базовый backend

- [x] 4.1 Определить внутренние checked-analysis handle и IR с source ranges; проверить, что backend не принимает непроверенный AST и upstream-типы не попадают в публичный Go API.
- [x] 4.2 Реализовать lowering literals, expressions, assignments и control flow в IR; проверить значения и порядок побочных эффектов на fixtures и явную diagnostic для неизвестного узла.
- [x] 4.3 Реализовать функции, closures, lexical scopes, destructuring и rest/spread в IR/backend; проверить захват переменных, порядок evaluation и TDZ на поведенческих fixtures.
- [x] 4.4 Подключить IR к байткоду и используемым частям VM; проверить выполнение минимальной TS-программы с host-вызовом и неизменяемость скомпилированного code.
- [x] 4.5 Стирать type-only syntax после проверки и сохранять связь с исходниками; проверить отсутствие runtime artifact у interfaces/type aliases/type-only imports и правильные TS-позиции ошибок.
- [x] 4.6 Описать границы frontend, IR и VM для разработчиков; проверить, что новый backend-fixture можно добавить по документированной процедуре и он отмечается в compatibility matrix.

## 5. Runtime-bearing конструкции TypeScript

- [x] 5.1 Реализовать enum и const-enum semantics с учётом compiler options; проверить numeric/string values, reverse mapping, constant expressions и импорт из другого модуля.
- [x] 5.2 Реализовать namespaces, declaration merging с runtime-частью и parameter properties; проверить совместный type/value namespace, инициализацию и доступ к экспортам.
- [x] 5.3 Реализовать class fields, static blocks, private members и наследование в новом backend; проверить порядок инициализации, super и private-brand semantics относительно эталонных fixtures.
- [x] 5.4 Реализовать standard decorators и их initializers; проверить порядок evaluation/application, context и инициализацию членов по fixtures выбранной версии.
- [x] 5.5 Реализовать допустимые legacy decorator options и требуемую ими metadata-emission семантику без скрытого внешнего runtime; проверить observable output и явно зарегистрированные metadata hooks.
- [x] 5.6 Реализовать JSX/TSX lowering для поддерживаемых factory/runtime modes; проверить type errors props, вызов зарегистрированной factory и отсутствие встроенной React/DOM зависимости.
- [x] 5.7 Реализовать async, generators и async iteration в новом backend; проверить возвращаемые значения, исключения, finally и порядок продолжений на fixtures.
- [x] 5.8 Реализовать resource management и необходимые disposal primitives; проверить нормальное завершение, abrupt completion, reverse disposal order и async disposal.
- [x] 5.9 Сверить runtime-bearing синтаксис с baseline matrix и добавить отдельные задачи/fixtures для оставшихся семейств; проверить отсутствие непокрытых обязательных конструкций перед отметкой этой задачи.
- [x] 5.10 Описать поддерживаемые TS/JSX/decorator options и стандартные примеры языка; проверить примеры напрямую через новый compiler/backend.

## 6. Модули и host-controlled resolution

- [x] 6.1 Подключить resolver приложения к upstream module/type resolution и canonical IDs; проверить in-memory graph, декларации, явно переданные package metadata, `.js` specifier к `.ts` source и отсутствие implicit filesystem fallback.
- [x] 6.2 Реализовать module records, imports/exports и live-binding cells; проверить повторный импорт, re-export и изменение экспортируемого значения.
- [x] 6.3 Реализовать linking циклических зависимостей и initialization states; проверить TDZ, порядок эффектов, однократное исполнение и propagation initialization errors.
- [x] 6.4 Реализовать top-level await и async module dependencies; проверить ожидание зависимых модулей и распространение rejection без дублирующей инициализации.
- [x] 6.5 Поддержать import assignment, export assignment и interop выбранных module options во внутреннем linker; проверить TS fixtures без внешнего CommonJS registry или глобального Node `require`.
- [x] 6.6 Реализовать host module bindings и declaration-only/type-only modules; проверить успешный `Check` для ambient declaration и отказ запуска исполняемого графа без требуемой реализации.
- [x] 6.7 Реализовать dynamic import с обязательным checking нового графа и runtime instance cache; проверить type error до эффектов загружаемых модулей, повторный импорт и разрешение только через resolver.
- [x] 6.8 Описать Go-пример resolver и host-модуля; проверить цикл загрузки in-memory проекта, canonical module identity и отсутствие предоставленного скрипту файлового API.
- [x] 6.9 Реализовать deferred module evaluation поддерживаемого TypeScript с проверкой полного статического графа до выполнения; проверить fixture `deferred-import`, момент первого доступа к namespace и порядок эффектов; документировать lazy evaluation.
- [x] 6.10 Реализовать import attributes и явно предоставленные JSON modules; проверить fixture `import-attributes`, сохранение attributes в resolution/linking и compile errors для некорректного JSON; документировать resolver policy без implicit filesystem loader.

## 7. Публичная компиляция и проверка привязок перед запуском

- [x] 7.1 Реализовать публичный `Compile` поверх того же анализа, что и `Check`; проверить одинаковые diagnostics и отсутствие `Program` при type, syntax, declaration и configuration errors.
- [x] 7.2 Направить все convenience execution APIs через checked compiler и удалить обход через старый `CompileAST`; проверить отсутствие пользовательских эффектов при ошибке в любом статическом модуле.
- [x] 7.3 Включить в Program версию frontend, options/profile identity, source maps и manifest требуемых host schemas; проверить стабильность manifest и его независимость от адресов Go-функций.
- [x] 7.4 Реализовать preflight привязок до module initialization; проверить missing/incompatible bindings, допустимость дополнительных регистраций и запуск одной программы с разными реализациями одинаковой schema.
- [x] 7.5 Реализовать типовое окружение прямой/косвенной eval и проверяемую динамическую компиляцию; проверить корректный захват разрешённых bindings и отсутствие эффекта нового исходника при ошибке типов.
- [x] 7.6 Перевести Function/async-function/generator constructors на проверяемый TS frontend; проверить корректные и ошибочные исходники и structured compilation exception без fallback в старый JS parser.
- [x] 7.7 Добавить content-based reuse analysis для неизменяемых снимков; проверить invalidation при изменении source, `.d.ts`, options, mapper, language version и registry schema без повторного выполнения user code.
- [x] 7.8 Описать новый жизненный цикл Environment -> Check/Compile -> Runtime, preflight errors и breaking API migration; проверить embedding-пример в двух совместимых runtime.

## 8. Стандартная среда и runtime guarantees

- [x] 8.1 Сверить VM numeric/string/object/property operations с обязательной baseline matrix; адаптировать существующие реализации и проверить выбранные conformance fixtures без обещания старой JS API совместимости.
- [x] 8.2 Закрыть пробелы в Array, collections и iterator families выбранной standard library; для каждого отсутствующего семейства выделить отдельную задачу до реализации и проверить соответствие runtime methods встроенным `.d.ts`.
- [x] 8.3 Закрыть пробелы в Promise, error и resource/disposal families; разбить обнаруженные крупные пробелы на задачи и проверить async error propagation, scheduling и соответствие объявленным типам.
- [x] 8.4 Закрыть пробелы в оставшихся стандартных Date/RegExp/Math/JSON/BigInt/typed-array и иных обязательных baseline families; выделить отдельные задачи для недостающих семейств и проверить их conformance fixtures без скрытия методов из standard declarations.
- [x] 8.5 Адаптировать interrupt и runtime lifecycle для module initialization и async continuations; проверить завершение бесконечного цикла и цепочки продолжений с отличимой interrupt error.
- [x] 8.6 Сохранить TS source maps и runtime stack traces через все lowering stages; проверить исключения в class initializer, decorator, async function и импортированном модуле с исходными TS-позициями.
- [x] 8.7 Проверить изоляцию module instances и host views между runtime; добавить cross-runtime tests с одним Program и разными host implementations, включая параллельные независимые runtime.
- [x] 8.8 Описать стандартную среду, host-capability policy, правила interruption и goroutine ownership; проверить, что документация совпадает с default environment и примерами управления runtime.
- [x] 8.9 Реализовать Float16Array, DataView half-precision operations и Math.f16round из обязательной standard library; проверить precision, NaN/infinity, signed zero, fixture `float16` и описать числовые правила.
- [x] 8.10 Реализовать SharedArrayBuffer и связанные операции хранения/роста буферов; проверить byte lengths, views, границы доступа, fixture `shared-memory` и документировать controlled sharing через Go API.
- [x] 8.11 Реализовать Atomics и ожидание/уведомление для Go runtime agents; проверить атомарные операции, wait/notify, прерывание ожидания приложением и отсутствие автоматически создаваемых host workers; добавить conformance fixtures и документацию.
- [x] 8.12 Реализовать WeakRef и поддержку допустимых weak keys; проверить удержание в пределах job, deref, fixture `weak-reference`, GC-sensitive поведение и документировать lifetime semantics.
- [x] 8.13 Реализовать FinalizationRegistry и доставку cleanup callbacks через управляемый runtime lifecycle; проверить register/unregister, held values, fixture `finalization` и отсутствие обещаний детерминированного GC в документации.
- [x] 8.14 Реализовать Intl.Collator и Intl.NumberFormat со встроенными locale assets; проверить сравнение, formatting/options и соответствующие declarations, расширить fixture `intl` и описать locale data policy без OS/network fallback.
- [x] 8.15 Реализовать Intl.DateTimeFormat с необходимыми timezone/locale assets; проверить UTC и поддерживаемые зоны, options и ranges по conformance fixtures, документировать использование clock/timezone данных.
- [x] 8.16 Реализовать Intl.PluralRules и Intl.RelativeTimeFormat; проверить locale selection, categories и formatting по отдельным fixtures и обновить документацию стандартного профиля.
- [x] 8.17 Реализовать Intl.Locale, DisplayNames и ListFormat; проверить canonicalization/options и observable outputs по отдельным fixtures и описать соответствующие API.
- [x] 8.18 Реализовать Intl.Segmenter и Intl.DurationFormat обязательного baseline; проверить segmentation/formatting/options и соответствие declarations по отдельным fixtures и документации.
- [x] 8.19 Реализовать Array.fromAsync; проверить async/sync iterables, array-like input, последовательное await/mapping, iterator closing, errors и constructor semantics через checked TS fixtures.
- [x] 8.20 Реализовать resizable ArrayBuffer и length-tracking/fixed-length views; проверить resize/maxByteLength/resizable, out-of-bounds recovery, detach/transfer и побочные эффекты conversions по fixtures.
- [x] 8.21 Завершить стандартный JSON parser и reviver context; проверить сохранение UTF-16, signed zero, числовое переполнение, duplicate properties, source text для primitive reviver values и raw JSON.
- [x] 8.22 Реализовать DisposableStack и AsyncDisposableStack; проверить use/adopt/defer/move, brand/state checks, reverse order, suppression и последовательное async disposal.
- [x] 8.23 Завершить современный RegExp baseline: indices, Unicode sets и string properties, inline modifiers, duplicate named groups в допустимых альтернативах; проверить declaration/runtime parity и отдельные conformance fixtures.
- [x] 8.24 Исправить Date calendar arithmetic на полном допустимом numeric range; проверить нормализацию переполненных компонентов, TimeClip boundaries, timezone conversion и отсутствие Go integer overflow.

## 9. CLI и удаление старых обходов

- [x] 9.1 Перевести CLI на TS-проект и новый compiler с форматированием diagnostics и ненулевым exit status при ошибке; проверить, что ошибочный проект не выполняется, а valid in-memory embedding example продолжает работать.
- [x] 9.2 Удалить автоматические `goja_nodejs`, console, `load` и `readFile` registrations и неиспользуемую зависимость; проверить отсутствие этих имён, process, timers и require в default environment и работу явно зарегистрированного logging API.
- [x] 9.3 Удалить либо изолировать старые parser/AST/compiler paths после завершения новой цепочки; проверить отсутствие достижимого публичного пути исполнения непроверенного JS и адаптировать полезные VM regression tests к TS entry points.
- [x] 9.4 Переписать README и CLI/embedding-примеры под TS-only contract, pure Go и host registry; проверить все документированные команды на собранном CLI и обновить сведения о зависимостях/лицензиях.

## 10. Интеграционная готовность и выпуск

- [x] 10.1 Запустить полную type-diagnostic compatibility matrix относительно зафиксированного stable frontend; проверить отсутствие необъяснённых различий при одинаковых source snapshots, declarations и options.
- [x] 10.2 Запустить runtime/module/standard-library compatibility matrix и relevant adapted regressions; проверить закрытие каждого обязательного пункта матрицы и отсутствие runtime-конструкций, поддерживаемых только checker.
- [x] 10.3 Проверить standalone-приложение, собранное с `CGO_ENABLED=0`, без Node.js/tsc в PATH, дочерних процессов, сети и implicit filesystem resolution; подтвердить полный цикл Check -> Compile -> Run с in-memory sources.
- [x] 10.4 Выполнить `CGO_ENABLED=0 go test ./...` и race checks подходящих Go packages; проверить отсутствие новых failures, shared mutable Program state и несовместимых default declarations.
- [x] 10.5 Проверить воспроизводимость compiler assets и release manifest, актуальность stable baseline и лицензионных notices; выпускать breaking release с точной заявленной версией только после завершения всех обязательных compatibility checks.
