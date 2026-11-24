## Why

Внешний клиент проверяет фактические ссылки маршрутизации одного роутера без выпуска ревизии. [Issue #185](https://github.com/SunnyDayDev/xkeen-ui-server/issues/185).

## What Changes

GET `/api/v1/routers/routing/dependencies?routerId=<exact>` проверяет B10 и B11 по одному авторизованному снимку.

## Capabilities

### New Capabilities

- `external-router-dependency-inspection` — GET `/api/v1/routers/routing/dependencies?routerId=<exact>` проверяет B10 и B11 по одному авторизованному снимку.

## Impact

Авторизованный снимок F12–F16, F20 и применимые доменные операции B06/B08/B10/B11.
