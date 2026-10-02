# Verification B12

## Среда

Go 1.27.1 darwin/arm64 из SDK проекта; GOCACHE в игнорируемом .build. Команды далее используют этот SDK. Первоначальное отсутствие go в PATH — ошибка окружения, не Red. Docker daemon на момент начала недоступен; к проверке стенда это будет проверено отдельно.

## TDD/BDD

| Шаг | Red | Green |
| --- | --- | --- |
| 1.1 Native tag | TestProjectionNativeTag: missing projection, null, после компилируемого пустого интерфейса | Целевой тест PASS после минимальной проекции outbound; независимость буфера проверена |
| 1.2 Positions/conflict/ready object | Nine missing root markers; conflicting marker overwritten; object variable unmarked | All TestProjection PASS after table and conflict guard; idempotence and nonrecursive marking PASS |
| 1.3 Unsupported/version/metadata | Unsupported buffer reformatted; invalid UUID was inserted | Unsupported data cloned, metadata checked; full configuration/elementjson suite PASS; numbers retained as json.Number |

1.4: документация проекции обновлена; зависимости ядра остались стандартными библиотеками и internal/jsonvalue.

| 2.1 Required/empty | TestCatalogExplicitEmptyAndMissing: nil candidate | PASS: six missing groups rejected; exactly five explicit containers including empty API file |
| 2.2 Metadata/singletons | Seven metadata cases wrongly succeeded; active log missing | TestCatalog PASS after full metadata checks, shared UUID scope, active selection and placement shape |
| 2.3 Optional/assignments | Missing stats/extra; all section collision cases wrongly succeeded | TestCatalog PASS: optional empty vs excluded, stats/API, FakeDNS [], DNS, future array/null/large number, reserved/repeated assignments |
| 2.4 Provenance/determinism | TestCatalogDeterminismIndependenceAndLateError: missing provenance | PASS after source/file/path result; byte and provenance independence, late error atomicity; existing deterministic serializer initially Green |

2.5: форма каталога документирована, полный suite configuration/elementjson PASS после gofmt.
| 3.1 Effective/inactive | Disabled inbound tried to prepare required variable (missing_value) | PASS after separate disable filters before content; active rule missing_value, replacement/local literals, null placement, current inheritance PASS |
| 3.2 Order/settings | TestCatalogRuleOrderAndSettings emitted B,L instead of L,B | PASS with B08 before content, reserved routing.rules guard; outbound B,A,L and FakeDNS input order PASS |

3.3 baseline: TestInbound/TestOutbound PASS before shared analyzer refactoring; full configuration/elementjson suite PASS.
| 3.3 Dependencies | Missing/disabled providers wrongly emitted a candidate; no findings, shape/ambiguity accepted | PASS after shared prepared-content analysis; all configuration/elementjson regressions PASS, public B10/B11 unchanged |
| 3.4 Balancers | Working fallback was falsely marked blocking when an unrelated direct edge failed | PASS after per-rule selector/fallback readiness; remaining fallback/reachability/unused-routing cases initially Green; full package regressions PASS |
| 3.5 Catalog identity | Inherited/object-variable objects and FakeDNS pools lacked marker | PASS after projection of selected roots; replacement/local origin, DNS/routing children, conflict path and empty container assertions; full suite PASS |

3.6: README/model/architecture/prototype/roadmap updated; local link targets and JSON examples checked; domain suite runs without Docker or DB. Xray validation still pending.
| 4.1 Demo | TestDemoCatalogAndWrite / TestDemoRejectsNonemptyAndWriteError: missing demo after compiled stubs | PASS: domain-built positive/negative snapshots, exact writes, nonempty refusal, IO failure and cleanup; CLI/unsafe filename regression initially Green |
| 4.2 Orchestration | sh lab/test-check-catalog.sh exits 1 against explicit failing check-catalog stub; Docker image/version separately available | PASS: test harness verifies positive/negative, read-only/network none/entrypoint, wrong-version and unavailable refusal, owned cleanup |

## Реальная проверка Xray

`sh lab/check-catalog.sh` с Go SDK в PATH и отдельным GOCACHE: PASS. Образ `xkeen-router-lab:dev`, Linux ARM64; фактическая версия `Xray 26.3.27 d2758a0 (go1.26.1 linux/arm64)`. Положительный каталог прошёл `run -test -format json -confdir /candidate`; отрицательный с `invalid-demo-protocol` был отвергнут с указанием этого протокола. Хеши до и после проверки совпали. Контейнеры одноразовые, сеть none, корневая FS и кандидат read-only, entrypoint `/opt/sbin/xray`; обычный Compose-сервис и volumes не использовались.

| Файл | SHA256 положительного кандидата |
| --- | --- |
| 01_log.json | e82ab072ac9d13dd0c7ff5166a2e60294e235072270c24980668c9b19621056a |
| 02_api.json | 9319a7d195b400296d2165d97a3515ef03fab6bbc2ce09d21c1f342e64988fba |
| 03_inbounds.json | d175b67281a759651923b1be7ae1450b189b108b46614f525c73ab82335b2493 |
| 04_outbounds.json | ebc09a6bb200544d1b1e74523faf36843ccdcdeac70e176ea43e9096a202226a |
| 05_routing.json | 8ee76806298393c8fcc8bf814bbd6ebe835d3534fc0d12baa82adcdcd49ed290 |
| 06_extra.json | a8c559be5d4e1082e236634fedb9584c661b4b9ec56d2606be9d93eae97e694a |

Отрицательный кандидат отличается только 04_outbounds.json: `ab028807ec5d2d77e5827ebbab97fff66c578a4ec3761dd7d38252d14b4e8e0b`.

Неподходящая версия и недоступный Docker проверены контрактным стендом с подменой команды: оба дают ненулевой статус, без подтверждения валидации. Первый запуск harness ошибочно требовал пустой TMPDIR целиком; macOS xcrun оставил свой cache. Уточнена проверка удаления только собственных каталогов `xkeen-catalog.*`, затем harness PASS. Это не отказ доменного поведения и не доказательство реальной проверки версии 99.1.1.

Проверка `-test` не запускает runtime и не подтверждает трафик, применение, публикацию или поддержку игнорируемых неизвестных полей. Отсутствие Go/Docker до настройки окружения не засчитывалось как Red.

4.4: documented commands/domain candidate/Xray distinction and actual result; checked changed Markdown links, JSON examples and absence of private absolute paths.

Acceptance regressions: unknown sections named inbound/outbound/rule initially triggered identity_marker_conflict; corrected explicit catalog placement mapping. Singleton API ambiguity initially surfaced after malformed log JSON; moved all active singleton multiplicity checks into metadata phase. Both new regression tests confirmed Red, then full configuration suite Green. Additional object-variable conflict/finding independence, hidden bindings/disabled metadata and known shapes initially Green.

## Общая приёмка

| Проверка | Фактический результат |
| --- | --- |
| gofmt -l cmd internal | Пустой вывод после форматирования main_test.go |
| go test ./... -count=1 | PASS всех пяти пакетов с тестами; jsonvalue без отдельных тестов |
| go vet ./... | PASS |
| go test -race ./... -count=1 | PASS всех пакетов с тестами |
| CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath ./cmd/... | PASS server + demo |
| CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath ./cmd/... | PASS server + demo |
| sh -n lab/check-catalog.sh и lab/test-check-catalog.sh | PASS |
| git diff --check | PASS |
| openspec validate assemble-xray-catalog --strict | PASS |
| Markdown | 12 изменённых документов/артефактов: локальные цели существуют, четыре fenced JSON-примера корректны, частных абсолютных путей нет |

Первые cross-build запуски дали предупреждение о недоступном внешнем GOPATH cache, хотя завершились успешно. Повторены с GOPATH в игнорируемом .build: обе сборки завершились без предупреждений. Процессные тесты и race получили разрешение на loopback. Linux-бинарники собраны, но не запускались: реальный Xray выполнялся в лабораторном ARM64-образе.

## Соответствие всем 36 сценариям дельты

| Scenario | Проверка |
| --- | --- |
| Missing group differs from an explicitly empty group | TestCatalogExplicitEmptyAndMissing, шесть групп |
| Catalog is determined by the supplied data | TestCatalogExplicitEmptyAndMissing, TestCatalogActiveSingletonAndAPI; никаких seed-служб/входов |
| Duplicate UUID crosses section boundaries | TestCatalogMetadataAndSingleton/cross_section и other_IDs |
| Inactive metadata is still checked | TestCatalogMetadataAndSingleton/exclusion/source; TestCatalogHiddenBindingsAndDisabledRecords |
| Explicitly empty groups retain all files | TestCatalogExplicitEmptyAndMissing |
| All rules are hidden while the file remains present | TestCatalogAllHiddenAndKnownShapes; TestCatalogEffectiveAndInactiveContent |
| Two active singleton sections are not merged | TestCatalogMetadataAndSingleton/singleton; TestCatalogSingletonMetadataBeforeContent |
| Empty API group differs from an API element with empty content | TestCatalogExplicitEmptyAndMissing; TestCatalogActiveSingletonAndAPI |
| Replacement supplies the whole effective object | TestCatalogEffectiveAndInactiveContent |
| Inactive unfinished content does not block assembly | TestCatalogEffectiveAndInactiveContent |
| Active unfinished content blocks the catalog | TestCatalogEffectiveAndInactiveContent after enable |
| Literal data is not interpreted again | TestCatalogEffectiveAndInactiveContent; TestCatalogInheritedLiteralAndNull |
| Literal null cannot occupy an inbound object position | TestCatalogInheritedLiteralAndNull; TestCatalogAllHiddenAndKnownShapes |
| Personal order is reflected in the emitted array | TestCatalogRuleOrderAndSettings, C,L,A,B → L,B |
| Empty rules preserve other routing settings | TestCatalogAllHiddenAndKnownShapes |
| Outbound sequence is independent of UUID sorting | TestCatalogOutboundAndFakeDNSSequence, B,A,L |
| Optional stats is retained only when supplied | TestCatalogOptionalSections; TestCatalogExplicitEmptyAndMissing |
| Empty optional group differs from a fully excluded optional section | TestCatalogOptionalSections |
| Additional DNS and future section are preserved | TestCatalogOptionalSections |
| Unknown nested fields keep types and order | TestCatalogDeterminismIndependenceAndLateError; generated demo and Xray SHA256 |
| Additional section cannot overwrite a baseline section | TestCatalogSectionAssignments |
| Each effective origin receives its own marker | TestCatalogIdentityOriginsAndChildren |
| Section markers are not copied to children | TestCatalogIdentityOriginsAndChildren; TestProjectionPositionsAndConflict |
| FakeDNS pool gets its own identity | TestCatalogIdentityFakeDNSAndUnsupported |
| Marker conflict in prepared content rejects the catalog | TestCatalogIdentityOriginsAndChildren; TestCatalogObjectVariableConflictAndFindingIndependence |
| Repeating projection preserves the matching marker | TestProjectionPositionsAndConflict |
| Missing outbound blocks the candidate without repairing the draft | TestCatalogDependenciesBlockAndRelease/outbound |
| Disabled inbound is not an emitted provider | TestCatalogDependenciesBlockAndRelease/disabled_inbound |
| Disabled rule releases its missing provider | TestCatalogDependenciesBlockAndRelease |
| Resolved fallback permits assembly with a finding | TestCatalogBalancerFallbackAndReachability |
| Successful scoped analysis does not skip routing content | TestCatalogAlwaysAssemblesRouting |
| Repeated assembly produces identical files | TestCatalogDeterminismIndependenceAndLateError |
| Result buffers and provenance are independent | TestCatalogDeterminismIndependenceAndLateError; TestCatalogObjectVariableConflictAndFindingIndependence |
| One late error returns no earlier files | TestCatalogDeterminismIndependenceAndLateError |
| Xray rejects an assembled candidate | real sh lab/check-catalog.sh, negative protocol |
| Unavailable validator does not certify the catalog | sh lab/test-check-catalog.sh, wrong-version/unavailable |

## Критерии issue #34

Все семь критериев сопоставлены: (1) полный снимок/выбор/порядок — каталоговые и действующие B04–B11 тесты; (2) пять файлов/явная пустота — empty/missing тесты; (3) routing/stats/extra/типы — order/optional/determinism; (4) A07 — projection и catalog identity тесты; (5) атомарные безопасные ошибки — metadata/dependencies/late-error/conflict; (6) реальный закреплённый Xray — точные хеши выше, positive PASS/negative rejection; (7) фактические TDD, регрессии, docs и strict validation — настоящий отчёт. Синхронизация/архив и закрытие issue выполняются отдельно после этой сверки.

DDD/Clean Architecture: ядро содержит инварианты, без файловой системы/БД/HTTP/процессов; cmd — адаптер записи демонстрации, lab — внешняя валидация. Нет UI, хранилища, агента, публикации/применения. Существующие интерфейсы и контрактные тесты A08/B01–B11 сохранены.

Финальный повтор реальной sh lab/check-catalog.sh: PASS обеих ветвей, все SHA256 совпали с первым запуском. Прямые production imports ядра: только стандартные библиотеки и internal/jsonvalue. ELF server/demo обеих архитектур статически связаны. Уточнённые assertions вложенных типов/порядка и extension при пустых rules сначала Green; целевые тесты повторно PASS без изменения логики.

## Завершение

Основная `openspec/specs/configuration-xray-catalog/spec.md` создана по дельте: Purpose сохранён дословно, восемь требований и 36 сценариев совпадают; strict validation основных specs — 9 passed/0 failed. Change архивирован CLI как `2026-10-02-assemble-xray-catalog` после синхронизации. CLI сообщил 21/22: единственный открытый пункт 5.3 включал само архивирование и закрытие issue; он отмечен после фактического завершения обоих действий.

[#34](https://github.com/SunnyDayDev/xkeen-ui-server/issues/34) отдельно обновлена по семи выполненным критериям и закрыта с причиной completed; проверено состояние CLOSED, closedAt 2026-10-02T07:50:54Z. Все 22 пункта завершены. До отдельного запроса публикации commit/push и GitHub Actions не выполнялись; перечисленные выше проверки локальные.
