# Проверка B05

Статус: реализация и локальная приёмка завершены. Issue: [#21](https://github.com/SunnyDayDev/xkeen-ui-server/issues/21).

## Среда и исходная проверка

Go 1.27.1 darwin/arm64 из локального SDK проекта; GOTOOLCHAIN=local, GOCACHE в игнорируемом .build. Команды ниже используют этот SDK в PATH. Первый запуск без SDK дал command not found; это ограничение окружения, не Red.

`go test ./internal/configuration/... -count=1` до изменений — PASS.

## Сопоставление сценариев

Все перечисленные тесты реализованы и прошли; фактическая история — в журнале ниже.

| Сценарий дельты | Тест |
| --- | --- |
| Full replacement does not merge missing fields | `TestSelectReplacement` |
| Template variable errors do not enter replacement | `TestSelectInactive` |
| Replacement strings remain literal | `TestSelectLiteral` |
| Root null is content rather than absence | `TestSelectLiteral` |
| Nested null remains literal | `TestSelectLiteral` |
| Non-object roots are not wrapped | `TestSelectLiteral` |
| Invalid replacement has no fallback | `TestSelectInvalidJSON` |
| Duplicate replacement keys are rejected | `TestSelectInvalidJSON` |
| Selection routes absent and variable settings to inheritance | `TestSelectInherited` |
| Invalid source fails even for literal replacement | `TestSelectIdentity` |
| Replacement ignores broken inactive content | `TestSelectInactive` |
| Template changes cannot alter a replacement | `TestSelectInactive` |
| Unknown mode is rejected | `TestSelectIdentity` |
| Replacement diagnostics and buffers are safe | `TestSelectSafety` |
| Switch and return keep values after template evolution | `TestTransitionReturn` |
| Reset succeeds before incomplete assembly | `TestTransitionReset` |
| Activating replacement requires fresh explicit content | `TestTransitionInvalid` |
| Updating replacement preserves inactive values | `TestTransitionReplace` |
| Discarding a candidate cancels only the edit | `TestTransitionSafety` |
| Invalid transitions preserve the prior state | `TestTransitionInvalid` |
| Repeated bindings identify both positions | `TestOverrideDuplicate` |
| Empty and distinct bindings are valid | `TestOverrideDistinct` |
| Invalid binding is not treated as absent | `TestOverrideInvalid` |

Сценарии B03: возврат сохранённых значений, смена типа, удаление определения и новое обязательное значение — `TestTransitionReturn`; актуальный дефолт после сброса, неполный черновик и отсутствие восстановления — `TestTransitionReset`; отмена и внешнее состояние — `TestTransitionSafety`.

## Журнал Red/Green

- 2.1 `go test ./internal/configuration -run '^TestSelectReplacement$' -count=1`: Red — nil вместо выбранного содержимого; минимальный выбор, gofmt и повтор — Green.
- 2.2 `TestSelectIdentity`: Red — все 16 неверных записей давали содержимое вместо ошибки. Извлечена общая проверка источника из B04; `go test ./internal/configuration -run '^(TestSelect|TestInherited)' -count=1` после gofmt — Green, регрессии B04 пройдены.
- 2.3 `TestSelectInherited`: Red — паника при отсутствующей записи. Добавлена наследуемая ветвь через B04; после gofmt все `TestSelect`/`TestInherited` — Green, проверено точное соответствие ошибок B04.
- 2.4 `TestSelectLiteral`: первоначальный Green — буквальная ветвь уже сохраняет все JSON-типы/null, метку и строки, а B04 отклоняет null трёх источников. Логика ради Red не менялась.
- 2.5 `TestSelectInvalidJSON`: Red — все 10 неверных JSON выдавались как успешные. Добавлен синтаксический parseJSON; gofmt и все TestSelect — Green. Проверены пути повторов, экранирование и смещения ошибок.
- 2.6 `TestSelectInactive`: после удаления преждевременно добавленного неиспользуемого импорта первый поведенческий запуск Green. Замена уже игнорирует изменённый/повреждённый шаблон и значения. Ошибка компиляции не считается Red.
- 2.7 `TestSelectSafety`: Red — изменение входа меняло результат и наоборот. Добавлено копирование собственного JSON; gofmt и все TestSelect/TestInherited — Green. Полное сравнение диагностической структуры не допускает значений/лишних полей.
- 3.1 `TestTransitionReplace`: Red — нет кандидата активации. Минимальная активация/обновление без проверки неактивных Values — Green после gofmt. Копирование и ошибки переходов проверяются отдельно в 3.4/3.5.
- 3.2 `TestTransitionReturn`: Red — возврат оставлял режим replacement. Добавлен переход к variables; TestTransition после gofmt — Green. Проверены успешный возврат и type_mismatch/unknown_variable/missing_value через новую границу без утраты кандидата.
- 3.3 `TestTransitionReset`: Red — сброс сохранял значения и режим. Добавлен nil-кандидат сброса; все TestTransition — Green. Проверены оба режима, актуальный дефолт, неполный результат, повторный сброс и отсутствие восстановления.
- 3.4 `TestTransitionInvalid`: Red — неверные источники/режимы/JSON принимались, в том числе при сбросе. Общая проверка источника, проверка действия/режима и нового JSON дали Green всего TestTransition после gofmt. Старый некорректный JSON и аргумент Content возврата/сброса не блокируют эти действия.
- 3.5 `TestTransitionSafety`: Red — обе стороны перехода разделяли карту/буферы значений. Глубокая копия Values и нового JSON дала Green всех TestTransition/TestSelect/TestInherited. Отмена означает отказ от независимого кандидата; интерфейсы не принимают и не меняют порядок/исключения/другие элементы, их интеграция остаётся B06.
- 4.1 `TestOverrideDuplicate`: Red — повторные ссылки принимались. Минимальная проверка повторов, gofmt, повторный запуск — Green; обе позиции и неизменность входа подтверждены.
- 4.2 `TestOverrideDistinct`: первоначальный Green для пустого списка, разных UUID и независимых роутеров. Логика не менялась.
- 4.3 `TestOverrideInvalid`: Red — неверные записи определялись как дубликаты, пустой список скрывал неверного владельца. Добавлена валидация владельцев/позиций/источников перед повторами; полный `go test ./internal/configuration/... -count=1` — Green после gofmt.

## Общая проверка

- `go test ./... -count=1` — PASS, включая процессные тесты с разрешёнными loopback-портами.
- `CGO_ENABLED=1 go test -race ./... -count=1` — PASS.
- `go vet ./...` — PASS; `gofmt -l cmd internal` — пустой вывод; `sh -n scripts/run-server.sh` — PASS.
- После небольшого рефакторинга копирования (Content аргумента возврата больше даже не копируется) и комментариев API целевые тесты повторены — PASS; общие проверки выше выполнены после этого изменения.
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...` и аналогичный `GOARCH=arm64` — PASS. Сервер отдельно собран для обеих архитектур с `-trimpath`; `file` подтвердил статические ELF x86-64 и AArch64. Первый запуск сборки завершился успешно с предупреждением о недоступном внешнем кеше модулей; повтор с GOMODCACHE в игнорируемом .build прошёл без предупреждений.
- Новые модули используют только стандартную библиотеку и существующие типы/операции ядра. Зависимости go.mod и контракт elementjson не менялись; ядро не получило I/O. Проверка Xray/Entware и UI не выполнялась и не является результатом B05.

## Границы приёмки

Все 23 сценария дельты сопоставлены тестам выше; восемь сценариев B03 возврата/сброса/отмены покрыты последовательностями Transition → Select. Форматы хранения, порядок и исключения не добавлены. Полный сброс возвращает отсутствие персонального содержимого, не удаление будущего агрегата роутера. Проверка повторных ссылок не утверждает существование каждого источника.

На момент локальной приёмки синхронизация основной spec и архивирование ещё не были выполнены; результаты отдельного этапа завершения приведены ниже. Issue #21 закрыта как выполненная; состояние CLOSED проверено через GitHub. Обзорная #1 остаётся OPEN. Коммит, push и GitHub Actions в этой сессии не выполнялись.

## Документальная приёмка

Строгая валидация change — PASS; основных specs — 6/6 PASS (до синхронизации B05). Проверены 11 Markdown-файлов, 137 локальных ссылок, 41 якорь, 97 JSON-примеров и три вхождения UUIDv4; соответствие всех 23 сценариев тестам и сохранность трёх прежних сценариев изменённого требования — PASS. Сканирование частных данных/секретов и ручная проверка новых примеров — PASS. `git diff --check` — PASS. Начальная проверка таблицы ошибочно приняла префикс группы TestSelect за имя функции; после ограничения поиска строками таблицы проверка прошла, это не ошибка реализации.

Проверка документации и design подтверждает прежние границы: модель/сериализация Element и B01/B02 не ужесточены, Xray и UI не объявлены реализованными. Все необходимые сценарии B05 и локальные критерии issue выполнены; синхронизация и архивирование выполнены отдельным этапом ниже.

## Синхронизация и архивирование — 2026-09-22

В `configuration-content-inheritance` изменено одно требование и добавлены три; семь остальных требований и Purpose сохранены без изменений. Основная спецификация содержит 11 требований и 55 сценариев. Все четыре блока дельты сверены с основной спецификацией до переноса; расхождений нет. Все 20 задач и четыре артефакта завершены.

Строгая валидация change до синхронизации — PASS; основных specs после синхронизации — 6/6 PASS. Change перенесён в `openspec/changes/archive/2026-09-22-add-full-content-replacement/` с сохранением `.openspec.yaml`. Ссылки и статус в документации обновлены. Issue #21 повторно проверена через GitHub: CLOSED; обзорная #1 — OPEN. На этом этапе код не менялся, Go-тесты повторно не запускались; результаты реализации приведены выше. Коммит и push не выполнялись.

После переноса проверены 8 Markdown-файлов, 74 локальных ссылок и 3 JSON-примеров в блоках кода — PASS; проверка частных локальных путей и `git diff --check` — PASS. Активных change нет.
