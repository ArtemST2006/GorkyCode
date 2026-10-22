# AI-service: временная заглушка

Контейнер нужен для проверки Docker Compose до появления реализации AI-сервиса.
GET /health возвращает 200, POST-запросы возвращают 501. AI-разработчик может
заменить Dockerfile и server.py своей реализацией, сохранив порт 8090 или
обновив docker-compose.yml.

Контракты будущих ручек `POST /api/job` и `POST /api/embed` описаны в
`../contracts/ai.openapi.yaml`.
