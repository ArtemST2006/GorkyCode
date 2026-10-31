workspace {
  model {
    user = person "Клиент" "Браузер с открытым WebSocket-соединением"

    gorky = softwareSystem "GorkyCode" "Голосовой ассистент для задач, напоминаний и встреч" {
      be = container "Backend" "Планировщик напоминаний, WebSocket-хаб, email-воркер" "Go 1.26 / chi" {
        scheduler = component "Scheduler" "Синхронизация и создание моментов уведомлений; скан 30s, sync 10m, horizon 7d" "Go goroutine"
        hub = component "WebSocket-хаб" "Доставка уведомлений и обработка ack на /api/v1/notifications/ws" "Go goroutine"
        emailer = component "Email-воркер" "Отправка писем по EMAIL_KINDS" "Go goroutine"
      }
      pg = container "PostgreSQL" "tasks, notifications" "PostgreSQL 16"
      rd = container "Redis" "Сессии авторизации" "Redis 7"
    }

    scheduler -> pg "Читает активные записи с датой (horizon 168h, batch 100)" "TCP"
    scheduler -> pg "UPSERT notifications: точное время события и опережения 72h/24h/3h/10m (уникальность task_id+kind+due_at)" "TCP"
    scheduler -> pg "Забирает наступившие pending (claim TTL 2m)" "TCP"
    scheduler -> hub "dispatch(notification)" "in-process"
    hub -> user "notification: kind, title, message, scheduled_at (RFC 3339)" "WSS"
    user -> hub "ack (идемпотентен)" "WSS"
    hub -> pg "status = delivered" "TCP"
    scheduler -> emailer "notification, если kind входит в EMAIL_KINDS и SMTP настроен" "in-process"
    emailer -> pg "email_sent_at" "TCP"
    user -> hub "Переподключение: WS connect + auth с токеном (в течение 5s)" "WSS"
    hub -> rd "Проверяет сессию по токену" "TCP"
    hub -> user "ready (user_id)" "WSS"
    hub -> pg "Неподтверждённые с наступившим моментом, затем новые; порядок created_at, id" "TCP"
    hub -> user "notification ×N (возможны дубликаты — клиент дедуплицирует по notification.id)" "WSS"
  }

  views {
    dynamic be "Notifications" "Планирование и доставка напоминаний" {
      scheduler -> pg "Читает активные записи с датой (horizon 168h, batch 100)" "TCP"
      scheduler -> pg "UPSERT notifications: точное время события и опережения 72h/24h/3h/10m (уникальность task_id+kind+due_at)" "TCP"
      scheduler -> pg "Забирает наступившие pending (claim TTL 2m)" "TCP"
      scheduler -> hub "dispatch(notification)" "in-process"
      hub -> user "notification: kind, title, message, scheduled_at (RFC 3339)" "WSS"
      user -> hub "ack (идемпотентен)" "WSS"
      hub -> pg "status = delivered" "TCP"
      scheduler -> emailer "notification, если kind входит в EMAIL_KINDS и SMTP настроен" "in-process"
      emailer -> pg "email_sent_at" "TCP"
      user -> hub "Переподключение: WS connect + auth с токеном (в течение 5s)" "WSS"
      hub -> rd "Проверяет сессию по токену" "TCP"
      hub -> user "ready (user_id)" "WSS"
      hub -> pg "Неподтверждённые с наступившим моментом, затем новые; порядок created_at, id" "TCP"
      hub -> user "notification ×N (возможны дубликаты — клиент дедуплицирует по notification.id)" "WSS"
    }

    styles {
      element "Component" {
        background "#e8f5e9"
      }
      element "Container" {
        background "#e3f2fd"
      }
      element "Person" {
        background "#fff3e0"
      }
    }
  }
}
