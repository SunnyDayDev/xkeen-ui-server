## ADDED Requirements

### Requirement: An external client reads evidence for one router revision

HTTPS GET SHALL возвращать только сохранённое безопасное свидетельство для точной пары роутер и ревизия.

#### Scenario: An exact allowed request succeeds

- **WHEN** запрошенная ревизия существует и имеет доказательство D06/E08
- **THEN** ответ отделяет контакт, историю applied, результат и состояние runtime

#### Scenario: Evidence or authority is unavailable

- **WHEN** свидетельство отсутствует или текущий runtime неопределён
- **THEN** ответ сообщает unknown для недоказанного результата и не выводит success из номера desired
