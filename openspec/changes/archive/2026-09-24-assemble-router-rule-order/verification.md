# Проверка B08 · 2026-09-23

## Сценарии и тесты

| Контракт | Проверка |
| --- | --- |
| B07: начальный, персональный и пустой порядок; следование перестановке шаблона без персонального списка | `TestRuleOrderInitialAndPersonal`, `TestRuleOrderEmptyAndPresentEmpty` |
| B07: общие и локальные в одном списке, исключение и восстановление позиции | `TestRuleOrderInitialAndPersonal`, `TestRuleOrderExclusionAndRestore` |
| B07: вставка на 0, 1, 2; перенос общего через локальное и индекс после изъятия; неверные индексы и повторный UUID | `TestInsertLocalRuleAtEveryIndex`, `TestInsertLocalRuleRejectsInvalidIndexAndDuplicate`, `TestMoveRuleUsesRemainingListIndex`, `TestMoveRuleRejectsBadIndexAndUnknownID` |
| B07: новый общий UUID на числовой индекс, сдвиг локального, удаление, пакетное изменение, сохранение персонального и следование шаблону по умолчанию | `TestReconcileRuleOrderTemplateChanges`, `TestReconcileRuleOrderRejectsDamagedPreviousList`, `TestReconcileEmptyDefaultReturnsEmptyCandidate` |
| B07: возврат с локальной позицией, исключением и полной заменой | `TestResetRuleOrderKeepsLocalSlotsAndExclusion`, `TestResetRuleOrderWithoutLocalReturnsInheritance`, `TestResetRuleOrderKeepsReplacementData` |
| B07: повтор исключённого UUID, ссылка на отсутствующий локальный UUID, пропущенный локальный UUID, безопасность и независимость | `TestRuleOrderDuplicateAndMissing`, `TestRuleOrderInvalidMetadataAndSafeErrors`, `TestRuleOrderResultsDoNotAliasInputsOrEachOther` |
| Дельта B08: наследуемый и локальный JSON, полная замена, исключённое содержимое, отказ без частичного результата, независимость | `TestOrderedSelectionKeepsChosenContent`, `TestOrderedSelectionFullReplacementIsLiteral`, `TestOrderedSelectionExcludedAndRestored`, `TestOrderedSelectionAtomicContentAndOrderFailures`, `TestOrderedSelectionBuffersAreIndependent` |
| B07: исчезнувший источник не очищает сохранённую привязку | `TestReconcileDoesNotCleanDeletedSourceBindings` |

Три сценария `Save gates publication of changed orders` относятся к C05–C07: B08 не имеет хранилища, желаемых ревизий, UI и агента. Они не объявлены прошедшими тестами B08.

## Фактические Red/Green

- Исходный и персональный порядок: тесты сначала получили пустые списки от заглушки (поведенческий Red), затем Green после сборки из UUID. После перехода к индексированию записей повторная проверка прошла.
- Исключение, повтор и пропуск: новые тесты показали невыполненное фильтрование и принятие повторов (поведенческий Red), затем Green после проверки полного снимка до исключений. После детерминизации выбора пропущенного UUID тесты повторены.
- Некорректные владельцы, UUID и безопасные ошибки: тесты через новую границу сразу дали Green благодаря уже существующим валидаторам B06. Этот результат **не считается Red** и не называется новой test-first реализацией проверок идентичности.
- Вставка, перенос, согласование шаблона и возврат: у каждой операции тесты сначала получили пустой кандидат от компилируемой заглушки (поведенческий Red), затем Green после минимальной логики и повторную проверку группы. Дополнительный тест пустого кандидата согласования позже дал отдельный Red; исправлен возврат независимого среза длины 0.
- Содержимое B06: тесты сначала получили пустой упорядоченный результат от заглушки (поведенческий Red), затем Green после соединения с единственным вызовом B06. В первом запуске один тест паниковал из-за предположения о длине ответа заглушки; условие теста исправлено до минимальной реализации.
- Исключённое содержимое и атомарные ошибки через уже работающую композицию дали первоначальный Green; повторные проверки после рефакторинга прошли.
- Независимость: тест обнаружил общий указатель `Source` между полным и видимым списками (поведенческий Red). После отдельного копирования источника Green; повторный вызов не зависит от мутации результата.

## Выполненные проверки

- `go test ./internal/configuration/... -count=1` — PASS.
- `go test ./... -count=1` — первый запуск в ограниченной среде не смог открыть `127.0.0.1:0` для существующих процессных тестов; повтор с разрешённым loopback — PASS для всех пакетов. Сбой среды не трактуется как Red.
- `go test -race ./... -count=1` с разрешённым loopback — PASS.
- `go vet ./...` — PASS.
- `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...` и `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` — PASS. Это проверка сборки, не запуск на Linux/Entware.
- `gofmt` применён к новым Go-файлам; `gofmt -l` не вывел файлов. Новый код находится в доменном `internal/configuration` и импортирует только стандартную библиотеку; инфраструктурных адаптеров нет.
- `openspec validate assemble-router-rule-order --strict` — PASS. Проверены 11 затронутых Markdown-файлов и 147 локальных ссылок: ошибок и конечных пробелов нет. Все 4 JSON-блока в затронутой документации разбираются; новых JSON-блоков B08 нет. Поиск частных путей, адресов и секретов в затронутых материалах не нашёл совпадений. `git diff --check` — PASS.

## Состояние приёмки

Реализована чистая сборка порядка; БД, HTTP, UI, публикация и Xray-каталог не добавлены. Все семь критериев [issue #27](https://github.com/SunnyDayDev/xkeen-ui-server/issues/27) сверены с реализацией и проверками; issue закрыта 2026-09-23, статус `CLOSED` проверен отдельно. Обзорная issue #1 остаётся открытой.

2026-09-24 дельта B08 синхронизирована с [основной спецификацией](../../../specs/configuration-rule-order/spec.md): добавлено одно требование и шесть сценариев при сохранении шести прежних требований. `openspec validate --specs` и строгая проверка change прошли. Все 13 задач выполнены; change архивирован. `openspec list --json` больше не показывает активных изменений.
