## Purpose

Список и боковая панель показывают наблюдаемую связь и состояние применения независимо.

## ADDED Requirements

### Requirement: Router presence and application status are presented separately

Веб-интерфейс SHALL показывать наблюдавшиеся устройства с раздельными статусами контакта и применения. Отказы чтения SHALL отличаться от пустого списка.

#### Scenario: An offline router retains its last known application history
- **WHEN** последний контакт наблюдавшегося роутера старше либо равен границе D06
- **THEN** роутер остаётся видимым, статус связи меняется, история применения сохраняется

#### Scenario: An unknown current runtime is distinct from historical success
- **WHEN** есть доказанная ранее применённая ревизия и новый отчёт о неизвестном runtime
- **THEN** интерфейс показывает последнюю доказанную ревизию и отдельно неопределённое состояние
