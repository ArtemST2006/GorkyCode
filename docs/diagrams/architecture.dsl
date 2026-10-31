workspace {
  model {
    user = person "Пользователь" "Работает с ассистентом через браузер"

    gorky = softwareSystem "GorkyCode" "Голосовой ассистент для задач, напоминаний и встреч" {
      fe = container "Frontend" "Nuxt, собранная статика" "SPA"
      ng = container "Nginx" "Единая точка входа: 8088 публичный, 8091 к ai-service" "nginx"

      be = container "Backend" "HTTP API, авторизация, STT, правила, уведомления" "Go 1.26 / chi" {
        api = component "HTTP API" "Хендлеры: /api/task, auth, integrations, WS" "Go"
        core = component "Сервисный слой" "Бизнес-правила: обработка задач, конфликты, календарь" "Go"
        store = component "Хранилище" "Репозитории PostgreSQL и Redis" "Go"
        stt = component "STT" "Распознавание речи" "sherpa-onnx"
        sched = component "Scheduler" "Планировщик напоминаний" "Go goroutine"
        hub = component "WebSocket-хаб" "Доставка уведомлений, ack" "Go goroutine"
        mailer = component "Email-воркер" "Отправка писем" "Go goroutine"
      }

      ai = container "AI-service" "Разбор запроса LLM, ответы, эмбеддинги" "Python 3.12 / FastAPI" {
        job = component "Job API" "POST /api/job: интент и поля" "FastAPI"
        respond = component "Respond API" "POST /api/respond: формулировка ответа" "FastAPI"
        embed = component "Embed API" "POST /api/embed: вектор текста" "FastAPI"
        ctx = component "Контекст диалога" "История (20 сообщений), черновики, выбор кандидатов" "Python"
        gw = component "LLM Gateway" "Вызовы Cloud.ru" "AsyncOpenAI"
        e5 = component "E5 encoder" "Векторизация, 384d, CPU" "torch"
      }

      pg = container "PostgreSQL" "Записи, векторы, уведомления" "PostgreSQL 16 + pgvector"
      rd = container "Redis" "Контекст (24h), сессии, OAuth state" "Redis 7"
    }

    fm = softwareSystem "Cloud.ru Foundation Models" "LLM GigaChat"
    gc = softwareSystem "Google Calendar API" "OAuth, freebusy, события"

    user -> ng "API и WebSocket" "HTTPS"
    ng -> fe "Раздаёт статику"
    ng -> be "Проксирует /api/**" "HTTP"
    api -> core "Действия над записями"
    api -> stt "WAV в текст"
    core -> store "Читает и пишет данные"
    api -> hub "Уведомления"
    sched -> store "Моменты уведомлений"
    sched -> hub "dispatch(notification)"
    mailer -> store "Статусы писем"
    job -> gw "chat.completions"
    job -> ctx "Контекст и история"
    respond -> gw "Формулировка сообщения"
    embed -> e5 "Векторизация текста"
    be -> pg "SQL, таймаут 3s" "TCP"
    be -> rd "Сессии, контекст, state" "TCP"
    ai -> fm "HTTPS, таймаут 60s"
    be -> gc "Создание встреч, freebusy" "HTTPS"
  }

  views {
    systemContext gorky "SystemContext" "GorkyCode и внешние системы" {
      include *
      autoLayout
    }

    container gorky "Containers" "Диаграмма контейнеров GorkyCode" {
      include *
      autoLayout
    }

    component be "BackendComponents" "Компоненты backend" {
      include *
      autoLayout
    }

    component ai "AIServiceComponents" "Компоненты ai-service" {
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
      element "Component" {
        background "#e8f5e9"
      }
      element "Person" {
        background "#fff3e0"
      }
    }
  }
}
