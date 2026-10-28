# typescript-language-service Specification

## Purpose

Определяет встраиваемые редакторские операции над виртуальными TypeScript-проектами с теми же правилами языка и объявлениями Go API, которые используются при проверке и компиляции.

## Requirements

### Requirement: Direct embedded editor API
Система SHALL предоставлять типизированный публичный Go API всех редакторских операций внутри процесса при `CGO_ENABLED=0`. Вызовы MUST работать без LSP-соединения, JSON-сериализации, VM, Node.js, дочерних процессов, сети и выполнения пользовательского кода или Go-привязок.

#### Scenario: Direct operation without a server
- **WHEN** Go-приложение предоставляет исходники из памяти и запрашивает completion без создания LSP-сервера или runtime
- **THEN** оно получает типизированные результаты без внешних процессов, сетевых операций и вызовов зарегистрированных функций

### Requirement: Consistent compiler and host environment
Редакторская сессия MUST использовать закреплённую версию языка, стандартные объявления, effective options и host declarations, согласованные с `Check`/`Compile`. Система SHALL предоставлять effective snapshot для проверки тех же входов. Замена окружения или настроек MUST инвалидировать зависимые результаты без изменения других сессий.

#### Scenario: Registered Go API changes
- **WHEN** приложение заменяет окружение с `add(number, number): number` на окружение с `add(string, string): string`
- **THEN** новые hover, completion и диагностика отражают новый контракт, а проверка экспортированного снимка возвращает согласованные ошибки типов

#### Scenario: Standard environment boundary
- **WHEN** сессия создана с настройками по умолчанию и без внешних declarations
- **THEN** подсказки используют стандартную среду проекта и не добавляют DOM, Node.js, console или timers

### Requirement: Versioned virtual document snapshots
Сессия SHALL поддерживать базовый каталог, открытие/изменение/сохранение/закрытие документов и несохранённые overlays. Каждый запрос MUST использовать согласованный снимок. Обновление базы MUST сохранять открытые overlays; close SHALL возвращать актуальную базу. Чтение исходников MUST ограничиваться предоставленным каталогом и resolver.

#### Scenario: Unsaved dependency and refreshed base
- **WHEN** импортируемый файл изменён в открытом документе, после чего приложение обновляет базовый каталог
- **THEN** запрос из импортирующего файла использует несохранённый текст, а после close использует новый базовый текст

#### Scenario: Request snapshot stability
- **WHEN** исходники обновляются во время выполнения запроса
- **THEN** результат запроса относится к одному захваченному снимку, а не к смеси старых и новых файлов

### Requirement: Virtual project configuration
Сессия SHALL обслуживать configured/inferred TS-проекты, применимые project references и package metadata внутри предоставленного каталога. Явные настройки host MUST иметь приоритет над project configuration; отсутствующие настройки SHALL использовать compiler defaults проекта. Ошибки конфигурации MUST возвращаться как диагностика без OS fallback.

#### Scenario: Configured projects in memory
- **WHEN** каталог содержит два связанных TS-проекта и их конфигурации с `extends` и references
- **THEN** запросы используют соответствующие настройки, находят доступные межпроектные символы и не читают конфигурации за пределами каталога

### Requirement: Editing incomplete programs
Система MUST предоставлять доступные редакторские результаты при синтаксических и типовых ошибках в TS/TSX. Ошибки MUST оставаться диагностикой и MUST NOT превращать редакторский снимок в проверенную исполняемую программу или обходить обязательную проверку `Compile`.

#### Scenario: Incomplete member expression
- **WHEN** документ заканчивается незавершённым обращением к свойству объекта
- **THEN** completion предлагает применимые свойства, диагностика отражает ошибку, а компиляция ошибочного снимка не создаёт программу

### Requirement: Structured editor diagnostics
Система SHALL возвращать синтаксические, семантические и suggestion-диагностики с кодами, severity, исходными диапазонами, tags и related locations. Фильтры editor diagnostics MUST влиять только на отображение; они MUST NOT ослаблять обязательные ошибки проверки/компиляции.

#### Scenario: Editor suppression and compile gate
- **WHEN** приложение скрывает определённый код в редакторской диагностике, а проект содержит соответствующую ошибку типов
- **THEN** отображаемый список учитывает фильтр, но `Compile` того же снимка по-прежнему отклоняет ошибочный проект

### Requirement: Completion and completion resolution
Система SHALL предоставлять контекстное completion и resolve с типами, документацией, snippets, деталями labels, commit characters и дополнительными edits для auto-imports. Результаты MUST учитывать применимые preferences эталона, host API и несохранённые exports других файлов.

#### Scenario: Auto import from an unsaved module
- **WHEN** другой открытый файл добавляет экспорт, а пользователь запрашивает и разрешает его completion
- **THEN** результат содержит актуальный символ, сведения о нём и правку корректного импорта для текущего проекта

### Requirement: Hover information and verbosity
Система SHALL предоставлять hover с типом, документацией/JSDoc и исходным диапазоном. Она MUST поддерживать применимое расширение verbosity эталона с признаком возможности дальнейшего раскрытия типа.

#### Scenario: Expanded alias information
- **WHEN** запрос hover над сложным type alias увеличивает уровень verbosity
- **THEN** возвращается более подробное представление типа и корректный признак возможности дальнейшего раскрытия

### Requirement: Signature assistance
Система SHALL возвращать сигнатуры, документацию параметров, применимую перегрузку и активный параметр с учётом позиции и причины вызова signature help.

#### Scenario: Overloaded host call
- **WHEN** пользователь перемещается между аргументами перегруженной объявленной host-функции
- **THEN** signature help выбирает применимую сигнатуру и активный параметр без вызова функции

### Requirement: Definition and source navigation
Система SHALL предоставлять definition, type definition, implementation и source definition между доступными файлами/проектами. Цели в host declarations и стандартных библиотеках MUST быть доступны как read-only virtual documents с получаемым текстом.

#### Scenario: Navigation to a registered function
- **WHEN** пользователь запрашивает определение зарегистрированной Go-функции
- **THEN** результат указывает на её виртуальное TypeScript-объявление, текст которого доступен приложению

#### Scenario: Source behind a declaration
- **WHEN** доступные declarations и metadata связывают символ с исходным TS-файлом
- **THEN** source definition возвращает исходную позицию, а обычная навигация сохраняет подходящие declaration targets

### Requirement: References and document highlights
Система SHALL находить ссылки на символ с управлением включением declaration и возвращать document highlights с корректным различением применимых чтений/записей. Поиск MUST учитывать несохранённые файлы и доступные связанные проекты.

#### Scenario: Cross-file references
- **WHEN** символ импортирован и используется в нескольких файлах, один из которых изменён в overlay
- **THEN** поиск возвращает актуальные исходные диапазоны ссылок, а highlights относятся к запрошенному документу

### Requirement: Safe symbol rename
Система SHALL предоставлять prepare rename и многофайловые rename edits с проверкой допустимости имени и семантики aliases/shorthand. Read-only host/library declarations MUST NOT изменяться; переименование локального alias SHALL оставаться доступным.

#### Scenario: Rename local alias of a host export
- **WHEN** пользователь переименовывает локальный alias импортированного host-символа
- **THEN** edits обновляют локальные употребления и сохраняют имя экспорта и замороженный host-контракт

### Requirement: Document and workspace symbols
Система SHALL возвращать иерархические символы документа и поиск символов workspace по доступным проектам, включая закрытые файлы и актуальные открытые overlays.

#### Scenario: Workspace symbol from a closed file
- **WHEN** приложение ищет имя символа, объявленного в закрытом файле каталога
- **THEN** результат содержит символ и его актуальную исходную позицию

### Requirement: Structural editor ranges
Система SHALL предоставлять folding ranges, вложенные selection ranges и linked editing ranges. Для поддерживаемого TSX linked editing MUST связывать соответствующие открывающие и закрывающие теги, включая доступные результаты при незавершённом вводе.

#### Scenario: Linked TSX tags
- **WHEN** позиция находится в имени парного TSX-тега
- **THEN** linked editing возвращает диапазоны связанных имён, а selection/folding сохраняют исходные границы конструкции

### Requirement: Call hierarchy
Система SHALL предоставлять подготовку иерархии вызовов и входящие/исходящие вызовы с исходными диапазонами между доступными файлами.

#### Scenario: Imported function call hierarchy
- **WHEN** функция вызывает импортированную функцию из другого файла
- **THEN** outgoing hierarchy первой и incoming hierarchy второй отражают вызов с корректным исходным диапазоном

### Requirement: Semantic token information
Система SHALL предоставлять semantic tokens для полного документа и диапазона с соответствующими типами и модификаторами токенов. Изменение исходника MUST отражаться в новых результатах без использования старых координат.

#### Scenario: Semantic tokens after an edit
- **WHEN** пользователь вставляет строку перед типизированным объявлением и повторяет запрос tokens
- **THEN** классификация сохраняет семантику символа и использует сдвинутые координаты текущего снимка

### Requirement: Inlay hint preferences
Система SHALL предоставлять inlay hints для параметров, типов и значений enum с применимыми настройками эталона, включая правила сокрытия совпадающих имён и типов.

#### Scenario: Parameter name hint configuration
- **WHEN** приложение переключает parameter-name hints с `none` на `literals`
- **THEN** новый результат показывает подсказки для подходящих литеральных аргументов и не сохраняет отключённые ранее результаты

### Requirement: Reference and implementation code lenses
Система SHALL предоставлять code lenses и resolve для references/implementations с актуальными целями и применимыми настройками включения и видимости.

#### Scenario: Resolve implementation count
- **WHEN** lens интерфейса разрешается в проекте с несколькими реализациями
- **THEN** результат содержит актуальное число и цели реализаций для перехода приложением

### Requirement: Document and range formatting
Система SHALL предоставлять форматирование документа и диапазона с применимыми настройками эталона: отступами, пробелами, переводами строк и semicolon preferences. Результат MUST быть набором edits без самостоятельной записи исходника.

#### Scenario: Formatting preferences
- **WHEN** приложение запрашивает форматирование с заданными отступами, CRLF и политикой semicolons
- **THEN** применение возвращённых edits к копии снимка даёт ожидаемый текст и сохраняет исходный каталог до явного host update

### Requirement: Complete quick fixes and source fixes
Система SHALL предоставлять code actions/resolve для полного применимого набора quick fixes и source fixes эталона, включая fix-all и remove-unused. Она MUST учитывать context filters, preferred/disabled actions и причины неприменимости, а не ограничиваться выбранными демонстрационными fixes.

#### Scenario: Fix all occurrences
- **WHEN** в документе несколько ошибок, для которых эталон поддерживает объединённое исправление
- **THEN** действие fix-all исправляет все применимые употребления и возвращает проверяемые edits

### Requirement: Import maintenance actions
Система SHALL предоставлять добавление отсутствующих импортов, удаление неиспользуемых импортов, сортировку и организацию импортов с режимами `All`, `SortAndCombine`, `RemoveUnused` и применимыми preferences эталона.

#### Scenario: Non-destructive import organization
- **WHEN** выбран режим `SortAndCombine`, а документ содержит неиспользуемый импорт
- **THEN** edits сортируют и объединяют импорты без удаления импорта, которое допустимо только в разрушающем режиме

### Requirement: Complete applicable refactoring operations
Система SHALL перечислять и выполнять весь применимый к поддерживаемому TS/TSX набор рефакторингов эталона. Она MUST поддерживать выбор действия, интерактивные аргументы, создание файлов и последующее rename/navigation как структурированные результаты, не требующие встроенного UI.

#### Scenario: Move selection to a supplied file
- **WHEN** приложение выбирает применимый move-to-file refactor и передаёт целевой виртуальный путь
- **THEN** результат содержит создание/изменение необходимых файлов, обновление импортов и последующие редакторские действия без самостоятельной записи на диск

### Requirement: File and directory rename edits
Система SHALL вычислять изменения импортов и ссылок при переименовании файлов и каталогов, включая многофайловые операции, с согласованной идентичностью модулей.

#### Scenario: Directory rename with import updates
- **WHEN** приложение сообщает планируемое переименование каталога с импортируемыми TS-модулями
- **THEN** возвращаются edits зависимых исходников, сохраняющие разрешение импортов после принятия переименования

### Requirement: Versioned editor edits and resolution handles
Edits, resolve handles и отложенные действия MUST относиться к конкретной сессии и снимку. После изменения влияющих исходников, окружения или настроек система MUST явно отклонять устаревшие действия. Вычисление результата MUST NOT самостоятельно менять базовый каталог или файловую систему.

#### Scenario: Stale completion resolution
- **WHEN** приложение пытается разрешить completion после замены окружения или исходника, к которому относится handle
- **THEN** получает отличимую ошибку устаревшего состояния вместо edits по старым диапазонам

### Requirement: Isolated cancellable bounded sessions
Сессии MUST изолировать изменяемое состояние, поддерживать отмену операций, настраиваемые ограничения удерживаемых снимков/источников/handles и идемпотентное закрытие. Закрытие MUST завершать принадлежащие сессии фоновые операции; последующие вызовы MUST возвращать отличимую ошибку.

#### Scenario: Independent session shutdown
- **WHEN** две сессии содержат одинаковые пути с разными исходниками, а одна отменяет запрос и закрывается
- **THEN** вторая продолжает возвращать собственные результаты, а первая освобождает ресурсы и отклоняет новые вызовы

### Requirement: Verified editor feature parity
Система SHALL покрывать все редакторские возможности `typescript-language-server` v6.0.2 для поддерживаемого TS/TSX. Полнота MUST подтверждаться inventory возможностей/preferences/providers и поведенческими fixtures через прямой Go API и LSP. Отсутствующая функция MUST NOT считаться покрытой из-за объявленной capability или пустого ответа.

#### Scenario: Applicable feature missing from the native provider
- **WHEN** эталон выполняет применимый TS-рефакторинг, а нативный provider пока его не реализует
- **THEN** parity gate остаётся незавершённым до реализации и прохождения соответствующего fixture через оба интерфейса

#### Scenario: Language version difference
- **WHEN** результат зависит от различия закреплённых версий TypeScript
- **THEN** fixture явно фиксирует версии и обоснованное ожидаемое поведение поддерживаемого языка, сохраняя требование наличия самой редакторской операции

#### Scenario: Stable source EOF coordinates
- **WHEN** TLS представляет EOF после завершающего перевода строки по-разному в зависимости от истории внутреннего кэша
- **THEN** прямой API и LSP MUST использовать каноническую UTF-16 позицию конца текущего исходника; это согласованное исключение паритета MUST быть явно обозначено в fixtures, сохраняющих сырые oracle-ответы и допускающих только узкую EOF-проекцию без изменения других диапазонов, порядка и текста edits; для координат text edits, заканчивающихся в EOF (включая вставки), в WorkspaceEdit и соответствующем command metadata fixture MUST дополнительно хранить независимо записанный oracle source span и SHA-256 исходника

#### Scenario: Stable LSP coordinates with Unicode separators
- **WHEN** исходник содержит U+2028/U+2029, а TLS смешивает compiler spans и подсчёт строк документа и возвращает пустые либо неверные диапазоны
- **THEN** прямой API и LSP MUST использовать исходные UTF-16 позиции со строками CRLF/LF/CR, считая U+2028/U+2029 обычными единицами текста; это согласованное исключение MUST быть явно обозначено в fixtures, сохраняющих сырые oracle-ответы, записанные исходные spans oracle и SHA-256 текста, без произвольной замены других результатов

#### Scenario: Stable insertion into a statement-free existing move target
- **WHEN** move-to-existing-file возвращает вставку `EOF+1` для существующего target без statements, включая пустой файл, whitespace или comments
- **THEN** прямой API и LSP MUST использовать фактический EOF; это согласованное исключение паритета MUST сохранять текст и порядок edits, сырой oracle-ответ, исходный TS6 span `start=SourceFile.end+1,length=0`, независимо записанные `SourceFile.end`/число statements и SHA-256; fixture MUST проверять statement-free текст и точное соответствие target команды `Move to file`, не проецируя другие диапазоны
