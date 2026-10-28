# Spec Delta

## Purpose

Определяет исполнение проверенного TypeScript во встраиваемой Go-среде со стандартными средствами языка и внешними возможностями, явно предоставленными приложением.

## ADDED Requirements

### Requirement: In-process pure Go operation
Проверка, компиляция и выполнение MUST работать внутри Go-процесса при `CGO_ENABLED=0`. Эти операции MUST NOT требовать Node.js, внешнего `tsc`, внешнего JavaScript-движка, дочерних процессов или сетевой загрузки compiler assets во время работы.

#### Scenario: Standalone embedded application
- **WHEN** Go-приложение собрано с `CGO_ENABLED=0` и запущено без Node.js и TypeScript в PATH и без сети
- **THEN** оно может проверить, скомпилировать и выполнить проект с предоставленными из памяти исходниками и привязками

### Requirement: Full supported language execution
Runtime MUST исполнять все runtime-конструкции поддерживаемой стабильной версии TypeScript с соответствующей семантикой. Типовые конструкции SHALL обрабатываться при компиляции; конструкции с runtime-поведением MUST реализовывать это поведение. Частичная реализация MUST NOT объявляться полной поддержкой версии.

#### Scenario: Runtime-bearing TypeScript constructs
- **WHEN** корректный проект использует enum, namespace, parameter properties, decorators и resource management, допустимые выбранной конфигурацией
- **THEN** наблюдаемые значения, побочные эффекты и порядок выполнения соответствуют поддерживаемому TypeScript

#### Scenario: TSX execution with host JSX API
- **WHEN** TSX-проект успешно проверен и приложение зарегистрировало требуемую JSX factory или JSX runtime-модуль
- **THEN** JSX-выражения исполняются через предоставленные привязки без встроенной зависимости от браузера или React

### Requirement: Standard language environment
Runtime SHALL предоставлять стандартные объекты языка и операции, соответствующие объявленной поддержке. Стандартные объявления, используемые checker по умолчанию, MUST соответствовать реализованной стандартной среде и MUST NOT неявно включать DOM или Node.js API.

#### Scenario: Standard collections and asynchronous values
- **WHEN** программа использует стандартные массивы, Map, Set и Promise поддерживаемой среды
- **THEN** checker видит соответствующие типы, а runtime предоставляет соответствующие реализации

### Requirement: Explicit external capabilities
Внешние возможности MUST предоставляться только Go-приложением. Runtime MUST NOT автоматически предоставлять файловую систему, сеть, process, require, console, таймеры или загрузку кода с диска. Чтение приложением входного файла CLI MUST NOT предоставлять файловый API исполняемому коду.

#### Scenario: Default environment
- **WHEN** приложение создаёт окружение без внешних регистраций
- **THEN** программа не получает неявных привязок `readFile`, `load`, `require`, `process`, `console` или `setTimeout`

#### Scenario: Explicitly registered capability
- **WHEN** приложение регистрирует типизированный модуль логирования
- **THEN** TS-код может обращаться к нему по зарегистрированному контракту

### Requirement: Program isolation and interruption
Проверенная программа SHALL быть пригодна для повторного выполнения в разных совместимых runtime без общего изменяемого состояния скрипта. Runtime MUST поддерживать прерывание выполнения со стороны Go-приложения, включая асинхронные продолжения.

#### Scenario: Separate module state
- **WHEN** одна программа исполняется в двух runtime с совместимыми привязками
- **THEN** каждый runtime имеет собственные экземпляры модулей и состояние пользовательского кода

#### Scenario: Interrupted computation
- **WHEN** Go-приложение прерывает бесконечный цикл либо выполнение цепочки асинхронных продолжений
- **THEN** runtime прекращает выполнение и возвращает приложению отличимую ошибку прерывания

### Requirement: Source-level runtime errors
Ошибки выполнения MUST содержать стек с именами и позициями исходных TypeScript-файлов. Преобразования TS-конструкций MUST NOT подменять пользовательские позиции внутренними IR-координатами.

#### Scenario: Exception after lowering
- **WHEN** исключение возникает внутри преобразованного TS-конструктора или async-функции
- **THEN** стек указывает на соответствующий `.ts` исходник и доступен Go-приложению
