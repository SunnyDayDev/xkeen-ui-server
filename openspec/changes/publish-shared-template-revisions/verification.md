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
