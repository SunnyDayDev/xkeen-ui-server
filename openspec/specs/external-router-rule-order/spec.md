## Purpose

Внешний клиент условно редактирует маршрутизацию одного роутера.

## Requirements

### Requirement: The external client presents an exact router order basis

Условный PATCH SHALL переставлять один точный общий или локальный UUID либо возвращать общий порядок. Неактивные позиции SHALL участвовать в полном списке.

#### Scenario: A conditional move changes only full order
- **WHEN** клиент предъявляет точное основание и задаёт новую позицию
- **THEN** меняется только персональный порядок, включая неактивные строки

#### Scenario: Confirmed reset restores inheritance
- **WHEN** клиент подтверждает возврат
- **THEN** локальные позиции сохраняются, а при их отсутствии вновь наследуется порядок

#### Scenario: Wrong source is refused atomically
- **WHEN** тег устарел, допуск отозван или координаты недопустимы
- **THEN** сохранённый порядок и остальные настройки остаются прежними
