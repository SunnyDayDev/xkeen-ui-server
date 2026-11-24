# Чистая установка xkeen-ui в Entware

Комплект выбранного коммита собирают локально по мере необходимости. G17/G18 проверяют установку на синтетическом HTTPS-зеркале. [Локальная сборка и проверка Entware](entware-local-build.md).


Инструкция устанавливает центральный сервер HTTPS и транспортного агента для Linux ARM64. Их можно разместить на разных узлах или вместе с разными UID/GID. Для применения Xray требуется отдельно подтверждённый профиль и валидатор публикации. Проверка этого пути относится к G18. [План G18](https://github.com/SunnyDayDev/xkeen-ui-server/issues/141).

Для испытанного выделенного Xray доступна отдельная установка управляемой роли. [Управляемый профиль](entware-application-profile.md).

## Что подготовить

- Entware ARM64 с `/opt/bin/sh`, curl, CA bundle, sha256sum и исполняемой файловой системой /opt. Нужны pidfd, проверяемый procfs и доступ root для регистрации init.d. Выделите серверу и агенту разные UID/GID, если роли работают в одной системе.
- HTTPS-имя сервера и соответствующая PEM-цепочка. Ключ хранится у root с режимом 0600. Для собственного CA подготовьте его сертификат, чтобы передать агенту отдельно.
- Точный тег, полный Git commit самого установщика и SHA-256 скрипта и `SHA256SUMS`. Получите значения у владельца через отдельно доверенный канал. Используйте существующий выпуск после его утверждения. [Как получить и проверить значения](entware-installation-contract.md#получение-конкретной-версии-с-github).

Загрузите скрипт по точному commit и проверьте его на доверенном рабочем месте. Перенесите его в закрытый каталог root на целевой Entware. Например, `/opt/etc/xkeen-ui-install` с режимом 0700. На каждом целевом узле ещё раз сверьте сумму перед запуском. В примере замените все три значения.

```sh
INSTALLER=/opt/etc/xkeen-ui-install/install-entware.sh
INSTALLER_SHA256='REPLACE_WITH_OWNER_APPROVED_INSTALLER_SHA256'
RELEASE_TAG='vX.Y.Z'
MANIFEST_SHA256='REPLACE_WITH_OWNER_APPROVED_MANIFEST_SHA256'
printf '%s  %s\n' "$INSTALLER_SHA256" "$INSTALLER" | /opt/bin/sha256sum -c - || exit 1
```

## Сервер

В корневом терминале Entware подставьте имя из сертификата, нужный слушатель и пути к PEM-файлам. Настройте браузер так, чтобы он доверял фактической цепочке и обращался по тому же origin.

```sh
/opt/bin/sh "$INSTALLER" install \
  --role server --version "$RELEASE_TAG" --manifest-sha256 "$MANIFEST_SHA256" \
  --run-as 65531:65531 --listen 0.0.0.0:8443 \
  --web-origin https://controller.example:8443 \
  --tls-cert /opt/etc/xkeen-ui-install/controller-cert.pem \
  --tls-key /opt/etc/xkeen-ui-install/controller-key.pem

/opt/etc/init.d/S91xkeen-ui-server admin setup
/opt/etc/init.d/S91xkeen-ui-server status
```

Команда `admin setup` в терминале запросит пароль дважды. При автоматизации передайте `--password-file` с абсолютным путём к обычному файлу 0600 владельца серверного UID. Содержимое читается локально. Откройте веб-консоль по заданному origin и войдите. После перезапуска используется прежняя база `/opt/var/lib/xkeen-ui/server/server.sqlite`. [Роль сервера](entware-server-install.md).

## Агент

Получите действующее приглашение для нового роутера от администратора. Поместите его в приватный файл root с режимом 0600. Для стандартного доверия CA используйте команду ниже. Для собственного CA добавьте `--ca-file /opt/etc/xkeen-ui-install/controller-ca.pem`.

```sh
/opt/bin/sh "$INSTALLER" install \
  --role agent-transport --version "$RELEASE_TAG" --manifest-sha256 "$MANIFEST_SHA256" \
  --run-as 65532:65532 --server https://controller.example:8443 \
  --invitation-file /opt/etc/xkeen-ui-install/agent-invitation

/opt/etc/init.d/S92xkeen-agent status
/opt/etc/init.d/S92xkeen-agent stop
/opt/etc/init.d/S92xkeen-agent start
```

Рабочий ключ остаётся в `/opt/var/lib/xkeen-ui/agent-transport`. После успешного подключения владелец может удалить обе копии использованного приглашения. При недоступном сервере повторите первичную команду с теми же тегом, манифестом, origin, CA и UID/GID. При смене проверенной версии используйте `update`. [Роль агента и обновление](entware-agent-install.md).

## Проверка на чистом стенде

В checkout с Go из `go.mod` и Docker запустите `sh lab/check-entware-install.sh`. Нужен собранный ARM64 образ стенда `xkeen-router-lab:dev` или явно выбранный `XKEEN_LAB_IMAGE`. [Подготовка образа](../lab/README.md).

Стенд запускает новый контейнер без внешней сети, готовит синтетический сертификат, выпускает тестовое приглашение и подаёт те же команды через локальное HTTPS-зеркало с `--release-mirror` и `--mirror-ca-file`. В отчёте должны пройти bootstrap, вход в установленный UI, restart SQLite, сохранение клиентского ключа при обновлении и восстановление прежней версии. Физические устройства, эксплуатационные секреты и опубликованный тестовый релиз для этой проверки не требуются.
