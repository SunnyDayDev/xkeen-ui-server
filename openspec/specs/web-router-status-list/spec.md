# web-router-status-list Specification

## Purpose
Список и боковая панель показывают наблюдаемую связь и состояние применения независимо.

## Requirements

### Requirement: Router presence and application status are presented separately

Веб-интерфейс SHALL показывать наблюдавшиеся устройства с раздельными статусами контакта и применения. Отказы чтения SHALL отличаться от пустого списка.

#### Scenario: An offline router retains its last known application history
- **WHEN** последний контакт наблюдавшегося роутера старше либо равен границе D06
- **THEN** роутер остаётся видимым, статус связи меняется, история применения сохраняется

#### Scenario: An unknown current runtime is distinct from historical success
- **WHEN** есть доказанная ранее применённая ревизия и новый отчёт о неизвестном runtime
- **THEN** интерфейс показывает последнюю доказанную ревизию и отдельно неопределённое состояние

#### Scenario: An invitation was consumed without a recorded contact
- **WHEN** агент зарегистрирован, а D06 ещё не принял контакт
- **THEN** роутер остаётся в ожидании на экране подключения и не появляется в обзоре

#### Scenario: A temporarily hidden router contacts the server
- **WHEN** наблюдаемый роутер был скрыт G05 и затем зафиксирован допустимый контакт
- **THEN** прежний RouterID возвращается в список с актуальными данными связи и применения

#### Scenario: A read fails or the selected router becomes unavailable
- **WHEN** чтение не удалось либо выбранная строка исчезла из успешно прочитанного списка
- **THEN** интерфейс различает ошибку, успешный пустой список и отсутствие выбранного роутера
- **AND** боковая панель не показывает данные прежнего снимка
