# Web administrator access

## Purpose

Вход владельца локальной установки и браузерная сессия администратора.

## Requirements

### Requirement: The installation owner controls initial access and recovery

Первичная настройка и восстановление SHALL требовать локальных полномочий над установкой. Локальная настройка и сброс SHALL принимать секрет из терминала или приватного файла. Первичная настройка SHALL создавать одного администратора. Пароль SHALL сохраняться в проверяемой форме без доступной открытой копии.

#### Scenario: An unconfigured instance cannot be claimed in the browser
- **WHEN** браузер открывает ещё не настроенный экземпляр
- **THEN** доступ к данным закрыт, а UI показывает направление локальной настройки

#### Scenario: Concurrent local setup only succeeds once
- **WHEN** два оператора одновременно выполняют первичную настройку
- **THEN** одна операция создаёт администратора, а вторая получает безопасный отказ

#### Scenario: Local password replacement revokes current browser sessions
- **WHEN** владелец локально заменяет пароль администратора
- **THEN** старый пароль и все прежние веб-сессии теряют доступ

### Requirement: Browser sessions govern protected UI access over HTTPS

Проверенный вход SHALL выдавать новую ограниченную по времени веб-сессию через явно настроенный HTTPS. Истечение и выход SHALL прекращать доступ на сервере. Перезапуск сервера SHALL сохранять неистёкшие и неотозванные сессии. Ошибки входа SHALL быть безопасными, а повторные попытки ограничены.

#### Scenario: A valid password starts a distinct browser session
- **WHEN** настроенный администратор вводит правильный пароль по HTTPS
- **THEN** браузер получает новую сессию и доступ к защищённому UI

#### Scenario: An expired or signed-out session cannot operate
- **WHEN** сервер фиксирует выход из текущей сессии или истекает её срок
- **THEN** действие требует нового входа
- **AND** истёкший токен больше не принимается

#### Scenario: Restart preserves a valid session
- **WHEN** сервер перезапускается с той же базой до истечения сессии
- **THEN** действующая сессия сохраняет доступ

#### Scenario: An expired session leaves an open draft in its tab
- **WHEN** сессия истекает с несохранённым черновиком в открытой вкладке
- **THEN** вкладка сохраняет этот буфер и позволяет повторный вход
- **AND** повторный вход возвращает управление пользователю без автоматической отправки буфера

### Requirement: The UI session has a separate authenticated scope

Сервер SHALL принимать изменяющие UI-запросы только с действующей веб-сессией, подтверждённым origin и защитой запроса. Веб-сессия, полномочия агента и авторизация внешнего API SHALL быть независимы.

#### Scenario: A mismatched origin or CSRF token cannot modify data
- **WHEN** запрос содержит действующую cookie, но не проходит проверку origin или токена запроса
- **THEN** данные не изменяются

#### Scenario: Authentication modes remain independent
- **WHEN** изменяется настройка внешнего API либо отзывается ключ агента
- **THEN** действительность веб-сессии определяется только правилами UI
- **AND** веб-сессия сама по себе не даёт полномочия агента или внешнего API
