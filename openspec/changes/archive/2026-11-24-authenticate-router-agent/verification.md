# Проверка D03

## D03.1. Приглашения, привязка и SQLite

- Первый запуск целевых тестов завершился ошибкой компиляции по отсутствующим пакетам общего токена и прикладного сервиса. После минимальной реализации прошли проверка миграции v5→v6, запрет выдачи неподготовленному роутеру, точная граница срока, замена приглашения, повтор на втором SQLite-соединении, конкурентное погашение и откат при отказе записи.
- Дополнительный Red обнаружил, что проигнорированная SQLite-запись могла вернуть успешный отзыв. После проверки конечного состояния отказ возвращается безопасно. Сценарий прошёл вместе с остальными.
- `go test ./internal/storage/sqlite -run '^(TestAgentCredential|TestCredentialMigration)' -race -count=20` — PASS. Независимое ревью и повтор целевых тестов завершены.

## D03.2. HTTP, TLS и запуск

- `go test ./internal/server -run '^TestAgentHandler' -count=1` сначала завершился с отсутствующим обработчиком. Green подтвердил оба запроса, один bearer, строгую JSON-форму, лимит, отсутствие чужого ID и безопасные статусы. Отдельная регрессия сначала обнаружила отсутствие ограничения чтения до проверки полномочия и прошла после переноса установки deadline.
- Процессный Red воспроизвёл отсутствие TLS-флагов. Green подтвердил корректную пару, безопасный отказ при неполной и непригодной паре до открытия SQLite и listener, сохранение HTTP health check и отказ недоверенному CA или имени.
- Сквозной процессный тест использует настоящие TLS и SQLite. Подготовленный роутер привязан один раз. После перезапуска точный повтор возвращает прежний ID. Другой кандидат и отозванный ключ отклоняются. SIGTERM штатно останавливает процесс. Независимое ревью и целевой запуск с `-race` завершены.
- `go test ./internal/server ./cmd/xkeen-ui-server -run '^(TestAgentHandler|TestProcess(AgentTLS|HTTPDoesNotEnableAgentEndpoints))' -race -count=5` — PASS.

## Платформы и артефакты

Прошли `go test ./...`, `go test -race ./...`, `go vet ./...`, строгий OpenSpec и `CGO_ENABLED=0` сборки Linux ARM64/AMD64. Три процессных теста повторены на статическом Linux ARM64 сервере в Entware aarch64. Docker запущен с `--network none`, тестовый сертификат и временная SQLite созданы внутри пробы. Использовался локальный образ `xkeen-router-lab:dev` и только искусственные данные.

```sh
probe=$(mktemp -d)
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$probe/server" ./cmd/xkeen-ui-server
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c -o "$probe/server.test" ./cmd/xkeen-ui-server
docker run --rm --platform linux/arm64 --network none -v "$probe:/probe:ro" \
  -e XKEEN_TEST_SERVER_BINARY=/probe/server --entrypoint /probe/server.test \
  xkeen-router-lab:dev -test.v -test.run '^TestProcess(AgentTLS|HTTPDoesNotEnableAgentEndpoints)'
```

D04 проверит отдельный агентский процесс и сохранение ключа на стороне агента. D05–D08 реализуют получение файлов, контакт, отчёты и подписку.
