## Purpose

Создать пустой черновик шаблона из самостоятельного HTTPS-клиента.

## ADDED Requirements

### Requirement: An authorized external client can create an empty template

Внешняя коллекция SHALL принимать явное создание с точным новым ID в разрешённом режиме F20. Сервер SHALL атомарно вставлять пустой черновик версии 1 и возвращать ссылку на его чтение.

#### Scenario: An authorized client creates a draft and follows its location

- **WHEN** внешний клиент передал допустимый незанятый ID
- **THEN** возвращаются точный ID, начальная версия и путь чтения того же пустого черновика
- **AND** создание не создаёт назначения или желаемые ревизии

#### Scenario: Duplicate creates race or the permit is revoked

- **WHEN** несколько клиентов создают тот же ID либо допуск изменился до записи
- **THEN** конфликт не перезаписывает существующую запись
- **AND** устаревшие полномочия не создают новую запись

#### Scenario: External authorization is disabled or absent

- **WHEN** внешний API выключен либо в защищённом режиме переданы неверные полномочия
- **THEN** поведение соответствует действующему F20 и новый шаблон не появляется
