# Проверка B01

Change `add-variable-value-preparation`, [issue #14](https://github.com/SunnyDayDev/xkeen-ui-server/issues/14). Отчёт фиксирует выполненные проверки, а не запланированные.

## Исходное состояние

Go отсутствовал в PATH; ранее установленный SDK не найден. Восстановлен Go 1.27.1 darwin/arm64 из официального toolchain proxy, модульная сумма сверена с Go checksum database: `h1:51Yfd9AJPm34szJ1qdVX7+kqAGDd3vI9FzVuY7UqLfA=`. Это настройка окружения, не Red.

`go test ./internal/configuration/... -count=1` до изменений — PASS для ядра и elementjson. Кэш сборки размещён в игнорируемом каталоге сборки проекта.

## Циклы разработки

Все целевые запуски ниже использовали `go test ./internal/configuration/... -count=1` (иногда с `-run` для следующего сценария); после Green выполнялась регрессия существующих тестов.

Исходная проверка завершена без ошибок.

2.1: TestPrepareEmpty — Red: заглушка вернула nil вместо самостоятельного пустого набора. Green: пустой набор; все существующие тесты PASS.

2.2: TestPrepareInvalidDefinitions — Red: неверные имена/типы принимались. Green: проверки UTF-8, имени, пяти типов и дублей с путями; регрессии PASS.

2.3: TestPrepareExactNames — Red: лишнее имя принималось; Green: точные имена, независимые наборы, Unicode без нормализации, выбор присутствующего значения/дефолта; регрессии PASS.

3.1: TestPrepareMissingAndDefaults — Red: отсутствующая обязательная переменная дала успешный набор с nil. Green: проверка заполненности до типа, Unicode White_Space, дефолты и обязательность всех определений; регрессии PASS. Для определения пустой строки используется существующий строгий parseJSON.

3.2: TestPrepareFilledValues — PASS с первого запуска за счёт выбора исходного JSON в 3.1; это регрессионная проверка, отдельного Red не было. Проверены оба источника, U+200B, непустые строки с пробелами, false/0/пустые контейнеры и вложенные пустые строки.

3.3: TestPrepareTypeMismatch — Red: 40 комбинаций неверных типов принимались. Green: матрица пяти типов для value/default отклоняется с ожидаемым типом и корневым путём; пустой дефолт для number не мешает персональному числу. Регрессии PASS.

3.4 / 4.1: TestPrepareExactData и TestPrepareStrictJSON — регрессионный PASS с первого запуска. Точность и буквальность следуют из выбора исходного JSON; строгий разбор наследуется от parseJSON, уже необходимого в 3.1. Проверены nil/пустой документ, UTF-8, второй корень/хвост, дубликаты декодированных ключей и неверный невыбранный дефолт; отдельный Red не заявляется.

4.2: TestPrepareRootNull — Red: null дал type_mismatch вместо null_not_allowed. Green: корневой запрет раньше проверки типа в обоих источниках, без fallback; строка null допустима. Регрессии PASS.

4.3: TestPrepareNestedNull — Red: вложенный null принимался либо давал type_mismatch. Green: рекурсивная доменная проверка, массивы/объекты и экранированные пути в обоих источниках, невыбранные дефолты и приоритет null над типом. Общий парсер не изменён; регрессии PASS.

5.1: TestPrepareDiagnosticsAndInputs — регрессионный PASS: nil вместо частичного набора, правильные ID/имя/источник, входы не меняются, полная JSON-структура ошибок не содержит демонстрационного значения. Позиции определений и JSON дополнительно покрыты 2.2, 3.3, 4.1–4.3.

5.2: TestPrepareIndependentBuffers — Red: результат менял входы, входы меняли результат, записи результата разделяли буфер. Green: копия каждого выбранного RawMessage в самостоятельную map; все регрессии PASS.

5.3: TestPrepareThenSubstitute и TestPrepareStoredDraft — регрессионный PASS. Дефолт и персональное число подставляются A06; A08 Encode/Decode сохраняют черновик с null и частичной ссылкой, B01 проверяет только переданные определения/значения и отклоняет null дефолта. Выполнены gofmt и повтор всех целевых тестов — PASS.

При итоговом просмотре диагностики добавлен TestPrepareInvalidUTF8DiagnosticName: Red — неверный UTF-8 сохранялся в имени лишнего персонального ключа; Green — имя опускается, unknown_variable/value сохраняется. Случай определения также проверен. После уточнения и переименования помощника проверки в validatePreparedValue целевые тесты повторно PASS.

## Итоговые проверки

Go 1.27.1: go test ./... -count=1, go vet ./..., go test -race ./... -count=1 — PASS. Первый полный запуск в песочнице не смог открыть временные localhost-порты (operation not permitted); это ограничение среды, повтор с разрешёнными локальными портами прошёл. Обе сборки CGO_ENABLED=0 GOOS=linux GOARCH=amd64/arm64 go build -trimpath ./cmd/xkeen-ui-server — PASS. gofmt -l cmd internal — пустой вывод; git diff --check — PASS. Импорты ядра проверены: стандартная библиотека и внутренний jsonvalue, без I/O и внешних зависимостей.

Обновлены README, AGENTS.md, архитектура, модель конфигурации, описание прототипа и roadmap. Проверены локальные ссылки/якоря, JSON-примеры и отсутствие частных путей. Первичный скрипт проверки JSON ошибочно счёл обозначение Go-полей {Name, Type, Default} примером JSON; после исключения этой нотации проверка прошла. UI и сохранённый формат не изменены.

## Соответствие 20 сценариям дельты

| Сценарий | Проверка (PASS) |
| --- | --- |
| Prepared defaults feed whole value substitution | `TestPrepareThenSubstitute` |
| Empty definitions and values | `TestPrepareEmpty` |
| Preparation does not interpret stored content | `TestPrepareStoredDraft` |
| Invalid and duplicate definitions | `TestPrepareInvalidDefinitions` |
| Exact names and independent elements | `TestPrepareExactNames` |
| Unicode names are not normalized | `TestPrepareExactNames` |
| Missing values and Unicode whitespace select defaults | `TestPrepareMissingAndDefaults` |
| Whitespace is preserved unless the entire string is missing | `TestPrepareFilledValues` |
| Unused definition still needs a value | `TestPrepareMissingAndDefaults` |
| False zero and empty containers are filled | `TestPrepareFilledValues` |
| Wrong personal type does not fall back | `TestPrepareTypeMismatch` |
| Invalid unused default is still checked | `TestPrepareTypeMismatch` |
| Data remains literal and numbers remain exact | `TestPrepareExactData` |
| Present but invalid JSON is not missing | `TestPrepareStrictJSON` |
| All defaults are parsed strictly | `TestPrepareStrictJSON` |
| Root null never uses a default | `TestPrepareRootNull` |
| Nested null and unselected defaults are rejected | `TestPrepareNestedNull` |
| One failure prevents partial output | `TestPrepareDiagnosticsAndInputs` |
| Result buffers are independent | `TestPrepareIndependentBuffers` |
| Diagnostics identify definitions and hide data | `TestPrepareInvalidDefinitions / TestPrepareDiagnosticsAndInputs / TestPrepareInvalidUTF8DiagnosticName` |

## Приёмка issue #14

- Определения, имена, типы, дубликаты и лишние имена — TestPrepareInvalidDefinitions / TestPrepareExactNames / TestPrepareTypeMismatch.
- Обязательность, дефолты и пустые строки — TestPrepareMissingAndDefaults / TestPrepareFilledValues / TestPrepareTypeMismatch.
- Строгий JSON и рекурсивный null — TestPrepareStrictJSON / TestPrepareRootNull / TestPrepareNestedNull.
- Атомарный результат, точные данные, владение буферами и безопасные ошибки — TestPrepareExactData / TestPrepareDiagnosticsAndInputs / TestPrepareIndependentBuffers / TestPrepareInvalidUTF8DiagnosticName.
- Совместимость A06/A08 — TestPrepareThenSubstitute / TestPrepareStoredDraft и существующие тесты ядра/адаптера.
- Полные проверки, Linux-сборки и документация — результаты выше; строгая OpenSpec-валидация PASS.

## Состояние завершения

Локальная приёмка реализации и отчёта выполнена до git-коммита и push. Результат последующей публикации и слияния отслеживается в [issue #14](https://github.com/SunnyDayDev/xkeen-ui-server/issues/14). Change архивирован 2026-09-17 после синхронизации дельты с основной спецификацией configuration-variables. Основной контракт A05 сохранён; пять требований и 20 сценариев отдельной подготовки добавлены без изменения прежних 12 требований. Полная сборка и сырой шаблон B02, UI/API, публикация и устройства не реализованы. Issue #14 закрыта как completed после сверки критериев; состояние CLOSED подтверждено чтением GitHub. Обзорная #1 остаётся открытой, поскольку общий план не завершён.

Приёмка завершена: все 20 сценариев сопоставлены тестам, шесть критериев issue #14 подтверждены. openspec validate add-variable-value-preparation --strict — PASS.


## Синхронизация и архивирование — 2026-09-17

- Все четыре артефакта завершены, задачи 18/18; незавершённых задач нет.
- Дельта содержит только пять добавляемых требований. После синхронизации каждый блок и все 20 сценариев совпали с основной спецификацией; прежние 12 требований и Purpose сохранены. Повторное применение дельты не требует изменений.
- `openspec validate --specs` — PASS: 5 спецификаций, 0 ошибок. Информационные замечания о длине требований не являются ошибками.
- Архив: `openspec/changes/archive/2026-09-17-add-variable-value-preparation/`; метаданные `.openspec.yaml` сохранены. Ссылки из документации и внутри архива обновлены.
- Issue #14 закрыта; общий план #1 остаётся открытым. Код при синхронизации и архивировании не менялся; повторные Go-тесты не запускались, результаты реализации приведены выше.
