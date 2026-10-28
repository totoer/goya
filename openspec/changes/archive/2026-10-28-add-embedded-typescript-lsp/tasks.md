# Tasks

## 1. Закреплённые источники и критерии паритета

- [x] 1.1 Создать `docs/typescript-lsp-parity.json`: закрепить TLS v6.0.2 и его commit, версии oracle TypeScript и native baseline; перечислить все редакторские methods, commands, preferences, action kinds и применимые quick-fix/refactor providers с implementation/test links и pending statuses. Проверка: inventory check отклоняет пропущенные/дублирующиеся строки и необоснованные исключения; Node/JS исключения совпадают с proposal.
- [x] 1.2 Добавить формат fixtures и общий runner в `testdata/lsp/` с каталогом, декларациями, настройками, последовательностью edits и expected results; проверить runner на записанном baseline fixture и документировать воспроизводимый development-only сбор эталонов без автоматической установки npm/Node в offline tests.
- [x] 1.3 Расширить closure `internal/tsfrontend/cmd/importupstream` пакетами `ls`, `project`, `lsp` из текущего pinned archive; обновить file lock/assets и проверить importer tests, read-only `-check` и `CGO_ENABLED=0 go build ./internal/tsfrontend/upstream/...`.
- [x] 1.4 Зафиксировать необходимые upstream adaptations отдельными воспроизводимыми patches, отключить внешние npm/ATA/process/watchdog/OS watcher hooks в embedded profile; проверить no-external-IO fixture, повторяемость import/check и сохранение notices.
- [x] 1.5 Обновить dependency/license inventory и frontend import guide для расширенного closure; проверить соответствие manifest, go.mod/go.sum, license checksums и документированных команд воспроизведения.

## 2. Публичные типы, сессии и снимки

- [x] 2.1 Добавить публичные `lsp`/`lsp/protocol` facade types, session creation, typed errors и документированный ownership; проверить внешний Go example без imports из `internal` и отсутствие обратной зависимости корневого `goja` на `lsp`.
- [x] 2.2 Реализовать базовый catalog snapshot и open/change/save/close overlays, вызывая resolver только при явном обновлении базы; проверить несохранённую зависимость, обновление resolver при открытом файле, возврат к базе после close и неизменность снимка активного запроса.
- [x] 2.3 Реализовать configured/inferred projects, `extends`, references и package metadata поверх виртуального каталога; проверить fixtures связанных проектов, отсутствующей config dependency и запрет OS fallback.
- [x] 2.4 Добавить необязательный виртуальный `Project.ConfigFile` и общий normalizer/resolver для editor/Check/Compile, подключить `Environment.TypeDeclarations`/`SchemaID`, приоритет host options и экспорт effective configured snapshot; проверить `extends`/references без build outputs, исходные module IDs и выполнение Compile, сохранение поведения без ConfigFile, совпадение diagnostics и инвалидацию после смены config/окружения/настроек.
- [x] 2.5 Отделить editor program с ошибками от checked `Analysis`; проверить completion в незавершённом TS/TSX, сохранение diagnostics и отказ `Compile` создать ошибочную программу.
- [x] 2.6 Реализовать двустороннее URI mapping, UTF-16 positions и read-only library/host document access; проверить virtual/file/Windows URI, escaping/collisions, emoji, CRLF и исходные диапазоны, описать контракт позиций в embedding guide.
- [x] 2.7 Реализовать generation/version-bound handles и edits с ограничениями retained state; проверить stale resolve после source/config/environment update, вытеснение кеша и отказ редактировать frozen host/library declaration.
- [x] 2.8 Реализовать cancellation, snapshot/checker ownership, ограниченную доставку events и идемпотентный Close; проверить session isolation, callbacks без блокировки state, разблокирование очередей при отмене и завершение фоновых задач.
- [x] 2.9 Добавить пример прямой сессии над in-memory `Project` и `Environment`, описать обновление базы, overlays и effective snapshot; проверить запуск примера без LSP-клиента, VM и внешних инструментов.

## 3. LSP-транспорт и общая диспетчеризация

- [x] 3.1 Подключить shared session к LSP framing на host-supplied IO и жизненному циклу initialize/initialized/shutdown/exit; проверить fragmented/multiple frames, версии request IDs и завершение соединения без `os.Exit`/закрытия чужих ресурсов.
- [x] 3.2 Подключить incremental document synchronization и host/file events к тем же session operations; проверить порядок нескольких edits, старую версию, save/close и virtual URI в protocol transcripts.
- [x] 3.3 Добавить общую dispatch/error/cancellation адаптацию, двунаправленные client requests и отделённый logger; проверить invalid params, method-not-found, malformed frames, отмену request и успешный последующий запрос без protocol-log contamination.
- [x] 3.4 Реализовать typed client interaction hooks и protocol-side WorkspaceEdit/applyEdit без самостоятельной мутации; проверить отказ/отмену клиента, resource operations и изменение снимка только после подтверждённых host/document events.
- [x] 3.5 Добавить in-memory LSP runner и capability variants для общего parity suite, документировать пример Serve с переданными потоками; проверить рабочий transcript без редакторских extensions, CLI или socket listener.

## 4. Диагностика, completion и информация о типах

- [x] 4.1 Подключить syntax/semantic/suggestion diagnostics, related locations/tags и editor-only filters; проверить host call error и ignored code через direct/wire fixtures, сохранив compilation gate для того же effective snapshot.
- [x] 4.2 Реализовать generation-aware publication, clearing/eager-clear и progress events; проверить детерминированный transcript, где медленный старый анализ завершается после исправленной версии и не публикуется как актуальный.
- [x] 4.3 Подключить completion/resolve: contextual entries, snippets, label details, commit characters и применимые preferences; проверить positive/negative fixtures через оба интерфейса и capability fallback без snippets.
- [x] 4.4 Реализовать auto-import indexes и additional completion edits по виртуальным package/declaration/overlay источникам; проверить добавление экспорта в несохранённом файле, смену Environment и ожидаемый текст после применения completion edits.
- [x] 4.5 Подключить hover/JSDoc с verbosity extension и signature help с overload/active-argument/trigger данными; проверить расширение type alias и перегруженный Go API через direct/wire fixtures без выполнения binding.
- [x] 4.6 Документировать diagnostics/completion/hover options и direct API, связать реализованные строки inventory с fixtures; проверить примеры и inventory links для каждого поддержанного preference variant.

## 5. Навигация и структурные операции

- [x] 5.1 Подключить definition/typeDefinition/implementation/sourceDefinition к общему ядру; проверить cross-file/project targets, source/declaration maps и чтение virtual host/lib targets через direct/wire fixtures.
- [x] 5.2 Подключить references и document highlights с declaration/read/write поведением; проверить импортированные символы, overlays и связанные configured projects через оба интерфейса.
- [x] 5.3 Реализовать prepareRename/rename с aliases, shorthand, invalid names и read-only host/library границей; проверить точные многофайловые edits и успешный `Check` изменённой копии каталога.
- [x] 5.4 Подключить document/workspace symbols, включая closed files и hierarchical capability fallback; проверить поиск в нескольких проектах и актуальность после overlay edit.
- [x] 5.5 Подключить folding/selection/linked-editing ranges; проверить вложенные TS-конструкции и TSX tags, включая незавершённый код, через direct/wire fixtures.
- [x] 5.6 Подключить prepare/incoming/outgoing call hierarchy; проверить взаимные cross-file цели и диапазоны вызовов, отмену и запрос неизвестного символа.
- [x] 5.7 Документировать навигацию и read-only virtual document запрос с именем/DTO, обновить inventory; проверить сценарий перехода к Go declaration без выгрузки declarations на диск.

## 6. Отображение и форматирование

- [x] 6.1 Подключить semantic tokens full/range и совместимую legend; проверить token types/modifiers и координаты после Unicode/CRLF edits через direct/wire fixtures.
- [x] 6.2 Подключить все применимые inlay-hint preferences; проверить параметры, return/variable/property types, enum values и suppression variants по inventory через оба интерфейса.
- [x] 6.3 Подключить references/implementations code lens и resolve с visibility preferences и client navigation commands; проверить количество/цели, stale lens и клиента без требуемого интерактивного расширения.
- [x] 6.4 Подключить formatting/rangeFormatting, все применимые formatting preferences и per-document workspace configuration; проверить точный итоговый текст, semicolons, CRLF, отступы и fallback при недоступной configuration.
- [x] 6.5 Добавить документацию visual/formatting settings и обновить inventory links; проверить все capability/preference variants fixtures из этой группы.

## 7. Исправления, импорты и переименование файлов

- [x] 7.1 Подключить codeAction/resolve, context.only/triggerKind и preferred/disabled поведение; проверить подходящие и неприменимые действия, stale action и typed error через direct/wire fixtures.
- [x] 7.2 Реализовать весь применимый baseline quick-fix provider inventory, включая отсутствующие native providers; для каждого provider проверить ожидаемые edits на положительном и отрицательном fixture и `Check` исправленной копии проекта.
- [x] 7.3 Реализовать все providers `source.fixAll.ts` и `source.removeUnused.ts`; проверить несколько употреблений, зависимые файлы и отсутствие pending provider rows в соответствующей части inventory.
- [x] 7.4 Реализовать addMissingImports/removeUnusedImports/sortImports/organizeImports и режимы All/SortAndCombine/RemoveUnused; проверить exact-text fixtures, предпочтения specifier и сохранение unused imports в неразрушающем режиме.
- [x] 7.5 Реализовать willRenameFiles и типизированное вычисление file/directory rename edits; проверить пакетные операции, обновление импортов, отсутствие самостоятельного rename и разрешение модулей после применения edits к копии каталога.
- [x] 7.6 Документировать source action kinds и семантику edits, обновить inventory с provider-level fixture links; проверить примеры исправления/организации импортов и version checks без записи на диск.

## 8. Полный набор рефакторингов

- [x] 8.1 Реализовать общий enumerate/apply refactor API с kind filtering, applicability reasons, action selection и typed continuation results; проверить выбор нескольких действий и отказ неприменимого refactor через direct/wire fixtures.
- [x] 8.2 Реализовать применимые baseline extract refactors для значений/функций/типов с scope/name selection; проверить каждый provider из inventory на expected edits, последующем rename и `Check` изменённой копии.
- [x] 8.3 Реализовать применимые baseline conversions для функций/выражений, imports/exports и прочих преобразований синтаксиса; проверить каждый provider/variant из закреплённого inventory, включая no-result случаи, без пропусков из-за отсутствия native provider.
- [x] 8.4 Реализовать применимые move-to-new/existing-file refactors с resource operations и interactive target arguments; проверить создание файлов и корректировку импортов в копии проекта, отказ клиента и отсутствие самостоятельной OS-записи.
- [x] 8.5 Закрыть оставшиеся refactor providers из конечного baseline inventory, не вошедшие в extract/conversion/move группы, и документировать названия/аргументы действий; проверка: у каждого применимого provider есть passing direct/wire fixtures, а refactor раздел inventory не содержит pending/unsupported.

## 9. Совместимость команд и настроек эталона

- [x] 9.1 Реализовать совместимые аргументы/результаты `_typescript.goToSourceDefinition`, `_typescript.organizeImports`, `_typescript.applyRenameFile` и `_typescript.applyCompletionCodeAction` поверх typed operations; проверить transcript каждой команды и эквивалентность прямому вызову.
- [x] 9.2 Реализовать `_typescript.applyRefactoring`, клиентские select-refactoring/show-references/rename continuations и capability-gated interactive flow; проверить complete/rejected/cancelled действия, новые файлы и stale generation без частичной мутации.
- [x] 9.3 Подключить все применимые initialization/dynamic editor preferences, configuration/refresh и `$/typescriptVersion`; проверить каждый preference inventory variant, текущую встроенную версию и отсутствие silent no-op для поддерживаемых настроек.
- [x] 9.4 Явно отклонять исключённые tsserver/plugin команды и не объявлять внешние npm/tsserver механизмы; проверить ошибки без побочных эффектов, документировать границы совместимости и обновить capability/command inventory.

## 10. Интеграционные критерии завершения

- [x] 10.1 Выполнить полный direct/wire parity suite на TS/TSX/MTS/CTS/declarations, configured/inferred projects, host API и несохранённых зависимостях; проверить отсутствие pending применимых methods/preferences/providers и обоснование version-specific fixtures в inventory.
- [x] 10.2 Выполнить `CGO_ENABLED=0 go test ./...`, pure-Go build и importer `-check` из проверенного archive, автономные embedding checks без Node/tsc/npm/сети и regression gates compilation/runtime; сохранить итоговую проверку в release documentation без требования внешних oracle tools для обычного тестового запуска.
- [x] 10.3 Выполнить session/protocol race tests в поддерживаемой race-конфигурации и repeated-edit/cancel/close workloads с проверкой лимитов/очередей; зафиксировать результаты измерений и проверить, что изоляция, освобождение ресурсов и backpressure соответствуют документации.
