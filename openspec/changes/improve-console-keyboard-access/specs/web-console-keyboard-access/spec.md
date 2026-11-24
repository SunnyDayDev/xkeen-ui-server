## Purpose

Выполнять основные действия консоли с клавиатуры и на узкой ширине.

## ADDED Requirements

### Requirement: Owners can complete primary console tasks with the keyboard

Готовые основные сценарии консоли SHALL иметь видимый фокус и последовательность действий с клавиатуры, включая навигацию, ввод, подтверждение и доступное продолжение после ошибки.

#### Scenario: An operation replaces the focused control

- **WHEN** подтверждение, отказ или открытие меню меняет доступные действия
- **THEN** фокус остаётся в текущей области работы или переходит к её следующему понятному действию

#### Scenario: The administrator reauthenticates after session expiry

- **WHEN** сессия истекает во время допустимого редактирования
- **THEN** вход и возвращение к буферу доступны с клавиатуры, а прежнее действие не отправляется повторно

### Requirement: Primary console tasks retain usable layout at narrow width

На ширине 320 пикселей основные разделы SHALL сохранять доступные по клавиатуре действия, подписи и ошибки без переполнения документа по горизонтали.

#### Scenario: A view contains a long identifier or authoring buffer

- **WHEN** пользователь открыл узкий экран с длинным ID или JSON
- **THEN** документ сохраняет ширину окна, длинное значение остаётся доступным в своей области
