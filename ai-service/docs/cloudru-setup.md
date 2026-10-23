# Cloud.ru: доступ и настройки

Актуализировано 23 октября 2026. Cloud.ru — единственный LLM-провайдер сервиса.
`POST /api/embed` использует локальную E5 и не обращается к Cloud.ru.
Команды запуска вынесены в [отдельный гайд](cloudru-live-testing.md).

## Получение ключа

Официальные источники проверены при обновлении документации:
[аутентификация](https://cloud.ru/docs/foundation-models/ug/topics/api-ref__authentication),
[быстрый старт Foundation Models](https://cloud.ru/docs/foundation-models/ug/topics/quickstart).

1. Выберите проект в [консоли Cloud.ru](https://console.cloud.ru/) и проверьте доступ к Foundation Models.
2. Откройте «Пользователи → Сервисные аккаунты» и создайте аккаунт уровня «Проект».
3. В его учётных данных создайте API-ключ для сервиса Foundation Models.
4. Настройте срок и интервал работы ключа, сохраните **Key Secret** при создании.
5. В Foundation Models выберите доступную вашему проекту модель и сверяйте её ID с настройкой сервиса.

В `CLOUDRU_API_KEY` записывается Key Secret. Пример модели в быстром старте:
`ai-sage/GigaChat3-10B-A1.8B`; endpoint — `https://foundation-models.api.cloud.ru/v1`.
Доступ к генерации и квоты зависят от проекта. Реальная генерация проверяется
быстрым live-тестом, а не наличием файла `.env`.

## Файл окружения

Если `.env` отсутствует, скопируйте [`.env.example`](../.env.example), затем заполните ключ.
Существующий `.env` редактируйте выборочно. Launcher читает `KEY=value`, `export KEY=value`,
кавычки и комментарии. Он не исполняет shell-код и не подставляет `${VARIABLE}`.
Приоритет у переменных окружения терминала.

| Переменная | Значение по умолчанию / смысл |
|---|---|
| `LLM_PROVIDER` | `cloudru`, других значений нет |
| `CLOUDRU_API_KEY` | Отсутствует; требуется для `/api/job` |
| `API_KEY` | Резервное имя, только если `CLOUDRU_API_KEY` отсутствует |
| `CLOUDRU_BASE_URL` | `https://foundation-models.api.cloud.ru/v1`; Settings требует HTTPS без credentials/query/fragment |
| `CLOUDRU_MODEL` | `ai-sage/GigaChat3-10B-A1.8B` |
| `CLOUDRU_FORCE_IPV4` | `false` |
| `CLOUDRU_TIMEOUT_SECONDS` | `60`, таймаут SDK-вызова |
| `CLOUDRU_MAX_RETRIES` | `0`, транспортные повторы; допустимо от 0 до 3 |
| `CLOUDRU_MAX_TOKENS` | `2500`, максимум выходных токенов |
| `JOB_TIMEOUT_SECONDS` | `150`, общий deadline обработки `/api/job` |
| `PORT` | `8090` для хостового приложения; Docker launcher публикует внешний `8091` |
| `LOG_LEVEL` | `INFO` |

Пустой `CLOUDRU_API_KEY` не заменяется `API_KEY`. Резервное имя для max tokens —
`CLOUDRU_MAX_COMPLETION_TOKENS`; `CLOUDRU_MAX_TOKENS` имеет приоритет.
Параметры E5 описаны в [embedding-encoder.md](embedding-encoder.md).
Значения уже существующего `.env` сохраняют приоритет над значениями по умолчанию.

## Запросы к модели и повторы

[CloudRuGateway](../app/llm/cloudru.py) использует `AsyncOpenAI` с Cloud.ru base URL
и `chat.completions.create`: `max_tokens`, `temperature=0.5`, `presence_penalty=0`,
`top_p=0.95`. `response_format`, reasoning-параметры и `max_completion_tokens`
в запрос не добавляются. Данные пользователя, контекст, календарные ориентиры
и внутренняя схема передаются модели с системной инструкцией.

Выход проверяется Pydantic-схемой и правилами сервиса. После невалидной completion
или решения допускается одна исправляющая попытка. Она отличается от транспортных
повторов: один `/api/job` может вызвать провайдера несколько раз.

При включённых транспортных повторах короткий числовой `Retry-After` учитывается;
при задержке более 5 секунд сервис возвращает ошибку без раннего повтора.
Учитывайте транспортные и исправляющие попытки при выборе общего job deadline.
Прямой probe из тестового скрипта выполняется с нулём транспортных повторов
и не меняет настройки работающего сервиса.

## Ошибки

| Code | Значение |
|---|---|
| `model_not_configured` | Ключ не задан |
| `model_authentication_failed` | Провайдер ответил 401/403 |
| `model_not_available` | Провайдер ответил 404 |
| `model_rate_limited` | Провайдер ответил 429 |
| `model_request_rejected` | Запрос отклонён провайдером, включая 400/422 |
| `model_unavailable` | Ошибка соединения или upstream 5xx |
| `model_timeout` | Таймаут вызова Cloud.ru |
| `job_timeout` | Общий deadline `/api/job` |
| `model_invalid_output` | Ответ модели не прошёл проверку после исправляющей попытки |

Публичная ошибка имеет формат Problem. Тело ошибки провайдера, ключ и prompt
в неё не включаются. Логи содержат код/класс ошибки и длительность; при timeout
тип причины помогает отличить ожидание соединения от чтения ответа.
Диагностику и реальные результаты смотрите в [гайде полного тестирования](service-live-testing.md).
