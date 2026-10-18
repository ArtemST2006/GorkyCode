# GorkyCode

Прототип голосового ассистента для задач, напоминаний и встреч. Проект пока
содержит каркасы backend и AI-сервиса и локальное окружение Docker Compose.

## Локальный запуск

Нужны Docker и Docker Compose. Из корня репозитория:

1. При необходимости скопировать `.env.example` в `.env` и изменить порты или пароль БД.
2. Выполнить `docker-compose up --build -d`.
3. Проверить `http://localhost:8080/health`, `http://localhost:8080/ready` и `http://localhost:8090/health`.
4. Остановить окружение командой `docker-compose down` (данные PostgreSQL сохраняются).

Для запуска Go backend вне контейнера сначала поднять зависимости командой
`docker compose up -d postgres redis`, затем перейти в каталог `backend`,
выполнить `cp .env.example .env`, скачать модель командой `make download-model`
и запустить сервис через `make run`. Оба файла `.env` исключены из Git.

Compose поднимает backend, AI-сервис, PostgreSQL и Redis. Данные PostgreSQL
сохраняются в именованном томе postgres_data; Redis используется как временный
компонент. Порты backend, AI, PostgreSQL и Redis доступны с хоста только через 127.0.0.1; их можно изменить в корневом .env.

Backend загружает конфиг, создаёт логгер и пул PostgreSQL, связывает repository с сервисным слоем и запускает HTTP-сервер на chi.
GET /health проверяет процесс, GET /ready — доступность PostgreSQL. Бизнес-методы
ассистента, обработка голоса, схема БД и работа с Redis ещё не реализованы.
AI-заглушка возвращает 501 на POST-запросы. Настройки Compose лежат в корневом
.env.example; переменные для запуска backend через make run — в backend/.env.example.

Контракты HTTP API лежат в `contracts/backend.openapi.yaml` и
`contracts/ai.openapi.yaml`. Команда `make generate` из каталога `backend`
генерирует Go-типы и маршруты backend из первого файла.

См. backend/README.md и ai-service/README.md.
