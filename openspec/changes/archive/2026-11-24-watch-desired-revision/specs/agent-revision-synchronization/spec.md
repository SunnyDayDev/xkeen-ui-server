## ADDED Requirements

### Requirement: Agent watch observes only its authorized latest desired number

Агент SHALL ожидать подсказку через `GET /agent/v1/watch` с номером последнего целого проверенного снимка или нулём. Сервер SHALL ограничивать время и число запросов и использовать только область действующего рабочего ключа. Ответ SHALL различать подсказку, штатное истечение ожидания и безопасный отказ.

#### Scenario: An already changed revision does not wait
- **WHEN** последний известный номер отличается от текущего зафиксированного номера
- **THEN** сервер сразу возвращает подсказку без каталога

#### Scenario: Duplicate requests cannot displace the authorized waiter
- **WHEN** для одного роутера уже выполняется запрос ожидания
- **THEN** лишний запрос получает безопасное ограничение, а исходный остаётся в своём сроке

#### Scenario: A stable state times out without a contact
- **WHEN** срок ожидания истёк и номер совпадает
- **THEN** сервер возвращает 204
- **AND** контакт и подтверждённая ревизия не меняются

### Requirement: Only committed revision changes cause a watch hint

Сервер SHALL повторно читать актуальный номер при установке и пробуждении ожидания. Успешный commit изменения SHALL уведомлять только затронутые изменившиеся области. Неуспешная, равная и чужая публикация SHALL оставлять ожидание прежним.

#### Scenario: Publication during waiter registration is observed
- **WHEN** публикация фиксируется между началом запроса и его регистрацией либо сразу после неё
- **THEN** повторное чтение или подсказка обнаруживают новую версию без потери изменения

#### Scenario: A group publishes changes for only part of its members
- **WHEN** групповая операция фиксирует две ревизии, а третий каталог остаётся равным
- **THEN** подсказку получают только оба роутера с новыми номерами

#### Scenario: Rollback cannot wake an agent as a changed revision
- **WHEN** подготовленная запись ревизий откатывается
- **THEN** агент не получает подсказку об откатившихся данных

### Requirement: Watchers release resources without stale authorization

Сервер SHALL проверять поколение перед выдачей подсказки и освобождать waiter при отзыве, замене, клиентской отмене и остановке. Агент SHALL сохранять возможность получить полный снимок без подсказки.

#### Scenario: Revoked authorization cannot receive a later change
- **WHEN** прежний ключ отозван или заменён до следующей фиксации
- **THEN** ожидающий запрос прежнего поколения прекращается с безопасным отказом

#### Scenario: Shutdown cancels pending watches before draining
- **WHEN** сервер начинает штатное завершение
- **THEN** ожидающие запросы отменяются до ожидания завершения других активных запросов

#### Scenario: Repeated or missing hints only cause a complete reread
- **WHEN** сигнал дублируется, пропадает или срок истекает
- **THEN** агент сохраняет периодическую прямую сверку
- **AND** до успешной проверки полного снимка номер известной ревизии не продвигается
