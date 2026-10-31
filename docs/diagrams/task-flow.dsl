workspace {
  model {
    user = person "Пользователь" "Работает с ассистентом через браузер"

    gorky = softwareSystem "GorkyCode" "Голосовой ассистент для задач, напоминаний и встреч" {
      ng = container "Nginx" "Единая точка входа" "nginx"
      be = container "Backend" "HTTP API, авторизация, STT, бизнес-правила, операции с БД" "Go 1.26 / chi"
      ai = container "AI-service" "Разбор запроса через LLM, формулировка ответа, E5-эмбеддинги" "Python 3.12 / FastAPI"
      pg = container "PostgreSQL" "Записи, векторы, уведомления" "PostgreSQL 16 + pgvector"
      rd = container "Redis" "Контекст диалога, сессии, OAuth state" "Redis 7"
    }

    fm = softwareSystem "Cloud.ru Foundation Models" "LLM GigaChat"

    user -> ng "POST /api/task: текст (JSON) или WAV (multipart, до 20 MiB)" "HTTPS"
    ng -> be "Проксирует /api/**" "HTTP"
    be -> rd "Загружает контекст по (user_id, task_id); нет или испорчен — новый диалог" "TCP"
    be -> ai "POST /api/job: текст, request_time, timezone, context" "HTTP"
    ai -> fm "chat.completions.create: схема действия, история до 20 сообщений" "HTTPS"
    fm -> ai "ready {action} или needs_clarification {context}" "HTTPS"
    be -> rd "Ветка уточнения: сохраняет context (TTL 24h, продлевается)" "TCP"
    be -> ng "Ветка уточнения: 200 needs_clarification с task_id и X-Task-Intent" "HTTP"
    ng -> user "Ветка уточнения: вопрос пользователю" "HTTPS"
    be -> pg "Ветка действия: операция (создание, список, поиск, изменение, удаление) одной транзакцией" "TCP"
    be -> ai "Ветка действия: POST /api/embed (E5, purpose document или query)" "HTTP"
    ai -> be "embedding 384d + model_version" "HTTP"
    be -> pg "Ветка действия: task_embeddings (при создании или смене title)" "TCP"
    be -> ai "Ветка действия: POST /api/respond с фактическим результатом" "HTTP"
    ai -> be "message (сбой генерации — резервное сообщение, операция сохраняется)" "HTTP"
    be -> rd "Ветка действия: обновляет context (TTL 24h)" "TCP"
    be -> ng "Ветка действия: 201 при создании, иначе 200; data + X-Task-Intent" "HTTP"
    ng -> user "Ответ ассистента" "HTTPS"
  }

  views {
    dynamic gorky "TaskFlow" "Поток обработки POST /api/task" {
      user -> ng "POST /api/task: текст (JSON) или WAV (multipart, до 20 MiB)" "HTTPS"
      ng -> be "Проксирует /api/**" "HTTP"
      be -> rd "Загружает контекст по (user_id, task_id); нет или испорчен — новый диалог" "TCP"
      be -> ai "POST /api/job: текст, request_time, timezone, context" "HTTP"
      ai -> fm "chat.completions.create: схема действия, история до 20 сообщений" "HTTPS"
      fm -> ai "ready {action} или needs_clarification {context}" "HTTPS"
      be -> rd "Ветка уточнения: сохраняет context (TTL 24h, продлевается)" "TCP"
      be -> ng "Ветка уточнения: 200 needs_clarification с task_id и X-Task-Intent" "HTTP"
      ng -> user "Ветка уточнения: вопрос пользователю" "HTTPS"
      be -> pg "Ветка действия: операция (создание, список, поиск, изменение, удаление) одной транзакцией" "TCP"
      be -> ai "Ветка действия: POST /api/embed (E5, purpose document или query)" "HTTP"
      ai -> be "embedding 384d + model_version" "HTTP"
      be -> pg "Ветка действия: task_embeddings (при создании или смене title)" "TCP"
      be -> ai "Ветка действия: POST /api/respond с фактическим результатом" "HTTP"
      ai -> be "message (сбой генерации — резервное сообщение, операция сохраняется)" "HTTP"
      be -> rd "Ветка действия: обновляет context (TTL 24h)" "TCP"
      be -> ng "Ветка действия: 201 при создании, иначе 200; data + X-Task-Intent" "HTTP"
      ng -> user "Ответ ассистента" "HTTPS"
    }

    styles {
      element "Software System" {
        background "#e8eaf6"
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
