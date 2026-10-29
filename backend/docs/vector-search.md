# Векторный поиск: exact и HNSW

## Что реализовано

Название задачи кодирует локальный `intfloat/multilingual-e5-small`:
384 float32, mean pooling, L2-нормализация, префикс `passage:`.
Для темы запроса тот же encoder использует `query:`. Веса, preprocessing
и идентификатор версии модели при интеграции индекса не меняются.

`search_events` объединяет `ILIKE` и семантические совпадения без дубликатов.
Поиск кандидатов удаления/изменения использует тот же репозиторий.
Фильтры: владелец, модель/версия, даты, время там, где оно поддерживается
операцией, тип записи. Существующий фильтр имён в `search_events` сохранён.
Порог cosine distance — `0.16`, отклонение от лучшего — `0.03`, выдача — до 20.

Индекс `task_embeddings_embedding_hnsw_idx` использует
`hnsw (embedding vector_cosine_ops)`, `m=16`, `ef_construction=64`.
Это ANN: он может пропускать соседей, найденных точным поиском.
Совпадение ненулевой выдачи с exact автоматически не гарантируется.

## Режимы и конфигурация

| Переменная | По умолчанию | Значение |
|---|---|---|
| `SEMANTIC_SEARCH_MODE` | `exact` | `exact` или `hnsw` |
| `SEMANTIC_SEARCH_EF_SEARCH` | `100` | Глубина поиска, не меньше candidates, до 1000 |
| `SEMANTIC_SEARCH_CANDIDATES` | `100` | До применения порогов; 20–1000 |
| `SEMANTIC_SEARCH_MAX_SCAN_TUPLES` | `20000` | Ограничение iterative scan; до 1000000 |
| `SEMANTIC_SEARCH_EXACT_THRESHOLD` | `1500` | Exact для малой выборки; 0 отключает проверку |
| `SEMANTIC_SEARCH_ANN_TIMEOUT` | `500ms` | Бюджет ANN, максимум 10s |

Настройки доступны в YAML `semantic_search`, переменные окружения имеют
приоритет. Некорректные значения отклоняются при загрузке backend.
Compose передаёт настройки из корневого окружения; локальный `make run`
читает `backend/.env`. Для применения требуется перезапуск backend.

Exact материализует скалярные расстояния до подходящих записей до
сортировки: HNSW не может незаметно изменить результат этого режима.
В HNSW сначала проверяется ограниченное число задач пользователя с
фильтрами. Их количество — верхняя граница числа совместимых векторов.
Если оно не превышает exact_threshold, выполняется exact.

Иначе выполняется ANN с `hnsw.iterative_scan=strict_order`; настройки
применяются через transaction-local `set_config` на том же соединении.
Внутренняя сортировка содержит только расстояние, внешняя добавляет
детерминированный порядок по датам/времени/ID. Все фильтры применяются
внутри SQL получения кандидатов, до LIMIT; в приложении чужие записи
не используются как промежуточная глобальная выдача.

При ошибке ANN, нехватке 20 сырых кандидатов или отсутствии совпадений
после порога выполняется один exact-запрос в оставшемся бюджете вызывающего
контекста. При уже отменённом общем контексте успешный fallback не обещается.
Если encoder недоступен или весь семантический запрос завершился ошибкой,
сервис сохраняет буквальный поиск. Он может пропустить синонимы.

`mode=hnsw` означает выбранный путь SQL: PostgreSQL вправе предпочесть
обычные индексы и сортировку. Реальное использование HNSW проверяется
планом выполнения, а не значением настройки или наличием индекса.

## Добавление и обновление сущностей

Создание сохраняет задачу даже при недоступности encoder. После вычисления
вектора код блокирует строку задачи, сверяет исходное название и сохраняет
вектор только если название ещё актуально. Переименование сохраняет новое
название и новый вектор одной транзакцией. Удаление работает через FK
`ON DELETE CASCADE`. Изменения HNSW PostgreSQL обслуживает автоматически.

Backfill восстанавливает отсутствующие/несовместимые векторы. По умолчанию
он запрашивает идентичность текущего encoder одним `purpose=query` вызовом.
Для dry-run без encoder передайте `--model` и `--model-version` явно.
Вектор вычисляется вне блокировки; перед записью сверяется название.
Повторный запуск не кодирует уже совместимые записи; ошибки повторяются
при следующем запуске. Проверка версии ответа предотвращает смешение
моделей при смене encoder в середине запуска.

Из backend, с настроенными DATABASE_URL и AI_SERVICE_URL:

```bash
make embedding-backfill ARGS='--dry-run --limit 1000'
make embedding-backfill ARGS='--batch-size 32 --workers 1 --limit 1000'
```

Через Compose после сборки backend:

```bash
docker compose run --rm --entrypoint /app/embedding-backfill backend --dry-run --limit 1000
docker compose run --rm --entrypoint /app/embedding-backfill backend --batch-size 32 --workers 1 --limit 1000
```

Команда возвращает JSON `scanned/stored/skipped/failed/dry_run`; при ошибках
вычисления/записи — ненулевой exit code. Для текущего encoder с одним
worker используйте `--workers 1`, чтобы не перегружать его. Названия и
поисковые запросы не попадают в отчёт. Backfill не вызывает `/api/job` или LLM.
Из-за одного вектора на task_id смена модели с перезаписью не обеспечивает
одновременное обслуживание двух embedding-пространств.

## Включение

1. Проверить версию `vector` (`>=0.8.0` для iterative scans). В Compose
   закреплена `0.8.7` на PostgreSQL 16.
2. Из backend выполнить `make migrate`. Новая миграция — `NO TRANSACTION`,
   индекс создаётся `CONCURRENTLY`. Для его построения нужны диск и память.
3. Проверить `indisvalid` и `indisready`, выполнить dry-run и backfill.
4. Проверить покрытие именно текущей моделью/версией; выполнить
   `ANALYZE tasks; ANALYZE task_embeddings;` после массовой загрузки.
5. Сравнить exact/HNSW на своих данных, включая редких пользователей,
   узкие периоды и конкурирующие записи других владельцев.
6. В корневом `.env` установить `SEMANTIC_SEARCH_MODE=hnsw`, затем
   `docker compose up -d --build backend`. Для локального запуска изменить
   `backend/.env` и перезапустить процесс.

Режим exact оставлен по умолчанию: синтетический benchmark не заменяет
измерения распределения пользовательских данных. Общий ANN-индекс может
быть невыгоден при очень селективных фильтрах. Порог переключения —
настройка нагрузки, а не свойство embedding-модели.

## Проверка и измерения

Обычные интеграционные тесты используют `POSTGRES_TEST_URL`,
`REDIS_TEST_URL`, `LOCAL_EMBED_TEST_URL` (последний включает реальный E5).
`go test -race ./...` включает фильтры, fallback и защиту записи вектора;
без соответствующих URL интеграционные проверки пропускаются.

Benchmark запускайте **только в отдельной тестовой БД** с применёнными
миграциями до `20261022190000`. Он создаёт 10000 синтетических векторов,
четырёх владельцев и оставляет их между фазами для реального построения
индекса. Настройте POSTGRES_TEST_URL и SEMANTIC_REPORT_DIR.

```bash
SEMANTIC_BENCHMARK_PHASE=baseline go test -run '^TestSemanticBenchmark$' -count=1 -v ./internal/storage/postgres
goose -dir migrations postgres "$POSTGRES_TEST_URL" up
SEMANTIC_BENCHMARK_PHASE=indexed go test -run '^TestSemanticBenchmark$' -count=1 -v ./internal/storage/postgres
SEMANTIC_BENCHMARK_PHASE=cleanup go test -run '^TestSemanticBenchmark$' -count=1 ./internal/storage/postgres
```

Опции benchmark: `SEMANTIC_BENCHMARK_ROWS=10000` (1000–100000),
`SEMANTIC_BENCHMARK_EF_SEARCH=100`, `SEMANTIC_BENCHMARK_REUSE=1` для
повторного baseline без перезагрузки fixture. Сравнивайте одинаковые объёмы.
Отчёты содержат p50/p95 SQL без encoder/LLM, raw Recall@20,
различия выдачи после порогов, ложнопустые ответы, размер индекса и планы.
Для `raw Recall@20` знаменатель — min(20, число точных соседей).
Различия на границе при равных расстояниях требуют отдельной трактовки.
Планы ANN должны сниматься с теми же transaction-local настройками.

В прикладных логах `semantic_search` есть requested_mode, mode,
duration_ms, candidates, returned, fallback_reason, failed.
`exact_small` означает малую выборку, `exact_fallback` — резервный путь.
Непустой ответ ANN может быть неполным; его качество проверяется
сравнением с exact, а не только долей пустых ответов.

## Диагностика и откат

```sql
SELECT extversion FROM pg_extension WHERE extname = 'vector';
SELECT indexrelid::regclass, indisready, indisvalid
FROM pg_index WHERE indexrelid = to_regclass('task_embeddings_embedding_hnsw_idx');
SELECT pg_size_pretty(pg_relation_size('task_embeddings_embedding_hnsw_idx'));
SELECT phase, blocks_done, blocks_total FROM pg_stat_progress_create_index;
SELECT count(*) AS total, count(e.task_id) AS compatible
FROM tasks t LEFT JOIN task_embeddings e ON e.task_id=t.id
  AND e.model = '<current model>' AND e.model_version = '<current version>';
```

Для быстрого отката установить `SEMANTIC_SEARCH_MODE=exact` и перезапустить
backend. Данные и индекс сохраняются. Down новой миграции удаляет только
HNSW (`DROP INDEX CONCURRENTLY`); другие более поздние миграции перед откатом
нужно учитывать отдельно.

При неудачном concurrent build проверьте каталог: может остаться INVALID
индекс. Удалите именно невалидный индекс `DROP INDEX CONCURRENTLY ...`
после диагностики и повторите миграцию. `IF NOT EXISTS` намеренно отсутствует:
он мог бы скрыть невалидный/неправильный индекс под ожидаемым именем.

Официальные источники:
[pgvector](https://github.com/pgvector/pgvector#hnsw),
[фильтрация и iterative scans](https://github.com/pgvector/pgvector#filtering),
[PostgreSQL concurrent build](https://www.postgresql.org/docs/16/sql-createindex.html#SQL-CREATEINDEX-CONCURRENTLY).
