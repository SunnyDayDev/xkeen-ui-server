# Первичная установка сервера Entware

G15 устанавливает центральный сервер Linux ARM64 из точного проверенного выпуска. Требуются прямой TLS, явный HTTPS origin веб-интерфейса и отдельные UID/GID. Выделите серверу другие UID и GID, чем агенту на том же устройстве. Серверный TLS-ключ размещается с доступом root и выделенной серверной группы.

Сначала получите и проверьте скрипт установки и SHA-256 манифеста. Подготовьте PEM-цепочку и приватный TLS-ключ владельца root с режимом 0600. Имя в browser origin должно быть в сертификате. [Получение проверенной версии](entware-installation-contract.md#получение-конкретной-версии-с-github).

```sh
/opt/bin/sh ./install-entware.sh install \
  --role server --version "$RELEASE_TAG" --manifest-sha256 "$MANIFEST_SHA256" \
  --run-as 65531:65531 --listen 0.0.0.0:8443 \
  --web-origin https://controller.example:8443 \
  --tls-cert /opt/etc/xkeen-ui-install/controller-cert.pem \
  --tls-key /opt/etc/xkeen-ui-install/controller-key.pem
```

Служба появляется после подтверждения собственного HTTPS-слушателя и доступности `/healthz`. Настройте администратора в локальном терминале. Команда запускает F02 от серверного UID с установленным путём SQLite.

```sh
/opt/etc/init.d/S91xkeen-ui-server admin setup
/opt/etc/init.d/S91xkeen-ui-server status
```

Автоматизированный локальный setup принимает `--password-file /absolute/path`. Файл должен быть обычным, с одной ссылкой, режимом 0600 и принадлежать UID сервера. Сам пароль вводится в терминале или читается из файла. Откройте веб-консоль по заданному origin и войдите.

`/opt/etc/init.d/S91xkeen-ui-server stop` штатно останавливает подтверждённый экземпляр. После `start` сервер использует прежние данные `/opt/var/lib/xkeen-ui/server/server.sqlite`. Точный повтор неудавшейся первичной установки использует тот же тег, манифест, адрес, права и TLS. Серверная смена версии и удаление отслеживаются отдельно. [Серверное обслуживание](https://github.com/SunnyDayDev/xkeen-ui-server/issues/120).

Для публикации нужен отдельный явно выбранный доверенный валидатор Xray. Проверка TLS/SQLite подтверждает центральный сервис, а проверка действительного каталога и подключение валидатора идут через контракт публикации. [Спецификация публикации](../openspec/specs/configuration-revision-publication/spec.md).
