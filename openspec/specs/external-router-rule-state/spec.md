# external-router-rule-state Specification

## Purpose
Внешний HTTPS-клиент работает с одним авторизованным снимком роутера.

## Requirements

### Requirement: The external operation is scoped to one current router

Условная запись SHALL сохранять точную позицию и происхождение правила и менять только его персональный признак участия с соблюдением общего отключения.

#### Scenario: An exact accepted snapshot governs the outcome
- **WHEN** действующее правило выключается либо ранее отключённое личной отметкой возвращается
- **THEN** его UUID остаётся на прежнем месте, а анализ действующих ссылок учитывает новое состояние

#### Scenario: Stale or denied authority prevents effects
- **WHEN** использованное основание изменилось до защищённой выдачи или записи либо отозван допуск F20
- **THEN** защищённые данные не возвращаются и состояние роутера не меняется
