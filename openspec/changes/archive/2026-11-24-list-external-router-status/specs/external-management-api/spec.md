## ADDED Requirements

### Requirement: Admitted clients read visible router operational status

HTTPS GET `/api/v1/routers` SHALL отдавать только подтверждённые видимые устройства с принятым контактом. Допуск F20 и проекция состояния SHALL использовать один SQLite read snapshot.

#### Scenario: An admitted client reads the visible list

- **WHEN** защищённый клиент с токеном или клиент явно открытого режима вызывает запрос по HTTPS
- **THEN** ответ содержит массив `routers` с точными ID, отображаемыми именами, ID шаблона, контактом, desired и при наличии последней подтверждённой applied; при пустом списке массив пуст

#### Scenario: Contact and runtime evidence differ

- **WHEN** устройство offline или для его последней попытки сохранено неопределённое состояние
- **THEN** связь, desired, исторически подтверждённая applied, состояние runtime и документированная ошибка отката отображаются раздельно без предположения об успешном применении

#### Scenario: The router is hidden or admission changes

- **WHEN** устройство скрыто по G04 либо разрешение F20 устарело
- **THEN** скрытое устройство исключено до следующего допустимого контакта, а устаревшее разрешение отклоняется без выдачи списка
