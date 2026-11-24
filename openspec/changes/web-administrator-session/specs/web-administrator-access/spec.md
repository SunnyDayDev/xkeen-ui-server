## ADDED Requirements

### Requirement: Local administrator commands control the application database

Локальные команды настройки и сброса SHALL работать с указанной базой установки. При локальном вводе SHALL приниматься только скрытый терминальный ввод или выбранный приватный файл. Настройка SHALL отказывать при уже существующем администраторе, а сброс SHALL отзывать все прежние веб-сессии.

#### Scenario: An unsafe password file cannot create or change an administrator
- **WHEN** указанный файл доступен посторонним, подменён ссылкой, не является обычным файлом или содержит неподходящий пароль
- **THEN** команда завершает работу безопасным отказом без смены полномочий

#### Scenario: Password reset races with an older successful verification
- **WHEN** вход проверил прежний пароль, а локальный сброс успел заменить поколение до выдачи сессии
- **THEN** проверка прежнего пароля не даёт действующей веб-сессии

### Requirement: Browser routes report bounded session states and enforce protected action

HTTP-канал UI SHALL быть доступен только на настроенном HTTPS origin. Статус SHALL различать отсутствие настройки, отсутствие действительной сессии и действительный вход. Вход SHALL выдавать новый непрозрачный идентификатор, а выход и продление SHALL требовать сессии, соответствующего origin и CSRF.

#### Scenario: Status distinguishes setup, anonymous and authenticated views
- **WHEN** браузер запрашивает состояние на настроенном HTTPS origin
- **THEN** он получает одно из трёх состояний, а токен действий доступен только с действующей сессией

#### Scenario: Passive reads do not extend a browser session
- **WHEN** после входа идут только чтение статуса либо фоновая загрузка
- **THEN** по окончании предела без действий сессия теряет доступ
- **AND** явное подтверждённое действие может продлить этот предел до абсолютного срока

#### Scenario: Secure login rotates the browser credential and bounds repeated attempts
- **WHEN** браузер успешно входит на настроенном origin
- **THEN** выдаётся новая защищённая host-only cookie
- **AND** слишком частые запросы получают безопасный отказ без выдачи сессии

#### Scenario: A different origin cannot log in, renew or sign out the browser
- **WHEN** меняющий запрос приходит без соответствующего origin либо без действующего токена действий там, где он нужен
- **THEN** сервер оставляет состояние администратора и сессии без изменений
