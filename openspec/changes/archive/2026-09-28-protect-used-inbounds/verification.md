# Проверка B10

## TDD/BDD: фактические циклы

| Задача | Red | Green / повторная проверка |
| --- | --- | --- |
| 1.1 Полный снимок и отключённые записи | `TestInboundDisabledMissingValueKeepsFullIdentity` и `TestInboundDisabledRuleDoesNotHideDuplicateIdentity` поведенчески упали на пустой заглушке: не было полного/активного порядка и пропускался дубликат UUID. Дополнительный `TestInboundDisabledStateIsValidatedBeforeContent` упал, когда повтор отключённого входа не проверялся. | После добавления проверки метаданных, порядка и состояний `go test ./internal/configuration -run 'TestInboundDisabled' -count=1` прошёл. Рефакторинг и форматирование выполнены, повторный целевой запуск прошёл. |
| 1.2 Действующая проекция | `TestInboundActiveProjectionSelectsEffectiveContent` поведенчески упал: выбранные общие/локальные правила были, но действующий вход не выбирался. Тест полной замены и исключения добавлен до выбора входов и проверял сохранение выбранной ветви. | После добавления выбора только действующих входов `go test ./internal/configuration/... -count=1` прошёл, включая регрессии B06/B08. Текущие ветви выбора одного элемента используют B05 и общий JSON-парсер; повторный запуск после форматирования прошёл. |
| 2.1 Разбор `inboundTag` | Тесты корневого массива и строки через запятую поведенчески упали: `References` был пустым. | После чтения только корневого поля и разбиения строки без trim целевой запуск `go test ./internal/configuration -run 'TestInboundReferences' -count=1` прошёл. Точное отсутствие trim дополнительно подтверждено разрешением ссылок в 2.2. |
| 2.2 Каталог входов и диагностика | Тесты разрешённой/отсутствующей/выключенной ссылки и дублирующегося тега поведенчески упали: находки не имели статуса, а дубликат не обнаруживался. | После построения каталога по эффективным тегам целевой запуск `go test ./internal/configuration -run 'TestInbound' -count=1` прошёл. Текст тега остался только во внутренней временной записи; возвращаемые находки содержат статус, UUID провайдера при разрешении и места без сырого JSON/значений. |
| 3.1 Отказ по используемому входу | `TestUsedInboundRejectsDisableAndRemoveWithoutMutation` и `TestUsedInboundReportsAllDependentRules` поведенчески упали: заглушка разрешала оба действия. | После отдельной проверки цели и действующих находок `go test ./internal/configuration -run 'TestUsedInbound' -count=1` прошёл для выключения, удаления, двух правил и неизменности входа. |
| 3.2 Совместный кандидат | Тесты одновременного изменения правила и отключения входа поведенчески упали: целевой вход, уже отмеченный отключённым в кандидате, ошибочно считался отсутствующим. | В приватном анализе проверки действия целевой вход временно учитывается как провайдер до самого действия, а состояние остальных правил берётся из кандидата; полные метаданные отключения по-прежнему проверяются. `go test ./internal/configuration -run 'TestInbound(SameCandidate|DisabledOrExcludedRule)' -count=1` прошёл после рефакторинга. |
| 3.3 Неготовый черновик и безопасный результат | Сценарий повторного включения при отсутствующем входе уже стал зелёным после 2.2; его тест не выдаётся за отдельный Red. `TestInboundAnalysisDoesNotReturnSelectedJSON` затем поведенчески упал: публичный результат раскрывал выбранный JSON. | Внутренний результат отделён от публичного; последний содержит только полный порядок, находки и состояние готовности. Целевой запуск `go test ./internal/configuration -run 'TestInbound(ReenabledRule|AnalysisDoesNotReturn|AnalysisResult)' -count=1` прошёл, включая независимость результатов и входов. |

Первый запуск `go test` не состоялся: Go отсутствовал в `PATH`. Первая попытка встроенного Go 1.27.1 не смогла создать системный build cache. Дальнейшие фактические запуски используют проектный Go 1.27.1 и `GOCACHE=$PWD/.build/go-cache`; эти ошибки окружения не засчитаны как Red.

После задачи 1.3 проверены 39 Markdown-ссылок и 3 полных JSON-блока `docs/configuration-model.md`: отсутствующих локальных целей и неверного JSON нет. Текст явно помечает B10 как работу в процессе и не объявляет хранение или UI готовыми.

После задачи 2.3 повторно проверены локальные ссылки и JSON-блоки `docs/configuration-model.md`: ошибок нет. Документ различает уже работающий разбор входов и ещё не реализованную защиту действия, а также B11/B12.

## Приёмка реализации

После задачи 3.4 обновлены `README.md`, `AGENTS.md`, `docs/architecture.md`, `docs/configuration-model.md`, `docs/roadmap.md` и `docs/prototype.md`. Проверка 156 локальных ссылок и заголовков, а также 4 JSON-блоков в затронутых документах прошла. Поиск локальных путей пользователя, частных адресов и личных почтовых доменов в затронутых материалах совпадений не дал. В документации разделены B10 и будущие B11/B12, хранилище, API и UI.

После рефакторинга общий внутренний выбор содержимого B05/B06 повторно используется B10. Фактические проверки:

| Проверка | Результат |
| --- | --- |
| `GOCACHE=$PWD/.build/go-cache ./.build/toolchain/golang.org/toolchain@v0.0.1-go1.27.1.darwin-arm64/bin/go test ./internal/configuration/... -count=1` | Пройдена после рефакторинга. |
| `GOCACHE=$PWD/.build/go-cache ./.build/toolchain/golang.org/toolchain@v0.0.1-go1.27.1.darwin-arm64/bin/go test ./... -count=1` | Пройдена после рефакторинга, включая регрессии B06/B08 и тесты сервера. |
| `GOCACHE=$PWD/.build/go-cache ./.build/toolchain/golang.org/toolchain@v0.0.1-go1.27.1.darwin-arm64/bin/go test -race ./... -count=1` | Пройдена после рефакторинга. |
| `GOCACHE=$PWD/.build/go-cache ./.build/toolchain/golang.org/toolchain@v0.0.1-go1.27.1.darwin-arm64/bin/go vet ./...` | Пройдена после рефакторинга. |
| `openspec validate protect-used-inbounds --strict` | Пройдена. |
| `git diff --check` и `gofmt -l` для затронутых Go-файлов | Пройдены; замечаний и неформатированных файлов нет. |

Первый полный и race-прогоны в песочнице не прошли из-за запрета локальных TCP-сокетов и записи служебного кэша Go. Они повторены с разрешёнными локальными сокетами и кэшем и прошли; исходный сбой не считался поведенческой неудачей.

### Критерии issue #31

1. Полный снимок и проверка метаданных до отбора: `TestInboundDisabledMissingValueKeepsFullIdentity`, `TestInboundDisabledRuleDoesNotHideDuplicateIdentity`, `TestInboundDisabledStateIsValidatedBeforeContent`, `TestInboundDisabledRuleStillChecksSourceAndOrder` и `TestInboundActiveProjectionSelectsEffectiveContent`. Результат без частичного успеха при ошибках подтверждён.
2. Смысловой `inboundTag` и итоговый тег входа: `TestInboundReferencesUseRootArrayAndIgnoreJSONEnabled`, `TestInboundReferencesSplitStringAtCommas`, `TestInboundCommaListDoesNotTrim`, `TestInboundReferencesIgnoreNestedKey`, `TestInboundActiveProjectionUsesReplacementAndExclusion` и `TestInboundReferenceResolutionAndMissingDraft`.
3. Защита действия по итоговому кандидату: `TestUsedInboundRejectsDisableAndRemoveWithoutMutation`, `TestUsedInboundReportsAllDependentRules`, `TestInboundSameCandidateRemovesLastReference`, `TestInboundSameCandidateStillHasAnotherReference`, `TestInboundDisabledOrExcludedRuleReleasesTarget`.
4. Безопасные ошибки и атомарность: `TestInboundDuplicateActiveTagIsAmbiguous`, `TestInboundInvalidRecognizedFieldHasSafeLocation`, `TestInboundRuleMustBeObject`, `TestInboundAnalysisDoesNotReturnSelectedJSON`, а также тест отказа без мутации из пункта 3.
5. Неготовый, но допустимый черновик: `TestInboundReenabledRuleCanBeUnreadyDraft`, `TestInboundDisabledProviderLeavesMissingReference` и тест отключённого правила с пустой обязательной переменной из пункта 1. `Ready=false` относится только к явным ссылкам на входы и не выдаётся за проверку полного Xray-каталога.
6. TDD/BDD Red/Green задокументированы выше, целевые и полные проверки пройдены. Регрессии B06/B08 прошли после общего рефакторинга, а их публичный контракт и поведение не менялись.

Issue [#31](https://github.com/SunnyDayDev/xkeen-ui-server/issues/31) закрыта как выполненная после проверки всех критериев и обновления чеклиста. Два изменённых требования дельты синхронизированы с [основной спецификацией](../../../specs/configuration-routing-dependencies/spec.md); `openspec validate --specs` прошло (8 спецификаций, 0 ошибок). Change архивирован 2026-09-28.
