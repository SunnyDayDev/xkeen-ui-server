## ADDED Requirements

### Requirement: An external client prepares a new router without agent authority

Внешняя подготовка SHALL проверять одно первое назначение через F09/C06 с обязательными значениями, реальным валидатором и поздним recheck F20.

#### Scenario: An exact allowed request succeeds

- **WHEN** допущенный клиент подтверждает точный источник и передаёт необходимые значения
- **THEN** возвращаются безопасные координаты нового назначения и первая желаемая ревизия

#### Scenario: Evidence or authority is unavailable

- **WHEN** источник, состояние роутера или допуск изменились, либо сборка и проверка не удались
- **THEN** новое назначение не фиксируется и полномочия агента не меняются
