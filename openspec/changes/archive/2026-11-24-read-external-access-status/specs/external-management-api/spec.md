## ADDED Requirements

### Requirement: Admitted clients read their external access mode

Внешний API SHALL отдавать только поля `enabled` и `protected` по `GET /api/v1/access` через действующий допуск F20. Данные выдаются без кэширования из того же снимка, который проверяет поколение допуска.

#### Scenario: An admitted client reads the active mode

- **WHEN** защищённый клиент с действующим токеном или клиент явно открытого режима вызывает запрос по HTTPS
- **THEN** ответ содержит `enabled: true` и фактический `protected`, без токена, его существования, отпечатка и поколения

#### Scenario: The mode or token is invalidated

- **WHEN** режим отключён, допуск отсутствует либо поколение изменилось после первичной проверки
- **THEN** ответ отклоняется согласно F20 и не возвращает статус режима

#### Scenario: A client attempts to change ownership settings

- **WHEN** запрос содержит другой HTTP-метод или неожиданные параметры
- **THEN** операция отклоняется без изменений режима и полномочий
