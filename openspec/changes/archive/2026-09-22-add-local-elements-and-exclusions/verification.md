# Проверки B06

## Журнал TDD/BDD

Все целевые запуски используют Go 1.27.1, `go test ./internal/configuration -run <имя> -count=1`; Go взят из локального SDK проекта, GOCACHE направлен в .build.

| Шаг | Тест | До логики | После логики |
| --- | --- | --- | --- |
| 1.1 | TestSelectionEmpty | Red: nil вместо успешного пустого набора | Green после создания непустого указателя и пустого map; gofmt |
| 1.2 | TestSelectionSharedDefaults | Red: пустой map вместо общего элемента | Green после вызова B05 |
| 1.2 | TestSelectionPersonalValuesAndTemplateEdits | Red: 443 вместо 8443 | Green после передачи переопределения и его позиции |
| 1.2 | TestSelectionSharedReplacement | Первоначальный Green через B05; проверены буквальность/null и игнорирование неактивных ошибок | Изменений логики не требовалось |
| 1.3 | TestSelectionLiteralLocal | Red: локальный элемент отсутствует | Green после буквального копирования |
| 1.3 | TestSelectionLocalRoots, TestSelectionActiveNull | Первоначальный Green; шесть корней и три активных источника null | Прежние контракты сохранены |
| 1.4 | TestSelectionAbsentLocalJSON | Red: отсутствующий JSON принят | Green после parseJSON |
| 1.4 | TestSelectionInvalidLocalJSON | Первоначальный Green всех семи классов через существующий парсер | Исправлений не требовалось |
| 2.1 | TestSelectionIdentityCollisions | Red: дубли приняты; исключённый конфликт дал invalid_json вместо duplicate_element_id | Green после полной проверки ID до содержимого |
| 2.1 | TestSelectionNewSharedCollision | Первоначальный Green | Снимок не изменён, конфликт нового общего обнаружен |
| 2.2 | TestSelectionMetadata | Red для UUID переопределения и четырёх ошибок исключения; остальные случаи первоначально Green | Green после проверки обоих списков ссылок до содержимого |
| 2.2 | TestSelectionUnknownExcludedMode | Red: invalid_json более раннего элемента вместо ошибки режима | Green после проверки всех режимов до содержимого |
| 2.3 | TestSelectionBindingSources | Red для missing/local_only обоих типов; foreign_* первоначально Green | Green после проверки существования общего источника |
| 2.4 | TestSelectionDuplicateBindings | Первоначальный Green через проверку ссылок 2.2 | Обе позиции и приоритет метаданных подтверждены |
| 2.4 | TestSelectionExclusionWithOverride | Red: исключённая полная замена возвращена | Green после фильтрации по исключениям |
| 3.1 | TestSelectionExcludedErrors | Первоначальный Green после фильтрации 2.4 | Шесть классов скрытых ошибок, пустой результат, локальный сосед и ошибки после снятия |
| 3.2 | TestSelectionRepeatedExclusion | Первоначальный Green | Два снимка и независимый другой роутер |
| 3.3 | TestSelectionRemoveExclusion | Первоначальный Green | Оба режима восстановлены с актуальным шаблоном |
| 3.4 | TestSelectionResetWhileExcluded | Первоначальный Green композиции с B05 | Сброс сохраняет исключение, затем текущий дефолт либо missing_value без восстановления данных |
| 4.1 | TestSelectionMixedAndIndependent, TestSelectionAtomicDiagnostics | Первоначальный Green: копии B05/bytes.Clone и атомарный возврат уже введены в 1.2–1.4 | Пять элементов, оба направления мутации, независимые Source/буферы, восемь диагностических ветвей |
| 4.2 | TestSelectionUnorderedAndLiteralFlags | Первоначальный Green | Перестановка входов, enabled и служебная метка буквальны |

Рефакторинг после этих проверок выделил `validateSelectionMetadata` из операции выбора; `go test ./internal/configuration/... -count=1` — PASS. Импорты ядра проверены через `go list`: стандартная библиотека и `internal/jsonvalue`, без HTTP/БД/elementjson/I/O. Никаких тестов с искусственным Red не добавлялось: уже работающие сценарии отмечены первоначальным Green.

## Сценарии дельты и тесты

| Сценарий | Проверка |
| --- | --- |
| Local strings and exact JSON remain unchanged | `TestSelectionLiteralLocal` |
| Local JSON supports every root type | `TestSelectionLocalRoots` |
| Malformed local JSON blocks the whole selection | `TestSelectionAbsentLocalJSON`, `TestSelectionInvalidLocalJSON` |
| Duplicate local keys are rejected at any depth | `TestSelectionInvalidLocalJSON` |
| Identity collisions are detected before exclusions | `TestSelectionIdentityCollisions` |
| A new shared element can conflict with an existing local element | `TestSelectionNewSharedCollision` |
| Invalid metadata fails even for an empty or excluded selection | `TestSelectionMetadata` |
| A foreign template cannot supply a binding | `TestSelectionBindingSources` |
| Missing shared sources are rejected without cleanup | `TestSelectionBindingSources` |
| Repeated bindings are errors rather than last write wins | `TestSelectionDuplicateBindings` |
| An exclusion can coexist with one override | `TestSelectionExclusionWithOverride` |
| Unknown modes are not hidden by exclusions | `TestSelectionUnknownExcludedMode` |
| Excluded inherited errors do not block other elements | `TestSelectionExcludedErrors` |
| Exclusion hides an invalid replacement without destroying it | `TestSelectionExcludedErrors` |
| Repeated selection preserves exclusion across template edits | `TestSelectionRepeatedExclusion` |
| Removing an exclusion restores the current mode | `TestSelectionRemoveExclusion` |
| Reset under exclusion does not resurrect the element | `TestSelectionResetWhileExcluded` |
| Mixed content uses the existing active branches | `TestSelectionMixedAndIndependent`, `TestSelectionActiveNull`, `TestSelectionPersonalValuesAndTemplateEdits`, `TestSelectionSharedReplacement` |
| Empty selection is a successful value | `TestSelectionEmpty`, `TestSelectionExcludedErrors` |
| A late content failure yields no partial result | `TestSelectionAtomicDiagnostics` |
| Results and diagnostics do not retain mutable input data | `TestSelectionMixedAndIndependent`, `TestSelectionAtomicDiagnostics` |
| Selection does not invent routing order or disable semantics | `TestSelectionUnorderedAndLiteralFlags` |

Исходные сценарии B03 `Local element is added alongside a shared element`, `Same tag does not create an override`, `Exclusion does not free the shared UUID` покрыты `TestSelectionLiteralLocal`, `TestSelectionRepeatedExclusion` и `TestSelectionIdentityCollisions`. `Excluded shared element stays absent`, `Exclusion also hides a full replacement`, `Reset while excluded preserves the exclusion` покрыты тестами повторного исключения, снятия и сброса выше. Атомарность и независимость B03 покрыты `TestSelectionAtomicDiagnostics` и `TestSelectionMixedAndIndependent`. Действующий контракт порядка не реализуется B06; проверяется только отсутствие зависимости выбора содержимого от входного порядка.

## Проверки реализации — 2026-09-22

| Проверка | Результат |
| --- | --- |
| `go test ./internal/configuration/... -count=1` после рефакторинга | PASS |
| `go test ./... -count=1` | PASS: cmd, configuration, elementjson, server; jsonvalue без собственных тестов |
| `go test -race ./... -count=1` | PASS |
| `go vet ./...` | PASS |
| `gofmt -l internal/configuration/selection.go internal/configuration/selection_test.go` | Пустой вывод |
| `sh -n scripts/run-server.sh` | PASS |
| `git diff --check` | PASS; новые файлы проверены отдельно |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...` | PASS |
| `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...` | PASS |
| `go test -c` всех пакетов для Linux amd64/arm64 без cgo | PASS: компиляция тестов, не их запуск на Linux |
| `go build -trimpath` сервера для Linux amd64/arm64 без cgo | PASS; `file` подтверждает статические ELF x86-64 и ARM aarch64 |

Процессным тестам разрешены loopback-порты. Изначально Go не был в PATH; использован существующий локальный SDK 1.27.1, без загрузки и смены версии. При кросс-компиляции Go выводил предупреждение о недоступной записи служебного module-cache вне рабочей области; команды сборки завершились успешно, артефакты получены. Это не поведенческий Red. Постоянное хранилище, Xray/Entware runtime, UI и макет не проверялись: B06 их не добавляет.

## Приёмка и состояние

Восемь критериев [issue #23](https://github.com/SunnyDayDev/xkeen-ui-server/issues/23) сопоставлены четырём требованиям дельты, таблице всех 22 сценариев и проверкам выше. Реализована чистая операция полного выбора, сохранены контракты A08/B01/B02/B04/B05 и формат elementjson. Обновлены README, AGENTS, архитектура, модель конфигурации, прототип и roadmap. Документы отделяют набор содержимого от порядка, зависимостей, хранения после перезапуска и готовности Xray.

На момент приёмки реализации change оставался активным, основная спецификация ещё не была синхронизирована с дельтой B06. Синхронизация и архивирование выполнены следующим этапом, описанным ниже. Коммит, push и GitHub Actions в этой сессии не выполнялись; результат находится в рабочем дереве. Issue #23 закрыта как выполненная после финальной проверки материалов.

Финальная проверка материалов: `openspec validate add-local-elements-and-exclusions --strict` — PASS; основные specs — 6/6 PASS. Проверены 11 Markdown-файлов, 141 локальная ссылка и 46 якорей, 72 корректных JSON-примера и один явно отрицательный пример повторного ключа, три UUIDv4 в документах. Все 22 сценария сопоставлены 23 тестовым функциям (с подслучаями); новые файлы проверены на форматирование и публичные данные. `git diff --exit-code` подтвердил отсутствие изменений основных specs и прежних реализаций B01/B02/B04/B05/Element/elementjson.

## Синхронизация и архивирование — 2026-09-22

В [основную спецификацию](../../../specs/configuration-content-inheritance/spec.md) добавлены четыре требования и 22 сценария B06. Все прежние 11 требований, 55 сценариев и Purpose сохранены; итог — 15 требований и 77 сценариев. Повторное сравнение подтвердило точное соответствие всех добавленных блоков дельте.

Перед переносом change прошёл строгую валидацию; синхронизированные основные specs — 6/6 PASS. Все 17 задач выполнены, change перенесён в этот архив вместе с исходным `.openspec.yaml` (schema `spec-driven`). Ссылки и статусы в документации обновлены. Issue #23 закрыта. Код на этом этапе не менялся; проверки Go из приёмки выше повторно не запускались. Коммит и push не выполнялись.

Проверка после переноса: 12 Markdown-файлов, 142 локальные ссылки и 46 якорей; 119 корректных JSON-примеров, два вхождения намеренно отрицательного примера повторного ключа и пять UUIDv4. Публичные данные и пробелы — PASS. Контрольные суммы всех исходных Go-файлов совпали со снимком перед архивированием. `openspec list --json` подтвердил отсутствие активных изменений, `git diff --check` — PASS. Состояние issue #23 повторно проверено: CLOSED.
