## ADDED Requirements

### Requirement: An external client renames one router

Внешний допущенный клиент SHALL читать и атомарно переименовывать отображаемое имя одного подтверждённого устройства по точному ETag.

#### Scenario: An exact allowed request succeeds

- **WHEN** имя изменено с действующим условным запросом
- **THEN** листинг и точное чтение показывают новое имя, а идентичность, назначенные и применённые ревизии сохраняются

#### Scenario: Evidence or authority is unavailable

- **WHEN** версия метадаты или допуск F20 изменились
- **THEN** запрос отклоняется без изменений метаданных или другой записи
