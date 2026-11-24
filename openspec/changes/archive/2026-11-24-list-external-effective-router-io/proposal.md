## Why

Внешний клиент должен знать эффективные входы и выходы, принадлежащие одному выбранному роутеру. [Issue #186](https://github.com/SunnyDayDev/xkeen-ui-server/issues/186).

## What Changes

GET `/api/v1/routers/io?routerId=<exact>` возвращает минимальный эффективный инвентарь входов и выходов.

## Capabilities

### New Capabilities

- `external-router-effective-io` — GET `/api/v1/routers/io?routerId=<exact>` возвращает минимальный эффективный инвентарь входов и выходов.

## Impact

Авторизованный снимок F12–F16, F20 и применимые доменные операции B06/B08/B10/B11.
