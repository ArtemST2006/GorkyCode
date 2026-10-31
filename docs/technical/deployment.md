# Deployment


## 1. Состав

- nginx - образ nginx:stable-alpine; порт 8088 публичный, 127.0.0.1:8091 - внутренний к ai-service.
- backend - сборка ./backend; порт 127.0.0.1:8080; ASR-модель в volume asr_model.
- ai-service - сборка ./ai-service; порт 127.0.0.1:8090; healthcheck /health.
- postgres - образ pgvector/pgvector:0.8.7-pg16-bookworm; порт 127.0.0.1:5432; volume postgres_data.
- redis - образ redis:7-alpine; порт 127.0.0.1:6380.

## 2. Требования к окружению

- Docker Engine и Compose v2.
- Установленный goose для миграций.
- Ключ Cloud.ru (CLOUDRU_API_KEY) - обязателен для разбора запросов.
- Google-креды - опциональны: без них интеграция отвечает 503 google_disabled.
- Python 3.10+ на хосте - только для launcher-скриптов ai-service вне Docker.

## 3. Первый запуск

1. Создать конфигурацию:

```
cp .env.example .env
cp backend/.env.example backend/.env
cp ai-service/.env.example ai-service/.env
```

и указать CLOUDRU_API_KEY в ai-service/.env.

2. Поднять базу и применить миграции:

```
docker-compose up -d postgres redis
make -C backend migrate
```

3. Запустить все сервисы:

```
docker-compose up -d --build
```

Проверка: http://localhost:8088/health должен вернуть 200. Первая сборка скачивает ASR-модель и E5-веса, поэтому занимает время.

## 4. Конфигурация

- Корневой .env - порты и креды PostgreSQL.
- backend/.env - БД, Redis, адрес ai-service, AUTH_SESSION_TTL, REMINDER_*, SMTP_*, GOOGLE_*.
- ai-service/.env - ключ Cloud.ru, модель, таймауты, параметры E5.
- Полные списки с дефолтами - в .env.example каждого сервиса и в [development](development.md).
- Смена порта PostgreSQL требует обновить DATABASE_URL в backend/.env.
- Секреты (CLOUDRU_API_KEY, GOOGLE_CLIENT_SECRET, SMTP_PASSWORD, пароль PostgreSQL) хранятся в командной папке Google Drive: https://drive.google.com/drive/folders/1xkUTSNy3ACi5QHWxzOTvSkzE8KtkrzWs. В репозиторий они не попадают: .env в .gitignore, значения переносятся в .env вручную при настройке окружения. Доступ к папке должен быть ограничен участниками команды; хранение секретов в Drive вместо менеджера секретов - осознанный компромисс текущего этапа.

## 5. Миграции

- Применение: make -C backend migrate.
- Статус: make -C backend migrate-status.
- Откат последней: make -C backend migrate-down.
- Новая: make -C backend migrate-create NAME=имя.

Миграции применяются вручную перед обновлением; автоприменения при старте контейнера нет. У каждой миграции есть down-скрипт, но автоматического отката на несколько версий нет. Откат может терять данные: например, миграция статусов конвертирует in_progress в active безвозвратно.

## 6. Обновление версии

```
git pull
docker-compose build
make -C backend migrate
docker-compose up -d
```

ASR-модель и E5-веса лежат в volume и не скачиваются повторно.

## 7. TLS и публичный доступ

Compose терминирует только HTTP на 8088. Демо-стенд использует туннель cloudflared. Пароли передаются открытым текстом по HTTP - для постоянного стенда нужен TLS перед Nginx
