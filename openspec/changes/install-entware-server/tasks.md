## 1. Серверная роль

- [ ] 1.1 Ревью контракта, TLS-bound readiness, прав ключа и пересечения с F09.
- [ ] 1.2 Red на HTTPS-зеркале для отказа неполного выпуска и ошибочного TLS, затем bootstrap Green.
- [ ] 1.3 Red на серверную регистрацию, HTTPS health и UI login, затем Green. Проверить статический ELF, SQLite, полноценные права и готовность экземпляра.
- [ ] 1.4 ARM64 Entware, session/admin через штатный protected setup, init.d stop/start с прежними данными и защита отдельной роли агента.
- [ ] 1.5 Документация, strict OpenSpec, архив, независимое ревью и PR.
