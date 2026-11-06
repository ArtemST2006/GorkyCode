# GorkyCode

Голосовой ассистент для задач, напоминаний и встреч: понимает запросы на естественном языке текстом или голосом, ведёт календарь пользователя и присылает напоминания.

**Статус:** прототип команды «Неймарк» для отбора GorkyCode 2026. Ответ ассистента только текстовый (TTS не реализован), повторяющихся событий нет, CI нет.

## Содержание

- [О проекте](#о-проекте)
- [Стек](#стек)
- [Установка и запуск](#установка-и-запуск)
- [Использование](#использование)
- [Конфигурация](#конфигурация)
- [Архитектура](#архитектура)
- [Разработка](#разработка)
- [Частые проблемы](#частые-проблемы)
- [Что готово и чего нет](#что-готово-и-чего-нет)
- [Безопасность](#безопасность)
- [Как внести вклад](#как-внести-вклад)
- [Команда и поддержка](#команда-и-поддержка)
- [Лицензия](#лицензия)

## О проекте

Чтобы создать задачу или встречу, обычно приходится заполнять форму из полей. GorkyCode позволяет сказать или написать фразу вроде «Назначь встречу с Иваном завтра в 15:00», и ассистент сам разберёт её. Что он умеет:

- **Создаёт** задачи, напоминания и встречи из свободного текста, понимает относительные даты («завтра», «через час», «до конца недели»).
- **Показывает** записи за период и **ищет** старые по названию и по смыслу (pgvector + эмбеддинги E5).
- **Изменяет и удаляет** записи безопасно: сначала показывает пронумерованных кандидатов и ничего не меняет до явного выбора номеров (можно выбрать несколько через запятую).
- **Уточняет**, если данных не хватает, и сохраняет контекст диалога на 24 часа.
- **Проверяет конфликты** при создании встречи: по базе и, при включённой опции, по занятости в Google Calendar.
- **Распознаёт речь** локально, без внешних сервисов: WAV → текст через sherpa-onnx.
- **Синхронизируется с Google Calendar.** Встречи создаются в основном календаре. Изменения и удаления тоже переносятся туда (best effort).
- **Напоминает** заранее (по умолчанию за 72 ч, 24 ч, 3 ч и 10 мин) и в момент события: в чат по WebSocket и на почту, если настроен SMTP.

Ожидаемое поведение на конкретных репликах описано в [`docs/dialogue-scenarios.md`](docs/dialogue-scenarios.md). Продуктовая документация (vision, PRD, user stories) лежит в [`docs/product/`](docs/product/).

## Стек

| Компонент | Технологии |
|---|---|
| Точка входа | Nginx (`nginx:stable-alpine`): раздаёт frontend, проксирует API и WebSocket, внутренний прокси к ai-service |
| Frontend | Nuxt 4 (SPA), Vuetify, TypeScript, SASS. Здесь, в `frontend/public/`, лежит только собранная статика; исходники — в отдельном репозитории [yegorweb/GorkyCodeFrontend](https://github.com/yegorweb/GorkyCodeFrontend) (сборка `nuxt generate`) |
| Backend | Go 1.26 (`go 1.26.7`), chi, pgx, go-redis, oapi-codegen, argon2id, Google Calendar API |
| Распознавание речи | sherpa-onnx на CPU. Модель по умолчанию — GigaAM v3 NeMo CTC (`sherpa-onnx-nemo-ctc-giga-am-v3-russian-2025-12-16`), поддерживается и Zipformer RU (transducer) |
| AI-сервис | Python 3.12, FastAPI, Cloud.ru Foundation Models (GigaChat) через OpenAI-совместимый клиент, локальный эмбеддер `intfloat/multilingual-e5-small` (384 измерения) |
| Данные | PostgreSQL 16 + pgvector (`pgvector/pgvector:0.8.7-pg16-bookworm`), миграции goose; Redis 7 |
| Контракты и запуск | OpenAPI 3 в `contracts/`, Docker Compose |

## Установка и запуск

### Требования

- Docker Engine и Docker Compose v2 версии **2.24+** (нужна поддержка `env_file` с `required`).
- [goose](https://github.com/pressly/goose) на хосте: миграции **не применяются автоматически**. Установить можно так: `go install github.com/pressly/goose/v3/cmd/goose@latest`.
- `make`.
- Ключ Cloud.ru Foundation Models (`CLOUDRU_API_KEY`), обязателен. Как получить: [`ai-service/docs/cloudru-setup.md`](ai-service/docs/cloudru-setup.md).
- Интернет при первой сборке: скачиваются Go-модули, torch, веса E5 и ASR-модель.
- Свободные порты: `8088`, а на `127.0.0.1` — `8080`, `8090`, `8091`, `5432`, `6380`.

Необязательно: Google OAuth Client для календаря, SMTP-сервер для писем.

### Быстрый запуск

Команды выполняются из корня репозитория.

```bash
# 1. Файлы окружения
cp .env.example .env
cp backend/.env.example backend/.env
cp ai-service/.env.example ai-service/.env
# в ai-service/.env укажите CLOUDRU_API_KEY (Key Secret, не Key ID)

# 2. База и миграции
docker compose up -d postgres redis
make -C backend migrate

# 3. Все сервисы
docker compose up -d --build
```

Первая сборка долгая: образ ai-service ставит torch и скачивает веса E5, а backend при первом старте скачивает ASR-модель в volume `asr_model`.

Проверка:

- <http://localhost:8088> — приложение. Пароль при регистрации должен быть не короче 15 символов.
- <http://localhost:8088/health> — backend жив; <http://localhost:8088/ready> — backend готов (503, если PostgreSQL недоступен).
- `docker compose ps` — `ai-service` в состоянии `healthy`.

Остановка: `docker compose down`. Полный сброс вместе с данными и моделью: `docker compose down -v`.

Обновление до новой версии:

```bash
git pull
docker compose build
make -C backend migrate
docker compose up -d
```

## Использование

Основной сценарий — веб-интерфейс на <http://localhost:8088>: регистрация, чат с ассистентом, голосовой ввод, подключение Google Calendar.

То же самое можно сделать через API (все запросы к `/api/task` требуют заголовок `Authorization: Bearer`):

```bash
# регистрация → в ответе user.id и access_token
curl -s http://localhost:8088/api/v1/auth/sign-up \
  -H 'Content-Type: application/json' \
  -d '{"name":"Тест","email":"test@example.com","password":"very-long-password-123","timezone":"Europe/Moscow"}'

# текстовый запрос
curl -s http://localhost:8088/api/task \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"user_id\":\"$USER_ID\",\"text\":\"Назначь встречу с Иваном завтра в 15:00\"}"

# голосовой запрос: WAV (.wav/.wave) до 20 MiB
curl -s http://localhost:8088/api/task \
  -H "Authorization: Bearer $TOKEN" \
  -F user_id=$USER_ID -F file=@request.wav
```

Если ассистенту не хватает данных, он отвечает `status: needs_clarification` и присылает `task_id`. Следующую реплику отправьте с этим `task_id`, чтобы продолжить диалог.

Основные эндпоинты:

| Метод и путь | Назначение |
|---|---|
| `POST /api/v1/auth/sign-up`, `sign-in`, `sign-out` | Регистрация (`name`, `email`, `password` 15–128 символов, `timezone` IANA), вход, выход |
| `POST /api/task` | Текстовая (JSON) или голосовая (multipart) команда |
| `GET /api/v1/tasks` | Список записей без AI-диалога |
| `GET`/`DELETE /api/v1/integrations/google`, `POST .../connect` | Google Calendar |
| `GET /api/v1/notifications/ws` | WebSocket-уведомления ([протокол](docs/postman/websocket-notifications.md)) |
| `GET /health`, `GET /ready` | Проверки состояния |

Полные схемы: [`contracts/`](contracts/) (OpenAPI). Описание семантики: [`docs/technical/api.md`](docs/technical/api.md). Postman-коллекции: [`docs/postman/`](docs/postman/).

## Конфигурация

Три файла окружения, все в `.gitignore`. Полные списки переменных с комментариями лежат в соответствующих `.env.example` и в [`docs/technical/development.md`](docs/technical/development.md).

| Файл | Что задаёт |
|---|---|
| `.env` (корень) | Порты, учётные данные PostgreSQL, **Google OAuth** (`GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URL`, `GOOGLE_FRONTEND_REDIRECT`; `GOOGLE_CHECK_BUSY` в шаблоне нет, его можно дописать) |
| `backend/.env` | Сессии, напоминания (`REMINDER_*`), **SMTP** (`SMTP_*`, `EMAIL_FROM`), `DATABASE_URL` для `make migrate` |
| `ai-service/.env` | `CLOUDRU_API_KEY`, `CLOUDRU_MODEL`, таймауты, параметры E5 |

> **Важно.** `docker-compose.yml` явно задаёт для backend `HTTP_ADDR`, `DATABASE_URL`, `REDIS_URL`, `AI_SERVICE_URL`, `MODEL_PATH`, `MODEL_URL` и все `GOOGLE_*`, кроме `GOOGLE_EVENT_DURATION`. Эти значения перекрывают `backend/.env`. Поэтому:
> - ключи Google указывайте в **корневом** `.env`, иначе получите `503 google_disabled`;
> - при смене порта или учётных данных PostgreSQL в корневом `.env` обновите `DATABASE_URL` в `backend/.env`;
> - ASR-модель в Docker меняется через `MODEL_URL` в `docker-compose.yml`, а не в `backend/.env`. Модель скачивается в volume один раз, поэтому после смены выполните `docker volume rm gorkycode_asr_model`.

Без `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET` интеграция с календарём выключена. Без `SMTP_HOST`/`EMAIL_FROM` письма не отправляются. Остальное работает в обоих случаях.

## Архитектура

![Архитектура GorkyCode](docs/media/architecture-containers.png)

Nginx — единая точка входа. На публичном порту он раздаёт frontend и проксирует API и WebSocket в backend, на внутреннем `8091` соединяет backend с ai-service. Backend (Go) отвечает за API, авторизацию, распознавание речи, планировщик напоминаний и все операции с данными. AI-сервис (FastAPI) разбирает запросы через GigaChat, строит эмбеддинги и формулирует ответы. PostgreSQL хранит записи и векторы, Redis — контекст диалогов, сессии и OAuth-state.

Путь запроса: текст или WAV → распознавание речи (для WAV) → ai-service возвращает структурированное действие → backend проверяет его и работает с БД → ai-service формулирует ответ по фактическому результату.

Ключевое решение: **модель предлагает, backend исполняет.** LLM не обращается к базе. Backend валидирует действие и владельца записи. Если модель дважды ответила не по схеме, ассистент переспрашивает и ничего не меняет.

Подробнее: [обзор архитектуры](docs/architecture/architecture-overview.md), [ADR](docs/architecture/adr.md), остальные диаграммы в [`docs/media/`](docs/media/) и [интерактивная версия](docs/diagrams/site/index.html).

## Разработка

```
backend/            Go: cmd/backend, internal/, migrations/, config/local.yaml, Makefile
ai-service/         FastAPI: app/, tests/, scripts/, docs/
frontend/public/    собранная статика Nuxt (исходники: github.com/yegorweb/GorkyCodeFrontend)
nginx/              default.conf
contracts/          OpenAPI — источник истины для API и генерации кода
docs/               продуктовая, архитектурная и техническая документация
```

**Backend без Docker** (Go 1.26.7+, C-компилятор с CGO):

```bash
docker compose up -d postgres redis
cd backend && cp .env.example .env
make download-model && make migrate && make run
```

Полезные цели (`make help`): `test`, `test-asr`, `lint`, `generate`, `migrate-status`, `migrate-down`, `migrate-create NAME=...`. Интеграционные тесты требуют запущенных PostgreSQL и Redis.

**AI-сервис на хосте** (Python 3.12):

```bash
cd ai-service && cp .env.example .env   # укажите CLOUDRU_API_KEY
bash scripts/run_host_service.sh
.venv/bin/python -m pytest -q -p no:cacheprovider tests/unit tests/test_embeddings_encoder.py
```

Live-тесты расходуют квоту Cloud.ru. Подробности в [`ai-service/README.md`](ai-service/README.md).

**Изменение API:** сначала правится YAML в `contracts/`, затем выполняется `make -C backend generate`. Копию AI-контракта в `ai-service/app/resources/` нужно обновлять синхронно, это проверяет тест.

Полное руководство: [`docs/technical/development.md`](docs/technical/development.md), [`docs/technical/deployment.md`](docs/technical/deployment.md).

## Частые проблемы

- **В логах backend `relation ... does not exist`.** Миграции не применены: выполните `make -C backend migrate`.
- **`goose: command not found`.** Установите goose (см. [Требования](#требования)).
- **`503 google_disabled`, хотя ключи есть в `backend/.env`.** В Docker их нужно указать в корневом `.env`.
- **`model_authentication_failed` / `model_not_configured`.** Проверьте `CLOUDRU_API_KEY` и `CLOUDRU_MODEL` в `ai-service/.env`, затем выполните `docker compose up -d ai-service`.
- **Микрофон не работает при открытии по IP.** Браузер даёт доступ к микрофону только на HTTPS или `localhost`.

## Что готово и чего нет

- [x] Текстовые и голосовые запросы, создание, список, поиск (включая семантический), изменение, удаление.
- [x] Уточнения с контекстом, проверка конфликтов встреч, несколько пользователей.
- [ ] Голосовой ответ (Text-to-Speech).
- [ ] Повторяющиеся события.
- [ ] Проверка конфликтов при переносе встречи (сейчас только при создании).
- [ ] Двусторонняя синхронизация с Google Calendar: изменения из Google в приложение не приходят.
- [ ] Калибровка порогов семантического поиска на размеченном наборе (сейчас они подобраны вручную: косинусное расстояние ≤ 0.16 и не дальше 0.03 от лучшего совпадения).
- [ ] Автоприменение миграций и CI.

## Безопасность

- Секреты (ключ Cloud.ru, Google OAuth, SMTP, пароль PostgreSQL) в репозиторий не попадают и вносятся в `.env` вручную по шаблонам `.env.example`. Участники команды получают значения внутри команды, внешним пользователям нужны собственные ключи.
- Речь распознаётся локально: аудио не уходит во внешние сервисы и хранится в памяти только на время запроса.
- Пароли хранятся в виде хешей argon2id. Записи и контекст диалога доступны только владельцу сессии.
- Ограничения текущего этапа: Compose поднимает только HTTP (пароли передаются открытым текстом, для публичного стенда нужен TLS перед Nginx), refresh-токен Google хранится в базе в открытом виде, нет rate limiting и резервного копирования БД.
- Об уязвимостях сообщайте напрямую владельцу репозитория ([ArtemST2006](https://github.com/ArtemST2006)), а не в публичных issues.

## Как внести вклад

Issues и pull request'ы приветствуются.

1. Создайте ветку от `main`.
2. Изменения API начинайте с `contracts/`.
3. Перед PR выполните `make -C backend lint test` и unit-тесты ai-service.
4. Используйте [Conventional Commits](https://www.conventionalcommits.org/ru/) и открывайте PR в `main` с ревьюером.

Правила ведения документации: [`docs/README.md`](docs/README.md).

## Команда и поддержка

Команда «Неймарк»:

- Денис Артемьев
- Артём Степанов — владелец репозитория ([ArtemST2006](https://github.com/ArtemST2006))
- Сергей Выдрин
- Александр Крума
- Егор Князев
- Андрей Воробьев

Вопросы и баги: [GitHub Issues](https://github.com/ArtemST2006/GorkyCode/issues).

## Лицензия

Лицензия пока не выбрана, файла `LICENSE` в репозитории нет. Поэтому по умолчанию все права на код принадлежат авторам. Использовать, изменять или распространять его можно только с разрешения команды.
