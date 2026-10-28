# Tasks

## 1. Подготовка module roots и checked graph

- [x] 1.1 Добавить `Project.ModuleRoots []string`, копирование/каноникализацию source paths и точных host-имён; проверить frontend-тестами дубликаты, неоднозначные и отсутствующие roots, declaration-only source и допустимый ambient host root.
- [x] 1.2 Разделить startup roots и preparation roots в normal/configured analysis и checked IR; тестами подтвердить, что дополнительный модуль не запускается до импорта, startup overlap не создаёт второй экземпляр, а прежние вызовы с пустым полем сохраняют выбор entry points.
- [x] 1.3 Собирать разрешённое замыкание статических и строковых динамических зависимостей вместе с графами `ModuleRoots`; тестами проверить рекурсию, ошибку в невыбранном плагине, type-only edges и отказ вычисляемого импорта файла, присутствующего только в каталоге.
- [x] 1.4 Включить нормализованные module roots и профиль в cache identity; тестами `internal/tsfrontend/cache_test.go` подтвердить invalidation при изменении набора, эквивалентность перестановок/дубликатов и отсутствие смешивания startup/prepared ролей.
- [x] 1.5 Описать поле и пример явного `EntryPoints` с двумя ленивыми плагинами в `docs/typescript-embedding.md`; проверить пример интеграционным тестом через публичные `Compile`/`RunProgram`, включая ESM и CommonJS root.

## 2. Frozen resolution catalog

- [x] 2.1 Подготовить checked metadata для точных импортов и допустимых aliases вычисляемых импортов; frontend-тестами сравнить literal/computed target для разных importer directories, TS/output extensions и relative normalization.
- [x] 2.2 Перенести применимые `paths`, package exports/imports и resolution-mode решения в конечный каталог/нормализованные правила разрешённых targets; проверить конфликтующие aliases, wildcard mappings, ESM/CJS режимы и отсутствие разрешений для посторонних файлов.
- [x] 2.3 Поддержать configured project-reference redirects и JSON targets в подготовленном каталоге; fixtures должны проходить без declaration outputs на диске и сохранять source identity, а неподдерживаемые import attributes — отклоняться.
- [x] 2.4 Передавать каталог и разрешённые targets из IR в `Program` как неизменяемые данные; тестом изменения входных map/options/resolver после `Compile` подтвердить неизменность целей и кода программы.
- [x] 2.5 Обновить описание resolution/preparation в `docs/typescript-backend.md` и пример resolver в embedding docs; проверочный resolver должен вызываться один раз при подготовке и не требоваться для использования готового каталога.

## 3. Предварительная проверка полного host-контракта

- [x] 3.1 Собирать manifest globals и host contracts всего подготовленного графа; тестом подтвердить наличие требований из lazy source root и вложенного literal dynamic import, сохранив сужение чисто статического named import.
- [x] 3.2 Сохранять полный namespace-контракт динамических host targets, включая host entries в `ModuleRoots`; воспроизведение `api.value: number` против runtime `string` должно возвращать ошибку совместимости до увеличения host-счётчика эффектов.
- [x] 3.3 Распространить startup preflight на невыбранные подготовленные плагины; тестами проверить missing binding, ambient declaration без реализации, совместимые разные runtime и допустимые дополнительные неиспользуемые регистрации.
- [x] 3.4 Описать ранний отказ и требование совместимости всего подготовленного набора в `docs/typescript-embedding.md`; пример с несовместимым lazy plugin должен завершаться до стартового инициализатора.

## 4. Исполнение подготовленного import()

- [x] 4.1 Заменить `compileDynamicModule` в `typescript_dynamic.go` на lookup каталога и запуск уже подготовленных units; заменить тест свежих resolver snapshots проверками нулевых resolver-вызовов после `Compile` и исполнения исходного frozen кода.
- [x] 4.2 Сохранить rejected Promise с `TypeError` для неизвестного computed target; через публичный API проверить неизвестные relative/bare specifiers, попытку `data:` исходника, незаявленный зарегистрированный host-модуль и target от другой программы в том же runtime.
- [x] 4.3 Подключить prepared units к существующему module lifecycle; `typescript_dynamic_test.go` должен проверять concurrency, shared static/dynamic namespace, live bindings, failure caching, thenable namespace и порядок promise jobs.
- [x] 4.4 Проверить и сохранить async SCC/cycles, top-level await, CommonJS interoperability, extension rewriting, JSON/import attributes и deferred import; соответствующие module/deferred/reference fixtures проходят без runtime-компиляции.
- [x] 4.5 Обновить примеры и compatibility-описание dynamic imports; тест documented plugin example должен показывать ленивую инициализацию выбранной реализации и отсутствие эффектов другой реализации.

## 5. Запрет встроенной компиляции строк

- [x] 5.1 Удалить встроенное глобальное свойство `eval` и его декларацию в owned standard profile overlay; тестами проверить отсутствие own property, `undefined` через `globalThis as any`, ошибку обычного имени, явный отказ регистрации глобального host-имени `eval` и допустимость методов `api.eval()`.
- [x] 5.2 Заблокировать call/construct paths всех четырёх семейств Function constructor общим `EvalError` guard до внутреннего `ToString`; негативные публичные тесты покрывают `.constructor`, прототипы, алиасы, `call`/`apply`/`bind`, Proxy forwarding и `Reflect.construct`.
- [x] 5.3 Добавить одинаковую symbol-aware goja-диагностику однозначных intrinsic constructor calls в `Check`/`Compile`; тестами проверить согласованные коды/диапазоны, отсутствие `Program` и отсутствие ложного запрета пользовательского `Function` в локальной области.
- [x] 5.4 Заменить успешные eval/function-constructor fixtures проверками профиля; положительными тестами сохранить closures, `arguments`, `this`, private fields, async/generator/async-generator functions, `instanceof Function` и обычные `call`/`apply`/`bind`.
- [x] 5.5 Обновить `README.md`, runtime/embedding/compatibility docs и owned library/runtime inventory для явно ограниченного профиля; inventory-тесты должны согласовываться с отсутствием eval и сохранением Function object model без ручного изменения vendored assets.

## 6. Удаление runtime-зависимости от frontend

- [x] 6.1 Удалить production `EvalAnalysis`, `DynamicAnalysis`, function-constructor validation и их неиспользуемые adapters/tests; проверить сборкой и поиском вызовов отсутствие этих путей из runtime, сохранив публичные Go `Compile`/`RunString`/`RunScript` с обязательной проверкой.
- [x] 6.2 Удалить eval-specific lowering selections/import resolver и compiler/VM инструкции direct eval, сохранив общие lexical mechanisms; root/frontend regression tests и положительные function tests из группы 5 должны проходить.
- [x] 6.3 Убрать `typeScriptProgram.analysis`, eval origin/base и resolver-bearing ссылки после переноса всех runtime metadata; проверить исполнение импортов после отключения compilation cache и очистки host-owned source catalogs, включая стек с исходными TS-позициями и `Function.prototype.toString`.
- [x] 6.4 Переработать `legacy_vm_test.go` и eval-зависимые исторические fixtures так, чтобы test-only harness не восстанавливал intrinsic в публичном профиле; одновременно выполнить legacy VM suite и публичные negative tests запрета, явно зафиксировав исключённые несовместимые conformance cases.
- [x] 6.5 Обновить backend/cache документацию о времени жизни Analysis и записать измерение heap allocations подготовки/повторного импорта небольшого пакета плагинов; повторные импорты должны выполнять только runtime-операции, без анализа новых исходников.

## 7. Согласование LSP и project snapshots

- [x] 7.1 Сохранить `ModuleRoots` во всех project clone/export/configured snapshot paths `lsp/session.go` и editor adapters; тесты snapshot round-trip и изменения исходного slice должны подтверждать независимое копирование и воспроизводимый `Check`/`Compile`.
- [x] 7.2 Использовать общий standard profile и symbol-aware диагностику запрещённых intrinsic calls в редакторском анализе; direct/wire diagnostic fixtures должны совпадать с compiler results на одинаковом snapshot и не запрещать пользовательские одноимённые функции.
- [x] 7.3 Дополнить `docs/typescript-lsp.md` описанием подготовленных roots и границы между редакторским анализом и runtime; проверить configured/reference проект экспортом snapshot и успешным запуском подготовленного plugin import.

## 8. Интеграционная проверка

- [x] 8.1 Выполнить `CGO_ENABLED=0 go test ./...`, `go vet ./...`, проверку gofmt и `git diff --check`; все новые сбои устранены, а существующие независимые сбои явно отделены в итоговом отчёте.
- [x] 8.2 Выполнить targeted `go test -race` для повторного использования Program в разных runtime, preparation/cache isolation и LSP snapshots; гонок и разделяемого изменяемого состояния модулей быть не должно.
- [x] 8.3 Выполнить `go run ./internal/tsfrontend/cmd/inventory -check` и применимые library/runtime fixture inventory checks; новый профиль явно исключает eval/code constructors, а поддерживаемые конструкции не исчезают из покрытия.
- [x] 8.4 При доступном Test262 выполнить профильные function/module cases и задокументировать исключения запрещённой генерации кода; итоговый отчёт не объявляет skipped cases подтверждением полной ECMAScript совместимости.
- [x] 8.5 Проверить пример миграции целиком: Go готовит два plugin roots и host-контракты, runtime выбирает один, неизвестный target отклоняется, обновление resolver не меняет старый Program, а новый `Compile` создаёт независимо запускаемую новую версию.

## Результаты интеграционной проверки (2026-10-28)

- `CGO_ENABLED=0 go test ./...` — успешно, включая полный direct/wire LSP fixture corpus.
- Targeted `go test -race` для root/frontend/LSP/fixture helpers — успешно. После исправления preflight повторно проверены prepared imports, параллельное использование Program и interrupted module lifecycle с `-race`.
- `go run ./internal/tsfrontend/cmd/inventory -check` — успешно: 108 standard declaration files; library/runtime fixture inventory checks также проходят.
- `git diff --check` и gofmt всех owned Go-файлов — успешно.
- Независимые прежние замечания: `go vet ./...` сообщает о сигнатуре `WriteByte` в `json_buffer.go:31`; общая проверка gofmt отмечает pinned upstream-файл `internal/tsfrontend/upstream/printer/emitflags.go`. Эти файлы в рамках изменения не исправлялись.
- Test262 недоступен: отсутствует `testdata/test262`, `TestTC39` явно skipped. Запрет генерации кода проверен публичными negative tests; legacy eval/code-constructor fixtures исключены из профиля явно и не считаются подтверждением полной ECMAScript совместимости.
