workspace {
  model {
    user = person "Пользователь" "Работает с ассистентом через браузер"

    gorky = softwareSystem "GorkyCode" "Голосовой ассистент для задач, напоминаний и встреч"

    fe = container gorky "Frontend" "Nuxt, собранная статика; исходники вне репозитория" "SPA"
    ng = container gorky "Nginx" "Единая точка входа: порт 8088 публичный, 8091 внутренний к ai-service" "nginx"
    be = container gorky "Backend" "HTTP API, авторизация, распознавание речи, бизнес-правила, планировщик напоминаний, WebSocket-хаб, email" "Go 1.26 / chi"
    ai = container gorky "AI-service" "Разбор запроса через LLM, формулировка ответа, E5-эмбеддинги" "Python 3.12 / FastAPI"
    pg = container gorky "PostgreSQL" "Записи, пользователи, векторы, подключения Google, уведомления" "PostgreSQL 16 + pgvector"
    rd = container gorky "Redis" "Контекст диалога (24h), сессии (24h), OAuth state (10m), кандидаты" "Redis 7"
    mp = container gorky "Mailpit" "Dev-SMTP с веб-интерфейсом" "Mailpit"

    fm = softwareSystem "Cloud.ru Foundation Models" "LLM GigaChat, OpenAI-совместимый API"
    gc = softwareSystem "Google Calendar API" "OAuth, freebusy, создание событий"

    user -> fe "Открывает интерфейс" "HTTPS"
    user -> ng "API-запросы и WebSocket" "HTTPS"
    ng -> fe "Раздаёт статику"
    ng -> be "Проксирует /api/**" "HTTP; таймаут 240s, WS 3600s"
    be -> ai "/api/job, /api/respond, /api/embed через Nginx:8091" "HTTP"
    be -> pg "SQL: записи, векторы, уведомления" "TCP; таймаут 3s"
    be -> rd "Сессии, контекст диалога, OAuth state, снимки кандидатов" "TCP"
    ai -> fm "chat.completions" "HTTPS; таймаут 60s, 1 исправляющая попытка"
    be -> gc "Создание встреч, freebusy" "HTTPS; таймаут 3s на вызов"
    be -> mp "Письма напоминаний" "SMTP; таймаут 45s"
  }

  views {
    container gorky "Containers" "Диаграмма контейнеров GorkyCode" {
      include *
      autoLayout
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
