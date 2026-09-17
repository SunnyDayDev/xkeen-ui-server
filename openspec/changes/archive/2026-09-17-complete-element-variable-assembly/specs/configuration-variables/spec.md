## ADDED Requirements

### Requirement: Complete variable assembly for one element

Сервис SHALL предоставлять единую операцию разрешения переменных одного элемента, принимающую ID для диагностики, сырой JSON шаблона, список определений и персональные значения. Операция SHALL применять все требования этой capability к именам, определениям, заполненности, дефолтам, типам, JSON, запрету `null`, синтаксису ссылок и подстановке. Результат SHALL быть новым JSON одного элемента либо отсутствовать при наличии хотя бы одной ошибки. Определения SHALL NOT автоматически добавляться при сборке; ID SHALL использоваться как диагностический контекст, без создания или проверки идентичности элементов этой операцией.

Успех SHALL означать разрешение переменных в JSON одного элемента; он SHALL NOT означать проверку схемы Xray, межэлементных зависимостей, проекцию служебной метки или готовность каталога конфигурации к публикации.

#### Scenario: Definitions defaults and template are assembled together

- **WHEN** шаблон равен `{"address":"${server.address}","port":"${port}","literal":"$${port}"}`, определены `server.address` типа string без дефолта и `port` типа number с дефолтом `443`, а персональное значение `server.address` равно `"edge.example.com"`
- **THEN** результат равен `{"address":"edge.example.com","port":443,"literal":"${port}"}` без ошибок

#### Scenario: Every definition and unselected default is checked

- **WHEN** шаблон не использует определённую переменную `host` без заполненного значения или дефолта
- **THEN** сборка возвращает `missing_value` для `host` без результата
- **AND** в отдельном вызове number-переменная `port` с дефолтом `"443"` и персональным значением `8443` даёт `type_mismatch` с источником `default`, даже если ссылка не используется

#### Scenario: Invalid definitions and extra personal values block assembly

- **WHEN** при корректном шаблоне определения содержат неверное имя, неизвестный тип или повтор имени либо персональный набор содержит необъявленное имя
- **THEN** единая операция возвращает соответственно `invalid_variable_name`, `invalid_variable_type`, `duplicate_variable` либо `unknown_variable` с тем же источником и местом, что отдельная подготовка значений
- **AND** результат отсутствует

#### Scenario: Assembly never declares an unknown reference

- **WHEN** шаблон равен `{"address":"${host}"}`, определения и персональный набор пусты
- **THEN** возвращается `unknown_variable`, источник `template`, имя `host` и путь `/address`
- **AND** определения остаются пустыми, результат отсутствует

#### Scenario: Supplied invalid JSON and null cannot bypass preparation

- **WHEN** персональное значение или присутствующий дефолт содержит невалидный JSON, повторный ключ или `null` на любой глубине
- **THEN** единая операция отклоняет его по правилам подготовки с `invalid_json`, `duplicate_json_key` либо `null_not_allowed` и источником `value` или `default`
- **AND** это распространяется на неиспользуемые значения и невыбранные дефолты без возврата частичного результата

#### Scenario: All supported roots and exact numbers remain supported

- **WHEN** корневой шаблон `"${value}"` получает через определения и персональный набор поочерёдно string `"443"`, number `9007199254740993`, number `0.12345678901234567890123456789`, boolean `false`, object `{}` и array `[]`
- **THEN** каждый результат имеет соответствующий JSON-тип и точное значение
- **AND** постоянный шаблон `{"extension":[0,false,"",{},[],1e400]}` с пустыми определениями и значениями успешно сохраняет структуру, неизвестные поля и точные числа

### Requirement: Prepared substitution validates original template syntax

Операция подстановки подготовленных значений SHALL применять `Literal escaping and unsupported interpolation` ко всем исходным строковым значениям шаблона, включая корень и вложенные массивы/объекты. Разбор SHALL выполняться после JSON-декодирования. Только исходная строка, целиком являющаяся одной корректной неэкранированной ссылкой, SHALL заменяться значением; проверка корректности имени SHALL предшествовать поиску имени в наборе. Полная сборка SHALL обеспечивать то же поведение.

При наличии корректной ссылки вместе с другим содержимым SHALL возвращаться `unsupported_interpolation`, независимо от наличия значения ссылки. При некорректной ссылке SHALL возвращаться `invalid_reference`. Если строка содержит обе причины, конкретная возвращаемая причина и полнота их накопления не фиксируются. Ключи SHALL оставаться буквальными. Операция подстановки SHALL NOT выбирать дефолты или проверять определения вместо отдельной подготовки.

#### Scenario: Escapes and isolated dollars are resolved once

- **WHEN** шаблон равен `["$${host}","https://$${host}/path","$$$$","cost $5","$","$${host"]` и подготовленный набор пуст
- **THEN** результат равен `["${host}","https://${host}/path","$$","cost $5","$","${host"]`
- **AND** экранированное начало незакрытой буквальной строки не даёт `invalid_reference`

#### Scenario: Odd dollar run leaves a real embedded reference

- **WHEN** исходная строка равна `"$$${host}"`
- **THEN** возвращается `unsupported_interpolation`: после экранированного доллара остаётся настоящая ссылка с дополнительным содержимым
- **AND** строка `"$$$${host}"` в отдельном вызове даёт буквальную строку `"$${host}"` без поиска переменной

#### Scenario: Partial references are rejected before lookup

- **WHEN** строка равна `"https://${host}/path"`, `" ${host} "`, `"${host}${port}"` или `"$${literal} ${host}"`
- **THEN** возвращается `unsupported_interpolation` как с подготовленными значениями для имён, так и без них
- **AND** результат отсутствует

#### Scenario: Malformed references are not unknown variables

- **WHEN** строка равна `"${}"`, `"${host"`, `"${server address}"`, `"${a{b}"` или `"${a$b}"`
- **THEN** возвращается `invalid_reference`, даже если низкоуровневый подготовленный набор содержит ключ с текстом ошибочного имени
- **AND** тот же запрет действует на некорректную ссылку внутри более длинной строки

#### Scenario: JSON escapes are decoded before template syntax

- **WHEN** исходный JSON равен `"\u0024{host}"` и подготовленное значение `host` равно `"edge.example.com"`
- **THEN** результат равен `"edge.example.com"`
- **AND** JSON `"\u0024\u0024{host}"` в отдельном вызове даёт буквальное `"${host}"`
- **AND** имя с JSON-экранированным пробелом, например `"${server\u0020address}"`, даёт `invalid_reference`

#### Scenario: Backslash is not a template escape

- **WHEN** исходный JSON равен `"\\${host}"`
- **THEN** декодированная обратная косая черта является дополнительным содержимым и возвращается `unsupported_interpolation`
- **AND** невалидное JSON-экранирование, например `"\${host}"`, даёт `invalid_json` до разбора ссылок

#### Scenario: Object keys are never processed as template strings

- **WHEN** шаблон равен `{"${key}":"$${host}","$${key}":"fixed","${broken":"fixed"}` и набор пуст
- **THEN** результат равен `{"${key}":"${host}","$${key}":"fixed","${broken":"fixed"}`
- **AND** ключи не объявляют и не требуют переменных и не вызывают ошибку ссылки

### Requirement: Prepared substitution rejects null in the raw template

Операция подстановки SHALL отклонять `null` в исходном шаблоне на любой глубине с `null_not_allowed`, источником `template` и путём к запрещённому значению, даже при отсутствии ссылок. Она SHALL сохранять строки `"null"` и иные разрешённые постоянные значения. Полная сборка SHALL обеспечивать тот же запрет наряду с проверками данных переменных из B01. Эти требования SHALL NOT ужесточать сохранение черновиков.

#### Scenario: Root and nested template null are rejected

- **WHEN** шаблон поочерёдно равен `null`, `{"optional":null}` и `{"a/b~c":[null]}` при пустом наборе
- **THEN** каждый вызов возвращает `null_not_allowed`, источник `template` и путь соответственно `""`, `/optional` и `/a~1b~0c/0`
- **AND** результат отсутствует

#### Scenario: Null text remains data

- **WHEN** шаблон равен `{"optional":"null","text":"","constant":false}` при пустом наборе
- **THEN** подстановка успешно сохраняет эти значения

#### Scenario: A saved draft can still fail assembly

- **WHEN** структурно корректный элемент содержит `null`, частичную ссылку или незаполненное определение
- **THEN** подготовка сохранения и чтение продолжают допускать его по контракту хранения
- **AND** последующая полная сборка отклоняет соответствующий случай с `null_not_allowed`, `unsupported_interpolation` или `missing_value`, не изменяя сохранённый черновик

### Requirement: Assembly preserves the boundary between template and data

Единая сборка и подстановка подготовленного набора SHALL обрабатывать синтаксис только исходных строковых значений шаблона. Полученные после `$$` строки и подставленные данные SHALL NOT снова проходить разбор ссылок или экранирования. Полная сборка SHALL при этом сохранять проверку `null` данных переменных и дефолтов. Неизвестные поля, порядок массивов и точные числа SHALL сохраняться по существующему контракту.

#### Scenario: Supplied strings and nested structures are never templates

- **WHEN** шаблон равен `{"text":"${text}","options":"${options}"}`, объявлены string `text` и object `options`, их персональные значения равны `"${other}"` и `{"items":["$$","${broken","https://${other}/path"]}`
- **THEN** результат равен `{"text":"${other}","options":{"items":["$$","${broken","https://${other}/path"]}}`
- **AND** определение `other` не требуется

#### Scenario: A chosen default remains literal data

- **WHEN** string-переменная `text` имеет дефолт `"$${other}"`, персональное значение отсутствует и шаблон равен `"${text}"`
- **THEN** результат равен `"$${other}"` без обработки долларов или поиска `other`

### Requirement: Complete assembly returns safe independent results and diagnostics

Единая операция SHALL сохранять входной шаблон, определения, дефолты и персональные значения при успехе и ошибке. Изменение успешного результата SHALL NOT менять входы. Ошибка SHALL содержать ID элемента, код и источник; JSON Pointer SHALL указываться для известного структурного места, а для синтаксической ошибки JSON — байтовая позиция от нуля. Правила локализации подготовки SHALL сохраняться: путь данных отсчитывается от корня конкретного значения или дефолта, путь определения — от списка определений.

Ошибки синтаксиса шаблона SHALL иметь источник `template`. Для `unknown_variable` с источником `template` имя SHALL быть точным именем корректной ссылки; для `unsupported_interpolation` имя SHALL указываться при единственной корректной ссылке и отсутствии других ошибок ссылки в этой строке. Для `invalid_reference` и строк с несколькими ссылками имя SHALL оставаться неуказанным. Ошибки SHALL NOT содержать сырой JSON, значения, дефолты или фрагменты некорректных ссылок. Ожидаемый тип SHALL указываться только для `type_mismatch`. Порядок независимых ошибок и полнота накопления не фиксируются.

#### Scenario: Nested syntax errors have safe locations

- **WHEN** элемент `element-demo` содержит `{"a/b~c":["https://${host}/path"]}`
- **THEN** ошибка имеет код `unsupported_interpolation`, источник `template`, ID `element-demo`, имя `host` и путь `/a~1b~0c/0`
- **AND** шаблон `{"a/b~c":["${}"]}` в отдельном вызове даёт `invalid_reference` по тому же пути без имени переменной

#### Scenario: Multiple references do not get an arbitrary diagnostic name

- **WHEN** строка равна `"${host}${port}"`
- **THEN** возвращается `unsupported_interpolation` с путём к строке и без имени переменной

#### Scenario: JSON failures stay distinct from reference failures

- **WHEN** шаблон содержит незавершённый JSON, неверный UTF-8, два корневых значения или повторные декодированные ключи объекта
- **THEN** сборка возвращает `invalid_json` с позицией либо `duplicate_json_key` с путём и источником `template`
- **AND** результат отсутствует

#### Scenario: Failure and success preserve input ownership

- **WHEN** сборка получает шаблон с известной ссылкой и отдельным ошибочным значением шаблона
- **THEN** возвращаются ошибки без частичного результата, все входы остаются прежними, диагностика не содержит подставленных данных
- **AND** в отдельном успешном вызове изменение буфера результата не меняет шаблон, определения, дефолты или персональные значения
