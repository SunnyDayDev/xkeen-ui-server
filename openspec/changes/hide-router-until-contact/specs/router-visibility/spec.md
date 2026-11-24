## ADDED Requirements

### Requirement: The visible list contains one eligible record per router

Видимая проекция SHALL содержать по одному точному RouterID для подтверждённого устройства с сохранённым контактом и без активного скрытия.

#### Scenario: An existing database is upgraded

- **WHEN** старое хранилище с настроенными и ранее связавшимися роутерами открывается после миграции
- **THEN** записи остаются видимыми с теми же данными, а повторный запуск сохраняет последующее скрытие

#### Scenario: Writing visibility or contact cannot finish

- **WHEN** запись видимости или транзакция D06 завершается отказом
- **THEN** частичное восстановление или частичное изменение контакта отсутствует
