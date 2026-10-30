# Backend

Для запуска вне Docker нужен Go 1.26.7+ и компилятор C (Clang/GCC) с поддержкой CGO.

## Локальный запуск
1. Поднимите зависимости из корня проекта:
   ```bash
   docker-compose up -d postgres redis
   ```
2. Скопируйте конфигурацию окружения:
   ```bash
   cd backend
   cp .env.example .env
   ```
3. Примените миграции PostgreSQL командой `make migrate` из каталога `backend`.
4. Скачайте и распакуйте модель распознавания речи (ASR Sherpa ONNX):
   ```bash
   make download-model
   ```
   Модель будет загружена и распакована в каталог `model/asr-model`.
5. Запустите сервис:
   ```bash
   make run
   ```


`make run` экспортирует переменные из `.env`; базовая конфигурация: `config/local.yaml`.

## Доступные команды Makefile

- `make help` — показать список доступных команд
- `make download-model` — скачать и распаковать ASR-модель в `model/asr-model`
- `make clean-model` — удалить скачанные файлы модели и архива
- `make build` — собрать бинарный файл в `bin/backend`
- `make run` — запустить локальный сервер
- `make test` — запустить тесты с race detector и отчётом покрытия
- `make lint` — проверить код с помощью golangci-lint
- `make format` — отформатировать код (gofumpt, goimports)
- `make migrate` — применить миграции к PostgreSQL

## Эндпоинты проверки

- `GET /health` — проверка работоспособности HTTP-сервера.
- `GET /ready` — проверка готовности сервиса (включая подключение к PostgreSQL).
- `POST /api/v1/auth/sign-up` — регистрация, возвращает пользователя и access token.
- `POST /api/v1/auth/sign-in` — вход по email и паролю, возвращает пользователя и access token.
- `POST /api/v1/auth/sign-out` — завершает сессию токена из `Authorization: Bearer <token>`.
- `POST /api/task` — требует `Authorization: Bearer <token>` и совпадения `user_id` с владельцем сессии; обрабатывает текстовую команду или WAV-файл через AI-сервис, поддерживает обычное общение, уточнение, создание, список, поиск и подтверждённое удаление записей. Для обычного общения возвращает `X-Task-Intent: chat`, `status: ok` и текстовый `message` без действий над записями; передайте тот же `task_id` для продолжения разговора. Если `task_id` не передан или его контекст истёк в Redis, backend начинает новый диалог с новым ID. Для короткой реплики без контекста возвращается `needs_clarification` с просьбой повторить команду целиком.
- Статус записи в `data.event.status` и `data.items[].status`: для `record_type=task` допустимы `active` (создана/ещё не начата), `in_progress` (в работе), `completed` (завершена). Для `meeting` и `reminder` остаются только `active` и `completed`. Создание по-прежнему возвращает `active`; смена статуса происходит через обычный диалог обновления записи с выбором кандидата и подтверждением.
- Для запроса о ближайшем времени AI заполняет поле `upcoming_minutes` в `list_events`. Backend вычисляет границы от времени запроса в часовом поясе пользователя и показывает записи с точным временем; запись только с датой не может быть отнесена к часовому окну. Повреждённый контекст Redis считается истёкшим. Если модель после повторной попытки не смогла выдать ответ по схеме, запрос завершается безопасным `needs_clarification` без действий над записями. Недоступность AI, Redis и базы данных остаётся ошибкой сервиса.

`search_events` объединяет буквальный поиск по названию с поиском по
совместимым E5-векторам.

Access token — случайная строка; сессии хранятся в Redis (`REDIS_URL`) со сроком жизни `AUTH_SESSION_TTL` (по умолчанию 24h).

## Google Calendar

Пользователь может подключить свой Google Calendar: после этого каждая новая
встреча (`record_type: meeting`), созданная через `POST /api/task`, сразу
появляется в его основном календаре. ID события сохраняется в
`tasks.google_event_id`. Задачи и напоминания в календарь не отправляются.
Пока `GOOGLE_CLIENT_ID` и `GOOGLE_CLIENT_SECRET` не заданы, интеграция
выключена: ручки ниже отвечают 503 `google_disabled`, а записи создаются как
раньше.

### Настройка в Google Cloud

1. Создайте проект в [Google Cloud Console](https://console.cloud.google.com/)
   и включите **Google Calendar API** (APIs & Services → Library).
2. Настройте OAuth consent screen: тип **External**, статус **Testing**.
   Добавьте свои Google-аккаунты в **Test users** — только они смогут
   подключиться.
3. Добавьте scopes: `https://www.googleapis.com/auth/calendar.events`,
   `https://www.googleapis.com/auth/calendar.events.freebusy` (проверка
   занятости для конфликтов встреч), `openid`, `email`. После добавления
   scope календарь нужно подключить заново.
4. Создайте OAuth client ID типа **Web application** и укажите Authorized
   redirect URI `http://localhost:8088/api/v1/integrations/google/callback`
   (через Nginx из Compose) или
   `http://localhost:8080/api/v1/integrations/google/callback` (для `make run`).
5. Скопируйте Client ID и Client secret в `.env` (корневой для Compose или
   `backend/.env` для `make run`) и примените миграции командой `make migrate`.

### Переменные окружения

- `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` — данные OAuth-клиента; без них интеграция выключена.
- `GOOGLE_REDIRECT_URL` — адрес callback; должен совпадать с redirect URI в Google Cloud.
- `GOOGLE_FRONTEND_REDIRECT` — куда backend вернёт браузер после callback
  (к адресу добавляется `?google=connected` или `?google=error`). Адрес
  берётся только из конфига; если он пустой, используется `/`.
- `GOOGLE_EVENT_DURATION` — длительность события в календаре, по умолчанию `1h`.

### Эндпоинты

- `POST /api/v1/integrations/google/connect` — Bearer; возвращает `auth_url`
  страницы согласия Google. Одноразовый `state` хранится в Redis 10 минут.
- `GET /api/v1/integrations/google/callback` — сюда Google возвращает браузер;
  Bearer не нужен, пользователь определяется по `state`. Всегда отвечает 302 на
  `GOOGLE_FRONTEND_REDIRECT`.
- `GET /api/v1/integrations/google` — Bearer; `connected`, `google_email`, `connected_at`.
- `DELETE /api/v1/integrations/google` — Bearer; отзывает токен в Google и
  удаляет подключение, 204 даже если подключения не было.

### Конфликты встреч

Перед созданием встречи backend проверяет, не пересекается ли она
(`[начало, начало + GOOGLE_EVENT_DURATION)`) с активными встречами
пользователя в PostgreSQL и, если календарь подключён, с занятым временем в
Google Calendar (free/busy). При пересечении встреча не создаётся сразу:
`POST /api/task` отвечает 200 `needs_clarification` с вопросом «В это время
уже есть встреча «X», всё равно поставить?». Клиент повторяет запрос с тем же
`task_id`: «да» создаёт встречу (201), «нет» возвращает 200 со
`status: cancelled`, другой ответ повторяет вопрос. Неподтверждённая встреча
хранится в Redis 10 минут. Ответ «да»/«нет» распознаёт backend, без
AI-сервиса. Ошибка Google при проверке только логируется и не мешает созданию
встречи. Задачи и напоминания на конфликты не проверяются.

### Ограничения

- Пока приложение в статусе Testing, Google выдаёт refresh token на 7 дней:
  после этого синхронизация перестаёт работать, backend удаляет подключение,
  и календарь нужно подключить заново.
- В календарь отправляется только создание встреч; изменения и удаление
  записей не синхронизируются, события из Google не импортируются.
- Синхронизация выполняется сразу в запросе создания записи с таймаутом 3
  секунды на каждый вызов Google. Ошибка синхронизации только логируется:
  запись всё равно создаётся, но события в календаре не будет.
- Refresh token хранится в PostgreSQL в открытом виде.

### Что можно улучшить

- Отправлять события через outbox и фоновый воркер с повторами, чтобы не
  замедлять создание записи и не терять события при сбоях Google.
- Шифровать refresh token в базе (например, ключом из KMS или переменной окружения).
- Синхронизировать изменение и удаление записей (`UpdateEvent`, `DeleteEvent` уже есть в клиенте).
- Добавить двустороннюю синхронизацию: импорт событий из Google Calendar.

## Разработка

- При разработке периодически запускайте `make lint` и `make test`.
- Придерживайтесь конвенции [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
- Как только задача выполнена, создавайте PR в `main` и ставьте ревьюера.
При разработке переодически пользуйтесь make lint. Также можно почитать commit conventions https://www.conventionalcommits.org/en/v1.0.0/. 
Как тоьлко таска закрыта, открывайте pr в main и тэгайте меня. Пуш в мэин закрою


# Тесты

использование: testing, httptest, mockery(или gomock), testify/assert, testyfy/requiere

Давайте сразу покрывать код юнит тестами ручки. Из-за обьявления интерфейсов в месте использования удобно генерировать моки с помощью mockery или gomock.
Т.е на уровне сервисов мок для репозитория, а на хэндлерах мок для сервисов 

Интеграционные тесты можно в конце бахнуть, ну а юнит нужно писать парралельно
