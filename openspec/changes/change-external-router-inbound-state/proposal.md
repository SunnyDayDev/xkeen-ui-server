## Why

Внешнему клиенту нужен безопасный способ изменить состояние существующего входа. [Issue #188](https://github.com/SunnyDayDev/xkeen-ui-server/issues/188).

## What Changes

GET и PATCH `/api/v1/routers/inbounds?routerId=<exact>&elementId=<uuid>` предоставляют условное переключение входа по F16/B10.

## Capabilities

### New Capabilities

- `external-router-inbound-state` — GET и PATCH `/api/v1/routers/inbounds?routerId=<exact>&elementId=<uuid>` предоставляют условное переключение входа по F16/B10.

## Impact

Авторизованный снимок F12–F16, F20 и применимые доменные операции B06/B08/B10/B11.
