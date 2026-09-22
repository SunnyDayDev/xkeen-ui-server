# Проверка B04: наследование только переменных

## Состояние

Реализация и локальная приёмка завершены; [issue #19](https://github.com/SunnyDayDev/xkeen-ui-server/issues/19) закрыта после выполнения всех критериев. Состояние CLOSED подтверждено отдельным чтением GitHub. Дельта синхронизирована с основной spec, change архивирован 2026-09-22.

## Среда и исходная проверка

Go 1.27.1 darwin/arm64 из локального SDK проекта; GOTOOLCHAIN=local, GOCACHE в игнорируемом .build. Ниже `go` означает этот SDK в PATH. Первоначальный вызов без SDK в PATH дал `command not found`: это ошибка окружения, не Red.

- `go test ./internal/configuration/... -count=1` — PASS до изменений кода.
- `openspec validate add-variable-only-inheritance --strict` — PASS до изменений кода.

## Матрица сценариев

Все перечисленные тесты реализованы и проходят; граница одного элемента проверена ревью. Дополнительные тесты диагностики и регрессий приведены в журнале ниже.

| Сценарий дельты | Проверка |
| --- | --- |
| Inheritance without personal values | `TestInheritedDefaults` |
| Template edit reaches a variable override | `TestInheritedTemplateEdit` |
| Default changes remain inherited | `TestInheritedDefaultChanges` |
| A different template is not the same source | `TestInheritedSourceMismatch` |
| Mismatched element UUID is rejected | `TestInheritedElementMismatch` |
| Empty values do not hide a wrong source | `TestInheritedEmptyWrongSource` |
| Source mismatch precedes variable validation | `TestInheritedSourceBeforeValues` |
| Absent and empty personal settings use the same defaults | `TestInheritedDefaults` |
| Invalid identity and context fail before assembly | `TestInheritedInvalidInputs` |
| Definition type changes preserve the personal value | `TestInheritedTypeChange` |
| Removed definitions do not silently discard values | `TestInheritedRemovedDefinition` |
| New required definitions block assembly without losing values | `TestInheritedNewRequired` |
| Template failures retain their original diagnostic context | `TestInheritedDiagnostics` |
| Successful results own their content and preserve JSON data | `TestInheritedIndependentContent` |
| Variable-only assembly does not handle a collection | `Ревью сигнатуры и импортов (5.1)` |

## Фактический журнал

- 2.1 `go test ./internal/configuration -run '^TestInheritedDefaults$' -count=1`: Red (3 случая: заглушка вернула nil без содержимого), после композиции B02 — Green. После gofmt повторный `go test ./internal/configuration/... -count=1` — PASS.
- 2.2 `go test ./internal/configuration -run '^TestInheritedInvalidInputs$' -count=1`: Red (15 случаев: неверные контексты/UUID пропускались или скрывались ошибкой переменных); после проверок — Green. После gofmt `go test ./internal/configuration -run '^TestInherited' -count=1` — PASS.
- 2.3 Отдельные `TestInheritedSourceMismatch` и `TestInheritedElementMismatch`: подтверждён Red (сборка с чужим источником / неверный приоритет), затем Green после соответствующей проверки. `TestInheritedEmptyWrongSource` и `TestInheritedSourceBeforeValues` добавлены после этих шагов: первоначальный Green благодаря общей проверке, без заявления Red. После gofmt все `^TestInherited` — PASS.
- 3.1 `TestInheritedTemplateEdit`: первоначальный Green с композицией B02; новое поле и персональное число сохранены. Логика не менялась.
- 3.2 `TestInheritedDefaultChanges`: первоначальный Green во всех трёх последовательных сценариях. B02 уже обеспечивает выбор актуального дефолта без изменения значений.
- 3.3 По одному добавлены и запущены `TestInheritedTypeChange`, `TestInheritedRemovedDefinition`, `TestInheritedNewRequired`: первоначальный Green кодов/полей ошибок B02 и сохранности данных. Контекст владельца проверяется отдельно в 4.1.
- 4.1 `TestInheritedDiagnostics`: Red во всех 7 случаях (пустая Location). После назначения контекста шаблона/роутера — Green; вся структура ошибки сравнивается с ожидаемой, включая отсутствие пользовательских данных. Повторный `^TestInherited` — PASS.
- 4.2 `TestInheritedIndependentContent`: первоначальный Green, 4 случая (object/array, default/value); точное число, порядок и буквальные строки, отсутствие метки и независимость буферов в обе стороны.
- 4.3 `TestInheritedVariableRegressions`, `TestInheritedCanonicalUUIDs`, `TestInheritedValidLocations`: первоначальный Green. Проверены null всех источников, неверный дефолт, лишнее имя, nil-значение, дубли ключей, неверная ссылка, корневые значения/массивы, экранирование и каноничность UUID. `go test ./internal/configuration/... -count=1` — PASS.
- 5.1 Ревью `inheritance.go`: импорты только encoding/json и unicode/utf8; I/O, зависимости на адаптеры и поля коллекции/порядка отсутствуют. `git diff` для assemble.go, element.go и elementjson пуст: низкоуровневые контракты не менялись. Успех ограничен одним наследуемым элементом.

## Общие проверки

- `gofmt -l cmd internal` — пустой вывод; `sh -n scripts/run-server.sh` — PASS.
- `go vet ./...` — PASS.
- Первые `go test ./... -count=1` и `go test -race ./... -count=1` в песочнице: ядро PASS, процессные/серверные тесты заблокированы запретом loopback bind. Это ограничения среды, не поведенческий Red.
- Повторные `go test ./... -count=1` и `go test -race ./... -count=1` с разрешёнными локальными портами — PASS во всех пакетах; jsonvalue не содержит отдельных тестов.
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o .build/b04/xkeen-ui-server-linux-amd64 ./cmd/xkeen-ui-server` — PASS.
- Аналогичная сборка с `GOARCH=arm64` — PASS. `file` подтвердил статические ELF x86-64 и ARM aarch64; запуск на физических устройствах не выполнялся.
- Первые сборки выдали предупреждение о недоступном внешнем кэше модулей. Повторены успешно с `GOMODCACHE` внутри игнорируемого `.build`, без предупреждения. Финальные тесты использовали тот же локальный кэш.

## Документация и контракт

- Строгая валидация `add-variable-only-inheritance` — PASS; основные specs — 6/6 PASS. Информационные сообщения о длине требований не являются ошибками.
- Проверены 12 Markdown-файлов: 124 локальные ссылки, 36 якорей, 37 JSON-примеров, 5 вхождений корректного UUIDv4 — PASS. Все 15 сценариев дельты связаны с существующими тестами либо ревью границы.
- Проверка публичных данных по шаблонам локальных путей/токенов/ключей и ревью примеров — PASS; примеры синтетические. `git diff --check` — PASS.
- README, AGENTS, архитектура, модель конфигурации, прототип и roadmap описывают именно один наследуемый элемент. Полная замена, наборы/исключения, хранение, UI и применение остаются вне результата B04.

## Приёмка issue #19

| Критерий | Подтверждение |
| --- | --- |
| Операция и источник результата | TestInheritedDefaults, TestInheritedTemplateEdit |
| Контекст/UUID и несовпадение до переменных | TestInheritedInvalidInputs, TestInheritedCanonicalUUIDs, TestInheritedSourceMismatch, TestInheritedElementMismatch, TestInheritedEmptyWrongSource, TestInheritedSourceBeforeValues |
| Изменение JSON/дефолтов | TestInheritedTemplateEdit, TestInheritedDefaultChanges |
| Изменение определений без потери данных | TestInheritedTypeChange, TestInheritedRemovedDefinition, TestInheritedNewRequired |
| Диагностика и независимость | TestInheritedDiagnostics, TestInheritedIndependentContent; снимки входов и полное сравнение структур ошибок |
| Red/Green и общие проверки | Фактический журнал и раздел общих проверок выше |
| Документация и OpenSpec | Раздел документации и контракта выше |

Новая реализация ограничена inheritance.go и её тестами. В основной спецификации сохранены остальные требования B03; уточнения B04 синхронизированы, change перенесён в архив. Git-коммит, push и запуск GitHub Actions в этой сессии не выполнялись; результат подтверждён локально. Xray, Docker-стенд, физический роутер и макет не проверялись, поскольку B04 — чистая операция ядра.

## Синхронизация и архивирование — 2026-09-22

В configuration-content-inheritance изменено одно требование, добавлено одно; шесть остальных требований и Purpose сохранены. Основная спецификация теперь содержит 8 требований и 35 сценариев. Оба блока дельты сверены с основной spec до переноса; неприменённых изменений нет. Строгая валидация основной spec и всех остальных — 6/6 PASS; change перед переносом — PASS. Все 15 задач завершены, метаданные .openspec.yaml сохранены при переносе. Issue #19 остаётся закрытой.

Архив: `openspec/changes/archive/2026-09-22-add-variable-only-inheritance/`. Ссылки артефактов и документации обновлены под новое расположение. Код не изменялся на этапе синхронизации/архивирования, Go-проверки повторно не запускались; их фактические результаты приведены выше.

После переноса: 12 Markdown-файлов, 124 локальные ссылки, 36 якорей, 43 JSON-примера и 5 UUIDv4 — PASS; проверка публичных данных и `git diff --check` — PASS. `openspec list --json` подтвердил отсутствие активных change.
