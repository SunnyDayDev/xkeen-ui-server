## Purpose

Внешний клиент условно редактирует маршрутизацию одного роутера.

## Requirements

### Requirement: The external client presents an exact router order basis

Вставка SHALL сохранять точный JSON, новый UUID, полный порядок и основание в одной персональной записи. Сервер SHALL сверять F20 и точную версию.

#### Scenario: Insert after an inactive member
- **WHEN** клиент использует точный ETag и позицию после отключённого или исключённого UUID
- **THEN** полный список сохраняет относительную позицию, собственный UUID и буквальный JSON

#### Scenario: A stale or invalid insertion does not commit
- **WHEN** основание конфликтует, позиция чужая, JSON недопустим или допуск отозван
- **THEN** локальное правило, порядок и желаемая ревизия не меняются
