## ADDED Requirements

### Requirement: Controlled application is an explicitly provisioned Entware role

Установка роли применения SHALL выбирать конкретный проверенный выпуск и согласованные root-owned локальные параметры с подтверждённым делегированием. Рабочий Xray, supervisor и агент SHALL получать отдельные полномочия. Неподтверждённая среда SHALL завершаться отказом до активации.

#### Scenario: An attested baseline enables the installed application service

- **WHEN** владелец подтверждает защищённый профиль с точным RouterID, origin, CA, ядром и первоначальным рабочим Xray
- **THEN** зарегистрированная служба запускает supervisor и агента в нужном порядке, сохраняет журнал и использует доказательство штатного supervisor при следующих стартах

#### Scenario: Mismatched capabilities or authority preserve the existing service

- **WHEN** среда, прошивка, привязка процесса или доверенный параметр не совпадают
- **THEN** установщик сообщает отказ без переключения существующей службы и без утверждения о применении

#### Scenario: Contact is reported separately from application

- **WHEN** установленный агент подключён, но нет действительного доказательства E08
- **THEN** сервер может показать контакт, а ревизия не отмечается применённой
