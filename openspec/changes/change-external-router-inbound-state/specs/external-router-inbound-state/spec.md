## Purpose

Внешний HTTPS-клиент работает с одним авторизованным снимком роутера.

## ADDED Requirements

### Requirement: The external operation is scoped to one current router

Изменение состояния SHALL сохранять все остальные настройки черновика. Активная ссылка SHALL блокировать выключение, а включение SHALL уважать общее отключение и исключение.

#### Scenario: An exact accepted snapshot governs the outcome
- **WHEN** действующее правило ссылается на выбранный активный вход
- **THEN** выключение отклоняется без записи

#### Scenario: Stale or denied authority prevents effects
- **WHEN** использованное основание изменилось до защищённой выдачи или записи либо отозван допуск F20
- **THEN** защищённые данные не возвращаются и состояние роутера не меняется
