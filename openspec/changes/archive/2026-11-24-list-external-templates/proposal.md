## Why

[#174](https://github.com/SunnyDayDev/xkeen-ui-server/issues/174) позволяет внешнему клиенту выбрать точный templateId для F21, используя metadata того же набора, который читает F06.

## What Changes

Добавить GET /api/v1/templates с детерминированным порядком, независимой проверкой внешнего допуска и полной проверкой HTTPS-потребителем.

## Capabilities

### Modified Capabilities

- `external-management-api` — список редактируемых шаблонов.

## Impact

Чтение метаданных SQLite, адаптер F20, документация потребителя. Содержимое шаблона по-прежнему читает F21.
