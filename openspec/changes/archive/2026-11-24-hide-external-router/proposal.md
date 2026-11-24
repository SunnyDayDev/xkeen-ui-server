## Why

Нужно временное исключение устройства из списка. [Issue #191](https://github.com/SunnyDayDev/xkeen-ui-server/issues/191).

## What Changes

GET и POST /api/v1/routers/visibility?routerId=<exact> реализуют явное скрытие в рамках G04/G05.

## Capabilities

### New Capabilities

### Modified Capabilities

- `external-management-api`: временное исключение устройства из списка.

## Impact

HTTPS, допуск F19/F20, точный ресурс и атомарная SQLite-проекция.
