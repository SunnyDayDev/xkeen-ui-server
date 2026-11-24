# Проверка Entware x86-64

Для следующей архитектуры выбрана x86-64. Upstream предоставляет отдельный [bootstrap Entware](https://github.com/Entware/installer.sh/blob/master/generic.sh) и [feed x64-k3.2](https://bin.entware.net/x64-k3.2/Packages.gz). Лабораторный образ использует закреплённый Xray 26.3.27 для amd64.

На Git `a46ae6c357f3415bef4ebb108298f0aff940acc9` и Go 1.27.1 получены два статических ELF x86-64 с `GOAMD64=v1` и `CGO_ENABLED=0`. В отдельном контейнере с `--network none`, отключёнными capabilities и UID/GID 65532 реальные процессы подтвердили:

- HTTPS-регистрацию агента, первый каталог и перезапуск с сохранением идентичности;
- настройку и сброс администратора, сессии, TLS и SQLite;
- восстановление SQLite, проверку сессии и курсора в копии и возврат к более свежим данным;
- явный отказ установщика с диагностикой `Entware installation requires Linux ARM64`.

В образе работают BusyBox 1.38.0 и Xray 26.3.27 amd64. Загруженное ядро `7.0.14-linuxkit` и архитектура `x86_64` получены из `uname`. Имя feed указывает целевую линейку пакетов. Совместимость с физической прошивкой, Linux 3.2, init.d-регистрация, hot/restart Xray и установка пакета x86-64 остаются вне подтверждённого режима. Штатный установщик поддерживает ARM64.

## Повтор проверки

Запускайте команды из отдельного checkout коммита, указанного выше, в оболочке POSIX на хосте с Go 1.27.1, Docker и возможностью исполнять linux/amd64. Результат с другой ревизией фиксируется отдельно.

```sh
docker buildx build --platform linux/amd64 --load -t xkeen-entware-x64-proof:local lab
proof=$(mktemp -d)
trap 'rm -rf -- "$proof"' EXIT
chmod 0755 "$proof"
export GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0
go build -trimpath -o "$proof/server" ./cmd/xkeen-ui-server
go build -trimpath -o "$proof/agent" ./cmd/xkeen-agent
go build -trimpath -o "$proof/installer" ./cmd/xkeen-entware-installer
go test -c -o "$proof/process-test" ./cmd/xkeen-ui-server
file "$proof/server" "$proof/agent"
docker run --rm --platform linux/amd64 --network none --cap-drop=ALL \
  --user 65532:65532 --mount "type=bind,src=$proof,dst=/proof,readonly" \
  -e XKEEN_TEST_SERVER_BINARY=/proof/server -e XKEEN_TEST_AGENT_BINARY=/proof/agent \
  --entrypoint /opt/bin/sh xkeen-entware-x64-proof:local -ec '
    uname -smr
    test -x /opt/etc/init.d/rc.unslung
    xray_version=$(/opt/sbin/xray version)
    printf "%s\n" "$xray_version" | head -2
    status=0
    refusal=$(/proof/installer version 2>&1) || status=$?
    test "$status" -eq 1
    test "$refusal" = "Entware installation requires Linux ARM64"
    printf "%s\n" "$refusal"
    /proof/process-test -test.v -test.run "^(TestProcessRouterAgentEnrollRestartAndRecover|TestProcessWebAdministratorLifecycle|TestProcessStorageRestoreAuthorityAndReturnToNewerDatabase)$"
  '
```
