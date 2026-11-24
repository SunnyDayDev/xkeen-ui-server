## ADDED Requirements

### Requirement: A managed agent checks its pinned router before synchronization

При явно заданном техническом ID агент SHALL включать его в запрос погашения, проверять возвращённый ID и сохранённую активную регистрацию до запуска контакта и синхронизации.

#### Scenario: The saved or received identity differs from the managed profile

- **WHEN** сохранённая привязка или подтверждённая сервером идентичность относится к другому RouterID
- **THEN** агент не запускает контакт, получение желаемого состояния или локальное применение под чужой привязкой
