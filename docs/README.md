# Документация GorkyCode

Голосовой ассистент для задач, напоминаний и встреч. Запуск — в корневом [README](../README.md).

## Состав

Продуктовая документация (product/):

- [Product Vision](product/product-vision.md) — проблема, аудитория, ценность, границы.
- [PRD](product/prd.md) — требования FR и NFR со статусом реализации.
- [User Stories](product/user-stories.md) — истории с критериями приёмки.
- [Use Cases](product/use-cases.md) — основные сценарии с потоками и ошибками.
- [Business Rules](product/business-rules.md) — правила BR.
- [Glossary](product/glossary.md) — термины предметной области.

Архитектура (architecture/):

- [Обзор](architecture/architecture-overview.md) — компоненты, принципы, путь запроса.
- [ADR](architecture/adr.md) — решения ADR-001…010 с обоснованиями.

Техническая документация (technical/):

- [API](technical/api.md) — сводка эндпоинтов, семантика /api/task, WebSocket; схемы — в contracts/.
- [База данных](technical/database.md) — таблицы, ограничения, индексы, Redis.
- [Интеграции](technical/integrations.md) — Cloud.ru, E5, Google Calendar, SMTP, WebSocket.
- [Deployment](technical/deployment.md) — docker-compose, миграции, обновление, backup.
- [Development](technical/development.md) — окружение, запуск, тесты, структура репозитория.
- [Operations](technical/operations.md) — health, логи, диагностика, бенчмарки ASR.

Прочее:

- [Открытые вопросы](open-questions.md) — нерешённые вопросы OQ.
- Диаграммы PlantUML (diagrams/): architecture, task-flow, notifications, er.
- [Проверочный набор диалогов](dialogue-scenarios.md) и Postman-коллекции (postman/).

## Как поддерживать

1. Изменение API — сначала YAML в contracts/, затем раздел API.
2. Новое архитектурное решение — новый ADR; смена решения — новый ADR, старый помечается Superseded.
3. Новое правило — Business Rules с новым BR-ID и связанное требование в PRD.
4. Нерешённый вопрос — в open-questions.md; решённый — удалить из списка и отразить факт в документе.
5. Идентификаторы FR, NFR, US, UC, BR, ADR, OQ сквозные: ссылаться на них, не пересказывать.
