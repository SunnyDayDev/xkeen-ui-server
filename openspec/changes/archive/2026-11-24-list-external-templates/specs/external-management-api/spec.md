## ADDED Requirements

### Requirement: External consumer lists editable templates

Авторизованный по F20 клиент SHALL получать через GET /api/v1/templates массив templates с точными ID и положительными версиями в виде десятичных строк, упорядоченный по байтам ID. Пустая коллекция SHALL возвращаться как пустой массив. Настройки доступа и строки читаются в одном согласованном снимке.

#### Scenario: Consumer selects an exact template

- **WHEN** внешний HTTPS-клиент читает список сохранённых шаблонов
- **THEN** результат содержит только поля id и version каждой записи, выбранный ID можно непосредственно передать GET F21, ответ имеет no-store

#### Scenario: Revoked client cannot enumerate

- **WHEN** поколение разрешения потеряло силу до чтения в SQLite
- **THEN** операция возвращает отказ F20 без идентификаторов или содержимого
