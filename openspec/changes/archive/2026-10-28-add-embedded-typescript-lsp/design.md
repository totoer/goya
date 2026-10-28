# Design

## Context

Мотивация и согласованная граница паритета описаны в `proposal.md`. Наблюдаемая основа проекта:

- `typescript.go` экспортирует `Project`, `SourceResolver`, `CompileOptions`, `Diagnostic` и независимый `Check`.
- `internal/tsfrontend/project.go` получает один снимок resolver, добавляет host declarations, нормализует настройки и строит native compiler program. `Analysis` создаётся только при отсутствии error-диагностик, а `Lower` требует проверенное состояние. Это граница исполняемого кода, которую редакторская служба не должна обходить.
- `internal/tsfrontend/files.go` реализует виртуальную read-only FS без OS fallback; имена с `:` запрещены. URI клиента поэтому требуют отдельного отображения.
- `Environment.TypeDeclarations()` и `SchemaID()` уже предоставляют отделённую от runtime сторону Go API. `Registry.Freeze` сохраняет окружение неизменяемым.
- `internal/tsfrontend/cache.go` кеширует успешные анализы по полному содержимому; это не сессия редактирования и не инкрементальный language service.
- Импортёр выбирает closure пакетов `compiler`, `bundled`, `parser`, `checker`, `ast`. Пакетов `ls`, `project`, `lsp` в локальном импорте пока нет. Импорт воспроизводится по archive digest и `files.lock.json`.
- В закреплённом upstream commit `2bd066d87f5bafd315be9f40889d0a60b9e58e0b` присутствует native LSP с передаваемыми FS/Reader/Writer, project session, completion, навигацией, форматированием, inlay hints, code lens, code actions и semantic tokens. Его список capabilities не доказывает паритет: в `internal/lsp/server.go` отсутствует слой команд `workspace/executeCommand` эталона и не объявлен refactor kind.
- Тесты `internal/tsfrontend/project_test.go`, `files_test.go`, `typescript_*_test.go` уже проверяют снимки, диагностику Unicode, отсутствие OS fallback и изоляцию; `goja/project.go` показывает, что чтение диска принадлежит приложению.

Источники сравнения:

- [typescript-language-server v6.0.2, обработчики](https://github.com/typescript-language-server/typescript-language-server/blob/v6.0.2/src/lsp-connection.ts).
- [Capabilities и команды эталона](https://github.com/typescript-language-server/typescript-language-server/blob/v6.0.2/src/lsp-server.ts).
- [Настройки и расширения](https://github.com/typescript-language-server/typescript-language-server/blob/v6.0.2/docs/configuration.md) и [документированные команды](https://github.com/typescript-language-server/typescript-language-server/blob/v6.0.2/README.md).
- [Native LSP закреплённого frontend](https://github.com/microsoft/typescript-go/blob/2bd066d87f5bafd315be9f40889d0a60b9e58e0b/internal/lsp/server.go).

## Goals / Non-Goals

**Goals:**

- Одна модель проекта и один механизм редакторских операций для прямых Go-вызовов и LSP.
- Повторное использование закреплённой реализации языка с локальным слоем совместимости и измеримым закрытием пробелов.
- Явная принадлежность исходников, окружений, ресурсов и применения правок встраивающему приложению.
- Независимые редакторские снимки и проверенные исполняемые программы с общими правилами языка.

**Non-Goals:**

- Переписывание checker или ослабление `Compile` ради незавершённого кода.
- Публичная зависимость приложения от внутренних AST/checker/project типов upstream.
- Смена закреплённой версии TypeScript, автоматическое обновление upstream или отдельный JavaScript language service. Остальные границы перечислены в proposal.

## Decisions

### 1. Публичная библиотека с общим ядром

Добавить `github.com/dop251/goja/lsp` с типизированной сессией и сервером; публичные DTO протокола разместить в `lsp/protocol`. Внутреннюю адаптацию разместить в `internal/tsfrontend` и его служебных подпакетах. Направление зависимостей: `lsp` использует корневой `goja` для существующих `Project`/`Environment`/`CompileOptions`; корневой пакет не импортирует `lsp`. Upstream-типы не входят в публичные сигнатуры. DTO могут генерироваться воспроизводимо, но принадлежат публичному фасаду проекта, а не нестабильной upstream API.

```text
Go application                 LSP client
      |                             |
      v                             v
Typed session API           JSON-RPC over supplied IO
      |                             |
      +--------------+--------------+
                     |
                     v
          Shared language service session
                     |
          +----------+-----------+
          |                      |
          v                      v
  Snapshots + host types    Native ls/project
          |                      |
          +----------+-----------+
                     |
                     v
          Pinned TypeScript frontend
```

Сессия предоставляет жизненный цикл документов, обновление проекта/окружения, методы для каждой строки матрицы ниже и события диагностики/прогресса. Прямые методы принимают `context.Context`, возвращают типизированные результаты и ошибки и не требуют JSON-RPC initialize, сериализации или клиента редактора. LSP-обработчики вызывают те же операции; команды преобразуются в типизированные действия. Server обслуживает переданные `io.Reader`/`io.Writer`, не владеет глобальными stdin/stdout и не завершает процесс. Сессией, переданной извне, владеет вызывающая сторона; явно созданной сервером сессией владеет сервер.

Альтернатива — отправлять JSON самому себе через in-memory connection. Она скрывает API за протоколом, усложняет обработку ошибок и не удовлетворяет прямому вызову редакторских операций.

### 2. Расширение закреплённого импорта

Расширить closure импортёра пакетами `ls`, `project`, `lsp` и их фактическими зависимостями из того же archive. Сохранить существующую проверку исходных/локальных digest, notices и запрет незаметной замены файлов. Добавить новые assets/dependencies в manifest и инвентари лицензий. Runtime build использует committed tree без сети и внешних генераторов.

Механическое перемещение imports остаётся отдельной адаптацией; необходимые изменения upstream FS/watchers, конфигурации и dispatch фиксируются как именованные воспроизводимые patches с исходным commit. Адаптер отключает ATA, npm, внешние процессы, watchdog родительского процесса и OS watchers; изменения каталога поступают из host API либо LSP file events. Вызов upstream со своей FS не считается достаточным доказательством отсутствия прочих OS-доступов: это проверяется отдельными fixtures.

Альтернативы — внешний `typescript-language-server` и ручная реализация всех алгоритмов поверх checker. Первый требует Node/process, второй дублирует существенную часть TypeScript language service. Native upstream лучше соответствует архитектуре, но его пробелы закрываются в рамках этого change.

### 3. Снимки, проекты и конфигурация

Сессия хранит базовый каталог из `Project.Files` и одного `Resolver.Snapshot` на явное обновление базы, открытые overlays и замороженный `Environment`. Resolver не вызывается на каждый hover и не перечитывается внутри одного запроса. Overlay перекрывает базовый файл даже при повторном snapshot resolver; закрытие документа снимает overlay и возвращает актуальную базу. Save сам по себе не пишет на диск: хост сообщает принятую базовую версию. Один запрос удерживает неизменяемый снимок до завершения.

Ключ актуальности включает generation сессии, содержимое/версии файлов, effective options, `Environment.SchemaID`, compiler baseline и editor preferences. Замена среды или настроек атомарно создаёт новую generation и инвалидирует зависимые результаты, auto-import indexes и resolve tokens. Сохраняется независимое состояние разных сессий при совпадающих путях.

Поддержать configured и inferred TS-проекты в предоставленном каталоге, package metadata, project references и workspace symbols/navigation между проектами. Конфигурации/`extends` читаются только из каталога. Явные настройки host имеют приоритет над конфигурацией проекта; текущие strict/ES2025/отсутствие DOM и Node служат default. Общий normalizer настроек и virtual resolver используется совместно с compilation frontend. Сессия экспортирует effective `Project`/options/environment snapshot для вызова `Check`/`Compile` над ровно теми же входами; существующий `Check` не начинает сам искать `tsconfig`.

Согласованное уточнение: добавить необязательное `Project.ConfigFile`, содержащее каноническое имя конфигурации внутри `Files`/снимка resolver. Пустое поле оставляет прежний способ выбора roots/options. Непустое поле явно включает разбор данного config и его `extends`/references без OS fallback. Host options перекрывают config options, а defaults применяются к отсутствующим настройкам. Явные `EntryPoints` выбирают исполняемые roots; при их отсутствии используются roots выбранной конфигурации. Declarations окружения включаются при обоих режимах.

`ProjectSnapshot(ctx, name)` экспортирует выбранный проект с `ConfigFile`, текущими исходниками/overlays, host options и замороженным Environment. Обычный `Snapshot` сохраняет исходный явно переданный ConfigFile. Config path и содержимое полного config graph участвуют в analysis identity и наследуются dynamic analysis. Redirects от ожидаемых declaration outputs к referenced TS source нормализуются в исходные module IDs как в checker, так и в execution IR: существование `.d.ts` на диске и отдельный build не требуются.

Ошибки синтаксиса/типов остаются диагностикой снимка; они не уничтожают native program, нужный language service. Сессия не создаёт `Analysis` с признаком checked и не возвращает редакторский AST для исполнения. Невалидная конфигурация даёт явную диагностику; доступные синтаксические операции продолжают работать, а недоступный семантический запрос возвращает объяснимый результат/ошибку.

Альтернатива — использовать только успешный `Analyze` и существующий глобальный cache. Она не обслуживает обычное состояние набираемого кода и не обеспечивает управление независимыми сессиями.

### 4. URI, позиции и объявления host API

Ввести session-owned двустороннее соответствие document URI и канонических виртуальных путей. `file:` URI — идентификатор, а не разрешение читать диск. Поддержать предоставленные приложением виртуальные схемы, пробелы, escaping и отображение Windows URI; запретить коллизии идентичностей. URI каталога/проекта и library/host declarations отображаются детерминированно.

Публичные редакторские позиции — zero-based UTF-16; LSP гарантирует default UTF-16, другие кодировки объявляются только при реализованной конвертации. Compiler diagnostics остаются с текущими one-based UTF-16 columns и UTF-8 offsets; перевод делается на границе без изменения существующего API. Fixtures покрывают emoji, CRLF, позиции на концах строк и диапазоны нескольких файлов. EOF всегда соответствует концу исходного текста: после завершающего CRLF/LF/CR это начало следующей строки. LSP считает U+2028/U+2029 обычными UTF-16 единицами текста, не разделителями строк. Для этих символов стабильные исходные координаты заменяют смешанные системы координат TLS; это второе явно согласованное исключение паритета. Зависимое от истории ScriptVersionCache представление TLS на предыдущей физической строке не воспроизводится; это явно согласованное исключение паритета для обоих интерфейсов.

Host declarations и встроенные libs видимы для hover/navigation как read-only virtual documents. API позволяет получить их текст; LSP предоставляет документируемый запрос чтения виртуального документа для клиентов, которым он нужен. Прикладной код отвечает за отображение документа. Rename пользовательских импортов/локальных aliases допустим, но редакторская правка не переименовывает замороженный экспорт Go API и не меняет library declarations.

### 5. Полная матрица редакторских операций

Зафиксировать baseline `typescript-language-server` v6.0.2; версия TypeScript в тестах эталона фиксируется отдельно. Таблица задаёт обязательные семейства, а реализационный inventory разворачивает их в каждую команду, настройку, refactor/quick-fix provider и variant из закреплённых исходников. Добавление строки не означает, что функция уже реализована.

| Семейство | LSP / расширение | Проверяемое поведение |
|---|---|---|
| Документы | open/change/save/close, incremental sync | Несохранённый dependency влияет на запросы; close возвращает базу |
| Диагностика | publishDiagnostics | Syntax/type/suggestion, related locations, tags, ignored codes, eager clear, удаление старых ошибок |
| Completion | completion + completionItem/resolve | Контекст, overloads, snippets, label details, commit characters, дополнительная вставка импортов |
| Hover | hover + verbosity extension | Типы, документация, JSDoc, диапазон, расширение представления типа |
| Сигнатуры | signatureHelp | Перегрузки, активный аргумент, trigger/retrigger |
| Определения | definition, typeDefinition, implementation | Cross-file targets, Location/LocationLink по capabilities |
| Исходное определение | `_typescript.goToSourceDefinition` | Переход от declarations к доступному исходнику |
| Ссылки | references, documentHighlight | Ссылки между файлами, корректные чтения/записи и declarations |
| Переименование | prepareRename, rename | Проверка имени, aliases, shorthand и многофайловые edits |
| Символы | documentSymbol, workspace/symbol | Иерархия, поиск, открытые/закрытые файлы и настроенные проекты |
| Структура | foldingRange, selectionRange | Корректные области и вложенные selections |
| Связанные диапазоны | linkedEditingRange | TSX opening/closing tags, поддержка незавершённого JSX |
| Иерархия вызовов | prepare/incoming/outgoing call hierarchy | Межфайловые вызовы и исходные диапазоны |
| Semantic tokens | full, range | Совместимые token types/modifiers и encoding |
| Inlay hints | inlayHint | Типы, имена параметров, значения enum и все применимые preferences |
| Code lens | codeLens + resolve | References/implementations, настройки видимости и клиентские команды навигации |
| Форматирование | formatting, rangeFormatting | Все применимые TS formatting options и запрос per-document configuration |
| Quick fixes | codeAction + resolve | Подходящие fixes, disabled reasons, preferred action, workspace edits |
| Общие исправления | source.fixAll.ts, source.removeUnused.ts | Все соответствующие providers эталона, а не один демонстрационный fix |
| Импорты | addMissingImports/removeUnusedImports/sortImports/organizeImports `.ts` | Все режимы, настройки и code actions on save |
| Рефакторинги | refactor kinds, `_typescript.applyRefactoring` | Полный применимый набор upstream refactors, выбор действия, создание файлов, последующее rename |
| Файлы/каталоги | willRenameFiles, `_typescript.applyRenameFile` | Пересчёт зависимых импортов, операции нескольких файлов |
| Completion action | `_typescript.applyCompletionCodeAction` | Выполнение дополнительных редакторских действий completion |
| Настройки/служебные события | configuration, preferences, `$/typescriptVersion` | Изменение editor settings, refresh, корректная версия встроенного языка |

Сохраняются идентификаторы редакторских команд и source action kinds эталона, чтобы клиент мог использовать существующие вызовы. `typescript.tsserverRequest`, `_typescript.configurePlugin` и настройки внешнего tsserver/npm исключены согласованной границей; их не объявлять как работающие операции. Клиентские `_typescript.selectRefactoring`, `editor.action.showReferences` и интерактивный выбор target file обрабатываются по capability/host hook, без разработки расширения редактора.

Выявленный пробел native server — команды/рефакторинги — реализуется отдельными типизированными операциями и переводом protocol аргументов эталона. Другие семьи используют native providers с адаптацией preferences/DTO. Полный импорт не подменяет реализацию отсутствующих providers; пустой ответ не является успешным parity fixture, если эталон возвращает применимое действие.

### 6. Правки и взаимодействия с клиентом

Запросы вычисляют edits над удерживаемым снимком. Прямой API возвращает структурированный результат с `WorkspaceEdit`, ожидаемой generation/версиями документов и, при необходимости, запросом выбора/последующей навигации. Создание/переименование файла описывается resource operation. Ядро не применяет edits к диску или исходному каталогу самостоятельно.

LSP-слой переводит результат в ответ/code action либо `workspace/applyEdit` для команд, которые этого ожидают. Ожидает подтверждения клиента; отказ, отсутствие capability или отмена дают явный результат и не меняют сессию. Новое состояние принимается только через документные/file events или host update. Интерактивный refactor доступен при соответствующей поддержке клиента; прямой API принимает типизированные аргументы выбора. Общий результат не зависит от наличия UI.

Сохранённые completion/action/lens handles связываются с сессией и generation, хранятся в ограниченном кеше. После изменения исходника/config/environment устаревший resolve/command возвращает content-modified/типизированную ошибку, а не применяет старые диапазоны. Для закрытых документов проверки generation дополняют версионные edits.

### 7. Протокол, планирование и ресурсы

LSP v3.17 — минимальная версия; более новые элементы используются только при согласованных capabilities. Реализовать JSON-RPC framing `Content-Length`, жизненный цикл initialize/initialized/shutdown/exit, request IDs, notifications, cancellation и двунаправленные запросы. Логи идут в переданный logger/соответствующие LSP events, не в поток протокола. Неподдерживаемый метод или некорректные параметры дают стандартную ошибку; malformed frame завершает только обслуживаемое соединение.

Изменения состояния применяются последовательно. Запросы работают с захваченными immutable snapshots, checker приобретается эксклюзивно по upstream pool contract. Отмена освобождает request-scoped ресурсы. Диагностика debounce/generation-aware: завершение старого запроса не публикует ошибку поверх нового состояния. Прямой API предоставляет те же diagnostic/progress events через host callback с отделённой доставкой; пользовательский callback не исполняется под блокировкой session state.

Настраиваемые limits покрывают retained snapshots/source bytes и resolve handles; активный запрос удерживает свой снимок до завершения. Очереди событий ограничены: superseded diagnostics допускают coalescing; критические ответы не теряются, backpressure снимается отменой контекста. `Close` идемпотентен, завершает фоновые задачи/ожидания и освобождает retained state; вызовы после закрытия получают отличимую ошибку. Server не закрывает чужие потоки и не вызывает `os.Exit`.

### 8. Проверка паритета как release gate

Добавить машинно читаемый inventory `docs/typescript-lsp-parity.json` и fixtures `testdata/lsp/`. Каждая применимая возможность, настройка и provider baseline связывается с реализацией и проверкой через direct API и LSP; до реализации статус честно остаётся pending. Финальный gate требует отсутствие pending/unsupported внутри согласованной границы. Неприменимость допустима только для исключённых Node/JS механизмов либо действительно отсутствующей в закреплённой версии языковой конструкции с обоснованием, а не как обход отсутствующей редакторской функции.

Golden fixtures фиксируют входной каталог, декларации, effective options, edit sequence и ожидаемые результаты/edits. Сбор эталонных результатов может быть отдельной development-only процедурой с явно установленным TLS/TypeScript; он не входит в offline runtime/test path и не запускает установки автоматически. Для конструкций, общих версиям языка, сравнивать с baseline TLS; для специфики закреплённого TS дополнительно использовать native language service того же commit. Нормализация удаляет только нерелевантные различия URI/порядка независимых наборов, сохраняя meaningful ordering, диапазоны, типы и изменения текста. Согласованные исключения для диапазонов — явно включённая в fixture проекция legacy EOF TLS в канонический конец текущего исходника и исправление смешанных координат TLS при U+2028/U+2029. Для Unicode-проекции fixture MUST хранить записанные исходные UTF-16 spans закреплённого oracle и SHA-256 текущего текста; ожидаемые диапазоны и позиции команд вычисляются только из этих spans и исходника, не из ответа реализации. Типы, порядок, количества, имена, текст и семантические результаты MUST сохраняться; другие позиции не нормализуются. Сырые oracle-ответы и provenance сохраняются; другие позиции, порядок и текст не изменяются. Для folding без endCharacter проекция требует явного записанного EOF textSpan с проверкой его соответствия исходнику, а не предположения по номеру строки. Для code-action edits с концом в EOF (включая вставки) fixture хранит отдельный oracle source span, SHA-256 и индексы action/document/edit; проекция меняет только точные EOF-координаты WorkspaceEdit и соответствующего one-based command metadata, сохраняя текст правок, порядок и остальные поля.

После применения edits к копии snapshot проверять полученный текст и `Check`; форматирование также проверяется точным ожидаемым текстом. Отдельно проверить negative/no-result cases, синтаксические ошибки, несохранённые зависимости, TSX, `.mts`/`.cts`, declaration/source maps, обновление Environment и configured/inferred projects. Импорт/pure-Go/no-external-IO проверки дополняются protocol transcript, direct-vs-wire и race/isolation tests.

## Risks / Trade-offs

- [Native upstream не содержит всех refactors/команд эталона] → Отдельные реализационные задачи, полный provider inventory и блокирующие fixtures; импорт сервера не считается завершением change.
- [Большой dependency closure и скрытые OS/npm hooks] → Воспроизводимый import, явные отключённые hooks, dependency/license review и автономные embedding tests.
- [Различные версии TypeScript у TLS и интерпретатора] → Разделение feature contract и language semantics, закреплённые oracle versions, объяснённые version-specific fixtures без ослабления полноты.
- [Утечки compiler/AST в публичное API] → Принадлежащие проекту DTO и адаптеры, external-package compile tests.
- [Инкрементальные кеши удерживают проекты или stale handles] → Ограничения retained state, generations, cancellation/close/race tests и отдельные замеры повторных правок.
- [Клиент не реализует интерактивные расширения] → Capability-aware действия и типизированный direct API; базовые LSP-возможности работают без editor-specific кода.
- [Расхождение editor configuration и Compile] → Общий normalizer/resolver, экспорт effective snapshot и сравнительные диагностики на одинаковых входах.

## Migration Plan

1. Зафиксировать inventory/fixtures и расширить pinned import с сохранением текущих compilation/runtime regression gates.
2. Добавить общий session layer и прямой API, затем подключить protocol facade и слой совместимости команд.
3. Закрыть все строки parity inventory, опубликовать примеры прямых вызовов и обслуживания переданных потоков; отдельный редакторский клиент/CLI не нужен.
4. Выполнить importer verification, offline `CGO_ENABLED=0` build/tests, parity/protocol checks и race tests на поддерживаемой race-конфигурации. Замерить repeated-edit workloads и документировать пределы кеша без выдуманного SLA.
5. При откате убрать новые public packages/adapters/import additions как единый change; форматы данных приложения и текущие программы не мигрируют, так как сессии живут в памяти. Текущие `Check`/`Compile` остаются доступными.

Согласованное расширение исключения EOF: при переносе в существующий файл без statements (пустой либо содержащий только whitespace/comments) TS 6 возвращает вставку в `EOF+1`. Встроенный API и LSP используют фактический EOF исходника. Fixtures сохраняют сырой ответ, независимо записанные TS6 span `start=SourceFile.end+1,length=0`, `SourceFile.end`, число statements и SHA-256. Проекция применяется только к точной вставке в выбранный target команды `Move to file`; текст, порядок edits и другие позиции сохраняются.
