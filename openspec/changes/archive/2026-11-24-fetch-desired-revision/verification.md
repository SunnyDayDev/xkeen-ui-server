# Проверка D05

24 ноября 2026 года, Go 1.27.1, Darwin ARM64 и изолированный Entware aarch64. [Issue #70](https://github.com/SunnyDayDev/xkeen-ui-server/issues/70).

| Цикл | Наблюдавшийся Red | Green |
| --- | --- | --- |
| Отдельное чтение SQLite | У хранилища отсутствовал метод `ReadDesired` | `TestDesiredRevisionReadsOnlyLatestCommittedFiles` прошёл 20 раз под детектором гонок с двумя подключениями. |
| Целостность и HTTP | Отсутствовали кодек, отдельный GET и клиентская операция. | Проверки `TestDesiredCodec*`, `TestAgentDesiredScopesAndCanonicalizesConditionalRead` и `TestDesiredHTTPSUsesBoundConditionalAndRejectsBadTransport` прошли. |
| Безопасное сохранение и повтор | У агента отсутствовали операция сверки и методы кэша. Реальный процесс запускался без записи желаемой ревизии. | `TestDesiredSync*`, `TestDesiredRetry*`, `TestDesiredCacheIsPrivateAtomicAndReopens` и `TestProcessAgentFetchesDesiredAcrossRestart` прошли. |
| Поздний результат и отмена | Меньший или несовместимый снимок, а также ответ после отмены записывались как новые. | Регрессии `TestDesiredSyncRejectsRegressedAndChangedImmutableRevisions` и `TestDesiredSyncDoesNotWriteCanceledFetch` прошли. |

Реальные процессы сервера и агента, TLS и отдельная база SQLite проверены на двух вымышленных роутерах. Чужой ETag с тем же номером не подавил полный ответ. При совпавшей копии агент оставил прежний файл. После перезапуска сервера и новой пары публикаций агент получил только ревизию 3. Отдельно проверен 204 при отсутствии снимка и действующем ключе.

Сквозной TLS-тест оборвал передачу точного 8 МиБ снимка с сохранённой копией предыдущего номера. Агент оставил копию прежней и затем получил точные файлы после повтора. После переоткрытия приватной записи 304 восстановил тот же полный кандидат. В Entware `aarch64-k3.10` с ядром `7.0.14-linuxkit` полные загрузка, запись и повторная сверка заняли 147 мс. Пиковый RSS совмещённого тестового процесса клиента, SQLite и HTTPS-обработчика — 130784 КиБ. Контейнер ограничивался 256 МиБ и не имел внешней сети. Показатель относится к стенду и совмещённому процессу.

Независимое ревью сервера и клиента подтвердило область авторизации, сильный ETag, сохранность до полного ответа, порядок отмены и поздних ревизий. Целевой повтор под детектором гонок, полный Go, race, vet, статические Linux ARM64/AMD64 и строгий OpenSpec прошли.

## Повторение

```sh
go test ./cmd/xkeen-ui-server -run 'TestProcessAgentFetchesDesiredAcrossRestart|TestAgentDesiredTLSKeepsCache' -count=1 -v
go test ./internal/storage/sqlite -run '^TestDesiredRevision' -count=20 -race
go test ./...
go test -race ./...
go vet ./...
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...
```

[Команды подготовки стенда и переменные бинарников](../2026-11-24-connect-router-agent/verification.md) подходят для этих двух сквозных тестов. Используется тот же изолированный контейнер и отдельный лимит `--memory 256m --memory-swap 256m`.
