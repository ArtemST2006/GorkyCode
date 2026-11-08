# GorkyCode - WebSocket уведомления через Postman

## URL
- `wss://surplus-findings-dollars-tension.trycloudflare.com/api/v1/notifications/ws`
- локально: `ws://localhost:8080/api/v1/notifications/ws`

## В Postman
1. New → WebSocket Request
2. URL: `wss://surplus-findings-dollars-tension.trycloudflare.com/api/v1/notifications/ws`
3. Connect
4. В панели Messages отправьте первым сообщением (в течение 5 секунд):

```json
{"type":"auth","token":"<access_token из sign-up/sign-in>"}
```

5. Сервер сначала вернёт:

```json
{"type":"ready","user_id":"<uuid>"}
```

6. Затем могут прийти неподтверждённые/новые уведомления:

```json
{
  "type": "notification",
  "notification": {
    "id": "5396099f-7c74-4953-9629-a4b5229087ac",
    "kind": "meeting_start",
    "task_id": "39183a1c-64a9-4f0d-8ab5-2e06e12fc9f0",
    "title": "Встреча с Иваном",
    "message": "Встреча "Встреча с Иваном" начинается сейчас.",
    "scheduled_at": "2026-10-30T12:00:00Z",
    "created_at": "2026-10-30T12:00:01Z"
  }
}
```

7. Подтвердите обработку:

```json
{"type":"ack","notification_id":"5396099f-7c74-4953-9629-a4b5229087ac"}
```

8. Повторный `ack` идемпотентен.

## Расшифровка голосовой команды
После `ready` в том же соединении может прийти `transcription` - текст, распознанный из WAV, отправленного в `POST /api/task`:

```json
{
  "type": "transcription",
  "user_id": "b80e93e1-9945-48e0-8460-3ee68bcfb402",
  "task_id": "39183a1c-64a9-4f0d-8ab5-2e06e12fc9f0",
  "request_id": "6a2f4d9c-0f1b-4f0a-9d5b-1f0a3c2b7d41",
  "text": "Поставь встречу с Иваном завтра в 15:00",
  "created_at": "2026-11-08T12:00:00Z"
}
```

Проверка: держите соединение открытым и отправьте в `POST /api/task` multipart с `user_id`, WAV-файлом и, при желании, полем `request_id`. Событие приходит сразу после распознавания речи, то есть раньше HTTP-ответа; `task_id` в нём совпадает с `task_id` ответа, `request_id` возвращается, если был передан.

## Проверка уведомлений
Создайте встречу/задачу/напоминание с близким временем (например, через минуту), подождите срабатывания, проверьте приход `NotificationMessage` и `AckMessage`.

## Коды ошибок WebSocket
- `4401` - неверный/истёкший токен
- `4408` - нет `AuthMessage` за 5 секунд
- `1003` - binary frame
- `1009` - сообщение больше 16 KiB
- `1008` - повторяющиеся protocol errors
- `ErrorMessage` с `code`: `invalid_message` или `unknown_notification`
EOF