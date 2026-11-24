## Why

Нужно безопасное переименование устройства. [Issue #190](https://github.com/SunnyDayDev/xkeen-ui-server/issues/190).

## What Changes

GET и PATCH /api/v1/routers/name?routerId=<exact> читают и меняют только отображаемое имя.

## Capabilities

### New Capabilities

### Modified Capabilities

- `external-management-api`: безопасное переименование устройства.

## Impact

HTTPS, допуск F19/F20, точный ресурс и атомарная SQLite-проекция.
