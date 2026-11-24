# Приёмка D07

[Issue #72](https://github.com/SunnyDayDev/xkeen-ui-server/issues/72).

| Граница | Red | Green |
| --- | --- | --- |
| Регистрация ожидания | Не было интерфейса ожидания | Управляемая гонка между регистрацией и чтением, таймаут с финальной проверкой, отзыв и остановка |
| SQLite и фиксация | Конструктор не принимал уведомитель | После фиксации отдельное чтение видит новый номер. Равный каталог, откат и конфликт не сигнализируют. Группа уведомляет только изменившиеся записи. |
| HTTPS | Не было маршрута и порта watch | TLS, 200/204, 401/429/503, канонический номер, область ключа и отказ выбора чужого ID |
| Агент | Не было цикла ожидания и сверки | Управляемые тики позволяют сверку при блокированном watch. Запоздалый результат не подменяет подтверждённый курсор. |

Полные `go test ./... -count=1`, `go test -race ./... -count=1`, `go vet ./...`, Linux ARM64/AMD64 без cgo, строгая валидация OpenSpec и проверка ссылок.

## Entware ARM64

После сборки [лабораторного образа](../../../../lab/README.md) запустить из корня:

```sh
(
set -eu
build=$(mktemp -d)
trap 'rm -rf "$build"' EXIT
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$build/agent" ./cmd/xkeen-agent
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$build/server" ./cmd/xkeen-ui-server
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c -o "$build/watch.test" ./cmd/xkeen-ui-server
docker run --rm --platform linux/arm64 --network none --read-only --user 1000:1000 \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,mode=1777 --cap-drop ALL --security-opt no-new-privileges \
  --mount "type=bind,src=$build,dst=/probe,readonly" \
  -e XKEEN_TEST_SERVER_BINARY=/probe/server -e XKEEN_TEST_AGENT_BINARY=/probe/agent \
  --entrypoint /probe/watch.test xkeen-router-lab:dev -test.run '^TestProcessRouterAgentWatch' -test.v
)
```

Два агента используют настоящий обработчик TLS и SQLite в тестовой композиции. Сохранены точные файлы новой ревизии затронутого роутера. Второй участник сохранил прежнюю копию. Отозванная регистрация завершила ожидание. Пропущенная подсказка и длинный разрыв между несколькими публикациями дополнительно проверяются D08.
