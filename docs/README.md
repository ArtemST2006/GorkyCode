# Документация GorkyCode

Голосовой ассистент для задач, напоминаний и встреч. Запуск - в корневом [README](../README.md).

## Состав

Продуктовая документация (product/):

- [Product Vision](product/product-vision.md) - проблема, аудитория, ценность, границы.
- [PRD](product/prd.md) - требования FR и NFR со статусом реализации.
- [User Stories](product/user-stories.md) - истории с критериями приёмки.
- [Use Cases](product/use-cases.md) - основные сценарии с потоками и ошибками.
- [Business Rules](product/business-rules.md) - правила BR.
- [Glossary](product/glossary.md) - термины предметной области.

Архитектура (architecture/):

- [Обзор](architecture/architecture-overview.md) - компоненты, принципы, путь запроса.
- [ADR](architecture/adr.md) - решения ADR-001…010 с обоснованиями.

Техническая документация (technical/):

- [API](technical/api.md) - сводка эндпоинтов, семантика /api/task, WebSocket; схемы - в contracts/.
- [База данных](technical/database.md) - таблицы, ограничения, индексы, Redis.
- [Интеграции](technical/integrations.md) - Cloud.ru, E5, Google Calendar, SMTP, WebSocket.
- [Deployment](technical/deployment.md) - docker-compose, миграции, обновление, backup.
- [Development](technical/development.md) - окружение, запуск, тесты, структура репозитория.
- [Operations](technical/operations.md) - health, логи, диагностика, бенчмарки ASR.

Прочее:

- Диаграммы Structurizr DSL (diagrams/): architecture (SystemContext, Containers, компоненты backend и ai-service), task-flow, notifications, database - вставить содержимое файла в редактор playground.structurizr.com/dsl.
- Скриншоты диаграмм: docs/media/architecture-*.png, task-flow.png, notifications.png, database-schema.png.
- Интерактивная версия (pan/zoom, переключение видов): diagrams/site/index.html - открыть локально в браузере; пересобирается командой structurizr export -format static.
- [Проверочный набор диалогов](dialogue-scenarios.md) и Postman-коллекции (postman/).
- Отчёт по бенчмаркам распознавания речи: technical/ASR_BENCHMARK_REPORT.pdf.

## Правила поддержки

1. Изменение API начинается с YAML в contracts/, затем обновляется раздел API.
2. Новое архитектурное решение фиксируется новым ADR; смена решения - новый ADR, старый помечается Superseded.
3. Новое правило фиксируется в Business Rules с новым BR-ID и связанным требованием в PRD.
4. Идентификаторы FR, NFR, US, UC, BR, ADR сквозные.
