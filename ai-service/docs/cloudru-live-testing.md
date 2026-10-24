# Запуск и быстрая проверка

Актуализировано 23 октября 2026. Все команды выполняются из корня `ai-service`.
Настройте ключ по [cloudru-setup.md](cloudru-setup.md). Выберите режим запуска
и используйте тот же `--runtime` в тестах.

## Хост

Нужны Python 3.12 с venv и Python 3 для запуска оболочки:

```bash
bash scripts/run_host_service.sh
```

Launcher создаёт/использует `.venv`, устанавливает закреплённые зависимости,
выполняет `pip check`, проверяет хеши весов и скачивает закреплённые файлы при
необходимости. Затем запускает Uvicorn и ждёт `/health`. Адрес по умолчанию —
`http://127.0.0.1:8090`; `PORT` или `--port` меняет его.

Повторный запуск подготовленного окружения:

```bash
bash scripts/run_host_service.sh --no-install --no-download
```

`--no-download` проверяет уже имеющиеся файлы. `--venv PATH` выбирает окружение,
`--python PATH` — Python 3.12 для его создания.

## Docker

Нужны Docker Engine и Python 3.10+ на хосте. SDK и ML-библиотеки на хосте не требуются:

```bash
bash scripts/run_docker_service.sh
```

Образ по умолчанию — `gorkycode-ai-service:cloudru`, контейнер — `gorkycode-ai-service`.
Внешний адрес — `http://127.0.0.1:8091`, внутри контейнера — порт 8090.
Веса находятся в образе по `/opt/embedding-model`; при старте загрузки нет.

Повторный запуск уже собранного образа:

```bash
bash scripts/run_docker_service.sh --no-build
```

`--name NAME` и `--image IMAGE` выбирают имена, `--port 8092` — внешний порт.
Существующий одноимённый контейнер не заменяется. После изменения кода или
скриптов образ нужно пересобрать.

## Общие параметры и остановка

| Параметр | Назначение |
|---|---|
| `--env-file PATH` | Настройки вместо `.env`; окружение терминала имеет приоритет |
| `--startup-timeout 120` | Ожидание `/health` после установки/сборки |
| `--disable-embeddings` | Отключить E5; `/api/job` использует Cloud.ru |
| `--allow-no-key` | Разрешить запуск без ключа для проверки E5; `/api/job` вернёт 503 |
| `--index-url URL` | Индекс остальных Python-зависимостей, по умолчанию GitVerse |
| `--torch-index-url URL` | CPU Torch, по умолчанию официальный индекс PyTorch |

`torch==2.10.0+cpu` устанавливается отдельно из CPU-индекса, затем остальные
зависимости — из GitVerse с constraints. Docker-образ собирается полным даже
при `--disable-embeddings`.

Дождитесь `READY`. Это проверка жизни HTTP-процесса: доступность E5 и Cloud.ru
подтверждают соответствующие тесты. Swagger — `/docs`, ReDoc — `/redoc`.
Ctrl+C в терминале launcher останавливает созданный процесс/контейнер.
Docker-контейнер запускается с `--rm`; образ остаётся. Остановка из другого терминала:

```bash
docker stop gorkycode-ai-service
```

Техническое состояние запуска хранится в `.runtime/` и удаляется launcher при
штатной остановке. `.env`, `.runtime`, `.venv` и локальные веса исключены из
Docker context. Ключи не передаются значениями в аргументах Docker CLI.

## Один реальный Cloud.ru-сценарий

Во втором терминале после `READY`:

```bash
python3 scripts/test_cloudru_live.py --runtime host
# Для Docker:
python3 scripts/test_cloudru_live.py --runtime docker
```

Проверяются настройки runtime, `/health`, `/openapi.json`, затем один `create_task`
через настоящий `/api/job`. `--container NAME` выбирает контейнер,
`--base-url http://HOST:PORT` задаёт адрес без суффикса `/api`.

Хостовый launcher позволяет тесту прочитать настройки процесса через Linux
`/proc/PID/environ` с проверкой launch ID. Без `.runtime/host.json` используются
`.venv` и `.env`; можно задать `--host-python PATH` и `--env-file PATH`.
Такой fallback не подтверждает совпадение настроек с ручным запуском сервера.
Для Docker настройки читаются через `docker exec`. Ключи в отчёт не включаются.

## Выбор сценариев и прямой probe

Список без сетевых запросов:

```bash
python3 scripts/test_cloudru_live.py --list
```

Расширенный быстрый набор: 18 сценариев, до 25 `/api/job` при прохождении всех шагов:

```bash
python3 scripts/test_cloudru_live.py --runtime host --suite full --limit 99
```

`--case clarification --case topic_switch` выбирает сценарии по ID.
С `--case` ограничение `--limit` не применяется. Без явного выбора лимит — один сценарий.

Прямой вызов Cloud.ru из выбранного runtime:

```bash
python3 scripts/test_cloudru_live.py --runtime docker --mode direct
```

`--mode api` — только сервис, `direct` — провайдер, `all` — probe и API.
Probe вызывает настоящий `CloudRuGateway` с просьбой «Ответь одним словом: готово.»
и проверяет завершённую непустую completion. `--direct-timeout 65` и
`--max-tokens 2500` относятся только к probe. При его провале в режиме `all`
API-сценарии не отправляются.

## Лимиты и отчёты

- Генерации расходуют квоту. `--interval 65` задаёт паузу между завершёнными
  платными шагами; `--interval 0` убирает её. Пауза не гарантирует отсутствие 429.
- `--max-requests` ограничивает API POST, но не внутренние вызовы сервиса.
- `--timeout 180` должен превышать действующий `JOB_TIMEOUT_SECONDS`.
- `--keep-going` продолжает независимые смысловые/структурные проверки после
  допустимых ответов 200/400. Транспортная ошибка или HTTP 5xx останавливает набор.
- По умолчанию отчёт создаётся в `reports/cloudru-live-host.json` либо
  `reports/cloudru-live-docker.json`; другой путь — `--report PATH`.
- JSON сохраняется в процессе работы и содержит запросы, ответы и результаты,
  без API-ключей. Прерванный вызов мог уже быть обработан провайдером.
- Коды выхода: 0 — успех, 1 — провал, 2 — параметры, 130 — Ctrl+C.
  Тест не останавливает исходный сервис.

`/api/embed` проверяется [полным функциональным runner](service-live-testing.md).
Карта файлов тестов и история прогонов находятся в том же гайде.
Наличие теста не означает его успех.
