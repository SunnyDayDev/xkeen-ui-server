## Why

Допущенный клиент должен видеть действующий режим собственного внешнего канала. [Issue #197](https://github.com/SunnyDayDev/xkeen-ui-server/issues/197).

## What Changes

- `GET /api/v1/access` возвращает два флага действующего режима после допуска F20.
- Предел ответа исключает идентификатор поколения, сведения о токене и его материал.

## Capabilities

### New Capabilities

### Modified Capabilities

- `external-management-api`: минимальный статус действующего внешнего доступа.

## Impact

HTTPS-маршрут, авторизация F19/F20, транзакционное чтение SQLite и документация API.
