# Проверка C06.3

24 ноября 2026 года. Go 1.27.1, Darwin ARM64. Сверено с [issue #52](https://github.com/SunnyDayDev/xkeen-ui-server/issues/52) и контрактом C05.

## Проверки до реализации и после исправлений

| Цикл | Наблюдавшийся Red | Green |
| --- | --- | --- |
| Миграция файла C03/C04 | Отсутствовали таблицы публикации и поколение C04 | `TestPublicationMigrationPreservesDrafts` и прежние тесты SQLite/C04 прошли после миграции |
| Согласованное чтение | Компилируемая заглушка возвращала общую ошибку вместо раздельных состояний | Оба `TestPublicationRead*` прошли, включая повторное открытие и повреждённую запись |
| Обычная фиксация | Заглушка не создавала ревизию и не возвращала специализированный отказ при смене назначения | `TestPublicationCommitPreservesRevisionBytesAndNoOp` и `TestPublicationOrdinaryCommitCannotChangeAssignment` прошли |
| Первое назначение | Режим, допускавший только обычную фиксацию, отклонял первое подтверждение, оба претендента на первый снимок получили отказ | `TestPublicationFirstConfirmationUsesAcceptedTemplate` и `TestPublicationBootstrapIsAtomicAndDoesNotActivateOtherDrafts` прошли |
| Регрессии независимого ревью | Повреждённая запись с небезопасным именем файла и ложный порядок принятой версии читались, опасное имя также принималось при записи | Оба `TestPublicationRejects*` прошли после проверки формы имени и полного основания |

После реализации обычной транзакции добавлены проверки конкуренции и отката. На первой проверке они прошли. Тесты показывают, что два разных результата с одними ожиданиями не фиксируются вместе, stale C04-черновик конфликтует при использовании, а не использованный черновик B сохраняется. Ошибка вставки после записи активного состояния откатывает транзакцию и возвращается без сырой диагностики SQLite. Bootstrap проверен также с принудительно пропущенной вставкой ревизии. Граница теста — хранилище, без исполнения Xray или всей команды C06.4.

## Итог

- `go test ./... -count=1`, `go vet ./...`, `CGO_ENABLED=1 go test -race ./... -count=1`, форматирование, строгая валидация OpenSpec и `git diff --check` прошли.
- `go test ./internal/storage/sqlite -count=20 -run 'TestPublication(Competing|Bootstrap)'` прошёл, используются независимые SQLite-подключения к одному файлу.
- Сервер собран для Linux AMD64 и ARM64 с `CGO_ENABLED=0`. `file` подтвердил статические ELF. Все 11 целевых тестов SQLite прошли в отдельном Entware aarch64-контейнере с `7.0.14-linuxkit`.

Контейнерная проверка не использует рабочие тома или сеть. Для повторения создать временный каталог в `$work` и выполнить:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -o "$work/storage.test" ./internal/storage/sqlite
docker run --rm --platform linux/arm64 --network none --read-only --cap-drop ALL \
    --security-opt no-new-privileges --tmpfs /tmp:rw,mode=1777 \
    --mount "type=bind,src=$work/storage.test,dst=/probe,readonly" \
    --entrypoint /opt/bin/sh xkeen-router-lab:dev -ec 'uname -smr; /probe -test.v -test.run "^TestPublication"'
```

Контракт групповой транзакции C07, проверка сервера Xray и агентское применение остаются за пределами этого запуска.
