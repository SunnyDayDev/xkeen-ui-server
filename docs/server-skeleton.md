# Каркас сервера: запуск и проверка

Реализован минимальный Go-сервер: HTTP-проверка процесса, параметры запуска и штатная остановка. Хранилища, управления конфигурацией, авторизации, UI и агента пока нет. `/healthz` не подтверждает готовность этих возможностей или применение ревизии.

## Toolchain

Для разработки используется **Go 1.27.1**. SDK нужен на машине сборки; готовый бинарник работает без установленного Go. `go.mod` задаёт языковую версию 1.27.0 и рекомендуемый toolchain 1.27.1. Перед проверками убедитесь в фактической версии:

```sh
go version
```

Установщик для своей ОС и архитектуры можно получить на [официальной странице Go](https://go.dev/dl/). Пример установки SDK в локальный каталог проекта на **Linux aarch64**, выполняемый из корня репозитория:

```sh
mkdir -p .build/toolchain
curl -fL -o .build/toolchain/go.tar.gz https://dl.google.com/go/go1.27.1.linux-arm64.tar.gz
printf '%s\n' '3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec  .build/toolchain/go.tar.gz' | sha256sum -c -
tar -xzf .build/toolchain/go.tar.gz -C .build/toolchain
export PATH="$PWD/.build/toolchain/go/bin:$PATH"
export GOTOOLCHAIN=local
go version
```

При этой проверке macOS-архив версии 1.27.1 возвращал 404. Нативный SDK macOS ARM64 был получен через [официальный toolchain proxy](https://proxy.golang.org/golang.org/toolchain/@v/v0.0.1-go1.27.1.darwin-arm64.zip) с проверкой модульной суммы по [Go checksum database](https://sum.golang.org/lookup/golang.org/toolchain@v0.0.1-go1.27.1.darwin-arm64). Это отдельный канал поставки той же версии, а не смена toolchain.

## Запуск

Из корня проекта, при доступном `go` в PATH:

```sh
sh scripts/run-server.sh
```

Скрипт собирает `.build/xkeen-ui-server` без cgo и через `exec` запускает его в foreground. Ошибка сборки прекращает запуск. Адрес по умолчанию — `127.0.0.1:8080`. В другом терминале:

```sh
curl --noproxy '*' -i http://127.0.0.1:8080/healthz
```

Ответ: HTTP 200, `Content-Type: application/json`, тело:

```json
{"status":"ok"}
```

Параметр адреса и помощь доступны как через скрипт, так и непосредственно у бинарника:

```sh
sh scripts/run-server.sh --listen 127.0.0.1:8081
```

```sh
./.build/xkeen-ui-server --help
./.build/xkeen-ui-server --listen '[::1]:0'
```

Запускайте один из приведённых вариантов. `--listen` принимает IP literal и порт 0–65535; порт 0 выбирается ОС. Фактический адрес выводится в stderr как JSON-запись с `event: "listening"`. Hostnames, пустой host и позиционные аргументы не поддерживаются. Явный `0.0.0.0:8080` или `[::]:8080` открывает прослушивание доступных сетевых интерфейсов; по умолчанию используется только loopback.

`GET /healthz` — единственный маршрут. Другие методы на нём возвращают 405 и `Allow: GET`; неизвестные пути возвращают 404. Настройки будущего внешнего API управления к этому техническому endpoint пока не применяются.

## Остановка и ошибки

В терминале используйте Ctrl+C; служба или другой процесс могут отправить SIGTERM. Сервер закрывает listener и ждёт уже активные запросы до 5 секунд. По истечении срока оставшиеся соединения закрываются принудительно.

| Код завершения | Значение |
| --- | --- |
| 0 | Штатная остановка или показ помощи |
| 1 | Ошибка открытия порта, обслуживания или остановки |
| 2 | Неверные аргументы |

Ошибки выводятся в stderr. Запись `listening` появляется только после успешного открытия listener; готовность принимать HTTP проверяется запросом `/healthz`.

## Тесты и сборка

Эти проверки автоматизированы в [CI](ci.md); там же описаны диагностика workflow и защита основных веток.

```sh
go test ./... -count=1
go vet ./...
go test -race ./... -count=1
```

Процессные тесты рассчитаны на Unix, требуют разрешённых loopback-соединений и собирают временный нативный бинарник. Проверка wrapper использует `.build/`. `-race` требует поддерживаемой среды и C toolchain на машине тестирования; это не добавляет cgo в поставляемый сервер. IPv6-проверка явно пропускается, если ОС не предоставляет IPv6 loopback.

Linux ARM64-бинарник для следующей пробы:

```sh
mkdir -p .build
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o .build/xkeen-ui-server-linux-arm64 ./cmd/xkeen-ui-server
```

## Изолированная проверка Entware aarch64

Требуется Docker и ARM64-образ `xkeen-router-lab:dev`, собираемый по [инструкции лаборатории](../lab/README.md). Проверка использует отдельный контейнер с отключённой внешней сетью; единственное монтирование — read-only бинарник. Порты не публикуются, volumes обычного стенда не подключаются, XKeen не запускается.

Команда из корня проекта проверяет запуск, JSON-ответ, SIGTERM с кодом 0 и повторный запуск на том же порту:

```sh
docker run --rm -i --name xkeen-server-probe-a03 \
  --network none --cap-drop ALL --security-opt no-new-privileges \
  --mount "type=bind,src=$(pwd)/.build/xkeen-ui-server-linux-arm64,dst=/tmp/xkeen-ui-server,readonly" \
  --entrypoint /opt/bin/sh xkeen-router-lab:dev -s <<'SH'
set -eu
server_pid=
cleanup() {
  if [ -n "$server_pid" ]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT
uname -sm
/opt/bin/opkg list-installed busybox
/opt/bin/opkg list-installed libc
for attempt in 1 2; do
  /tmp/xkeen-ui-server --listen 127.0.0.1:8080 >/tmp/server.log 2>&1 &
  server_pid=$!
  ready=0
  for retry in $(seq 1 15); do
    if ! kill -0 "$server_pid" 2>/dev/null; then
      cat /tmp/server.log
      exit 1
    fi
    body=$(curl --noproxy '*' -fsS --max-time 1 http://127.0.0.1:8080/healthz 2>/dev/null || true)
    if [ "$body" = '{"status":"ok"}' ]; then ready=1; break; fi
    sleep 1
  done
  [ "$ready" = 1 ] || { cat /tmp/server.log; exit 1; }
  kill -TERM "$server_pid"
  wait "$server_pid"
  server_pid=
  echo "PASS: attempt $attempt, health JSON OK, SIGTERM exit 0"
done
SH
```

После выполнения временный контейнер удаляется благодаря `--rm`. Проверка удаления:

```sh
docker ps -a --filter 'name=^/xkeen-server-probe-a03$' --format '{{.Names}}'
```

Ожидается пустой вывод. Обычный экземпляр лаборатории не изменяется.

## Подтверждённый результат и границы

На 2026-09-07 прошли нативные тесты на Darwin ARM64, проверка гонок, `go vet`, кросс-сборка и два цикла запуска в Linux/Entware aarch64. Linux-бинарник имеет формат ELF AArch64 без `PT_INTERP` и `PT_DYNAMIC`. Подробные Red/Green и ограничения сохранены в [отчёте change](../openspec/changes/archive/2026-09-08-add-server-skeleton/verification.md).

Это подтверждает жизненный цикл каркаса в лаборатории. Физический роутер, его ядро и накопитель, SQLite, потребление памяти под целевой нагрузкой, установщик и регистрация в `init.d` ещё не проверены.
