## Why

Внешний клиент меняет буквальный JSON существующего локального правила по точному UUID. [Issue #182](https://github.com/SunnyDayDev/xkeen-ui-server/issues/182).

## What Changes

GET и PATCH `/api/v1/routers/rules/item?routerId=<exact>&elementId=<uuid>` обслуживают только локальную запись.

## Capabilities

### New Capabilities

- `external-router-local-rule-edit` — GET и PATCH `/api/v1/routers/rules/item?routerId=<exact>&elementId=<uuid>` обслуживают только локальную запись.

## Impact

Авторизованный снимок F12–F16, F20 и применимые доменные операции B06/B08/B10/B11.
