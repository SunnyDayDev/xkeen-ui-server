## Why

Нужно состояние отдельной ревизии для внешнего клиента. [Issue #196](https://github.com/SunnyDayDev/xkeen-ui-server/issues/196).

## What Changes

GET /api/v1/routers/application?routerId=<exact>&revision=<decimal> читает сохранённые доказательства для точного номера.

## Capabilities

### New Capabilities

### Modified Capabilities

- `external-management-api`: состояние отдельной ревизии для внешнего клиента.

## Impact

HTTPS, допуск F19/F20, точный ресурс и атомарная SQLite-проекция.
