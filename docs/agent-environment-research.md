# A02. Первая целевая среда агента

Для разработки агента предлагается **Go без cgo на Linux ARM64**, первая среда Entware — **`aarch64-k3.10`**. В лаборатории проверены запуск статического ARM64-бинарника, запись и переименование файла в `/opt/tmp`, а также чтение файла установленным Entware BusyBox. [Issue #3](https://github.com/SunnyDayDev/xkeen-ui-server/issues/3).

## Выбор среды

| Вариант | Что даёт | Ограничения |
| --- | --- | --- |
| Go без cgo | Один исполняемый файл без динамического загрузчика и отдельной среды выполнения. Стандартная библиотека покрывает файлы, запуск процессов, отмену и сетевые соединения. Общий toolchain с сервером. | Собирается отдельно под каждую архитектуру. Совместимость с ядром и расход памяти будущего агента зависят от реализации и среды. |
| POSIX shell + Entware BusyBox | Уже доступен в Entware и подходит для запуска службы. | Долгоживущий процесс потребует координации команд и их кодов ошибок для файловых операций, повторных подключений и таймаутов. |

Базовый `GOARM64=v8.0` фиксирует ARMv8-A. `CGO_ENABLED=0` отключает cgo. Обозначение feed `aarch64-k3.10` само по себе не подтверждает совместимость с конкретным ядром или прошивкой. [Переменные сборки Go](https://go.dev/doc/install/source#environment), [документация cgo](https://pkg.go.dev/cmd/cgo), [минимальные требования Go](https://go.dev/wiki/MinimumRequirements), [feed Entware](https://bin.entware.net/aarch64-k3.10/), [справка Keenetic об Entware](https://support.keenetic.com/hero/kn-1012/en/18482-installing-opkg-entware-in-the-router-s-internal-memory.html).

## Воспроизведение лабораторной проверки

Нужны Go 1.27.1, Docker c Linux ARM64, `file` и `mktemp`. Команды выполняются из корня репозитория в POSIX-совместимой оболочке. Исходник в примере создаёт временную пробу для проверки среды. Он удаляется вместе с бинарником при выходе из оболочки.

```sh
(
set -eu
docker build --platform linux/arm64 --tag xkeen-router-lab:dev lab
probe_dir=$(mktemp -d)
trap 'rm -rf "$probe_dir"' EXIT
cat > "$probe_dir/probe.go" <<'GO'
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func main() {
	dir, err := os.MkdirTemp("/opt/tmp", "a02-")
	check(err)
	defer os.RemoveAll(dir)
	draft := filepath.Join(dir, "draft")
	ready := filepath.Join(dir, "ready")
	check(os.WriteFile(draft, []byte("a02-probe\n"), 0600))
	check(os.Rename(draft, ready))
	output, err := exec.Command("/opt/bin/busybox", "cat", ready).Output()
	check(err)
	if string(output) != "a02-probe\n" {
		check(fmt.Errorf("unexpected subprocess output: %q", output))
	}
	fmt.Println("PASS: write, same-directory rename, Entware BusyBox read")
}
GO
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOARM64=v8.0 \
  go build -trimpath -o "$probe_dir/probe" "$probe_dir/probe.go"
file "$probe_dir/probe"
go version -m "$probe_dir/probe"

docker run --rm --name xkeen-agent-probe-a02 --platform linux/arm64 \
  --network none --cap-drop ALL --security-opt no-new-privileges \
  --read-only --tmpfs /opt/tmp:rw,nosuid,nodev,noexec \
  --mount "type=bind,src=$probe_dir/probe,dst=/tmp/a02-probe,readonly" \
  --entrypoint /opt/bin/sh xkeen-router-lab:dev -ec '
uname -smr
grep "^Entware=" /usr/local/share/router-lab/versions.txt
/opt/bin/opkg list-installed busybox
/opt/bin/opkg list-installed libc
/tmp/a02-probe
set -- /opt/tmp/a02-*
test "$#" -eq 1
test "$1" = "/opt/tmp/a02-*"
echo "PASS: temporary probe files removed"
'
test -z "$(docker ps -a --filter 'name=^/xkeen-agent-probe-a02$' --format '{{.Names}}')"
echo "PASS: temporary container removed"
)
```

## Результат и границы

24 ноября 2026 года Go 1.27.1 на Darwin ARM64 собрал статически связанный ELF AArch64 размером 2 864 582 байта. В Docker на ядре `7.0.14-linuxkit` и архитектуре `aarch64` стенд сообщил feed `aarch64-k3.10`, BusyBox `1.38.0-5` и libc `2.27-12`. Проба и проверки удаления её файлов и контейнера завершились с кодом 0. Для запуска использовались отключённая сеть, read-only корень, временный `/opt/tmp` и read-only монтирование бинарника. Результат воспроизведён на свежей сборке образа стенда.

Границы проверки:

- ARM64 здесь проверен в контейнере с ядром Docker. Физические модели Keenetic и их ядра остаются непроверенными, включая модели с ARM64. ARM32, MIPS/MIPSEL и x86-64 вне первого профиля агента. AMD64-сборка стенда сама по себе статуса поддержки агента не меняет.
- Запись и переименование проверены на временной файловой системе. Устойчивость после сбоя питания, поведение реального накопителя, доступное место и пиковое потребление памяти предстоит проверить.
- Канал синхронизации, TLS, настройки доверенных сертификатов, жизненный цикл службы и применение ревизии проверяются в соответствующих задачах. Минимальные версии KeeneticOS, XKeen и Xray предстоит определить. G11 должен проверить собранный агент в согласованной среде.

[Архитектура](architecture.md) · [Стенд](../lab/README.md) · [План задач D01, D04 и G11](roadmap.md).
