## Purpose

Предоставить защищённую серверную часть подключения агента и чтения собственной идентичности.

## ADDED Requirements

### Requirement: Agent enrollment binds a working credential to the invitation device

Сервер SHALL принимать приглашение и рабочий кандидат на `POST /agent/v1/enroll` через защищённый канал. Область привязки SHALL определяться приглашением. Успешная привязка и её допустимый точный повтор SHALL возвращать только технический ID роутера.

#### Scenario: An invitation enrolls the prepared router
- **GIVEN** сервер выдал действующее приглашение подготовленному роутеру
- **WHEN** агент передаёт это приглашение и допустимый рабочий кандидат
- **THEN** ответ содержит технический ID роутера и не раскрывает рабочий ключ

#### Scenario: An exact replay returns the same identity
- **WHEN** сервер уже зафиксировал погашение, а агент повторяет точный запрос в допустимый срок
- **THEN** результат содержит тот же ID и не создаёт ещё одну регистрацию

#### Scenario: A competing candidate cannot replace the bound key
- **WHEN** одно приглашение погашено с другим рабочим кандидатом
- **THEN** запрос получает безопасный отказ без чужих данных и без изменения привязки

### Requirement: A working credential can check the identity of its own router

Сервер SHALL возвращать технический ID области действующего ключа на `GET /agent/v1/identity`. Проверка SHALL оставлять состояние контакта и применения прежним.

#### Scenario: A surviving working key identifies the same device
- **WHEN** агент с сохранённым действующим ключом проверяет идентичность после потери ответа или переустановки
- **THEN** ответ содержит технический ID привязанного роутера
- **AND** контакт и подтверждённая ревизия не меняются

#### Scenario: Missing and invalid keys disclose no device identity
- **WHEN** рабочий ключ отсутствует, неизвестен, заменён, отозван либо вместо него передано приглашение
- **THEN** запрос получает безопасный отказ без идентичности, ключа и состояния регистрации

#### Scenario: A different router cannot be selected by the request
- **WHEN** запрос пытается выбрать область другого роутера
- **THEN** сервер не возвращает идентичность или данные указанного роутера

### Requirement: Agent enrollment is served only by the explicitly configured TLS endpoint

Сервер SHALL принимать привязку и проверку идентичности только при явно настроенном TLS. Отсутствие настройки SHALL сохранять существующий HTTP health check. Неполная или непригодная настройка TLS SHALL останавливать запуск с безопасной диагностикой.

#### Scenario: The existing HTTP server remains usable without TLS configuration
- **WHEN** параметры сертификата и ключа не заданы
- **THEN** существующий HTTP health check доступен
- **AND** HTTP-запрос агента не принимается даже с заявленным forwarded-протоколом HTTPS

#### Scenario: An incomplete or unusable TLS configuration fails startup
- **WHEN** передан только один файл либо пару невозможно загрузить
- **THEN** listener не открывается и диагностика не раскрывает ключ или содержимое файлов

#### Scenario: The configured server performs a real TLS handshake
- **WHEN** агент доверяет сертификату настроенного HTTPS origin и имя совпадает
- **THEN** агент может выполнить привязку или проверку действующего ключа
- **AND** проверка доступности сервера работает через тот же защищённый listener

### Requirement: Enrollment failures and identity responses are safe to handle

Сервер SHALL ограничивать вход и возвращать различимые результаты для неверного запроса, недопустимых полномочий и временной недоступности операции. Ответы SHALL исключать секреты и чужие данные и не кэшироваться.

#### Scenario: A malformed or oversized request receives a bounded response
- **WHEN** тело привязки не соответствует формату или превышает ограничение
- **THEN** сервер отказывает до изменения состояния регистрации

#### Scenario: A storage failure cannot produce successful identity
- **WHEN** хранилище не может надёжно подтвердить привязку или действующий ключ
- **THEN** сервер возвращает безопасную ошибку недоступности
