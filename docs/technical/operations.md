# Operations


## 1. Health checks

- GET /health (backend) — жив ли процесс.
- GET /ready (backend) — готовность, включает PostgreSQL; 503 при недоступности.
- GET /health (ai-service) — жив ли процесс; готовность Cloud.ru и E5 не проверяет.
- Compose healthchecks: postgres (pg_isready), redis (ping), ai-service (/health) — управляют порядком старта.

Контейнеры перезапускаются автоматически (restart: unless-stopped). Backend стартует после готовности postgres и redis, nginx — после backend и ai-service.

## 2. Логирование

- Backend: структурный slog, уровень LOG_LEVEL (по умолчанию info). Ошибки логируются с операцией, task_id и user_id.
- ai-service: уровень LOG_LEVEL; при ошибке LLM логируются код и длительность, ключ и prompt не пишутся.
- Агрегации нет: docker-compose logs сервис.


## 3. Диагностика типовых проблем

- /ready возвращает 503 — PostgreSQL недоступен: логи postgres, креды и порт в DATABASE_URL.
- /api/task возвращает 502/504 — недоступен ai-service или Cloud.ru: проверить curl localhost:8090/health, ключ CLOUDRU_API_KEY, квоты; в логах ai-service искать код model_*.
- /api/task возвращает 503 — недоступен Redis или база.
- Разбор запросов деградировал — в логах ai-service код model_invalid_output (модель отвечает вне схемы); проверить смену CLOUDRU_MODEL.
- Диалог «забыл» контекст — Redis перезапускался или истекли 24 часа; ожидаемое поведение.
- Голос не распознаётся — скачана ли модель (model/asr-model/tokens.txt), формат WAV (.wav или .wave, mono 16 kHz, до 20 MiB); прогнать make -C backend test-asr.
- Уведомления не приходят — была ли WS-авторизация в течение 5 секунд (код 4408), есть ли строка в notifications, статус записи active; для писем — настроен ли SMTP.
- Письма не отправляются — пустые SMTP_HOST или EMAIL_FROM; в разработке проверять Mailpit на :8025.
- Календарь не подключается — consent screen в Testing: тестовый пользователь добавлен? redirect URI совпадает с GOOGLE_REDIRECT_URL? refresh token истёк через 7 дней — подключить заново.
- Встреча не создана, 409 — занятое время (meeting_conflict): ожидаемое поведение, предложить другое время в том же диалоге.

## 4. Диагностические утилиты (backend/cmd)

- wsprobe — проверка WebSocket-уведомлений на живом стенде (auth → ready → notification → ack).
- asrbench, sttbench — замеры точности и задержки распознавания.
- memtest — потребление памяти (использовался при сравнении моделей).

## 5. Бенчмарки ASR

Отчёт ASR_BENCHMARK_REPORT.pdf (technical/): 40 записей, 16 kHz mono, замер 30.10.2026.

Замеры по 40 записям, 16 kHz mono: CER — ошибки на символ, задержка — средняя на реплику, RAM — максимальная память процесса.

- GigaAM v3 int8: CER 0.0073, 32 реплики из 40 без ошибок, 240.8 мс, 330 МБ.
- Zipformer int8: CER 0.0183, 21/40, 116.0 мс, 303 МБ.
- Zipformer fp32: CER 0.0121, 23/40, 181.4 мс, 632 МБ.

Обе конфигурации поддерживаются кодом, выбор — по наличию файлов модели. GigaAM точнее, Zipformer быстрее и экономнее по памяти.

## 6. Обслуживание данных

- Неподтверждённые уведомления удаляются по REMINDER_RETENTION (720 часов, 30 дней).
- Пересогласование уведомлений: окно REMINDER_HORIZON (168 часов), батч 100, не более 500 задач за проход.
- Redis чистится по TTL автоматически.
- docker-compose down сохраняет volumes; down -v удаляет данные.

