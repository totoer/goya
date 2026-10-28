# typescript-lsp Specification

## Purpose

Определяет публичный встраиваемый LSP-интерфейс языковой службы TypeScript, позволяющий подключать стандартных клиентов и собственные приложения к тем же операциям, которые доступны прямыми Go-вызовами.

## Requirements

### Requirement: Host supplied protocol transport
Сервер SHALL обслуживать LSP v3.17 или совместимый более новый протокол поверх предоставленных Go-приложением потоков чтения/записи с JSON-RPC и `Content-Length` framing. Он MUST NOT требовать конкретного редактора, отдельного процесса, глобальных stdin/stdout или сетевого listener.

#### Scenario: In-memory protocol connection
- **WHEN** приложение соединяет клиента и сервер потоками в памяти
- **THEN** клиент обменивается стандартными LSP-запросами без запуска внешнего сервера или настройки редактора

### Requirement: Connection lifecycle and ownership
Сервер MUST соблюдать initialize/initialized/shutdown/exit, изолировать состояние соединений и освобождать принадлежащие ему ресурсы при завершении. Он MUST NOT завершать процесс приложения или закрывать чужие потоки/сессии без явно переданного владения.

#### Scenario: Shutdown inside an embedding application
- **WHEN** один клиент выполняет shutdown/exit
- **THEN** соответствующее обслуживание завершается, а приложение, независимая сессия и другие соединения продолжают работать

### Requirement: Capability driven complete editor interface
Сервер SHALL предоставлять все операции `typescript-language-service` через соответствующие методы LSP и редакторские расширения эталона. Capabilities MUST отражать реализованное поведение и учитывать поддержку клиента, включая resolve, snippets, links, hierarchical symbols, refresh и интерактивные действия.

#### Scenario: Client without snippet or location-link support
- **WHEN** клиент не объявляет snippets и definition links
- **THEN** completion и navigation остаются доступны с поддерживаемыми клиентом представлениями и не требуют этих capabilities

#### Scenario: Advertised refactoring support
- **WHEN** сервер объявляет поддержку refactor code actions
- **THEN** применимый fixture возвращает и выполняет рефакторинг, а не только пустой список действий

### Requirement: Incremental synchronization and host catalog updates
Сервер SHALL обрабатывать open/change/save/close, последовательные incremental changes и доступные file events с сохранением версий документов. Неверные или устаревшие изменения MUST NOT повреждать состояние. Запись/rename на диске MUST оставаться ответственностью клиента/host.

#### Scenario: Ordered edits in one notification
- **WHEN** одна нотификация содержит несколько incremental changes
- **THEN** каждый диапазон применяется к результату предшествующего изменения и дальнейший запрос видит итоговый текст

#### Scenario: Outdated document version
- **WHEN** клиент присылает изменение с версией не новее принятой версии документа
- **THEN** сервер сохраняет актуальный текст и сообщает диагностируемую проблему синхронизации

### Requirement: Virtual URI and position fidelity
Сервер MUST сохранять двустороннюю идентичность URI и виртуальных исходников, включая предоставленные host схемы, escaped имена и file URI разных платформ. Zero-based UTF-16 MUST поддерживаться по умолчанию. Строки MUST разделяться CRLF/LF/CR; U+2028/U+2029 MUST считаться обычными UTF-16 единицами текста. Смешанные координаты TLS для этих символов MUST заменяться стабильными исходными координатами по явно согласованному и проверяемому через oracle spans исключению паритета. Навигация, диагностика и edits MUST использовать позиции исходного текста без OS-чтения по URI.

#### Scenario: Escaped virtual URI and Unicode position
- **WHEN** документ с пробелом в URI содержит emoji перед ошибочным аргументом и переводы строк CRLF
- **THEN** диагностика, hover и edits ссылаются на исходный URI и корректные UTF-16 диапазоны

#### Scenario: EOF independent of cache history
- **WHEN** исходник заканчивается переводом строки и запрос возвращает EOF-диапазон
- **THEN** сервер MUST вернуть начало следующей строки, совпадающее с прямым API, независимо от порядка открытия файлов и внутреннего кэша; legacy EOF TLS допускается только как явно обозначенная проекция oracle fixtures; для text edits с концом в EOF (включая вставки, результаты запросов и payload `workspace/applyEdit`) и их command metadata обязательны записанный исходный span oracle и SHA-256, без изменения текста или порядка правок

#### Scenario: Stable insertion into a statement-free existing move target
- **WHEN** move-to-existing-file возвращает вставку `EOF+1` для существующего target без statements, включая пустой файл, whitespace или comments
- **THEN** прямой API и LSP MUST использовать фактический EOF; это согласованное исключение паритета MUST сохранять текст и порядок edits, сырой oracle-ответ, исходный TS6 span `start=SourceFile.end+1,length=0`, независимо записанные `SourceFile.end`/число statements и SHA-256; fixture MUST проверять statement-free текст и точное соответствие target команды `Move to file`, не проецируя другие диапазоны


### Requirement: Shared direct and protocol operation behavior
Для одинакового снимка, настроек и эквивалентных client capabilities результаты LSP MUST соответствовать прямым Go-вызовам языковой службы. Протокольная обработка MUST NOT выполнять отдельную независимую проверку проекта или использовать другое окружение типов.

#### Scenario: Direct and protocol completion parity
- **WHEN** один fixture запрашивает completion/resolve через прямой API и через LSP
- **THEN** после перевода протокольных представлений символы, сведения и auto-import edits совпадают

### Requirement: Compatible editor commands and extensions
Сервер SHALL поддерживать редакторские команды эталона `_typescript.goToSourceDefinition`, `_typescript.applyRefactoring`, `_typescript.organizeImports`, `_typescript.applyRenameFile`, `_typescript.applyCompletionCodeAction`, уведомление `$/typescriptVersion` и hover verbosity. Команды MUST использовать те же типизированные операции ядра.

#### Scenario: Existing organize imports command
- **WHEN** клиент вызывает `_typescript.organizeImports` с документированным режимом
- **THEN** сервер вычисляет соответствующие edits и использует поддерживаемое клиентом применение правок с семантикой выбранного режима

#### Scenario: Embedded TypeScript version
- **WHEN** инициализированному клиенту отправляется `$/typescriptVersion`
- **THEN** версия соответствует встроенному frontend, а источник версии явно обозначает встроенное окружение

### Requirement: Source actions and editor configuration
Сервер SHALL сохранять применимые source action kinds эталона с суффиксом `.ts`, принимать применимые initialization preferences и dynamic configuration, включая formatting, completion, diagnostics, inlay hints и code lens. Per-document formatting configuration SHALL запрашиваться у поддерживающего клиента; без неё используются явные настройки и defaults.

#### Scenario: Code actions on save
- **WHEN** клиент запрашивает `source.addMissingImports.ts`, `source.removeUnusedImports.ts`, `source.sortImports.ts`, `source.organizeImports.ts`, `source.removeUnused.ts` или `source.fixAll.ts`
- **THEN** для применимого исходника возвращается соответствующее действие с корректными edits

#### Scenario: Per-document formatting settings
- **WHEN** клиент поддерживает workspace configuration и задаёт отдельные formatting preferences для документа
- **THEN** formatting учитывает настройки этого документа, а отмена/недоступность configuration запроса обрабатывается без зависания соединения

### Requirement: Version aware diagnostic publication
Сервер SHALL публиковать актуальные диагностики и очищать устранённые ошибки, учитывая editor filters и eager-clear preference. Результаты superseded снимков MUST NOT публиковаться поверх нового состояния. Применимые progress/refresh события SHALL учитывать capabilities клиента.

#### Scenario: Slow old diagnostic computation
- **WHEN** диагностика старой версии завершается после проверки исправленного документа
- **THEN** клиент сохраняет результат исправленной версии и не получает устаревшие ошибки как актуальные

### Requirement: Client mediated edits and interactive actions
Сервер MUST представлять правки и resource operations как WorkspaceEdit либо согласованные client requests. Отказ клиента, отсутствие нужной capability, отмена или устаревший снимок MUST NOT изменять проект. Интерактивные refactors и клиентские команды навигации SHALL выдаваться только при соответствующей поддержке клиента.

#### Scenario: Client rejects a refactor edit
- **WHEN** клиент отклоняет `workspace/applyEdit` для рефакторинга, создающего файл
- **THEN** сервер не создаёт файл, не изменяет каталог и возвращает явный неуспешный результат

#### Scenario: Client selects a refactor destination
- **WHEN** клиент объявляет поддержку interactive move-to-file и передаёт выбранный target file
- **THEN** сервер выполняет действие с переданным аргументом без самостоятельного показа UI или выбора пути

### Requirement: Virtual declaration content access
Сервер SHALL предоставлять документированный запрос получения текста read-only virtual documents для host declarations и встроенных библиотек, на которые указывает навигация. Содержимое MUST соответствовать окружению текущей сессии; доступ MUST NOT требовать записи assets на диск.

#### Scenario: Read a navigation target
- **WHEN** клиент запрашивает текст виртуального host declaration по URI, полученному из definition
- **THEN** получает объявление текущего окружения и признак read-only без создания файла в workspace

### Requirement: Cancellation and protocol failure isolation
Сервер MUST поддерживать отмену запросов, корректные request IDs и ошибки JSON-RPC/LSP для неверных параметров, неизвестных методов и устаревших результатов. Ошибка запроса MUST NOT повреждать сессию; повреждённый frame MUST NOT завершать процесс приложения. Логи MUST NOT смешиваться с потоком протокола.

#### Scenario: Cancel and continue
- **WHEN** клиент отменяет длительный references request и отправляет новый hover
- **THEN** отменённый запрос освобождает ресурсы и получает корректное завершение, а hover обслуживается на актуальном снимке

#### Scenario: Invalid method followed by valid request
- **WHEN** клиент вызывает неизвестный метод, затем корректный documentSymbol
- **THEN** первый запрос получает method-not-found, а второй возвращает символы без повторной инициализации

### Requirement: Explicit compatibility boundary
Паритет SHALL распространяться на редакторские возможности поддерживаемого TS/TSX. Сервер MUST NOT объявлять поддержку JS/JSX language service, произвольного `typescript.tsserverRequest`, JS-плагинов `tsserver`, `_typescript.configurePlugin`, npm/ATA или выбора внешнего `tsserver.js`. Запрос исключённой команды MUST давать явную ошибку без внешних побочных эффектов.

#### Scenario: Excluded tsserver passthrough
- **WHEN** клиент вызывает `typescript.tsserverRequest` либо `_typescript.configurePlugin`
- **THEN** получает явную ошибку неподдерживаемой операции без запуска Node.js, загрузки плагина или сетевого обращения
