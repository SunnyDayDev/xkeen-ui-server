# Проверка C07

## C07.1. Групповое SQLite-хранилище

24 ноября 2026 года, Go 1.27.1, Darwin ARM64. Схема C06.3 v5 использована без миграции.

| Цикл | Наблюдавшийся Red | Green |
| --- | --- | --- |
| Согласованное групповое чтение | Заглушка не возвращала подтверждённых участников | `TestPublicationGroupReadUsesOnlyConfirmedAssignments` после реализации read transaction |
| Проверка состава и версий | Заглушка не различала недопустимый план, устаревшие токены и четыре варианта изменения состава | `TestPublicationGroupRejectsIncompletePlansAndStaleTokens`, `TestPublicationGroupDetectsPhantomMembership` |
| Общая запись и поздний отказ | Корректная запись, пустое принятие и вынужденный SQL-отказ ещё не работали | `TestPublicationGroupCommitsDraftOrderAndOnlyChangedRevisions`, `TestPublicationGroupRollsBackASecondRouterWriteFailure`, `TestPublicationGroupAcceptsAStoredTemplateWithNoMembers` |
| Целостность общего снимка и режима порядка | Разные данные под одним номером читались, а групповая запись могла сбросить персональный режим | `TestPublicationGroupRejectsDivergentSourceWithSameVersion` и `TestPublicationGroupCannotResetPersonalOrder` |

Использованы независимые подключения SQLite. Фантом обнаруживается, даже если исходные персональные токены не менялись. Триггер теста позднего отказа срабатывает на втором роутере только после вставки новой ревизии первого, затем повторное открытие подтверждает откат. Пустая группа не создаёт ревизий, сохранение равного результата оставляет прежний номер и фиксирует новое основание порядка.

Полный `go test ./... -count=1`, `go vet ./...`, `CGO_ENABLED=1 go test -race ./... -count=1` и строгий OpenSpec прошли. `go test ./internal/storage/sqlite -count=20 -run 'TestPublicationGroup(Detects|RollsBack)'` прошёл. В изолированном Entware aarch64 с ядром `7.0.14-linuxkit` прошли восемь `TestPublicationGroup*`, собранных через `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c ./internal/storage/sqlite`. Использовался временный SQLite-файл и отдельный контейнер без сети и рабочих томов.

## C07.2. Общая команда

Семь прикладных тестов сначала дошли до ожидаемого Red: заглушка возвращала один технический отказ вместо выбора общего источника, вызова Xray, сверки предварительного просмотра, результата каждого роутера и поведения при пустом составе. Те же сценарии стали зелёными после реализации команды. Независимое ревью повторило `go test ./internal/application/publication ./internal/storage/sqlite -run 'Group|Shared' -count=1`.

`TestSharedTemplatePublicationWithSQLite` проверяет полный цикл с двумя независимыми SQLite-соединениями. С теми же данными `TestSharedTemplatePublicationWithIsolatedXray` прошёл через постоянный локальный сокет изолированного воркера и закреплённый Xray 26.3.27 в Linux ARM64. Порядок сверяется при одинаковых файлах и после добавления правила. Проверены изменение одного каталога при полной замене второго, отказ последнего участника, сохранённый черновик другого шаблона, фантом нового назначения, повтор с актуальным составом, точные файлы после переоткрытия SQLite и принятие пустой группы без валидатора. После штатной остановки воркера временные задания отсутствовали, журнал был пуст.

Для повтора поднимите рабочий сокет по [руководству валидатора](../../../../docs/catalog-validator.md). Используйте отдельное временное хранилище и синтетические данные.

```sh
XKEEN_TEST_VALIDATOR_SOCKET=/path/to/socket go test ./internal/storage/sqlite -run '^TestSharedTemplatePublicationWithIsolatedXray$' -count=1 -v
go test ./...
go test -race ./...
go vet ./...
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...
```

Все перечисленные проверки выполнены успешно. Строгая валидация OpenSpec прошла для изменения и обеих синхронизированных спецификаций.
