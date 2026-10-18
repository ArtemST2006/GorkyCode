# AI-service: временная заглушка

Контейнер нужен для проверки Docker Compose до появления реализации AI-сервиса.
GET /health возвращает 200, POST-запросы возвращают 501. AI-разработчик может
заменить Dockerfile и server.py своей реализацией, сохранив порт 8090 или
обновив docker-compose.yml.

Контракт будущей ручки `POST /api/job` описан в `../contracts/ai.openapi.yaml`.
