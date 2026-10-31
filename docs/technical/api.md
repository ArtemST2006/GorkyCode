# API

Контракты OpenAPI задают схемы и поведение полностью:

- contracts/backend.openapi.yaml — публичный API (из него генерируется серверный код).
- contracts/backend-addition.openapi.yaml — WebSocket-уведомления и GET /api/v1/tasks.
- contracts/ai.openapi.yaml — внутренний API ai-service, вызывает только backend.
- contracts/on-update.proposal.openapi.yaml — архив, не используется.

Ниже — сводка и правила, которые не видны из схем напрямую.

## 1. Общие правила

- Авторизация: заголовок Authorization: Bearer с токеном от sign-up или sign-in. Сессии хранятся в Redis 24 часа.
- user_id в теле запроса обязан совпадать с владельцем сессии, иначе 403.
- Ошибки возвращаются в формате Problem Details (RFC 9457) с полем code: unauthorized, forbidden, invalid_credentials, meeting_conflict, google_disabled и другие.
- Лимит тела запроса — 20 MiB, синхронизирован с Nginx.
- Rate limiting не реализован, хотя контракт описывает ответ 429 (OQ-13).

## 2. Эндпоинты backend

| Метод и путь | Назначение |
|---|---|
| GET /health | Работает ли процесс |
| GET /ready | Готовность; 503, если PostgreSQL недоступен |
| POST /api/task | Основная ручка: текст (JSON) или WAV (multipart) |
| POST /api/v1/auth/sign-up | Регистрация, возвращает пользователя и токен |
| POST /api/v1/auth/sign-in | Вход |
| POST /api/v1/auth/sign-out | Завершение сессии токена |
| GET /api/v1/tasks | Список записей без AI-диалога: период (to исключается), типы, подстрока, limit до 500 |
| GET /api/v1/integrations/google | Статус подключения календаря |
| POST /api/v1/integrations/google/connect | Адрес страницы согласия Google, state живёт 10 минут |
| GET /api/v1/integrations/google/callback | OAuth callback; всегда 302, адрес возврата только из конфигурации |
| DELETE /api/v1/integrations/google | Отключение календаря, идемпотентно |
| GET /api/v1/notifications/ws | WebSocket уведомлений |

## 3. POST /api/task

- Ровно один источник команды: text в JSON или WAV-файл в multipart. task_id передаётся только для продолжения диалога.
- task_id — идентификатор диалога. Если он не найден в Redis или испорчен, начинается новый диалог. Короткая реплика без контекста возвращает 200 needs_clarification с просьбой повторить запрос целиком.
- Заголовок X-Task-Intent содержит проверенный интент: create_event, list_events, search_events, on_delete, on_update, chat. В ошибках заголовка нет.
- Создание — 201 с data.event (запись PostgreSQL; data.event.id — не task_id). Список и поиск — 200 ok. Уточнение — 200 needs_clarification. Найденные кандидаты — candidates или update_candidates. Результат — deleted или updated.
- Задача со сроком без времени участвует в списках по дате, но не в окнах по времени.
- on_delete и on_update не меняют базу до явного выбора номеров. Устаревший выбор — 409. Занятое время встречи — 409 meeting_conflict; новое время указывается с тем же task_id.
- Повторный запрос с тем же user_id и task_id, пока первый обрабатывается, получает 409 conflict.
- Сообщение ответа формирует ai-service (/api/respond) по фактическому результату. Сбой генерации заменяет сообщение резервным текстом, операция сохраняется.

## 4. WebSocket /api/v1/notifications/ws

Клиент подключается и в течение 5 секунд отправляет auth с токеном. Сервер отвечает ready, затем доставляет notification. Клиент подтверждает обработку ack; повторный ack идемпотентен. Неподтверждённые уведомления досылаются при переподключении (возможны дубликаты — клиент устраняет их по notification.id) и хранятся 30 дней.

Коды закрытия: 4401 — неверный или истёкший токен, 4408 — нет auth за 5 секунд, 1003 — binary frame, 1009 — сообщение больше 16 KiB, 1008 — повторные ошибки протокола.

## 5. Внутренний AI API

- POST /api/job — разбор запроса: действие или уточняющий вопрос. Общий срок 150 с.
- POST /api/respond — формулировка одного сообщения по фактическому результату.
- POST /api/embed — вектор E5 (384 числа); тело до 16 KiB; 503 при занятом кодировщике.
- GET /health — жив ли процесс; готовность Cloud.ru и E5 не проверяет.

Ошибки LLM нормированы кодами: model_not_configured, model_authentication_failed, model_rate_limited, model_unavailable, model_timeout, job_timeout, model_invalid_output. Тело ошибки провайдера, ключ и prompt в публичный ответ не включаются.
