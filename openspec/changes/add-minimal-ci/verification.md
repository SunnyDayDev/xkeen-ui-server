# Проверка A04: CI и защита веток

Сценарии подготовлены до workflow. Это BDD-проверка процесса CI; существующий сервер не разрабатывается повторно test-first. Отсутствие workflow и сбой окружения не считаются Red.

| Сценарий | Ожидаемый результат | Фактический результат |
| --- | --- | --- |
| Корректный push | Все проверки и обе сборки успешны | — |
| Корректный PR | Успешный check на проверяемой версии PR | — |
| Неверное форматирование | Ненулевой код, путь, файл не исправлен | PASS: exit 1, путь cmd/probe.go, исходник неизменен; после gofmt exit 0 |
| Падающее ожидание TestHealth | Tests и job failure, видны имя и причина; merge заблокирован | — |
| Восстановление ожидания | Все проверки успешны, блокировка CI снята | — |
| Ошибка команды | Ненулевой код не подавляется | PASS: синтаксическая ошибка входа gofmt завершает тот же shell-шаг ненулевым кодом |
| Защита веток | PR, required check с актуальной базой, запрет force push/удаления, без bypass | — |

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
