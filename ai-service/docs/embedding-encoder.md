# Локальный CPU encoder E5

Актуализировано 23 октября 2026 по [реализации encoder](../app/embeddings/encoder.py)
и [манифесту](../app/embeddings/manifest.json). Endpoint: `POST /api/embed`.
Векторизация работает локально с настоящими весами E5 и не вызывает Cloud.ru.

## Вход и ответ

```json
{"purpose":"document","text":"Подготовить презентацию для команды"}
```

Для поисковой темы используется `purpose=query`. Один запрос содержит один текст.
Ответ имеет ровно три поля: `embedding`, `model`, `model_version`.
`embedding` — 384 конечных float32-совместимых числа с L2-нормой в пределах
`1 ± 1e-3`; ответ содержит `Cache-Control: no-store`.

## Пространство и вычисление

| Параметр | Значение |
|---|---|
| Модель | `intfloat/multilingual-e5-small` |
| Ревизия | `614241f622f53c4eeff9890bdc4f31cfecc418b3` |
| Размерность | 384 |
| Вычисление | CPU float32, eval/inference mode |
| Подготовка | Unicode NFC, strip внешних пробелов; регистр и внутренние пробелы сохраняются |
| Document | Префикс `passage: ` |
| Query | Префикс `query: ` |
| Токены | Правое усечение до 512 вместе с префиксом и special tokens |
| Pooling | Среднее по токенам attention mask без padding |
| Нормализация | L2 float32 |

Префиксы добавляет encoder. Клиент передаёт исходный текст без них.
Веса и tokenizer загружаются один раз при startup из локальных файлов,
с проверкой SHA256 и закреплённых runtime-версий. `trust_remote_code=False`,
`local_files_only=True`; автоматического скачивания во время inference нет.

`model_version` равен `e5-sha256:` + SHA256 канонического JSON манифеста
(ключи отсортированы, компактные separators, UTF-8 без ASCII escaping).
Текущее значение:

```text
e5-sha256:3da8a42dd48932664cf16c461f4fe9ed42bc2e2ab8350907e8d3b398fbe954e2
```

Хеш включает ревизию, файлы, tokenizer/runtime и правила обработки текста.
При его изменении сохранённые векторы нужно пересчитать для совместимого
сравнения. Проверку пары model/model_version выполняет потребитель векторов.

## Настройки и ресурсы

| Переменная | По умолчанию |
|---|---|
| `EMBEDDING_ENABLED` | `true` |
| `EMBEDDING_MODEL_PATH` | `models/multilingual-e5-small`; в Docker `/opt/embedding-model` |
| `EMBEDDING_NUM_THREADS` | `2`, CPU-потоки Torch |
| `EMBEDDING_MAX_WORKERS` | `1`, одновременно занятые encoder workers |
| `EMBEDDING_TIMEOUT_SECONDS` | `10`, deadline embedding-запроса |

[EmbeddingService](../app/services/embeddings.py) выполняет CPU-работу в ограниченном
ThreadPoolExecutor. При заполнении workers возвращается 503, очередь запросов
не накапливается. Timeout/disconnect не освобождает слот до завершения реальной
CPU-операции. Middleware учитывает загрузку тела в общем HTTP deadline.
При отсутствии/невалидности весов процесс остаётся жив: `/api/embed` отдаёт 503,
а `/health` и независимо настроенный `/api/job` продолжают обслуживаться.

| Ситуация | HTTP |
|---|---:|
| Успех | 200 |
| Некорректный JSON/поля/пустой текст | 400 |
| Тело больше 16384 байт, включая chunked | 413 |
| Content-Type не application/json | 415 |
| Неожиданная ошибка вычисления/невалидный результат | 500 |
| Выключен, не загрузился или занят encoder | 503 |
| Истёк deadline | 504 |

Ошибки имеют формат `application/problem+json`. В API нет batch, dimensions,
threshold, индекса и поиска документов; эти параметры не добавляются в запрос.

## Запуск и проверка

```bash
bash scripts/run_host_service.sh --allow-no-key
# Во втором терминале:
python3 scripts/test_service_live.py --runtime host --sections contract,validation,embeddings
```

Для Docker используйте соответствующий launcher и `--runtime docker`.
Отдельная проверка уже скачанных файлов без загрузки:

```bash
.venv/bin/python scripts/download_embedding_model.py models/multilingual-e5-small --check-only
```

Загрузку/проверку также выполняет host launcher; в Docker она идёт при сборке.
[Технические тесты](service-live-testing.md) проверяют формат, детерминизм и HTTP-поведение.
Они не устанавливают качество ранжирования или порог релевантности.
Ожидаемая интеграция: [семантический поиск](semantic-search/backend-handoff.md).
