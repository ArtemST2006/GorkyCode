# Development

Инструкция для нового разработчика. Термины — [Glossary](../product/glossary.md), архитектура — [обзор](../architecture/architecture-overview.md).

## 1. Требования к окружению

- Go 1.26.7+ и компилятор C (clang/gcc) с поддержкой CGO — backend и sherpa-onnx.
- goose — миграции; golangci-lint — линтер.
- Docker Engine и Compose v2 — PostgreSQL, Redis, Mailpit, полный запуск.
- Python 3.12 — ai-service на хосте (в Docker достаточно 3.10+).

## 2. Запуск

### Полный стек через Docker

```
cp .env.example .env
cp backend/.env.example backend/.env
cp ai-service/.env.example ai-service/.env   # указать CLOUDRU_API_KEY
docker-compose up -d --build
make -C backend migrate
```

### Backend без Docker

```
docker-compose up -d postgres redis mailpit
cd backend
cp .env.example .env
make download-model    # ASR-модель в model/asr-model
make migrate
make run               # экспортирует .env, базовый конфиг config/local.yaml
```

AI-сервис должен быть доступен на http://localhost:8090: либо docker-compose up -d ai-service nginx, либо bash scripts/run_host_service.sh из каталога ai-service (Python 3.12). Дождитесь READY.

## 3. Тесты

Backend:

```
make -C backend test        # unit + race + покрытие (coverage.html)
make -C backend test-asr    # распознавание на тестовых WAV
make -C backend lint        # golangci-lint, go mod verify, fmt
```

Интеграционные тесты backend (файлы *_integration_test.go) требуют живых PostgreSQL и Redis.

AI-service, без сети:

```
cd ai-service
.venv/bin/python -m pytest -q -p no:cacheprovider tests/unit tests/test_embeddings_encoder.py
```

Live-проверки с реальным Cloud.ru — отдельный runner (scripts/test_service_live.py), расходует квоту; обычный pytest их не запускает. Подробности — ai-service/docs/service-live-testing.md.

## 4. Миграции

```
make -C backend migrate-create NAME=имя   # создать
make -C backend migrate                   # применить
make -C backend migrate-status            # статус
make -C backend migrate-down              # откатить последнюю
```

Каждая миграция обязана содержать Up и Down.

## 5. Контракты и генерация кода

1. Изменение API начинается с YAML: contracts/backend.openapi.yaml или contracts/ai.openapi.yaml.
2. make -C backend generate регенерирует internal/generated/api.gen.go (сервер) и internal/generated/ai/client.gen.go (клиент AI). Эти файлы не правятся руками.
3. Копию AI-контракта ai-service/app/resources/ai.openapi.yaml обновить синхронно: тест test_contract_sync.py проверяет побайтовое равенство.
4. contracts/on-update.proposal.openapi.yaml — архив, не редактируется.

## 6. Переменные окружения

Основные переменные backend/.env:

- HTTP_ADDR = :8080 — адрес HTTP.
- DATABASE_URL = postgres://gorkycode:…@localhost:5432/gorkycode — база.
- REDIS_URL = redis://localhost:6380/0.
- AI_SERVICE_URL = http://localhost:8090.
- AUTH_SESSION_TTL = 24h — срок сессии.
- MODEL_PATH = model/asr-model — каталог ASR-модели.
- REMINDER_OFFSETS = 72h,24h,3h,10m — опережения уведомлений.
- REMINDER_MIN_ADVANCE_LEAD = 5h — минимальный горизонт опережения.
- REMINDER_RETENTION = 720h — хранение неподтверждённых уведомлений.
- SMTP_HOST и EMAIL_FROM — пустые по умолчанию; без них email выключен.
- GOOGLE_CLIENT_ID и GOOGLE_CLIENT_SECRET — пустые по умолчанию; без них календарь выключен.
- GOOGLE_EVENT_DURATION = 1h — длительность для проверки конфликтов.

Основные переменные ai-service/.env:

- CLOUDRU_API_KEY — ключ Foundation Models, обязателен.
- CLOUDRU_MODEL — модель GigaChat; значение см. в .env.example.
- CLOUDRU_TIMEOUT_SECONDS = 60 — таймаут вызова.
- JOB_TIMEOUT_SECONDS = 150 — общий срок /api/job.
- CLOUDRU_MAX_RETRIES = 0 (в .env.example — 1) — транспортные повторы, 0–3.
- EMBEDDING_MODEL_PATH = models/multilingual-e5-small — веса E5 (в Docker /opt/embedding-model).
- EMBEDDING_NUM_THREADS = 2, EMBEDDING_MAX_WORKERS = 1 — CPU-пул.

## 7. Структура репозитория

```
backend/
  cmd/backend/                  точка входа
  cmd/{asrbench,sttbench,...}/  диагностические утилиты
  internal/http-server/         хендлеры и формат ошибок
  internal/service/             бизнес-логика
  internal/service/subservice/  create/list/search/update/delete записей
  internal/storage/postgres/    репозитории PostgreSQL
  internal/storage/redis/       репозитории Redis
  internal/reminder/            планировщик напоминаний
  internal/notifications/       WebSocket-хаб, email-воркер
  internal/voice/               sherpa-onnx STT
  internal/generated/           oapi-codegen, не править руками
  migrations/                   goose SQL
  config/local.yaml             базовый конфиг
ai-service/
  app/api/routes/               /api/job, /api/respond, /api/embed, /health
  app/llm/                      шлюз Cloud.ru
  app/services/                 контекст диалога, jobs, embeddings
  app/embeddings/               E5 encoder и манифест
  app/resources/                копия AI-контракта, обязательна для старта
  tests/                        unit и live-тесты
  scripts/                      launcher-скрипты
contracts/                      OpenAPI, источник истины
frontend/public/                собранная Nuxt-статика (исходники вне репо)
nginx/default.conf              маршрутизация, CORS, WS-прокси
docs/                           документация
```

## 8. Правила разработки

- Conventional Commits; PR в main с ревьюером.
- Юнит-тесты пишутся параллельно с кодом: мок репозитория на уровне сервисов, мок сервисов на уровне хендлеров (mockery или gomock).
- Периодически запускать make lint и make test.
- CI отсутствует, проверки запускаются локально
