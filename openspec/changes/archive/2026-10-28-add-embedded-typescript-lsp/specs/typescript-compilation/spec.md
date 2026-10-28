# Spec Delta

## ADDED Requirements

### Requirement: Explicit virtual project configuration
`Project` SHALL принимать необязательный `ConfigFile`, указывающий на конфигурацию в предоставленном виртуальном каталоге. `Check`/`Compile` MUST согласованно учитывать её `extends`, references и настройки; явные host options MUST иметь приоритет. Пустой `ConfigFile` MUST сохранять прежний выбор исходников и настроек без автоматического поиска конфигурации.

#### Scenario: Configuration entirely in memory
- **WHEN** приложение передаёт `ConfigFile`, конфигурацию, её зависимости и TS-исходники из памяти
- **THEN** проверка и компиляция используют этот config graph без OS-чтения и без генерации build artifacts

#### Scenario: Explicit options override configuration
- **WHEN** конфигурация отключает strict, а приложение явно включает strict в options
- **THEN** редактор, `Check` и `Compile` используют strict и сообщают согласованные ошибки

#### Scenario: Existing calls preserve behavior
- **WHEN** приложение оставляет `ConfigFile` пустым, а каталог содержит неиспользуемый `tsconfig.json`
- **THEN** `Check`/`Compile` используют прежние правила roots/options и не начинают автоматически применять этот config

### Requirement: Configured analysis identity
Идентичность анализа MUST учитывать выбранный виртуальный config и содержимое его графа вместе с исходниками, options, версией языка и Environment. Ошибка конфигурации или referenced source MUST блокировать создание исполняемой программы; изменение графа MUST инвалидировать прежние результаты.

#### Scenario: Changed inherited configuration
- **WHEN** после успешного анализа содержимое inherited config меняет применимые настройки
- **THEN** новый анализ применяет новые настройки и не использует несовместимый кешированный результат
