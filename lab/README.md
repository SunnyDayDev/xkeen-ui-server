# Entware / XKeen / Xray lab

Работающий Docker-стенд с настоящим Entware/opkg. По умолчанию XKeen управляет Xray в режиме `Other` с SOCKS-входом. Прямой запуск Xray доступен как отдельный режим. Центрального сервиса и агента в стенде пока нет.

## Запуск

Из корня проекта, при работающем Docker Engine:

```sh
docker compose -f lab/compose.yaml build router
docker compose -f lab/compose.yaml up -d --wait router
docker compose -f lab/compose.yaml ps
docker compose -f lab/compose.yaml exec router sh
```

Образ содержит Debian slim, Entware в `/opt`, Entware BusyBox, библиотеки и сетевые утилиты. Xray установлен из закреплённого официального релиза; исходные скрипты XKeen и его init-скрипт — из закреплённой ревизии. Интерактивный установщик XKeen не используется. Собственного `.ipk` агента ещё нет.

Порты на хост не публикуются. API Xray слушает `127.0.0.1:10085` внутри контейнера, SOCKS — `127.0.0.1:1080`. Встроенная HTTP-мишень на `127.0.0.1:18080` возвращает `router-lab-ok`; она использует BusyBox базовой системы, поскольку сборка BusyBox Entware не содержит `httpd`.

В контейнере доступны обычные команды:

```sh
opkg list-installed
xkeen -status
xkeen -restart
xray api lsrules --server=127.0.0.1:10085
curl --noproxy '' --proxy socks5h://127.0.0.1:1080 http://127.0.0.1:18080/
```

Контейнер XKeen продолжает работать, когда служба Xray остановлена: это позволяет экспериментировать с `xkeen -stop` / `xkeen -start`, как на роутере. Healthcheck при недоступном API показывает `unhealthy`.

## Раздельные файлы

```text
/opt/etc/xray/configs/
  01_log.json
  02_api.json
  03_inbounds.json
  04_outbounds.json
  05_routing.json
```

Каждый файл содержит JSON-объект с соответствующим корневым ключом (`api`, `inbounds`, `outbounds`, `routing`). Xray собирает их через `-confdir`; XKeen использует тот же каталог через переменную окружения Xray. Нумерация задаёт порядок чтения. Разбиение файлов — возможность Xray, а API — gRPC API самого Xray.

Файлы в `lab/configs` копируются только в пустой каталог первого запуска. Рабочий каталог хранится в отдельном Docker volume и переживает пересоздание контейнера. Изменение примеров в репозитории не перезаписывает существующий volume.

`exclude_ips` относится к XKeen: его результатом является `/opt/etc/xkeen/ip_exclude.lst`. Этот файл не входит в каталог JSON Xray и в данном стенде не проверяется на трафике.

## Проверки

```sh
sh lab/check.sh
LAB_RUNTIME=xray sh lab/check.sh
```

Каждый запуск создаёт отдельный временный Compose-проект, собственную сеть и volume. По завершении, включая ошибку, тестовые ресурсы удаляются; обычный экземпляр стенда не затрагивается. Проверки используют уже собранный образ.

Проверяется:

1. Реальный Entware/opkg, валидность полного каталога конфигов и доступность API.
2. Успешный запрос через SOCKS к локальной HTTP-мишени.
3. Отклонение некорректного полного конфига до применения при сохранении работающего процесса и трафика.
4. Замена всего набора правил через `xray api adrules --append=false`, удаление старого `ruleTag`, блокировка нового запроса и неизменность PID вместе со временем старта процесса.
5. Запись нового файла через rename в том же каталоге.
6. Сохранение блокировки после `xkeen -restart` в режиме XKeen и после перезапуска контейнера в обоих режимах.
7. Возврат разрешающего правила через API и восстановление трафика.

Перед проверкой блокировки тест отдельно подтверждает доступность HTTP-мишени напрямую. Неудачный HTTP-запрос из-за остановленной мишени не засчитывается как успешная блокировка.

Прямой Xray в обычном экземпляре:

```sh
LAB_RUNTIME=xray docker compose -f lab/compose.yaml up -d --wait router
```

Возврат к XKeen:

```sh
LAB_RUNTIME=xkeen docker compose -f lab/compose.yaml up -d --wait router
```

Отдельная краткая проба запуска XKeen без постоянного volume:

```sh
docker compose -f lab/compose.yaml run --rm xkeen-probe
```

## Границы результата

Проверен Linux ARM64. В сборке предусмотрен AMD64, но его выполнение не проверялось. В контейнере отсутствует KeeneticOS: XKeen сообщает об отсутствии RCI на `127.0.0.1:79`, но успешно запускает SOCKS-прокси в режиме `Other`. Прошивка не подменяется фиктивным успешным API.

TProxy/Redirect, политики Keenetic, события netfilter, `exclude_ips`, другие архитектуры и совместимость старых Xray требуют отдельных проверок. Контейнер использует ядро Linux среды Docker. `NET_ADMIN` предоставляется для сетевых операций XKeen только внутри его network namespace; host network и privileged не используются.

Горячая проверка относится к **замене правил**. Она не доказывает горячее применение всех полей `routing` или всего конфига Xray. В частности, изменение `domainStrategy` требует отдельного пути применения. Ответ API и запись на диск — отдельные операции; журналирование, откат и восстановление после сбоя питания будут частью будущего агента.

Версии XKeen/Xray и контрольные суммы архивов закреплены в сборке. Debian и Entware feeds изменяемые: будущая пересборка может получить иные системные пакеты. Manifest конкретной сборки находится в `/usr/local/share/router-lab/entware-packages.txt`, версии — в `versions.txt`, контрольные суммы артефактов — в `artifacts.sha256` в том же каталоге. [Результат первой проверки](results/arm64.md).

## Остановка

Сохранить конфиги:

```sh
docker compose -f lab/compose.yaml down
```

Удалить также конфиги стенда и начать следующий запуск с примеров:

```sh
docker compose -f lab/compose.yaml down --volumes
```

## Источники

- [Entware installer](https://github.com/Entware/installer.sh/blob/master/generic.sh)
- [XKeen, использованная ревизия](https://github.com/jameszeroX/XKeen/tree/e461c4e9964fb8ac78e5fe01aa2e27ab980af712)
- [Xray v26.3.27](https://github.com/XTLS/Xray-core/releases/tag/v26.3.27)
- [Загрузка каталога конфигурации Xray](https://github.com/XTLS/Xray-core/blob/v26.3.27/main/run.go)
- [Команда adrules](https://github.com/XTLS/Xray-core/blob/v26.3.27/main/commands/all/api/rules_add.go)
