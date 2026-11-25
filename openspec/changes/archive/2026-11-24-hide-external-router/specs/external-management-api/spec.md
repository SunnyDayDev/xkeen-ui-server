## ADDED Requirements

### Requirement: An external client temporarily hides one router from the list

Внешнее удаление из списка SHALL означать временное скрытие по G04/G05 и требовать действующий ETag и явное подтверждение.

#### Scenario: An exact allowed request succeeds

- **WHEN** допущенный клиент подтверждает скрытие точного устройства
- **THEN** оно исчезает из видимого списка, а последующий принятый контакт возвращает существующую запись

#### Scenario: Evidence or authority is unavailable

- **WHEN** допуск или условная версия изменились, либо подтверждение отсутствует
- **THEN** сервер отказывает без изменения видимости или регистрации
