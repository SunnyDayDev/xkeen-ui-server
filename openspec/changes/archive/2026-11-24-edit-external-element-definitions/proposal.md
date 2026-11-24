## Why
[#177](https://github.com/SunnyDayDev/xkeen-ui-server/issues/177) открывает внешнему клиенту редактирование определений одного существующего элемента с теми же правилами сохранения, что у F08.

## What Changes
Добавить условный PATCH для упорядоченных определений переменных. Успех возвращает итоговую F21 representation с сильным ETag.

## Capabilities
### Modified Capabilities
- `external-management-api` — изменить определения конкретного UUID шаблона.

## Impact
HTTPS-адаптер управления, общая операция F08, writer F20/F22, потребительские тесты. База, UI, агент и ревизии не меняются.
