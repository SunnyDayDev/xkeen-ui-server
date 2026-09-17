# Проверка B02

Задача: [#16](https://github.com/SunnyDayDev/xkeen-ui-server/issues/16). Ниже зафиксирована локальная приёмка. Результат последующей публикации и CI отслеживается в связанной issue.

## Окружение и исходное состояние

Go 1.27.1 darwin/arm64 из локального SDK проекта; GOTOOLCHAIN=local, GOCACHE в игнорируемом .build. В командах ниже `go` означает этот SDK. Первый вызов без настройки PATH завершился `command not found`; это ошибка окружения, не Red. После подключения уже установленного SDK исходная проверка `go test ./internal/configuration/... ./internal/jsonvalue/... -count=1` прошла: configuration и elementjson — PASS; jsonvalue — нет отдельных тестов.

## План трассировки

- Синтаксис, null и локализация — `template_syntax_test.go` через SubstituteValues.
- Единая операция, все источники данных/ошибок и независимость — `assemble_test.go`.
- Черновики — существующие проверки A08 плюс проверка сохранения/чтения с последующим отказом сборки.
- Подготовка, числа, точные имена и строгий JSON — существующие A06/B01 и сквозные проверки новой границы.

## Фактические циклы

Исходная проверка пройдена до правок кода. Issue #16 открыта, содержит все шесть критериев приёмки.

2.1: TestTemplateNull — Red: 3 случая возвращали null без ошибок. Добавлен доменный nullPath после разбора шаблона. Green и регрессии: go test ./internal/configuration/... -count=1 — PASS. Переиспользована существующая проверка; общий JSON-парсер не менялся.

2.2: TestTemplateEscapes — Red: буквальные ссылки и пары долларов не преобразовывались. Минимальная обработка неподлежающих подстановке строк дала Green; go test ./internal/configuration/... -count=1 — PASS. Обработка составных ссылок следует отдельным циклом 2.3.

2.3/2.4: перед сканером отдельно запущены TestTemplateInterpolation (10 ошибок Red: пропуск либо unknown_variable) и TestTemplateInvalidReferences (ошибочные имена принимались, незакрытые строки пропускались). Однопроходный сканер заменил минимальную обработку 2.2, проверяет имя до lookup и классифицирует строку целиком. Green и регрессии: go test ./internal/configuration/... -count=1 — PASS.

2.5: TestTemplateDecodedSyntaxAndDiagnostics — PASS сразу, регрессия после сканера: JSON-экранирование, буквальные ключи, backslash, источник/имя/JSON Pointer, безопасная диагностика, invalid_json с offset 2. Дополнительная логика не понадобилась; Red не заявляется.

3.1: TestAssembleElement — Red на компилируемой заглушке (отсутствует корректный JSON-результат), затем композиция PrepareValues → SubstituteValues. Green и регрессии: go test ./internal/configuration/... -count=1 — PASS. Ошибка сборки не использовалась как Red.

3.2: TestAssemblePreparationErrors и TestAssembleDefaultsAndExactNames — PASS сразу через композицию. Проверены все заявленные коды и источники B01, пути, ExpectedType, неиспользуемое определение/дефолт, Unicode whitespace, точные имена. Это сквозные регрессии, дополнительного Red/кода нет.

3.3/3.4: TestAssembleStrictInputs и TestAssembleTemplateContract — PASS сразу, сквозные регрессии. Проверены синтаксис/UTF-8/дубликаты/null всех трёх источников (включая неиспользуемые и невыбранные), точные позиции и пути, nil личного значения против отсутствия, отсутствие автообъявления и независимость вызовов.

4.1/4.2: TestAssemblyLiteralData, TestAssembleTypesAndNumbers, TestAssembleInputOwnership — PASS сразу (регрессии). Обе границы сохраняют буквальные данные; сборка проверена на пяти типах, точных числах, произвольных полях, порядке массивов, независимости результата и неизменности всех входов при успехе/ошибке. Секреты примеров и неверное имя не попадают в JSON-диагностику.

4.3: TestDraftRoundTripBeforeAssembly — PASS: null, частичная ссылка и незаполненная переменная сохраняются/читаются, сборка отказывает и не меняет запись. После форматирования и рефакторинга полный целевой запуск go test ./internal/configuration/... ./internal/jsonvalue/... -count=1 — PASS; общий JSON-парсер, хранение и обнаружение объявлений не менялись.

## Итоговые проверки

- `go test ./internal/configuration/... ./internal/jsonvalue/... -count=1` — PASS после форматирования/рефакторинга.
- Первый `go test ./... -count=1` в песочнице: ядро PASS, процессные/серверные тесты не могли открыть loopback (`bind: operation not permitted`). Это ограничение среды, не поведенческий Red. Также наблюдалось предупреждение записи модульного кэша; кэш перенесён в игнорируемый `.build/go-mod-cache`.
- `go test ./... -count=1` с разрешённым loopback — PASS всех пакетов с тестами.
- `CGO_ENABLED=1 go test -race ./... -count=1` с разрешённым loopback — PASS всех пакетов с тестами.
- `go vet ./...` — PASS; повтор после настройки модульного кэша без предупреждений.
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o .build/xkeen-ui-server-b02-linux-amd64 ./cmd/xkeen-ui-server` — PASS.
- `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o .build/xkeen-ui-server-b02-linux-arm64 ./cmd/xkeen-ui-server` — PASS. `file` подтвердил ELF x86-64/AArch64, оба статически слинкованы.
- `gofmt -l cmd internal` — пусто; `git diff --check` — PASS.
- `openspec validate complete-element-variable-assembly --strict` — PASS.
- Проверка обновлённых документов и артефактов: 105 локальных ссылок/якорей и 10 JSON-примеров (блоки и RawMessage) — PASS; частных путей/ключей в них нет. Невалидные JSON-примеры в сценариях отмечены явно.
- Изменения ядра просмотрены: только стандартная библиотека и существующие доменные операции, без I/O и новых внешних зависимостей. `go.mod`, общий JSON-парсер, реализация сохранения и обнаружения объявлений не менялись.

## Трассировка дельты

| Сценарий | Проверка |
| --- | --- |
| Definitions defaults and template are assembled together | TestAssembleElement |
| Every definition and unselected default is checked | TestAssemblePreparationErrors |
| Invalid definitions and extra personal values block assembly | TestAssemblePreparationErrors |
| Assembly never declares an unknown reference | TestAssembleTemplateContract |
| Supplied invalid JSON and null cannot bypass preparation | TestAssembleStrictInputs |
| All supported roots and exact numbers remain supported | TestAssembleTypesAndNumbers |
| Escapes and isolated dollars are resolved once | TestTemplateEscapes |
| Odd dollar run leaves a real embedded reference | TestTemplateInterpolation |
| Partial references are rejected before lookup | TestTemplateInterpolation |
| Malformed references are not unknown variables | TestTemplateInvalidReferences |
| JSON escapes are decoded before template syntax | TestTemplateDecodedSyntaxAndDiagnostics |
| Backslash is not a template escape | TestTemplateDecodedSyntaxAndDiagnostics |
| Object keys are never processed as template strings | TestTemplateDecodedSyntaxAndDiagnostics |
| Root and nested template null are rejected | TestTemplateNull, TestAssembleStrictInputs |
| Null text remains data | TestTemplateNull |
| A saved draft can still fail assembly | TestDraftRoundTripBeforeAssembly |
| Supplied strings and nested structures are never templates | TestAssemblyLiteralData |
| A chosen default remains literal data | TestAssemblyLiteralData |
| Nested syntax errors have safe locations | TestTemplateDecodedSyntaxAndDiagnostics, TestAssembleTemplateContract |
| Multiple references do not get an arbitrary diagnostic name | TestTemplateDecodedSyntaxAndDiagnostics |
| JSON failures stay distinct from reference failures | TestAssembleStrictInputs |
| Failure and success preserve input ownership | TestAssembleInputOwnership |

Существующие TestPrepare* сохраняют всю матрицу типов, заполненности, дефолтов и строгого JSON B01. TestAssembleDefaultsAndExactNames добавляет сквозной контроль Unicode White_Space, точек, регистра и отсутствия Unicode-нормализации. TestExactNumbers/TestInputsAndDiagnostics и остальные тесты A06 остаются регрессиями низкоуровневой границы. Тесты A08 проверяют идентичность, точный round-trip и объявления. Все эти тесты выполнены в полном запуске.

## Приёмка issue #16

Все шесть критериев выполнены локально: единая операция; синтаксис/null; граница данных/типы/числа; безопасная локализация; сохранение черновиков и фактические Red/Green; общие проверки и документация. На этапе локальной реализации коммит, push, PR и CI не выполнялись. Доставка/применение на устройстве, Xray, метка и межэлементная сборка не проверялись и не заявляются.

Документация актуализирована по фактической реализации. Дельта: все 22 сценария сопоставлены выполненным тестам; критерии #16 сверены, реализация готова к завершению. Осталась операционная задача синхронизации/архива и закрытия issue.

## Завершение

2026-09-17: пять требований и 22 сценария дельты синхронизированы с основной configuration-variables; предыдущий текст спецификации сохранён без изменений. `openspec validate --specs --strict` — 5/5 PASS. Change архивирован, `openspec list --json` возвращает пустой список. Локальные ссылки повторно проверены после переноса. Issue #16 закрыта как completed; состояние CLOSED подтверждено через GitHub. Обзорная #1 остаётся OPEN. Выполнены 18/18 задач. На момент этой локальной приёмки изменения находились в рабочем дереве без коммита и публикации.
