# Центральный сервер в Docker

Локальный образ включает Go-сервер и встроенный интерфейс. Поддерживаются Linux AMD64 и ARM64. Сборка использует Go 1.27.1 и запускает конечный процесс с UID/GID 65532.

```sh
docker build --tag xkeen-ui-server:local .
docker volume create xkeen-ui-server-data
docker run --rm -it \
  --mount type=volume,source=xkeen-ui-server-data,target=/var/lib/xkeen-ui-server \
  xkeen-ui-server:local admin setup
```

Команда запрашивает пароль дважды через интерактивный терминал. Для автоматизированной локальной настройки `--password-file` принимает обычный файл 0600 владельца UID 65532 с единственной ссылкой. Передайте отдельный том с файлом только для чтения. Docker монтирует сертификат и ключ отдельно. Размещайте их с правами, позволяющими процессу читать ключ. [Правила F02 и F03](server-skeleton.md#сессия-администратора-f03).

```sh
docker run --name xkeen-ui-server --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges -p 127.0.0.1:8443:8080 \
  --mount type=volume,source=xkeen-ui-server-data,target=/var/lib/xkeen-ui-server \
  --mount type=bind,source=/path/to/xkeen-tls,target=/tls,readonly \
  xkeen-ui-server:local --listen 0.0.0.0:8080 \
  --tls-cert /tls/cert.pem --tls-key /tls/key.pem \
  --web-origin https://controller.example:8443
```

Замените путь к TLS и имя в примере. Адрес браузера должен совпадать с `--web-origin`, а имя должно быть указано в сертификате. Том хранит `state/xkeen-ui-server.sqlite` и сопутствующие файлы. Подготовьте права UID/GID 65532, если используете привязанный каталог вместо именованного тома. При пересоздании контейнера используйте тот же том. По умолчанию образ слушает HTTP 8080 для локальной проверки `/healthz`. Интерфейс и канал агента используют соответствующие режимы TLS.

Контейнер служит центральным процессом. Отдельный валидатор Xray настраивается на доверенном рабочем месте. Требования к проверке каталога описаны в [контракте публикации](../openspec/specs/configuration-revision-publication/spec.md). Образу достаточно тома состояния и указанных файлов доверия. Серверный операторский API публикации относится к следующей поставке.

```sh
sh lab/check-server-image.sh
XKEEN_TEST_SERVER_PLATFORM=linux/amd64 sh lab/check-server-image.sh
```

Проверка создаёт временный образ, отдельные тома и сертификат. Она подтверждает чистый запуск, страницу и JS по TLS, настройку администратора, сессию и повторный вход после пересоздания. [Отчёт G10](../openspec/changes/archive/2026-11-24-package-central-server/verification.md).
