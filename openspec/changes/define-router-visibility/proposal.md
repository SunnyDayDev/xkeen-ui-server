## Why

Список устройств должен позволять убрать запись из текущего обзора, сохраняя регистрацию, конфигурацию и доказательства применения. [G04 #155](https://github.com/SunnyDayDev/xkeen-ui-server/issues/155) определяет контракт для [G05 #156](https://github.com/SunnyDayDev/xkeen-ui-server/issues/156).

## What Changes

- Владелец временно скрывает подтверждённый роутер по стабильному RouterID.
- Следующий допустимый D06 контакт возвращает его в список с тем же именем и историей.
- Скрытие, отзыв ключа и окончательное удаление данных имеют разные полномочия и эффекты. G05 охватывает ядро и хранение. UI/API продолжаются отдельно.

## Capabilities

### New Capabilities

- `router-visibility` — временное скрытие и атомарное возвращение записи по подтверждённому контакту.

## Impact

Видимая проекция устройств, SQLite и контакт D06. Согласовано с [D02 #60](https://github.com/SunnyDayDev/xkeen-ui-server/issues/60), [D03 #65](https://github.com/SunnyDayDev/xkeen-ui-server/issues/65), [D06 #71](https://github.com/SunnyDayDev/xkeen-ui-server/issues/71).
