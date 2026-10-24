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
- `POST /api/task` — требует `Authorization: Bearer <token>` и совпадения `user_id` с владельцем сессии; обрабатывает текстовую команду или WAV-файл через AI-сервис, поддерживает уточнение, создание, список и поиск записей.

Access token — случайная строка; сессии хранятся в Redis (`REDIS_URL`) со сроком жизни `AUTH_SESSION_TTL` (по умолчанию 24h).

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
