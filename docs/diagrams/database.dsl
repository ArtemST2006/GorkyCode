workspace {
  model {
    db = softwareSystem "PostgreSQL" "Схема данных GorkyCode (миграции backend/migrations)" {
      users = container "users" "Пользователи: name, email (уникален по lower), password_hash (argon2id), timezone" "таблица"
      tasks = container "tasks" "Записи: type (meeting/task/reminder), title, date, time, status, google_event_id" "таблица"
      emb = container "task_embeddings" "Векторы названий: embedding vector(384), model, model_version" "таблица"
      gi = container "google_integrations" "Подключения Google Calendar: refresh_token, access_token, token_expiry, calendar_id" "таблица"
      nt = container "notifications" "Уведомления: kind (task_due/reminder_due/meeting_start), due_at, status" "таблица"
    }

    tasks -> users "FK user_id (CASCADE)"
    emb -> tasks "FK task_id (CASCADE)"
    nt -> users "FK user_id (CASCADE)"
    nt -> tasks "FK task_id (CASCADE); UNIQUE (task_id, kind, due_at)"
    gi -> users "FK user_id (CASCADE)"
  }

  views {
    container db "Schema" "Схема данных GorkyCode: таблицы и внешние ключи" {
      include *
      autoLayout
    }

    styles {
      element "Container" {
        background "#e3f2fd"
      }
    }
  }
}
