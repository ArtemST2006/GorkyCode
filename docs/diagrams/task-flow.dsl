workspace {
  model {
    user = person "Пользователь" "Работает с ассистентом через браузер"

    gorky = softwareSystem "GorkyCode" "Голосовой ассистент для задач, напоминаний и встреч" {
      ng = container "Nginx" "Единая точка входа" "nginx"
      be = container "Backend" "HTTP API, правила, операции с БД" "Go 1.26 / chi"
      ai = container "AI-service" "Разбор запроса LLM, ответы, эмбеддинги" "Python 3.12 / FastAPI"
      pg = container "PostgreSQL" "Записи, векторы" "PostgreSQL 16 + pgvector"
      rd = container "Redis" "Контекст диалога (24h)" "Redis 7"
    }

    fm = softwareSystem "Cloud.ru Foundation Models" "LLM GigaChat"

    user -> ng "1: POST /api/task (текст или WAV)" "HTTPS"
    ng -> be "2: проксирование /api/**" "HTTP"
    be -> rd "3: чтение контекста; 13: обновление (TTL 24h)" "TCP"
    be -> ai "4: /api/job; 8: /api/embed; 11: /api/respond" "HTTP"
    ai -> fm "5: chat.completions (схема действия)" "HTTPS"
    fm -> ai "6: ready {action} или needs_clarification" "HTTPS"
    ai -> be "9: embedding 384d; 12: message (сбой - резервный текст)" "HTTP"
    be -> pg "7: операция; 10: task_embeddings" "TCP"
    be -> ng "14: 201 при создании, иначе 200; data + X-Task-Intent" "HTTP"
    ng -> user "15: ответ; после шага 6 - вопрос уточнения" "HTTPS"
  }

  views {
    dynamic gorky "TaskFlow" "Поток обработки POST /api/task" {
      user -> ng "1: POST /api/task (текст или WAV)"
      ng -> be "2: проксирование /api/**"
      be -> rd "3: чтение контекста; 13: обновление (TTL 24h)"
      be -> ai "4: /api/job; 8: /api/embed; 11: /api/respond"
      ai -> fm "5: chat.completions (схема действия)"
      fm -> ai "6: ready {action} или needs_clarification"
      ai -> be "9: embedding 384d; 12: message (сбой - резервный текст)"
      be -> pg "7: операция; 10: task_embeddings"
      be -> ng "14: 201 при создании, иначе 200; data + X-Task-Intent"
      ng -> user "15: ответ; после шага 6 - вопрос уточнения"
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
