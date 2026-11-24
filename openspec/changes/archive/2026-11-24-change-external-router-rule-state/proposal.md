## Why

Внешний клиент меняет признак участия существующего правила, сохраняя полную позицию. [Issue #184](https://github.com/SunnyDayDev/xkeen-ui-server/issues/184).

## What Changes

GET и PATCH `/api/v1/routers/rules/state?routerId=<exact>&elementId=<uuid>` для точного общего или локального правила.

## Capabilities

### New Capabilities

- `external-router-rule-state` — GET и PATCH `/api/v1/routers/rules/state?routerId=<exact>&elementId=<uuid>` для точного общего или локального правила.

## Impact

Авторизованный снимок F12–F16, F20 и применимые доменные операции B06/B08/B10/B11.
