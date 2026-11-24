## Why

Внешнему HTTPS-клиенту нужен отдельный способ создать пустой черновик с точным ID. [Issue #175](https://github.com/SunnyDayDev/xkeen-ui-server/issues/175).

## What Changes

- Поддержать POST коллекции шаблонов с явным идентификатором.
- Возвращать точный ID и начальную версию либо безопасный конфликт.
- Сохранить независимость внешнего допуска F19/F20, веб-сессии и ключей агента.

## Capabilities

### New Capabilities

- `external-template-creation`: создать пустой шаблон через внешний HTTPS API.

### Modified Capabilities

## Impact

Коллекция, совместимая с [#174](https://github.com/SunnyDayDev/xkeen-ui-server/issues/174), и существующая модель пустого шаблона F06/C02.
