# Приёмка D04

[Issue #67](https://github.com/SunnyDayDev/xkeen-ui-server/issues/67). Агент проверен с [серверным контрактом D03](../../../specs/agent-server-enrollment/spec.md) и целевым [Entware aarch64](../../../../docs/agent-environment-research.md).

## Red и Green

| Сценарий | Фактический Red | Green |
| --- | --- | --- |
| Первое подключение и сохранность кандидата | До реализации пакет прикладной операции содержал только тесты. | Проверены сохранение до запроса, проверка ключа после привязки, повтор с тем же ключом и приглашением, отклонение смены origin и ключа, бюджет и отмена. |
| Локальное состояние | До реализации пакет файлового адаптера содержал только тесты. | Режимы доступа, владелец, повторное открытие, файловая блокировка и безопасный отказ на небезопасных типах файлов проверены. |
| TLS и ответы | До реализации пакет HTTPS-адаптера содержал только тесты. | Проверены точный D03 wire, ошибка доверия и имени, запрет перенаправления, различение 401/503, дублирующие поля и превышение лимита. |
| Реальный агент | До добавления команды процессная сборка не находила `cmd/xkeen-agent`. | Реальные процессы агента и D03 подтвердили привязку, восстановление, отказ отозванному ключу и штатную остановку. |
| Восстановление после сбоя записи | При ошибке закрепления ранее видимого pending-состояния проверка отправляла один запрос. | Повторный запуск заново закрепляет pending до сети. Ошибка записи оставляет число попыток обращения равным нулю. |
| Запись родительского каталога и неподходящий CA | При недоступном для синхронизации родительском каталоге запись возвращала успех. При FIFO вместо CA старт блокировался. | Недоступность родительского каталога даёт безопасный отказ. Открытие файла CA с неблокирующим режимом позволяет сразу отклонить FIFO. |

## Выполненные проверки

- `go test ./... -count=1`, `go test -race ./... -count=1` и `go vet ./...`.
- Независимая проверка состояния и процесса, включая повторные тесты с race, прошла после исправления замечаний.
- Строгая валидация OpenSpec, локальные ссылки и `git diff --check`.
- Статические ELF агента Linux ARM64 и AMD64 и сквозной ARM64-прогон в Entware. В контейнере использовались Linux 7.0.14-linuxkit и Entware `aarch64-k3.10`.

## Воспроизведение Entware

Из корня репозитория, после сборки `xkeen-router-lab:dev` по [инструкции стенда](../../../../lab/README.md):

```sh
(
set -eu
build_dir=$(mktemp -d)
trap 'rm -rf "$build_dir"' EXIT
for arch in arm64 amd64; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
    -o "$build_dir/xkeen-agent-$arch" ./cmd/xkeen-agent
  file "$build_dir/xkeen-agent-$arch"
done
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -o "$build_dir/xkeen-server" ./cmd/xkeen-ui-server
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c \
  -o "$build_dir/e2e.test" ./cmd/xkeen-ui-server
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c \
  -o "$build_dir/state.test" ./internal/agent/statefile
docker run --rm --platform linux/arm64 --network none --read-only --user 1000:1000 \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,mode=1777 --cap-drop ALL --security-opt no-new-privileges \
  --mount "type=bind,src=$build_dir/xkeen-agent-arm64,dst=/xkeen-agent,readonly" \
  --mount "type=bind,src=$build_dir/xkeen-server,dst=/xkeen-server,readonly" \
  --mount "type=bind,src=$build_dir/e2e.test,dst=/e2e.test,readonly" \
  --mount "type=bind,src=$build_dir/state.test,dst=/state.test,readonly" \
  --entrypoint /opt/bin/sh xkeen-router-lab:dev -ec '
uname -smr
grep "^Entware=" /usr/local/share/router-lab/versions.txt
XKEEN_TEST_SERVER_BINARY=/xkeen-server XKEEN_TEST_AGENT_BINARY=/xkeen-agent \
  /e2e.test -test.run "^TestProcessRouterAgent" -test.v
/state.test -test.v
'
)
```

Сценарий создаёт временную SQLite, синтетическое действующее назначение, приглашение и сертификат. После реальной первой привязки он восстанавливает локальную запись в том виде, в каком она находилась перед получением ответа, оставляя серверную фиксацию. Следующий процесс подтверждает прежнее поколение и ID без файла приглашения. Это проверка восстановления из соответствующего состояния. Потеря ответа моделируется в целевом прикладном тесте. Физический роутер, доставка и применение конфигурации остаются за пределами прогона.
