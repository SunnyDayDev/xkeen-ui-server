## Why

[F21 #108](https://github.com/SunnyDayDev/xkeen-ui-server/issues/108) открывает автоматизациям чтение выбранного редактируемого шаблона по контракту F19.

## What Changes

- Реализовать GET /api/v1/templates/item для одного точного templateId.
- Проверять актуальность допуска F20 в одной транзакции чтения с C02, возвращать детерминированный снимок и сильный ETag.

## Capabilities

### Modified Capabilities

- `external-management-api` — выбор и получение одного сохранённого шаблона.

## Impact

Внешний адаптер, общий путь восстановления C02, SQLite. В этой задаче интерфейс только читает данные.
