# Spec Delta

## ADDED Requirements

### Requirement: Referenced project source identities
При явной виртуальной конфигурации система MUST разрешать применимые project-reference redirects от ожидаемых declaration outputs к предоставленным TS-исходникам. Checker, execution graph и dynamic analysis MUST согласованно использовать исходную идентичность модуля без предварительной записи outputs и без подмены TS-модуля host-привязкой.

#### Scenario: Execute a referenced source without outputs
- **WHEN** приложение импортирует ожидаемый output referenced проекта, а каталог содержит только configs и TS-исходники
- **THEN** `Check` принимает корректный граф, `Compile` создаёт программу, runtime исполняет соответствующий TS-модуль и не требует host-реализацию отсутствующего output

#### Scenario: Error in a redirected source
- **WHEN** referenced TS-исходник содержит ошибку типов
- **THEN** `Check` и `Compile` возвращают ошибку с исходным TS-именем и диапазоном до выполнения кода
