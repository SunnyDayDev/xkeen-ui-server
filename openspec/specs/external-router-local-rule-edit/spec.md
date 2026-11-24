# external-router-local-rule-edit Specification

## Purpose
Внешний HTTPS-клиент работает с одним авторизованным снимком роутера.

## Requirements

### Requirement: The external operation is scoped to one current router

Изменение SHALL принимать только существующий локальный UUID согласованного роутера и сохранять точный JSON с неизменными порядком и идентичностью.

#### Scenario: An exact accepted snapshot governs the outcome
- **WHEN** клиент предъявляет чужой или общий UUID либо недействительный исходник
- **THEN** операция отклоняется без изменения любого правила

#### Scenario: Stale or denied authority prevents effects
- **WHEN** использованное основание изменилось до защищённой выдачи или записи либо отозван допуск F20
- **THEN** защищённые данные не возвращаются и состояние роутера не меняется
