# server-docker-deployment Specification

## Purpose

Определить запуск центрального сервера в Docker с явным доверием TLS и долговременным состоянием SQLite, включая встроенный интерфейс управления.

## Requirements

### Requirement: The packaged server runs with explicit durable state and optional TLS

Образ SHALL запускать центральный сервер непривилегированным процессом на Linux AMD64 и ARM64. Рабочий том SHALL хранить SQLite, включая применённые миграции и данные администратора.

#### Scenario: An empty named volume starts the service
- **WHEN** владелец запускает образ с новым именованным томом в рабочем местоположении
- **THEN** сервер завершает миграции и отвечает на проверку доступности

#### Scenario: Explicit TLS serves the embedded interface
- **WHEN** сервер получает читаемые сертификат, ключ и согласованный HTTPS origin
- **THEN** он сохраняет контракт существующей авторизации и обслуживает встроенную HTML-страницу с её ресурсами
- **AND** создание пароля выполняется защищённой локальной командой

#### Scenario: Recreated service keeps durable administrator state
- **WHEN** контейнер штатно завершается и новый контейнер использует тот же том
- **THEN** ранее сохранённый пароль и действующая серверная сессия продолжают работать
