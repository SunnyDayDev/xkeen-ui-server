## Decisions

Выбрать x86-64 по отдельному доступному bootstrap и пакету Xray. Собрать чистый linux/amd64 образ Entware. Собрать Go без cgo на GOAMD64=v1. В контейнере на loopback проверить штатные процессы, F03 через TLS и SQLite, D04/D05 с перезапуском и резервирование. Сохранить отказ production installer. Зафиксировать фактическое ядро LinuxKit и отсутствие проверки init-роли, managed Xray и физического устройства.
