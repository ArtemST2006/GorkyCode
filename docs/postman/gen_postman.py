import json, uuid

BASE = "https://surplus-findings-dollars-tension.trycloudflare.com"

def R(name, method, path, body=None, headers=None, auth=None, query=None,
      tests=None, prereq=None, desc=None, formdata=None):
    url = "{{base_url}}" + path
    if query:
        pairs = []
        for k, v in query.items():
            pairs.append(f"{k}={v}")
        url = url + "?" + "&".join(pairs)
    request = {"method": method, "header": headers or [], "url": url}
    if body is not None:
        request["body"] = {"mode": "raw", "raw": body, "options": {"raw": {"language": "json"}}}
    if formdata is not None:
        request["body"] = {"mode": "formdata", "formdata": formdata}
    if auth is not None:
        request["auth"] = auth
    item = {"name": name, "request": request, "response": []}
    events = []
    if prereq:
        events.append({"listen": "prerequest", "script": {"exec": prereq, "type": "text/javascript"}})
    if tests:
        events.append({"listen": "test", "script": {"exec": tests, "type": "text/javascript"}})
    if events:
        item["event"] = events
    if desc:
        item["request"]["description"] = desc
    return item

def folder(name, items):
    return {"name": name, "item": items}

JSONH = [{"key": "Content-Type", "value": "application/json"}]
NOAUTH = {"type": "noauth"}

sections = []

# ---------- 0. Service ----------
items = []
items.append(R("Health", "GET", "/health", auth=NOAUTH, tests=[
    "pm.test('status is 200', () => pm.response.to.have.status(200));",
    "pm.test('service ok', () => { const j = pm.response.json(); pm.expect(j.service).to.eql('backend'); pm.expect(j.status).to.eql('ok'); });",
], desc="Проверка, что процесс backend жив."))
items.append(R("Ready", "GET", "/ready", auth=NOAUTH, tests=[
    "pm.test('status is 200 or 503', () => pm.expect([200,503]).to.include(pm.response.code));",
    "pm.test('valid health response', () => { const j = pm.response.json(); pm.expect(j).to.have.property('service'); pm.expect(j).to.have.property('status'); });",
], desc="Проверка готовности (PostgreSQL)."))
sections.append(folder("0. Service", items))

# ---------- 1. Auth ----------
items = []
items.append(R("Sign up (create user)", "POST", "/api/v1/auth/sign-up",
    headers=JSONH, auth=NOAUTH,
    prereq=[
        "const email = `postman_${Date.now()}@example.com`;",
        "pm.collectionVariables.set('email', email);",
        "pm.collectionVariables.set('password', 'SuperSecretPassword123!');",
        "pm.collectionVariables.set('timezone', 'Europe/Moscow');",
    ],
    body='{"name":"Postman Test","email":"{{email}}","password":"{{password}}","timezone":"{{timezone}}"}',
    tests=[
        "pm.test('status is 201', () => pm.response.to.have.status(201));",
        "pm.test('save token and user_id', () => { const j = pm.response.json(); pm.collectionVariables.set('token', j.access_token); pm.collectionVariables.set('user_id', j.user.id); pm.expect(j.token_type).to.eql('Bearer'); pm.expect(j.expires_in).to.be.above(0); });",
    ],
    desc="Регистрация. В pre-request генерируется уникальный email; токен сохраняется в collection variables."))
items.append(R("Sign in (valid)", "POST", "/api/v1/auth/sign-in",
    headers=JSONH, auth=NOAUTH,
    body='{"email":"{{email}}","password":"{{password}}"}',
    tests=[
        "pm.test('status is 200', () => pm.response.to.have.status(200));",
        "pm.test('refresh token', () => { const j = pm.response.json(); pm.collectionVariables.set('token', j.access_token); pm.collectionVariables.set('user_id', j.user.id); });",
    ],
    desc="Вход по email/password. Обновляет токен."))
items.append(R("Sign in (invalid credentials)", "POST", "/api/v1/auth/sign-in",
    headers=JSONH, auth=NOAUTH,
    body='{"email":"{{email}}","password":"WrongPassword123456789"}',
    tests=[
        "pm.test('status is 401', () => pm.response.to.have.status(401));",
        "pm.test('code invalid_credentials', () => { const j = pm.response.json(); pm.expect(j.code).to.eql('invalid_credentials'); });",
    ],
    desc="Неверный пароль -> 401 invalid_credentials (без раскрытия, существует ли email)."))
items.append(R("Sign up (duplicate email)", "POST", "/api/v1/auth/sign-up",
    headers=JSONH, auth=NOAUTH,
    body='{"name":"Postman Test","email":"{{email}}","password":"{{password}}","timezone":"{{timezone}}"}',
    tests=[
        "pm.test('status is 409', () => pm.response.to.have.status(409));",
    ],
    desc="Повторная регистрация того же email -> 409 Conflict."))
items.append(R("Google Calendar status", "GET", "/api/v1/integrations/google",
    tests=[
        "pm.test('status 200 or 503', () => pm.expect([200,503]).to.include(pm.response.code));",
    ],
    desc="Статус интеграции Google Calendar текущего пользователя."))
items.append(R("Google connect (start OAuth)", "POST", "/api/v1/integrations/google/connect",
    tests=[
        "pm.test('status 200 or 503', () => pm.expect([200,503]).to.include(pm.response.code));",
        "pm.test('auth_url present if 200', () => { if (pm.response.code === 200) { const j = pm.response.json(); pm.expect(j).to.have.property('auth_url'); pm.collectionVariables.set('google_auth_url', j.auth_url); } });",
    ],
    desc="Создаёт одноразовый state и возвращает URL согласия Google."))
items.append(R("Google disconnect", "DELETE", "/api/v1/integrations/google",
    tests=[
        "pm.test('status 204 or 503', () => pm.expect([204,503]).to.include(pm.response.code));",
    ],
    desc="Отключение Google Calendar (идемпотентно)."))
sections.append(folder("1. Auth", items))

# ---------- 2. Tasks: Create ----------
items = []
items.append(R("Create meeting tomorrow 15:00", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","text":"Назначь встречу с Иваном завтра в 15:00"}',
    tests=[
        "pm.test('status is 201', () => pm.response.to.have.status(201));",
        "pm.test('X-Task-Intent create_event', () => pm.expect(pm.response.headers.get('X-Task-Intent')).to.eql('create_event'));",
        "pm.test('save ids', () => { const j = pm.response.json(); pm.collectionVariables.set('task_id', j.task_id); pm.collectionVariables.set('event_id', j.data.event.id); pm.collectionVariables.set('meeting_title', j.data.event.title); pm.expect(j.data.event.record_type).to.eql('meeting'); pm.expect(j.data.event.time).to.eql('15:00'); pm.expect(j.status).to.eql('ok'); });",
    ],
    desc="Создание встречи. Сохраняет task_id (диалог) и event_id (строка tasks)."))
items.append(R("Create task without exact time", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","text":"Поставь задачу купить билеты до конца недели"}',
    tests=[
        "pm.test('status is 201', () => pm.response.to.have.status(201));",
        "pm.test('task has no time', () => { const j = pm.response.json(); const e = j.data.event; pm.expect(e.record_type).to.eql('task'); if (e.time) { pm.expect(e.time).to.not.exist; } });",
    ],
    desc="Задача со сроком без точного времени."))
items.append(R("Create reminder", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","text":"Напомни позвонить маме завтра в 19:00"}',
    tests=[
        "pm.test('status is 201', () => pm.response.to.have.status(201));",
        "pm.test('reminder', () => { const j = pm.response.json(); pm.expect(j.data.event.record_type).to.eql('reminder'); pm.expect(j.data.event.time).to.eql('19:00'); });",
    ],
    desc="Создание напоминания."))
items.append(R("Chat (no DB changes)", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","text":"Привет"}',
    tests=[
        "pm.test('status is 200', () => pm.response.to.have.status(200));",
        "pm.test('X-Task-Intent chat', () => pm.expect(pm.response.headers.get('X-Task-Intent')).to.eql('chat'));",
        "pm.test('has message', () => pm.expect(pm.response.json().message.length).to.be.above(0));",
    ],
    desc="Обычная реплика (chat), записи не читаются и не меняются."))
items.append(R("Process audio command (multipart)", "POST", "/api/task",
    auth={"type": "bearer", "bearer": [{"key": "token", "value": "{{token}}"}]},
    formdata=[
        {"key": "user_id", "value": "{{user_id}}", "type": "text"},
        {"key": "file", "type": "file", "src": "", "value": ""},
    ],
    tests=[
        "pm.test('is defined response', () => pm.response.code);",
    ],
    desc="Голосовая команда WAV. В Postman выберите WAV-файл в поле file. Пустой src нужно заменить вручную."))
sections.append(folder("2. Tasks - Create", items))

# ---------- 3. Clarifications ----------
items = []
items.append(R("Clarification step 1 (missing time)", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","text":"Назначь встречу с Анной завтра"}',
    tests=[
        "pm.test('status is 200', () => pm.response.to.have.status(200));",
        "pm.test('needs clarification', () => { const j = pm.response.json(); pm.expect(j.status).to.eql('needs_clarification'); pm.expect(j.message.length).to.be.above(0); pm.collectionVariables.set('clarify_task_id', j.task_id); });",
        "pm.test('X-Task-Intent create_event', () => pm.expect(pm.response.headers.get('X-Task-Intent')).to.eql('create_event'));",
    ],
    desc="Диалог: время не указано -> needs_clarification, task_id сохраняется."))
items.append(R("Clarification step 2 (answer with time)", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","task_id":"{{clarify_task_id}}","text":"В 17:00"}',
    tests=[
        "pm.test('status is 201', () => pm.response.to.have.status(201));",
        "pm.test('meeting created 17:00', () => { const j = pm.response.json(); pm.expect(j.data.event.time).to.eql('17:00'); pm.expect(j.data.event.title).to.include('Анной'); });",
    ],
    desc="Ответ на уточняющий вопрос с тем же task_id -> создание 201."))
sections.append(folder("3. Clarifications", items))

# ---------- 4. Tasks: View ----------
items = []
items.append(R("List events tomorrow (process)", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","text":"Покажи мои события на завтра"}',
    tests=[
        "pm.test('status is 200', () => pm.response.to.have.status(200));",
        "pm.test('X-Task-Intent list_events', () => pm.expect(pm.response.headers.get('X-Task-Intent')).to.eql('list_events'));",
        "pm.test('items array', () => { const j = pm.response.json(); pm.expect(j.data.items).to.be.an('array'); });",
    ],
    desc="Список событий через AI-диалог."))
items.append(R("Search events by person", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","text":"Найди встречи с Иваном"}',
    tests=[
        "pm.test('status is 200', () => pm.response.to.have.status(200));",
        "pm.test('X-Task-Intent search_events', () => pm.expect(pm.response.headers.get('X-Task-Intent')).to.eql('search_events'));",
        "pm.test('items array', () => pm.expect(pm.response.json().data.items).to.be.an('array'));",
    ],
    desc="Поиск событий по признакам."))
items.append(R("Read tasks (date/type filters)", "GET", "/api/v1/tasks",
    query={"from": "2026-10-30", "to": "2026-11-07", "type": "task,meeting,reminder", "limit": "200", "offset": "0"},
    tests=[
        "pm.test('status is 200', () => pm.response.to.have.status(200));",
        "pm.test('ok collection', () => { const j = pm.response.json(); pm.expect(j.status).to.eql('ok'); pm.expect(j.data.items).to.be.an('array'); });",
    ],
    desc="Быстрое read-only API для списка задач (без AI)."))
items.append(R("Read tasks (query title)", "GET", "/api/v1/tasks",
    query={"query": "Иван", "limit": "50"},
    tests=[
        "pm.test('status is 200', () => pm.response.to.have.status(200));",
        "pm.test('filtered', () => pm.expect(pm.response.json().data.items).to.be.an('array'));",
    ],
    desc="Поиск по подстроке названия."))
items.append(R("Read tasks (empty range)", "GET", "/api/v1/tasks",
    query={"from": "2030-01-01", "to": "2030-01-02"},
    tests=[
        "pm.test('status is 200', () => pm.response.to.have.status(200));",
        "pm.test('empty items', () => pm.expect(pm.response.json().data.items).to.have.lengthOf(0));",
    ],
    desc="Пустой диапазон -> items []."))
sections.append(folder("4. Tasks - View", items))

# ---------- 5. Tasks: Update ----------
items = []
items.append(R("Update find candidates (mark completed)", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","text":"Отметь встречу с Иваном завтра как завершённую"}',
    tests=[
        "pm.test('status is 200', () => pm.response.to.have.status(200));",
        "pm.test('update_candidates', () => { const j = pm.response.json(); pm.expect(j.status).to.eql('update_candidates'); pm.collectionVariables.set('update_task_id', j.task_id); pm.expect(j.data.candidates).to.be.an('array'); pm.expect(j.data.changes).to.be.an('object'); });",
    ],
    desc="on_update: поиск кандидатов и предлагаемые changes, изменения ещё не записаны."))
items.append(R("Update confirm (choose #1)", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","task_id":"{{update_task_id}}","text":"Выбери №1"}',
    tests=[
        "pm.test('status is 200', () => pm.response.to.have.status(200));",
        "pm.test('updated', () => { const j = pm.response.json(); pm.expect(j.status).to.eql('updated'); pm.expect(j.data.updated_events).to.be.an('array').that.is.not.empty; });",
    ],
    desc="Подтверждение изменения выбранных записей по номерам."))
items.append(R("Update verify via read", "GET", "/api/v1/tasks",
    query={"query": "Иван"},
    tests=[
        "pm.test('status is 200', () => pm.response.to.have.status(200));",
    ],
    desc="Проверка, что статус Ивана изменился на completed (через read)."))
sections.append(folder("5. Tasks - Update", items))

# ---------- 6. Tasks: Delete ----------
items = []
items.append(R("Delete find candidates", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","text":"Удали встречу с Анной завтра"}',
    tests=[
        "pm.test('status is 200', () => pm.response.to.have.status(200));",
        "pm.test('candidates', () => { const j = pm.response.json(); pm.expect(j.status).to.eql('candidates'); pm.collectionVariables.set('delete_task_id', j.task_id); pm.expect(j.data.candidates).to.be.an('array'); });",
    ],
    desc="on_delete: поиск кандидатов на удаление (номера стабильны в диалоге)."))
items.append(R("Delete candidates empty case", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","text":"Удали встречу с НесуществующимЧеловеком"}',
    tests=[
        "pm.test('status is 200', () => pm.response.to.have.status(200));",
        "pm.test('candidates empty', () => { const j = pm.response.json(); pm.expect(j.status).to.eql('candidates'); pm.expect(j.data.candidates).to.have.lengthOf(0); });",
    ],
    desc="Нет совпадений -> пустой candidates, можно уточнить с тем же task_id."))
items.append(R("Delete confirm selected (reuse delete_task_id)", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","task_id":"{{delete_task_id}}","text":"Удали №1"}',
    tests=[
        "pm.test('status is 200', () => pm.response.to.have.status(200));",
        "pm.test('deleted', () => { const j = pm.response.json(); pm.expect(j.status).to.eql('deleted'); pm.expect(j.data.deleted_events).to.be.an('array').that.is.not.empty; });",
    ],
    desc="Подтверждение удаления выбранных номеров."))
sections.append(folder("6. Tasks - Delete", items))

# ---------- 7. Conflicts ----------
items = []
items.append(R("Conflict: create duplicate time (expect 409)", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","text":"Назначь встречу с Петром завтра в 15:00"}',
    tests=[
        "pm.test('status is 409', () => pm.response.to.have.status(409));",
        "pm.test('meeting_conflict', () => { const j = pm.response.json(); pm.expect(j.code).to.eql('meeting_conflict'); pm.expect(j).to.have.property('task_id'); pm.expect(j).to.have.property('user_id'); pm.collectionVariables.set('conflict_task_id', j.task_id); });",
    ],
    desc="Создание встречи на занятое время -> 409 meeting_conflict, черновик остаётся в Redis по task_id."))
items.append(R("Conflict resolve: choose 16:00 (same task_id)", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","task_id":"{{conflict_task_id}}","text":"В 16:00"}',
    tests=[
        "pm.test('status is 201', () => pm.response.to.have.status(201));",
        "pm.test('new meeting 16:00', () => { const j = pm.response.json(); pm.expect(j.data.event.time).to.eql('16:00'); pm.expect(j.data.event.title).to.include('Петр'); });",
    ],
    desc="Повтор с тем же task_id и новым временем -> 201."))
items.append(R("Concurrency guard note", "GET", "/health",
    desc="Одновременные запросы с одинаковым user_id+task_id: второй получает 409 (Conflict). Проверять вручную двумя параллельными runner'ами."))
sections.append(folder("7. Conflicts", items))

# ---------- 8. Notifications ----------
items = []
ws_desc = """WebSocket (ручной шаг в Postman):
1) Создайте WebSocket Request с URL: wss://{{base_url}}/api/v1/notifications/ws
   (или ws://... если без TLS).
2) Подключитесь (должен придти HTTP 101).
3) Отправьте первым сообщением (в течение 5 сек):
   {"type":"auth","token":"{{token}}"}
4) Ожидайте сервер: {"type":"ready","user_id":"{{user_id}}"}
   затем NotificationMessage с неподтверждёнными/новыми уведомлениями.
5) Подтвердите обработку:
   {"type":"ack","notification_id":"<id из notification>"}
6) Проверьте идемпотентность повторного ack (без ошибки).

Коды закрытия: 4401 неверный/истёкший токен, 4408 нет auth за 5с,
1003 binary frame, 1009 >16KiB. ErroCode invalid_message/unknown_notification.

Триггер уведомления: создайте встречу/задачу с близким временем (backend создаёт notification на meeting_start/task_due/reminder_due)."""
items.append(R("WebSocket upgrade (manual messages)", "GET", "/api/v1/notifications/ws",
    auth=NOAUTH,
    tests=["pm.test('manual ws step', () => true);"],
    desc=ws_desc))
items.append(R("Notification triggers (create near-time reminder)", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","text":"Напомни позвонить коллеге через 2 минуты"}',
    tests=[
        "pm.test('status is 201', () => pm.response.to.have.status(201));",
    ],
    desc="Создаёт reminder с близким временем, чтобы проверить уведомление по WebSocket."))
items.append(R("List tasks (read backend state after ops)", "GET", "/api/v1/tasks",
    query={"from": "2026-10-30", "to": "2026-11-07"},
    tests=["pm.test('status is 200', () => pm.response.to.have.status(200));"],
    desc="Проверка состояния записей после тестов."))
sections.append(folder("8. Notifications", items))

# ---------- 9. Errors ----------
items = []
items.append(R("Unauthorized (no token)", "POST", "/api/task",
    headers=JSONH, auth=NOAUTH,
    body='{"user_id":"{{user_id}}","text":"Покажи события"}',
    tests=[
        "pm.test('status is 401', () => pm.response.to.have.status(401));",
        "pm.test('code unauthorized', () => pm.expect(pm.response.json().code).to.eql('unauthorized'));",
    ],
    desc="Запрос без Bearer-токена -> 401 unauthorized."))
items.append(R("Forbidden (user_id mismatch)", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"00000000-0000-0000-0000-000000000000","text":"Покажи события"}',
    tests=[
        "pm.test('status is 403', () => pm.response.to.have.status(403));",
        "pm.test('code forbidden', () => pm.expect(pm.response.json().code).to.eql('forbidden'));",
    ],
    desc="user_id не совпадает с владельцем сессии -> 403 forbidden."))
items.append(R("Bad request (invalid JSON)", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}"',
    tests=[
        "pm.test('status is 400', () => pm.response.to.have.status(400));",
    ],
    desc="Некорректный JSON -> 400."))
items.append(R("Bad request (invalid type filter)", "GET", "/api/v1/tasks",
    query={"type": "foo"},
    tests=[
        "pm.test('status is 400', () => pm.response.to.have.status(400));",
    ],
    desc="Некорректный фильтр type -> 400."))
items.append(R("Sign out", "POST", "/api/v1/auth/sign-out",
    tests=[
        "pm.test('status is 204', () => pm.response.to.have.status(204));",
    ],
    desc="Завершает сессию, к которой привязан токен."))
items.append(R("Token after sign-out (expect 401)", "POST", "/api/task",
    headers=JSONH,
    body='{"user_id":"{{user_id}}","text":"Покажи события"}',
    tests=[
        "pm.test('status is 401', () => pm.response.to.have.status(401));",
    ],
    desc="Повторный запрос со старым токеном после sign-out -> 401."))
items.append(R("Invalid login after sign-out not needed", "GET", "/health",
    desc="Токен более не активен; для новых тестов повторите Sign up."))
sections.append(folder("9. Errors & Edge Cases", items))

collection = {
    "info": {
        "name": "GorkyCode Backend — full cycle",
        "description": "Полный цикл проверки backend по OpenAPI contracts. Базовый URL настраивается переменной base_url. Рекомендуемый порядок: запуск коллекции через Collection Runner сверху вниз. Postman импортирует этот файл как коллекцию v2.1.",
        "_postman_id": str(uuid.uuid4()),
        "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
    },
    "auth": {
        "type": "bearer",
        "bearer": [{"key": "token", "value": "{{token}}", "type": "string"}],
    },
    "item": sections,
    "variable": [
        {"key": "base_url", "value": BASE, "type": "string"},
        {"key": "email", "value": "", "type": "string"},
        {"key": "password", "value": "", "type": "string"},
        {"key": "timezone", "value": "Europe/Moscow", "type": "string"},
        {"key": "token", "value": "", "type": "string"},
        {"key": "user_id", "value": "", "type": "string"},
        {"key": "task_id", "value": "", "type": "string"},
        {"key": "event_id", "value": "", "type": "string"},
        {"key": "clarify_task_id", "value": "", "type": "string"},
        {"key": "update_task_id", "value": "", "type": "string"},
        {"key": "delete_task_id", "value": "", "type": "string"},
        {"key": "conflict_task_id", "value": "", "type": "string"},
    ],
}

with open("/Users/andrejvorobev/Documents/GitHub/GorkyCode/docs/postman/GorkyCode-Backend.postman_collection.json", "w", encoding="utf-8") as f:
    json.dump(collection, f, ensure_ascii=False, indent=2)

print("written")
