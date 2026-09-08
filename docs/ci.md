# CI и защита основных веток

Workflow [CI](../.github/workflows/ci.yml) проверяет push любых веток и открытие, обновление или повторное открытие pull request. Фильтра путей нет: правки документации тоже проверяются. Push тегов не запускает этот workflow. У ветки с открытым PR допустимы два запуска.

## Проверки

Один job `checks` работает на GitHub-hosted Ubuntu 24.04 AMD64, не дольше 15 минут. Go выбирается из `toolchain` в `go.mod`; После выбора SDK setup-go устанавливает `GOTOOLCHAIN=local`, запрещая дальнейшую автоматическую смену SDK. Задавать эту переменную перед setup-go нельзя: тогда action выбирает директиву `go`. Шаг Go version дополнительно сверяет фактическую версию с `toolchain`. Actions закреплены commit SHA с версиями в комментариях; кеш setup-go отключён.

Шаги выполняются последовательно: форматирование Go, синтаксис launcher, тесты, vet, race и сборки Linux AMD64/ARM64. Ошибка делает шаг и job неуспешными; последующие обычные шаги пропускаются. Путь файла, имя теста или сообщение компилятора находятся в журнале соответствующего шага. CI не исправляет исходники автоматически.

Из корня проекта при установленном Go, указанном в `go.mod`, проверки повторяются так:

```sh
export GOTOOLCHAIN=local
go version
```

```sh
sh -ec '
  unformatted=$(gofmt -l cmd internal)
  if [ -n "$unformatted" ]; then
    printf "%s\n" "$unformatted"
    exit 1
  fi
'
```

```sh
sh -n scripts/run-server.sh
go test ./... -count=1 -v
go vet ./...
CGO_ENABLED=1 go test -race ./... -count=1 -v
mkdir -p .build
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o .build/xkeen-ui-server-linux-amd64 ./cmd/xkeen-ui-server
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o .build/xkeen-ui-server-linux-arm64 ./cmd/xkeen-ui-server
```

Проверка самого YAML: `actionlint .github/workflows/ci.yml` (при подготовке использован actionlint 1.7.12). Это локальная проверка конфигурации; workflow запускает проверки Go и shell без установки actionlint.

Процессные тесты требуют Unix и разрешённых loopback-соединений. Отсутствующий IPv6 отмечается явным skip. Race требует C toolchain на машине тестирования, но поставляемые бинарники собираются без cgo. Race инструментирует тестируемые пакеты; дочерний процесс текущий `TestMain` собирает обычным `go build`.

ARM64-сборка проверяет компиляцию. Выполнение в Entware остаётся [отдельной лабораторной пробой](server-skeleton.md#изолированная-проверка-entware-aarch64); CI не подтверждает работу физического роутера.

## Полномочия и публикация

Workflow имеет только `contents: read`, checkout не сохраняет credentials. Пользовательские secrets и PAT не нужны. Бинарники остаются в рабочем каталоге job; releases, Docker images и скачиваемые бинарные artifacts не публикуются. PR использует обычное событие `pull_request`; для fork действуют ограничения и возможное одобрение запуска со стороны GitHub.

## Защита main/master

Активен ruleset [Protect main and master](https://github.com/SunnyDayDev/xkeen-ui-server/rules/22507237) для `refs/heads/main` и `refs/heads/master`; существующая основная ветка — `main`, создавать `master` не требуется. Защита включена после первого успешного CI run с закреплённым toolchain; параметры проверены для обоих имён веток.

- Изменения принимаются через PR.
- Обязательна проверка `checks` от GitHub Actions; PR должен быть актуален относительно базовой ветки.
- Force push и удаление запрещены.
- Bypass отсутствует, в том числе для администраторов.
- Обязательных approving reviews — 0. Дополнительный участник, CODEOWNERS approval, merge queue и подписи коммитов не требуются.

Ruleset настраивается администратором вне workflow, а его состояние хранит GitHub. Имя required check и integration ID берутся из реального check run. При переименовании или удалении job нужно согласованно обновить required check, иначе слияния будут ждать отсутствующую проверку. Остальные ограничения защиты сохраняются.

Фактические ссылки на runs, отказ/восстановление и ruleset фиксируются в отчёте A04, ссылка на который публикуется в [плане разработки](roadmap.md).
