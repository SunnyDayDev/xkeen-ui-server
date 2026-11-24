## Purpose

Показать итоговые файлы роутера в авторизованной браузерной области центрального сервера.

## ADDED Requirements

### Requirement: Catalog preview exposes the server assembled bytes

Владелец SHALL уметь просмотреть и скачать итоговые файлы одного явно выбранного кандидата роутера. В обеих формах SHALL использоваться один полный точный каталог B12. Ошибки сборки SHALL исключать частичную выдачу.

#### Scenario: Preview and download use the same rendered content
- **WHEN** согласованный кандидат успешно собирается на сервере
- **THEN** имена и байты в просмотре и скачивании совпадают, включая неизвестные поля

#### Scenario: An invalid candidate cannot masquerade as verified files
- **WHEN** хотя бы одна обязательная часть каталога не собирается
- **THEN** сервис возвращает ошибку, а сохранённая желаемая ревизия не меняется
