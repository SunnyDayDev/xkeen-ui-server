## ADDED Requirements

### Requirement: The installed server explicitly pins remote validation and agent origin

Первичная установка сервера Entware SHALL принимать полный набор доверия к явно выбранному исполнителю HTTPS mTLS и независимый публичный agent origin. Приватный ключ SHALL оставаться в защищённых файлах. Неполные параметры SHALL отклоняться до включения службы.

#### Scenario: An installed server verifies publication against the chosen external worker

- **WHEN** владелец задал действительный HTTPS origin исполнителя и выделенную CA и клиентскую пару
- **THEN** F09 подключается к этому исполнителю по mTLS, а F10 показывает отдельно настроенный agent origin

#### Scenario: No executor is configured

- **WHEN** сервер установлен без конфигурации исполнителя
- **THEN** HTTPS и веб-консоль доступны, публикация сохраняет отказ при недоступности валидатора
