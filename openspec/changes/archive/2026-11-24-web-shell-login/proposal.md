## Why

Сервер F03 предоставляет сессию, но пользователю нужен вход через обычный браузер и первая защищённая оболочка интерфейса. [F04 #97](https://github.com/SunnyDayDev/xkeen-ui-server/issues/97).

## What Changes

- Выдать Svelte-оболочку и форму входа с того же HTTPS origin, что и F03.
- Реализовать экранные состояния F02, навигацию доступных страниц, выход и повторный вход в той же вкладке.
- Встроить проверенную сборку Svelte в бинарник Go. На целевой машине запуска требуется только бинарник.
- Передать F05 существующую защищённую область оболочки. Список и создание шаблонов не входят в F04.

## Capabilities

### New Capabilities

- `web-console-shell` — доставка оболочки, вход и восстановление браузерного интерфейса.

### Modified Capabilities

## Impact

Сборка веб-клиента, статический HTTP-слой существующего HTTPS Go-сервера, состояния Pencil и браузерные проверки. [F01 #45](https://github.com/SunnyDayDev/xkeen-ui-server/issues/45), [F02 #74](https://github.com/SunnyDayDev/xkeen-ui-server/issues/74), [F03 #94](https://github.com/SunnyDayDev/xkeen-ui-server/issues/94).
