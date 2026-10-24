# Полный функциональный набор

Актуализировано 23 октября 2026. В `tests/live/` находятся 153 unittest-метода
плюс HTTP/диалоговые subtests. Используются настоящие Uvicorn/FastAPI, E5 и
Cloud.ru. Gateway, SDK и encoder не подменяются. Входы и ожидания хранятся в
fixtures; reference-ответы не отправляются модели и не возвращаются вместо неё.

## Подготовка и запуск

Запустите сервис в отдельном терминале по [гайду запуска](cloudru-live-testing.md).
Runner использует стандартную библиотеку Python 3.10+; pytest для него не нужен.
Все команды выполняются из корня `ai-service`.

```bash
python3 scripts/test_service_live.py --runtime host
# Либо для запущенного Docker-контейнера:
python3 scripts/test_service_live.py --runtime docker
```

По умолчанию выбраны все группы, `--llm-suite full`, пауза `--interval 65` и
лимит `--max-cloud-requests 100`. Прогон включает платные генерации и может
занять более полутора часов. Число внутренних исправляющих/транспортных вызовов
Cloud.ru может быть больше числа попыток runner.

Для Docker после изменения кода или scripts пересоберите образ.
`--container NAME` выбирает основной контейнер, `--image IMAGE` — образ для
временных fault-контейнеров. Значение образа по умолчанию:
`gorkycode-ai-service:cloudru`. Для ручного запуска поддерживаются `--base-url`,
`--host-python` и `--env-file`; правила обнаружения runtime описаны в гайде запуска.

## Выбор групп

| Группа | Методов | Содержание |
|---|---:|---|
| `contract` | 5 | Health, docs, OpenAPI, зеркала, 404/405, Problem |
| `validation` | 41 | Некорректные `/api/job`: JSON/UTF-8, UUID, даты, timezone, context/draft |
| `embeddings` | 27 | Настоящая E5, векторы, HTTP-границы, нагрузка, disconnect, восстановление |
| `faults` | 10 | Реальные процессы с изменёнными настройками: ключ, модель, веса, таймауты, логи |
| `jobs` | 70 | Диалоги, календарь, операции, поиск, извлечение и параллельные запросы Cloud.ru |

Список без сети и запуска процессов:

```bash
python3 scripts/test_service_live.py --list
```

73 проверки без Cloud.ru-генерации:

```bash
python3 scripts/test_service_live.py --runtime host --sections contract,validation,embeddings
```

Их можно выполнить на embeddings-only запуске:

```bash
bash scripts/run_host_service.sh --allow-no-key
```

Только 70 LLM-тестов:

```bash
python3 scripts/test_service_live.py --runtime host --sections jobs
```

`--llm-suite smoke` оставляет в jobs один `create_task`, `core` — диалоги без
42 extraction-случаев, `full` добавляет их. За пределами группы jobs выбранные
группы продолжают выполняться; минимальный LLM-прогон требует `--sections jobs`.

Выбор конкретного теста, без повторения проваленных запросов:

```bash
python3 scripts/test_service_live.py --runtime host --test '*test_dialogue_clarification'
```

`--test` можно повторять. `--no-faults` пропускает отдельные fault-процессы;
`--failfast` останавливает unittest-набор после первой ошибки.

## Fault-профили

Группа запускает отдельные настоящие экземпляры со случайными портами.
Изменяются настройки этих экземпляров, а не `.env` и конфигурация основного сервиса.
Ключ берётся из выбранного runtime и передаётся через окружение процесса.
В Docker создаются временные контейнеры `ai-live-test-*`; после проверки удаляются
созданные тестом процессы/контейнеры. Некоторые fault-тесты обращаются к Cloud.ru.

Для наблюдения `model_invalid_output` ограничивается бюджет настоящей модели.
Если она вернула валидный ответ, результат — `skipped`, а не успешная проверка 500.
Квота/429, произвольные upstream 5xx и неожиданные внутренние исключения специально
не форсируются и обозначены в матрице как `not_forced`.

## Как читать результат

HTTP 200 с неверным intent, фильтром или лишним уточнением считается провалом.
Языковые проверки используют ограниченную рубрику: спорные формулировки нужно
разбирать по фактическому ответу. После технического сбоя обычные Cloud.ru-запросы
пропускаются; fault-профили с ожидаемыми техническими ошибками учитываются отдельно.

`--max-cloud-requests` ограничивает попытки runner, включая fault-вызовы,
а не внутренние исправляющие/транспортные вызовы SDK. `--interval` меняет паузу.
`--timeout` должен превышать общий deadline работающего сервиса.

Отчёт по умолчанию создаётся при новом запуске в
`reports/service-live-host.json` или `reports/service-live-docker.json`.
`--report PATH` меняет путь. Предыдущие отчёты могут отсутствовать; один Markdown
с описанием прошлых прогонов не заменяет исходный JSON.

В отчёте есть фактические ответы, ошибки, длительности, пропуски, HTTP-статусы
и матрица требований из [coverage.json](../tests/live/coverage.json).
Запись синхронизирована для параллельных проверок.

- `passed` — выбранный unittest-набор завершился без failures/errors; skips возможны.
- `all_selected_verified` — дополнительно отсутствуют пропуски.
- Матрица различает `verified`, `failed`, `partial`, `not_run`, `not_selected`, `not_forced`.
- Коды выхода: 0 — без ошибок, 1 — провал/ошибка, 2 — параметры/пустой выбор, 130 — Ctrl+C.

Покрытие строк/веток Python не измеряется. Тесты не проверяют SQL, векторный
индекс и конечное качество выдачи backend. Исходный сервис runner не останавливает.

## Статические тесты

Launcher на хосте устанавливает runtime-зависимости. Для pytest в подготовленном
хостовом окружении дополнительно установите dev-зависимости:

```bash
.venv/bin/python -m pip install --index-url https://pypi-mirror.gitverse.ru/simple/ -c requirements.lock -e '.[dev]'
.venv/bin/python -m pytest -q -p no:cacheprovider tests/unit tests/test_embeddings_encoder.py
```

Это 67 статических проверок. Обычный pytest без выбора путей также собирает
153 live-теста, но пропускает их: HTTP-контекст создаёт только отдельный runner.
Статические тесты схем/манифестов не являются измерением качества модели.
Карта файлов и результаты приведены ниже.

## Карта тестов

Собираются 220 проверок: 67 статических и 153 live-метода.
Дополнительные subtests не включены в эти числа.

### Тестовые файлы

| Файл | Число | Назначение и зависимости |
|---|---:|---|
| [test_embeddings_encoder.py](../tests/test_embeddings_encoder.py) | 26 | Подготовка текста, манифест, хеши файлов, версия пространства, проверка вектора. Веса не загружаются; сеть запрещена fixture |
| [unit/test_config.py](../tests/unit/test_config.py) | 22 | Значения по умолчанию, env-приоритеты, aliases, ограничения и скрытие ключей |
| [unit/test_contract_sync.py](../tests/unit/test_contract_sync.py) | 2 | Зеркала OpenAPI относительно канона; JSON-примеры embedding/Problem |
| [unit/test_ready_message.py](../tests/unit/test_ready_message.py) | 6 | Непустой ready.message, null-поля unsupported, совместимость message с контрактом ответа коллекции |
| [unit/test_schemas.py](../tests/unit/test_schemas.py) | 11 | Запросы, ответы, context/draft, UUID, даты, время и согласованность status/intent/action |
| [live/test_contract.py](../tests/live/test_contract.py) | 5 | Реальные health/docs/OpenAPI/404/405; запущенный сервис |
| [live/test_validation.py](../tests/live/test_validation.py) | 41 | Реальные HTTP-отказы для невалидного job; Cloud.ru не вызывается |
| [live/test_embeddings.py](../tests/live/test_embeddings.py) | 27 | Настоящая E5 через HTTP: векторы, NFC, trim, truncation, 16 KiB, chunked, timeout, CPU/busy, disconnect и восстановление |
| [live/test_faults.py](../tests/live/test_faults.py) | 10 | Отдельные процессы/контейнеры с отсутствующим/неверным ключом, моделью, весами, отключением E5, таймаутами и проверкой логов; часть вызовов идёт в Cloud.ru |
| [live/test_jobs.py](../tests/live/test_jobs.py) | 70 | 27 диалоговых сценариев, параллельный сценарий и 42 extraction-случая через настоящий Cloud.ru |

Один статический тест `test_collection_message_does_not_depend_on_database_results`
в `test_ready_message.py` читает `../contracts/backend.openapi.yaml`. Он проверяет
схему на примерах с пустыми/непустыми items и не запускает backend/БД.
Это оставшаяся зависимость от соседнего контракта; исходники backend тестам не нужны.

### Вспомогательные файлы и данные

| Файл | Роль |
|---|---|
| [conftest.py](../tests/conftest.py) | Контракт, OAS30-валидатор и фабрика данных для схем. Подмен gateway/encoder нет |
| [live/live_support.py](../tests/live/live_support.py) | HTTP-клиент, Session, валидатор используемых OAS-ключей, учёт Cloud.ru-попыток, временные fault-процессы и параллельный запуск |
| [live/coverage.json](../tests/live/coverage.json) | Требования и шаблоны ID тестов для матрицы; не измеряет покрытие строк |
| [fixtures/search_extraction_cases.json](../tests/fixtures/search_extraction_cases.json) | 42 входа и ожидания для `ExtractionTests`. Поле reference не подставляется в ответ сервиса и не отправляется модели |
| [fixtures/embed-wire.json](../tests/fixtures/embed-wire.json) | Синтетические one-hot JSON-примеры для статической проверки формата, размерности и нормы |
| [fixtures/semantic-search/dataset.json](../tests/fixtures/semantic-search/dataset.json) | Корпус 60 документов/40 запросов и ожидания интеграции. Текущий runner его не использует; автоматической оценки ранжирования сейчас нет |
| [manual_cases.json](../tests/manual_cases.json) | Старые ручные сценарии, автоматически не запускаются. Частично дублируют live-набор; точные ожидания query нужно сверять с текущим prompt |

`FakeGateway`, `FakeEncoder`, тесты с подменой SDK и сломанные тесты удалённых
скриптов убраны. Статические примеры JSON остаются данными для проверки схем,
а не бесплатной заменой настоящей модели в live-проверках.


## Проверенные запуски на 23 октября 2026

Это сводка наблюдавшихся запусков. Обновление документации не является новым
прогоном Cloud.ru. Полный набор нельзя считать успешным: остаются провалы
извлечения и интерпретации запросов.

### После очистки проекта

| Проверка | Runtime | Результат |
|---|---|---|
| Ruff для app, scripts, tests, server.py | Хост | Успешно |
| Статические тесты | Хост | 67 passed, 54 subtests passed |
| pytest без выбора путей | Хост | Дополнительно 153 live-теста skipped; Session создаёт отдельный runner |
| Live contract | Хост, настоящий HTTP | 5 passed |
| Live validation | Хост, настоящий HTTP | 41 passed |
| Live embeddings | Хост, настоящая CPU E5 | 27 passed |

Всего в отдельном HTTP/E5-прогоне — 73 passed, без ошибок и пропусков;
попыток обращения к Cloud.ru — 0. Он запускался на отдельном порту и был
остановлен после проверки. Полный набор jobs/faults после очистки не повторялся.
Docker после очистки повторно не проверялся.

### Предыдущие реальные прогоны

Эти результаты относятся к запуску до удаления устаревших тестов и материалов:

| Группа | Хост | Docker |
|---|---|---|
| Contract + validation + E5 | 73 прошли | 73 прошли |
| Fault-профили | 9 прошли, 1 пропущен | 9 прошли, 1 пропущен |
| 70 LLM-тестов jobs | 21 прошёл, 49 провалились | Полный jobs-набор не выполнялся |

Использовались реальные процессы FastAPI/Uvicorn, CPU E5 и Cloud.ru
`ai-sage/GigaChat3-10B-A1.8B`. Параметры основного прогона: provider timeout 20 с,
job deadline 45 с, max_tokens 2048, интервал 1 с. Это параметры того запуска,
а не текущие значения по умолчанию и не подтверждение текущего `.env`.

49 провалов не равны 49 независимым дефектам. Повторяются ошибки выбора
status/intent, лишние уточнения и извлечение полей; часть языковых ожиданий
требует ручного разбора ответа. Например, на запрос «Найди проект «Задача-42»»
модель запросила дополнительное уточнение и добавила ограничение task вместо
готового поиска по теме без выдуманного типа записи.

В обычных пользовательских сценариях наблюдались `model_invalid_output`/HTTP 500.
Специальный fault-тест усечения completion был пропущен: реальная модель
вместила валидный ответ в бюджет 128 tokens. Это не успешная принудительная проверка 500.

### Границы доказательств

- Исходные JSON прежних прогонов сейчас отсутствуют в `reports/`; прежние
  агрегаты `*-latest.json` также недоступны. Сводка сохранена как история,
  а не как доступный машинный отчёт. Полную статистику заново из неё не восстановить.
- HTTP/E5-отчёт после очистки сохранялся во временном каталоге `/tmp`.
  В постоянный `reports/` он не переносился. Для нового прогона задавайте свой `--report`.
- Успех технических E5-проверок не измеряет релевантность поиска или Recall/MRR.
  Корпус 60 документов/40 запросов сохранён, действующего evaluator ранжирования нет.
- 429/quota и произвольные upstream 5xx специально не форсировались.
- Покрытие строк и веток Python не измерялось. SQL/pgvector, фильтрация и
  конечная выдача backend в этот набор не входят.

Для нового воспроизводимого результата после запуска сервиса используйте
`--report PATH`, например:

```bash
python3 scripts/test_service_live.py --runtime host --sections contract,validation,embeddings --report reports/http-e5-host.json
python3 scripts/test_service_live.py --runtime host --sections jobs --report reports/jobs-host.json
```

Вторая команда обращается к Cloud.ru. Для Docker замените runtime.
Объединение документации не является новым прогоном этих команд.
