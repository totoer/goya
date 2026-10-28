# Design

## Context

Мотивация и границы изменения описаны в `proposal.md`. Это сквозная замена языкового frontend и контракта встраивания, поэтому данный design необходим.

Текущий путь в `runtime.go:1367` имеет вид `Parse -> compileAST -> Program`, а `RunString` и `RunScript` вызывают его перед VM. `Program` в `compiler.go` содержит инструкции и source map, но не информацию о проверке типов или требованиях к Go API. `Runtime.Set` связывает значения только с runtime. В `wrapReflectFunc` недостающие аргументы заполняются нулями, лишние игнорируются, завершающий Go `error` превращается в исключение. Такие правила нужно согласовать с новым статическим контрактом.

Есть дополнительные пути компиляции: `CompileAST`, `eval`, конструкторы `Function` / async function / generator function. Модули CLI подключаются через `goja_nodejs/require`; там же неявно предоставляются console, `load` и `readFile`. Собственной системы типов и полноценного ESM linker в текущем проекте нет.

На дату исследования 2026-10-24 npm `typescript@latest` указывает на 7.0.2; 7.1 является beta. Это исходная проверенная точка, а не разрешение брать beta или считать `main` стабильным. Перед реализацией фиксируется актуальный стабильный релиз и проверяется соответствие его опубликованному набору исходников.

Проверенные upstream-источники:

- [Метаданные стабильного пакета](https://registry.npmjs.org/typescript/latest).
- [TypeScript 7.0: Go implementation и отсутствие публичного API в 7.0](https://devblogs.microsoft.com/typescript/announcing-typescript-7-0/).
- [Upstream compiler и его internal-зависимости](https://raw.githubusercontent.com/microsoft/typescript-go/main/internal/compiler/program.go).
- [Upstream checker](https://raw.githubusercontent.com/microsoft/typescript-go/main/internal/checker/checker.go).
- [Upstream virtual filesystem](https://raw.githubusercontent.com/microsoft/typescript-go/main/internal/vfs/vfs.go).

Ссылки на `main` показывают устройство исходников; они не являются воспроизводимой зависимостью релиза. Исторический staging-репозиторий переносился, поэтому путь к стабильному source snapshot устанавливается по release metadata, а не по названию репозитория.

## Goals / Non-Goals

**Goals:**

- Отделить анализ проекта от исполняющей VM и предоставить стабильный Go facade поверх сопровождаемого upstream frontend.
- Использовать один снимок исходников и типов от проверки до генерации байткода.
- Сделать соответствие объявлений и host-привязок проверяемой частью запуска.
- Сохранить полезные реализации объектов, строк, чисел, исключений, async и прерывания из Goja, не связывая архитектуру с прежними API.

**Non-Goals:**

- Самостоятельное воспроизведение системы типов TypeScript и новая семантика типов, несовместимая с TypeScript.
- Node.js, DOM, npm installation, React или внешний JavaScript engine как обязательная часть библиотеки.
- Бинарная совместимость старого байткода, прежних Go API и старого JavaScript parser/AST.
- Гарантия отсутствия всех ошибок времени выполнения: разрешённые TypeScript `any`, assertions и доверенные `.d.ts` сохраняют обычные ограничения статической проверки.

## Decisions

### 1. Adapt the official Go frontend behind a local facade

Использовать контролируемую копию транзитивно необходимых upstream Go-пакетов, стандартных `.d.ts` и generated assets. Размещать её в `internal/tsfrontend/upstream/`; переписывать пути импортов при воспроизводимом обновлении, сохраняя package structure и выделенный patch set. Не использовать подмену module path проекта или `go:linkname` для обхода `internal`.

Facade в `internal/tsfrontend/` скрывает upstream-типы от публичного API. Он предоставляет анализ неизменяемого проекта, диагностики, информацию об исходниках и доступ к проверенному дереву для собственного backend. Адаптация сохраняет upstream binding, type relations, control-flow analysis и diagnostics; преобразование в наш IR выполняется после проверки.

Source manifest фиксирует точную версию, commit/release provenance, хеши исходников и библиотек, лицензии, список локальных патчей и контрольные тесты. Обновление upstream является явной операцией сопровождения. Сборка и работа готовой библиотеки не требуют npm, Node.js или генерации через внешние инструменты.

Альтернативы: собственный checker слишком дорог для полной и обновляемой совместимости; внешний `tsc` нарушает pure Go in-process требование; Go API новой beta не подходит вместо стабильного релиза.

### 2. Introduce immutable project and environment snapshots

Логические публичные сущности: `Project`, `CompileOptions`, `Environment`, `Diagnostic`, `Program`, `Runtime`. Точные имена Go-полей можно уточнить при реализации без изменения контракта.

`Project` содержит entry points и resolver приложения. `Environment` является зафиксированным результатом сборки реестра host API и деклараций. Resolver материализует один снимок канонических исходников и `.d.ts`; его содержимое не перечитывается между checking и lowering. Стандартные compiler assets встроены в библиотеку и читаются из внутренней read-only FS. Пользовательские файлы и package metadata доступны только через host resolver.

```text
Go registrations --> Registry builder --> Frozen Environment
                                            |           |
                                            v           v
                                       Declarations   Bindings
                                            |           |
Sources + Resolver --> Project snapshot --> Check        |
                                            |           |
                                      Checked analysis  |
                                            |           |
                                            v           |
                                       Lowering / IR    |
                                            |           |
                                            v           v
                                          Program --> Runtime
```

Ключ анализа включает версию frontend, compiler options, содержимое и канонические идентификаторы всего графа, standard-library profile и schema digest окружения. Повторное использование результатов разрешено только при совпадении ключа. Изменение resolver, mapper или host-типа создаёт новый снимок.

Альтернатива — брать типы из живого `Runtime.Set` — связывает компиляцию с изменяемым runtime и не позволяет надёжно проверять проект заранее.

### 3. Make checking a non-bypassable compilation stage

`Check(project, environment, options)` возвращает структурированные диагностики без создания VM и без вызовов host-функций. `Compile` использует тот же анализ; при любой error возвращает диагностики и отсутствие `Program`. Разрешены предупреждения без error. Проверяются compiler configuration, глобальные объявления, синтаксис, семантика и разрешение всего статического графа.

По умолчанию выбирается `strict: true` поддерживаемого релиза. Явные настройки strict-подпроверок сохраняют TypeScript-семантику, но не могут пропустить сам этап проверки. Настройки `noCheck`, `skipLibCheck`, `skipDefaultLibCheck` и аналогичные пропуски проверок отклоняются. TS-директивы подавления ошибок и явный `any` обрабатываются как в upstream: обязательная проверка не означает запрет допустимых средств языка. Никакой `transpile-only` режим не предоставляется. Не следует путать прежний `strict bool` Goja с новым набором TS compiler options.

`RunString` / `RunScript` становятся удобными обёртками того же пути. Старый публичный `CompileAST` удаляется либо заменяется операцией, которая заново выполняет полный анализ; публичного превращения непроверенного AST в `Program` не будет. `Program` имеет закрытое представление и создаётся только после успешного checking и backend validation.

Поддерживаемые динамические конструкторы исходников, включая `eval` и `Function`, также используют TS frontend. Прямая `eval` получает статическое окружение текущей позиции и runtime bindings; для этого программа хранит необходимые scope/type metadata. Новые исходники сначала проверяются, затем исполняются; ошибка становится catchable compilation exception с исходными diagnostics. Типы динамически полученных значений не угадываются по текущему содержимому VM. Семантика динамических путей покрывается отдельными conformance fixtures.

Альтернатива — проверять лишь публичный `Compile` — оставляет обход через convenience API, AST, динамическую загрузку и конструкторы функций.

### 4. Compile checked TypeScript directly to an execution IR

Новый backend получает checked TS AST, нормализует runtime-bearing конструкции и формирует внутренний IR с исходными позициями. Далее IR компилируется в инструкции Go VM. Промежуточная генерация JS-текста и его повторный разбор не используются.

Type-only объявления и импорты не создают runtime code. Enums, namespaces, parameter properties, class initialization, legacy/standard decorators, JSX, async/generators и resource management преобразуются с сохранением их эффектов и порядка. Frontend остаётся источником правил языка; корректность lowering проверяется fixtures с ожидаемым поведением, выведенным из эталонной компиляции соответствующей версии.

IR отделяет собственно операции выполнения от module bindings и host-call boundaries. Первоначально можно переиспользовать инструкции `compiler*.go` и `vm.go`, затем переписывать их без обязательства сохранить прежний формат. Новый opcode dispatch или оптимизация по типам не являются целью этой миграции: типы TypeScript не дают права менять наблюдаемую runtime-семантику.

Таблица возможностей связывает каждую runtime-конструкцию и standard-library member выбранного релиза с реализацией и fixture. До полного покрытия промежуточные сборки маркируются development и не объявляют полную поддержку версии. Неизвестный backend узел немедленно даёт собственную compilation diagnostic; молчаливое удаление или обход checking запрещены.

Альтернатива — сначала emitting JS и затем старый parser — упрощает первый прототип, но сохраняет дополнительную языковую границу и зависимость от возможностей старого JS frontend. Для выбранной целевой архитектуры используется прямой backend.

### 5. Generate host types and bindings from the same descriptor

Registry builder создаёт descriptor каждого глобального имени и host-модуля: TS-тип, Go-binding strategy, правила преобразования, видимость, mutability и schema version. Reflection поддерживает обычные Go-сигнатуры; явный TS-контракт и converter дополняют сложные типы и низкоуровневые callbacks. Явный контракт не превращает произвольную реализацию в доказанно корректную: адаптер проверяет границу вызова, приложение отвечает за смысл своей реализации.

Начальные правила bridge:

| Go representation | TypeScript contract / conversion |
| --- | --- |
| `bool`, `string`, `float64` | `boolean`, `string`, `number`; без строковых числовых coercions |
| `int8..int32`, `uint8..uint32` | `number`; при входе проверяются конечность, целочисленность и диапазон |
| `float32` | `number` только с явно выбранной политикой округления/точности |
| `int`, `uint`, `int64`, `uint64`, `uintptr` | Требуется политика: точный `bigint` либо проверяемый safe-number adapter; platform-dependent поведение запрещено |
| Go struct / array | Структурный object / фиксированный tuple; одинаковый mapper в checker и runtime |
| `[]T`, `map[string]T` | `T[] | null`, строковая index signature с учётом nullability; nil и пустое значение различаются |
| `*T` | `T | null`; циклы и recursive named types сохраняются |
| Variadic arguments | Rest parameter с проверкой каждого элемента |
| 0 / 1 / несколько результатов | `void` / один тип / tuple в порядке Go-результатов |
| Завершающий `error` | Исключение при ненулевой ошибке; в успешный TS-result не входит |
| `any` / interfaces / dynamic handlers | Явная schema или `unknown` с converter; автоматического fallback в `any` нет |

Структуры по значению и обычные коллекции передаются как копии с сохранением циклов в пределах одной конверсии. Указатели/host-объекты могут представляться живыми views; их descriptor отдельно задаёт доступность записи, mapper и identity. Default host-view поля readonly; изменяемый view регистрируется явно. На входе plain TS-объекта в Go создаётся и проверяется значение ожидаемого Go-типа. Неоднозначные aliases и нестандартные maps требуют явного adapter.

Адаптеры всегда проверяют фактические значения, даже если исходник прошёл checking: `any`, assertions, `.d.ts` и внешнее изменение Go-объектов не являются runtime-доказательством типа. Не допускаются прежние молчаливые обрезания лишних аргументов и заполнение обязательных параметров нулями. Для optional/variadic преобразований действует описанный контракт. Precision policy является частью schema digest.

Альтернативы: только ручные `.d.ts` легко расходятся с Go API; только reflection не описывает динамические handlers, overloads и неоднозначные числовые типы.

### 6. Separate module resolution, linking and instances

Frontend получает resolver через virtual FS и одинаковую таблицу canonical module IDs. Resolver возвращает TS/TSX source, declaration-only module или registered host module и явно предоставленные metadata; implicit host filesystem loader отсутствует. Specifier вроде `./util.js` может разрешаться в `util.ts` как обычный TS-проект: это не добавляет поддержку JS-входа.

Внутренний linker создаёт module records с live-binding cells, export/import relations, async initialization dependencies и состояниями initialization/error. ESM cycles и top-level await обрабатываются как граф, а не как последовательные вызовы `RunScript`. Import assignment/export assignment нормализуются для выбранных module options; требуемая interop-семантика реализуется внутри linker без Node.js registry и без глобального `require`.

Статический граф полностью проверяется до публикации `Program`. Declaration-only imports не требуют runtime implementation, type-only imports стираются. Ambient value declaration может успешно провериться без binding, но запуск соответствующего исполняемого графа обязан обнаружить отсутствие реализации до его инициализации.

Dynamic import обращается к тому же resolver. Если совместимого checked artifact нет, сначала проверяется и компилируется новый статический граф. Побочные эффекты его модулей до успешной проверки и preflight отсутствуют; ранее выполненный вызывающий код при ошибке динамического импорта не откатывается. Module instance cache принадлежит runtime; исходники и code могут разделяться, пользовательское состояние — нет.

Альтернатива — текущий внешний CommonJS registry — не обеспечивает TS-граф, live bindings, ESM cycles, top-level await и контроль доступных host-модулей.

### 7. Bind checked programs to compatible host contracts

`Program` хранит language/frontend version, options/profile identity, module code, source maps и manifest требуемых host-контрактов. Перед созданием module instances runtime сверяет manifest с зафиксированным окружением. Для каждого требуемого binding используется точное совпадение канонической schema и adapter policy; это консервативнее сложной runtime-проверки structural assignability. Дополнительные bindings допустимы.

Несовпадение или отсутствие привязки возвращается приложению до пользовательских инициализаторов. Разные экземпляры Go-реализации одного контракта допустимы. Hot replacement на другую сигнатуру требует нового Environment и повторной компиляции. Состояние объектов и модулей создаётся отдельно в каждом runtime. Скомпилированный code остаётся immutable.

Альтернатива — сравнивать лишь имена — позволяет выполнить проверенную программу против другой сигнатуры. Сравнение всего реестра целиком слишком строго блокирует дополнительные неиспользуемые регистрации.

### 8. Keep language builtins and external capabilities distinct

Встроенный default library profile содержит реализованные стандартные объекты языка соответствующего релиза; DOM и Node declarations в него не входят. Нельзя получить полную поддержку выбранной версии, лишь скрыв ещё не реализованные standard-language members из `.d.ts`: соответствующая поддержка входит в release gate. Пользовательские declarations для внешних возможностей не создают реализации автоматически.

Console, filesystem, network, process, timers и внешняя загрузка доступны через registry. Даже module loader не читает host FS без resolver приложения. CLI явно читает входной TS-файл как Go-приложение и передаёт проект библиотеке; это не экспортирует файловые возможности внутрь скрипта. Удаляются автоматические `goja_nodejs`, `load`, `readFile` и console registrations.

Текущий механизм interrupt адаптируется для новых module initialization и async paths. Стек и diagnostics ссылаются на исходные TS-позиции через IR source mapping. Runtime рассчитан на один исполняющий goroutine одновременно; разные runtime могут работать независимо, host-синхронизацией управляет приложение.

Альтернатива — включить Node declarations или runtime по умолчанию — создаёт недоступные при исполнении API и противоречит выбранной среде встраивания.

## Risks / Trade-offs

- [Upstream internal API и перенос репозитория] -> изолированный source snapshot с происхождением, минимальный patch set, facade и воспроизводимое обновление; не импортировать произвольный `main` как стабильный релиз.
- [Объём полной совместимости] -> отдельная матрица syntax/type/runtime/stdlib и release gate без исключения обязательных возможностей. Этапы реализации не объявляются законченным подмножеством продукта.
- [Checker совпадает, backend меняет поведение] -> runtime fixtures для всех TS transforms, module cycles, resource cleanup и async; сравнение с эталонными результатами.
- [Проверка пропущена по одному из путей] -> единый checked-analysis boundary и проверки всех публичных, AST и динамических entry points.
- [Расхождение Go declarations и реализации] -> общий descriptor, manifest preflight и проверки фактических значений на bridge.
- [Числовая точность и platform-dependent `int`] -> явная policy и граничные проверки вместо silent conversion.
- [Изменяемый resolver между проверкой и запуском] -> immutable snapshot и content-based keys; новое содержимое требует нового анализа.
- [Compiler latency / память] -> переиспользование неизменяемых снимков, cancellation и ограничиваемые caches; оптимизация после измерения representative проектов.
- [Pure Go ошибочно принимается за полную sandbox-гарантию] -> отсутствие неявных внешних возможностей проверяется явно; Go-приложение сохраняет ответственность за зарегистрированные функции и управление ресурсами.

## Migration Plan

1. Зафиксировать upstream baseline, manifest и матрицу соответствия; добавить frontend adapter и smoke fixtures, пока существующий путь остаётся внутренней основой миграции.
2. Реализовать Environment, typed registry, project snapshots и независимый `Check`; затем сделать `Compile` проверяемой границей.
3. Подключить новый IR/backend, bridge, module linker и необходимые VM/stdlib изменения; завершить все обязательные пути компиляции.
4. Перевести публичные операции и CLI на TS, убрать автоматические внешние возможности и старые обходы, обновить документацию с примерами Go embedding.
5. Пройти полную compatibility matrix, pure-Go build и release review. Только после этого заявлять поддержку точной стабильной версии.

Старые тесты сохраняются либо адаптируются, когда проверяют используемую семантику значений и VM; их прохождение не означает обещание совместимости JavaScript API. Изменение публикуется как breaking release. При отказе от миграции приложение остаётся на прежнем релизе; runtime fallback с TS на непроверенный JavaScript отсутствует. Эти артефакты описывают план: реализация данным change capture не выполняется.
