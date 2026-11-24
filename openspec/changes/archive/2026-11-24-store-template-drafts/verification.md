# Проверка C02

Дата проверки: 24 ноября 2026 года. Go 1.27.1, Darwin ARM64. Linux/Entware запускался в отдельном контейнере с ядром `7.0.14-linuxkit` и архитектурой `aarch64`.

| Сценарий | Наблюдавшийся Red | Green |
| --- | --- | --- |
| Create/Get, пустой, отсутствующий, повторный и недопустимый ID | Компилируемая заглушка возвращала общую ошибку вместо результата операции | `go test ./internal/application/templates -count=1` |
| Полный набор UUID, структура и A08 | Черновик с повтором в разных секциях записывался. Новая переменная не объявлялась | `go test ./internal/configuration/... ./internal/application/templates -count=1` |
| Миграция, конкуренция и повторное открытие | В файле C01 оставалась только версия 1, адаптер ещё не сохранял черновики | `go test ./internal/storage/sqlite -count=1 -run '^TestTemplates'` |
| Безопасное чтение повреждённой оболочки | Повреждённое поле `NAME` переписывало `name` при чтении | После строгого декодирования `TestTemplatesRejectCorruptEnvelope` прошёл |

Интеграционные тесты подтвердили миграцию v2 с сохранением прежних данных, конфликт двух создателей одного ID, восстановление победившего черновика после закрытия файла и различение повреждённой и отсутствующей записи. При чтении сохранились последовательности секций и элементов, явно пустая секция, отключение, точные исходные байты JSON, нетронутая пользовательская метка, отсутствующий дефолт, явный `null` и целое число за пределами точности float64. Сохранение объявляет новую переменную, чтение не делает этого повторно.

`go test ./... -count=1`, `go vet ./...`, `CGO_ENABLED=1 go test -race ./... -count=1` и строгая проверка OpenSpec прошли. `CGO_ENABLED=0` сборки сервера для Linux ARM64 и AMD64 дали статические ELF. На ARM64 также собран и проверен тестовый бинарник:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -o "$work/storage.test" ./internal/storage/sqlite
docker run --rm --platform linux/arm64 --network none --read-only --cap-drop ALL \
    --security-opt no-new-privileges --tmpfs /tmp:rw,mode=1777 \
    --mount "type=bind,src=$work/storage.test,dst=/probe,readonly" \
    --entrypoint /opt/bin/sh xkeen-router-lab:dev -ec 'uname -smr; /probe -test.v -test.run "^TestTemplates"'
```

`$work` — временный локальный каталог. Все четыре теста `TestTemplates*` прошли в Entware. Проба проверяет внутренний сервис и SQLite-адаптер. Физические роутеры и публикация ревизий в неё не входят.
