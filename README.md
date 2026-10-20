# GorkyCode

Прототип голосового ассистента для задач, напоминаний и встреч. Проект пока
содержит каркасы backend и AI-сервиса и локальное окружение Docker Compose.

## Локальный запуск

Нужны Docker и Docker Compose. Из корня репозитория:

1. При необходимости скопировать `.env.example` в `.env` и изменить порты или пароль БД.
2. Выполнить `docker-compose up --build -d`.
3. Проверить API через публичный вход Nginx: `http://localhost:8088/health` и
   `http://localhost:8088/ready`. Локальный вход к AI-сервису:
   `http://localhost:8091/health`.
4. Остановить окружение командой `docker-compose down` (данные PostgreSQL сохраняются).

Для запуска Go backend вне контейнера сначала поднять зависимости командой
`docker-compose up -d postgres redis`, затем перейти в каталог `backend`,
выполнить `cp .env.example .env`, скачать модель командой `make download-model`
и запустить сервис через `make run`. Оба файла `.env` исключены из Git.

Compose поднимает Nginx, backend, AI-сервис, PostgreSQL и Redis. Nginx
проксирует запросы фронтенда на backend без изменения путей. Публичный вход
доступен на всех сетевых интерфейсах через `NGINX_PUBLIC_PORT` (по умолчанию
8088). Второй вход Nginx, `NGINX_AI_PORT` (по умолчанию 8091), доступен с хоста
только через 127.0.0.1 и проксирует запросы к AI-сервису. Внутри Compose
backend получает `AI_SERVICE_URL=http://nginx:8091`; AI-сервис не публикует
порт на хосте. `upstream ai_backend` в `nginx/default.conf` отслеживает адреса
экземпляров AI-сервиса через DNS Compose для будущего масштабирования. Код
backend пока не выполняет вызовы AI-сервиса: это адрес для будущего клиента.

Для WAV-запросов установлен лимит тела 20 МБ в `nginx/default.conf`; его нужно
согласовывать с лимитом backend. Данные PostgreSQL сохраняются в именованном
томе postgres_data; Redis используется как временный
компонент. Порты backend, PostgreSQL и Redis доступны с хоста только через
127.0.0.1; их можно изменить в корневом .env. Для публикации API в интернете
потребуется добавить HTTPS перед передачей токенов авторизации.

Backend загружает конфиг, создаёт логгер и пул PostgreSQL, связывает repository с сервисным слоем и запускает HTTP-сервер на chi.
GET /health проверяет процесс, GET /ready — доступность PostgreSQL. Ручки
авторизации используют PostgreSQL и Redis-сессии. Обработка `/api/task` пока
не реализована. AI-заглушка возвращает 501 на POST-запросы. Настройки Compose
лежат в корневом .env.example; переменные для запуска backend через make run —
в backend/.env.example.

Контракты HTTP API лежат в `contracts/backend.openapi.yaml` и
`contracts/ai.openapi.yaml`. Команда `make generate` из каталога `backend`
генерирует Go-типы и маршруты backend из первого файла, Go-типы и клиент
AI-сервиса — из второго. Для отдельной генерации доступны
`make generate-backend` и `make generate-ai`.

См. backend/README.md и ai-service/README.md.
