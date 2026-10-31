# База данных

Диаграмма — diagrams/er.puml.

## 1. users

Пользователи.

- id — UUID, первичный ключ.
- name — VARCHAR(100), непустой.
- email — VARCHAR(320); уникальность по нижнему регистру (индекс users_email_unique).
- password_hash — TEXT, только argon2id-хеш.
- timezone — TEXT, по умолчанию Europe/Moscow.
- created_at, updated_at — TIMESTAMPTZ.

## 2. tasks

Записи пользователя: задачи, встречи, напоминания.

- id — UUID, первичный ключ.
- user_id — UUID, внешний ключ на users, каскадное удаление.
- type — TEXT со значениями meeting, task, reminder (CHECK).
- title — TEXT, непустой.
- date — DATE, может отсутствовать.
- time — TIME, может отсутствовать; CHECK: задаётся только вместе с датой.
- status — TEXT, по умолчанию active; CHECK: active или completed, либо task с in_progress.
- google_event_id — TEXT; NULL означает, что запись не отправлена в Google.
- created_at — TIMESTAMPTZ.

Индекс task_user_date_time_idx (user_id, date, time) — списки за период и проверка конфликтов.

## 3. task_embeddings

Векторы названий для семантического поиска.

- task_id — UUID, первичный ключ, внешний ключ на tasks с каскадным удалением.
- embedding — vector(384), CHECK на положительную норму.
- model, model_version — TEXT: версия модели; сравнивать можно только векторы одной версии.
- created_at — TIMESTAMPTZ.


## 4. google_integrations

Подключения Google Calendar, одно на пользователя.

- user_id — UUID, первичный ключ, внешний ключ на users с каскадным удалением.
- refresh_token — TEXT; хранится в открытом виде (OQ-16).
- access_token — TEXT, по умолчанию пустой.
- token_expiry — TIMESTAMPTZ.
- calendar_id — TEXT, по умолчанию primary.
- google_email — TEXT.
- created_at, updated_at — TIMESTAMPTZ.

## 5. notifications

Запланированные и доставленные уведомления.

- id — UUID, первичный ключ.
- user_id — UUID, внешний ключ на users, каскадное удаление.
- task_id — UUID, внешний ключ на tasks, каскадное удаление (это id записи, не диалога).
- kind — TEXT: task_due, reminder_due, meeting_start (CHECK).
- title, message — TEXT, непустые.
- due_at — TIMESTAMPTZ, момент срабатывания (в WS-протоколе передаётся как scheduled_at).
- status — TEXT: pending или delivered (CHECK).
- send_attempted_at, email_sent_at — TIMESTAMPTZ.
- created_at — TIMESTAMPTZ.

Индексы:

- UNIQUE (task_id, kind, due_at) — не более одного уведомления каждого вида на момент.
- (due_at) WHERE status = pending — планировщик берёт наступившие.
- (user_id, created_at) WHERE status = pending — досылка при переподключении по порядку создания.
- (created_at) — очистка старых записей.

## 6. Redis

- Контекст диалога /api/task: пара (user_id, task_id), черновики, кандидаты, история до 20 сообщений. TTL 24 часа, продлевается.
- Сессии авторизации: opaque token → user. TTL по AUTH_SESSION_TTL, 24 часа.
- OAuth state Google: state → user_id. TTL 10 минут.


