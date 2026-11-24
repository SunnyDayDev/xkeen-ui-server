## ADDED Requirements

### Requirement: Managed startup is gated by durable local recovery

Контролируемый путь штатного запуска SHALL разрешать только актуальную полностью проверенную генерацию после разрешения локального журнала. Проверка и разрешённый запуск SHALL быть изолированы от параллельного применения. Отсутствие связи с сервером SHALL не менять результат решения.

#### Scenario: An interrupted startup cannot load a mixed catalog
- **WHEN** каталог содержит допустимую смесь после прерывания записи
- **THEN** штатный запуск остаётся закрытым
- **AND** recovery вызывает восстановление прежней точной генерации до открытия барьера

#### Scenario: Competing launches cannot reuse a stale authorization
- **WHEN** штатный старт пересекается с восстановлением или новым намерением
- **THEN** старт ждёт согласованного результата или безопасно прекращается

#### Scenario: A pre-intent interruption preserves the existing runtime
- **WHEN** сбой произошёл до устойчивого намерения, а текущая рабочая база доказана
- **THEN** recovery сохраняет её без перезаписи каталога

### Requirement: Recovery distinguishes interrupted, committed and unresolvable attempts

Recovery SHALL опираться на защищённый журнал и повторно проверяемые доказательства. Отсутствие сообщения о результате SHALL не отменять доказанный commit. Неопределённость или потеря доказательств SHALL сохранять барьер и историческое подтверждение.

#### Scenario: An unseen start or uncertain hot effect restores the previous baseline
- **WHEN** новый процесс не был доказан либо горячая операция могла изменить runtime без устойчивой фиксации
- **THEN** recovery использует прежнюю закреплённую генерацию и полную проверку процесса

#### Scenario: A visible commit marker still requires durable storage and live proof
- **WHEN** после ошибки синхронизации виден допустимый commit
- **THEN** recovery подтверждает устойчивость записи и доказывает диск и runtime до разрешения результата

#### Scenario: An unreported durable completion is rechecked without rollback
- **WHEN** существующая завершённая генерация подтверждена, но отчёт потерян
- **THEN** recovery сохраняет её и тот же идентификатор результата

#### Scenario: Recovery can be interrupted and resumed
- **WHEN** процесс прерывается во время восстановления
- **THEN** следующая попытка сохраняет право только на тот же проверяемый исход
- **AND** завершённый устойчивый результат не вызывает ещё одной замены каталога

#### Scenario: Terminal failure or conflicting evidence keeps the gate closed
- **WHEN** восстановление имеет устойчивый отказ либо копия, каталог, процесс или журнал противоречат друг другу
- **THEN** штатный старт и новая автоматическая попытка блокируются
- **AND** последнее подтверждение остаётся историей без утверждения о текущем runtime
