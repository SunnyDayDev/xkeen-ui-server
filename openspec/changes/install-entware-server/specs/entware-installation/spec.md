## ADDED Requirements

### Requirement: A fresh Entware server runs with explicit TLS and durable state

Установщик SHALL получать проверенный ARM64 выпуск, явный HTTPS origin, адрес слушателя, сертификат и приватный TLS-ключ. Сервер SHALL запускаться как выделенный непривилегированный пользователь. SQLite SHALL сохраняться в отдельном приватном каталоге.

#### Scenario: An installed server exposes the web shell over direct TLS

- **WHEN** владелец подтверждает корректный выпуск, TLS, роль и адрес
- **THEN** служба отвечает на HTTPS health, отдаёт встроенные HTML и ассет, а локальная настройка администратора использует штатный пароль из терминала или приватного файла владельца процесса

#### Scenario: Restart keeps the same administrator and database

- **WHEN** владелец останавливает и снова запускает подтверждённую службу через её регистрацию
- **THEN** она использует тот же SQLite, HTTPS, настройки и учётную запись администратора
- **AND** каталог агента и его собственная служба не изменяются

#### Scenario: Wrong TLS configuration prevents a confirmed install

- **WHEN** входная TLS-пара, адрес либо подтверждение слушателя некорректны
- **THEN** новая служба не включается в init.d и чужой процесс продолжает работать
