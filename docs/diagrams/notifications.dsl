workspace {
  model {
    user = person "Клиент" "Браузер с открытым WebSocket-соединением"

    gorky = softwareSystem "GorkyCode" "Голосовой ассистент для задач, напоминаний и встреч" {
      be = container "Backend" "Планировщик напоминаний, WebSocket-хаб, email-воркер" "Go 1.26 / chi" {
        scheduler = component "Scheduler" "Моменты уведомлений; скан 30s, sync 10m" "Go goroutine"
        hub = component "WebSocket-хаб" "Доставка и ack" "Go goroutine"
        emailer = component "Email-воркер" "Письма по EMAIL_KINDS" "Go goroutine"
      }
      pg = container "PostgreSQL" "tasks, notifications" "PostgreSQL 16"
      rd = container "Redis" "Сессии авторизации" "Redis 7"
    }

    scheduler -> pg "1: sync — записи, UPSERT моментов, claim" "TCP"
    scheduler -> hub "2: dispatch(notification)" "in-process"
    hub -> user "3: notification; 10: ready; 12: notification ×N" "WSS"
    user -> hub "4: ack; 8: reconnect + auth (≤5s)" "WSS"
    hub -> pg "5: delivered; 11: досылка pending (created_at)" "TCP"
    scheduler -> emailer "6: notification (EMAIL_KINDS)" "in-process"
    emailer -> pg "7: email_sent_at" "TCP"
    hub -> rd "9: сессия по токену" "TCP"
  }

  views {
    dynamic be "Notifications" "Планирование и доставка напоминаний" {
      scheduler -> pg "1: sync — записи, UPSERT моментов, claim"
      scheduler -> hub "2: dispatch(notification)"
      hub -> user "3: notification; 10: ready; 12: notification ×N"
      user -> hub "4: ack; 8: reconnect + auth (≤5s)"
      hub -> pg "5: delivered; 11: досылка pending"
      scheduler -> emailer "6: notification (EMAIL_KINDS)"
      emailer -> pg "7: email_sent_at"
      hub -> rd "9: сессия по токену"
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
