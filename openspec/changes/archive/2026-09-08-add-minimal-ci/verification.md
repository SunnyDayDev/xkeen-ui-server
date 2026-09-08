# Проверка A04: CI и защита веток

Сценарии подготовлены до workflow. Это BDD-проверка процесса CI; существующий сервер не разрабатывается повторно test-first. Отсутствие workflow и сбой окружения не считаются Red.

| Сценарий | Ожидаемый результат | Фактический результат |
| --- | --- | --- |
| Корректный push | Все проверки и обе сборки успешны | PASS: run 34185611785, Go 1.27.1 |
| Корректный PR | Успешный check на проверяемой версии PR | PASS: run 34185613682, Go 1.27.1 |
| Неверное форматирование | Ненулевой код, путь, файл не исправлен | PASS: exit 1, путь cmd/probe.go, исходник неизменен; после gofmt exit 0 |
| Падающее ожидание TestHealth | Tests и job failure, видны имя и причина; merge заблокирован | PASS: run 34185804095, Tests failure, PR mergeStateStatus BLOCKED |
| Восстановление ожидания | Все проверки успешны, блокировка CI снята | PASS: runs 34185898055/34185895561; PR mergeStateStatus CLEAN |
| Ошибка команды | Ненулевой код не подавляется | PASS: синтаксическая ошибка входа gofmt завершает тот же shell-шаг ненулевым кодом |
| Защита веток | PR, required check с актуальной базой, запрет force push/удаления, без bypass | PASS: активный ruleset 22507237, правила перечитаны для main и master |

## Инструменты

- Checkout [v7.0.1](https://github.com/actions/checkout/releases/tag/v7.0.1): `3d3c42e5aac5ba805825da76410c181273ba90b1`.
- Setup Go [v7.0.0](https://github.com/actions/setup-go/releases/tag/v7.0.0): `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e`.
- SHA разрешены через GitHub refs API для release tags 2026-09-08.

## Ограничения

ARM64-сборка не подтверждает выполнение на Entware или физическом роутере. Race проверяет тестируемые пакеты; дочерний процесс текущие тесты собирают без race-инструментации. Реальные устройства, продуктовые функции и основные specs не изменяются.

## Локальные проверки

- actionlint 1.7.12 (архив проверен по release checksums): код 0 для workflow.
- `sh -n scripts/run-server.sh`, `git diff --check`: код 0.
- Проверка форматирования использовала shell-фрагмент из workflow и временную копию Go-файла; отказ, восстановление и распространение ошибки gofmt подтверждены.
- Новые rules успешно прочитаны через `openspec instructions`; продуктовые specs не менялись.

- Go 1.27.1 darwin/arm64: полный `go test ./... -count=1 -v`, `go vet ./...`, `CGO_ENABLED=1 go test -race ./... -count=1 -v` прошли; IPv6 не пропущен.
- Обе Linux-сборки из design выполнены без cgo; ELF machine AMD64/AArch64 подтверждены, PT_INTERP и PT_DYNAMIC отсутствуют.
- Локальные ссылки в изменённых документах и артефактах проверены; частных путей и ключей не найдено.

## Обнаруженный дефект выбора Go

Первые runs [push](https://github.com/SunnyDayDev/xkeen-ui-server/actions/runs/34185394095) и [PR](https://github.com/SunnyDayDev/xkeen-ui-server/actions/runs/34185401531) на `d4a98db136815736b499f370a8edadeafbd8c65d` прошли, но журнал показал Go 1.27.0. Они не засчитываются как выполнение требования toolchain 1.27.1. В setup-go v7.0.0 заранее заданный GOTOOLCHAIN=local выбирает директиву go; сам action устанавливает local после разбора версии. До исправления добавлена проверка совпадения GOVERSION с toolchain в go.mod.

Red подтверждён: [PR run 34185546122](https://github.com/SunnyDayDev/xkeen-ui-server/actions/runs/34185546122), commit `55166907213bfc34a6e035b6bf31d936a9793a88`: шаг Go version вернул exit 1, «got go1.27.0, want go1.27.1». После этого удалён преждевременный env; проверка точной версии сохранена.

Green выбора toolchain: [push](https://github.com/SunnyDayDev/xkeen-ui-server/actions/runs/34185611785), [PR](https://github.com/SunnyDayDev/xkeen-ui-server/actions/runs/34185613682), commit `990b7a2782b557ed0b166dceb1c20760c89a34d2`. Все шаги прошли с Go 1.27.1 linux/amd64; Ubuntu 24.04.4, image ubuntu-24.04 20260831.293.1.

## Защита веток

Активирован [Protect main and master](https://github.com/SunnyDayDev/xkeen-ui-server/rules/22507237), ID 22507237. Условия: refs/heads/main и refs/heads/master; bypass пуст. Требуются PR и checks (GitHub Actions integration 15368), strict policy включена, обход при создании ветки выключен. Force push и удаление запрещены; approving reviews 0. Правила перечитаны через ruleset API и effective rules API для обоих имён веток; master не создавался. Разрушительные пробы над main не выполнялись.

## Проверка отказа теста

Commit `6ae23444771a6c0aa7ea82d8275323511aca4436` временно заменил ожидаемое status:ok на status:ci-probe в helper существующего TestHealth. Локальный `go test ./internal/server -run TestHealth -count=1 -v` вернул 1: фактическое status:ok не совпало с ожиданием. Общий helper также используется TestUnsupportedRequests.

[PR run](https://github.com/SunnyDayDev/xkeen-ui-server/actions/runs/34185804095) и [push run](https://github.com/SunnyDayDev/xkeen-ui-server/actions/runs/34185801168) завершились failure на Tests; последующие проверки и сборки пропущены. PR #10 имел mergeable=MERGEABLE, но mergeStateStatus=BLOCKED при двух FAILURE checks. Конфликта кода не было; слияние блокировала обязательная проверка.

## Восстановление и итоговая сверка

Commit `bba2610a5e6d0786415005e37057963ce8f29d72` восстановил исходные ожидания. Локальный TestHealth прошёл. [PR run](https://github.com/SunnyDayDev/xkeen-ui-server/actions/runs/34185898055) и [push run](https://github.com/SunnyDayDev/xkeen-ui-server/actions/runs/34185895561) прошли целиком; PR #10 перешёл из BLOCKED в CLEAN.

`git diff origin/main -- cmd internal go.mod scripts openspec/specs` пуст: проверочная мутация удалена, продуктовые файлы и specs не меняются. Валидация `openspec validate add-minimal-ci --strict --json` прошла с skip_specs. Проверены 48 локальных ссылок/якорей и 16 shell/JSON-блоков изменённых документов и артефактов; actionlint и git diff --check без замечаний.

HTTPS push сначала отклонён из-за отсутствия workflow scope у OAuth-токена; использован уже настроенный SSH-доступ того же аккаунта. Права токена и remote origin не изменялись. Это ограничение способа доставки, а не Red тестов.

## Завершение

Change архивирован 2026-09-08 через OpenSpec с skip_specs, без изменений основных specs. На момент вызова archive оставался только пункт 4.2 — само архивирование и закрытие issue; после проверки архива и CLOSED issue #5 он отмечен выполненным. Все 13 задач выполнены. Последний коммит оформления архива и документации проходит обычный CI перед слиянием PR #10.
