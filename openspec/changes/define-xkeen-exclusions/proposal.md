## Why

[G06 #166](https://github.com/SunnyDayDev/xkeen-ui-server/issues/166) задаёт текстовый формат исключений XKeen и границу проверки перед реализацией.

## What Changes

- Определить точный формат адресов, переменных, результата и отказов.
- Различать сохранение текста, успешную сборку, наличие возможностей XKeen и эффект firewall.
- Перед принятием согласовать с владельцем поведение публикации при отсутствии XKeen.

## Capabilities

### New Capabilities

- `xkeen-exclusions` — сборка отдельного текстового списка исключений.

## Impact

[G07](https://github.com/SunnyDayDev/xkeen-ui-server/issues/167) реализует сборку. [G08](https://github.com/SunnyDayDev/xkeen-ui-server/issues/168) добавляет общий редактор, [#117](https://github.com/SunnyDayDev/xkeen-ui-server/issues/117) — персонализацию. [G09](https://github.com/SunnyDayDev/xkeen-ui-server/issues/169) проверяет эффект трафиком.
