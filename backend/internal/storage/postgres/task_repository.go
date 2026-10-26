package postgres

import (
	"context"
	"fmt"
	"gorkycode/backend/internal/model"
	"math"
	"strconv"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// semanticMaxCosineDistance admits only reasonably close title matches;
	// L2-normalized vectors make the cosine distance range 0..2. In the
	// bundled E5 model unrelated calendar titles often have similarity 0.8+;
	// 0.6 returned nearly every meeting for a person's name.
	semanticMaxCosineDistance = 0.16
	// A global threshold alone admits meetings with different people. Keep
	// only results close to the best match for this particular query.
	semanticMaxDistanceFromBest = 0.03
	// semanticLimit bounds the semantic supplement of the substring search.
	semanticLimit = 20
)

// TaskRepository reads user tasks from PostgreSQL.
type TaskRepository struct {
	pool *pgxpool.Pool
	sb   sq.StatementBuilderType
}

// NewTaskRepository creates a task repository over a connection pool.
func NewTaskRepository(pool *pgxpool.Pool) *TaskRepository {
	return &TaskRepository{
		pool: pool,
		sb:   sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
	}
}

// ListEvents returns the user's dated tasks with from <= date < to, ordered by
// date and time. Empty types means all record types; types are tasks.type
// values (meeting, task, reminder). Tasks without a date never match a period.
func (r *TaskRepository) ListEvents(
	ctx context.Context,
	userID uuid.UUID,
	from, to time.Time,
	types []string,
) ([]model.Task, error) {
	q := r.sb.
		Select("id", "user_id", "type", "title", "date", "to_char(time, 'HH24:MI')", "status", "created_at").
		From("tasks").
		Where(sq.Eq{"user_id": userID}).
		// Dates are passed as YYYY-MM-DD so the time zone of from/to cannot shift the day.
		Where(sq.Expr("date >= ?::date", from.Format(time.DateOnly))).
		Where(sq.Expr("date < ?::date", to.Format(time.DateOnly))).
		OrderBy("date", "time NULLS FIRST", "created_at", "id")
	if len(types) > 0 {
		q = q.Where(sq.Eq{"type": types})
	}
	return r.fetchTasks(ctx, q)
}

// SearchEvents finds titles containing the query without case sensitivity.
// The date bounds are optional and exclude undated tasks only when provided.
func (r *TaskRepository) SearchEvents(
	ctx context.Context,
	userID uuid.UUID,
	query string,
	from, to *time.Time,
	types []string,
) ([]model.Task, error) {
	q := r.sb.
		Select("id", "user_id", "type", "title", "date", "to_char(time, 'HH24:MI')", "status", "created_at").
		From("tasks").
		Where(sq.Eq{"user_id": userID}).
		Where(sq.Expr("title ILIKE ? ESCAPE '!'", "%"+escapeLike(query)+"%"))
	if from != nil {
		q = q.Where(sq.Expr("date >= ?::date", from.Format(time.DateOnly)))
	}
	if to != nil {
		q = q.Where(sq.Expr("date < ?::date", to.Format(time.DateOnly)))
	}
	if len(types) > 0 {
		q = q.Where(sq.Eq{"type": types})
	}
	q = q.OrderBy("date DESC NULLS LAST", "time DESC NULLS LAST", "created_at DESC", "id")
	return r.fetchTasks(ctx, q)
}

// StoreEmbedding upserts the title embedding so the task becomes findable
// by semantic search; the model identity keeps vectors comparable.
func (r *TaskRepository) StoreEmbedding(ctx context.Context, taskID uuid.UUID, result model.EmbeddingResult) error {
	if !validVector(result.Vector) {
		return fmt.Errorf("invalid document embedding vector")
	}
	if strings.TrimSpace(result.Model) == "" || strings.TrimSpace(result.Version) == "" {
		return fmt.Errorf("embedding model identity is missing")
	}

	query, args, err := r.sb.
		Insert("task_embeddings").
		Columns("task_id", "embedding", "model", "model_version").
		Values(taskID, sq.Expr("?::vector", vectorLiteral(result.Vector)), result.Model, result.Version).
		Suffix("ON CONFLICT (task_id) DO UPDATE SET embedding = EXCLUDED.embedding, model = EXCLUDED.model, model_version = EXCLUDED.model_version").
		ToSql()
	if err != nil {
		return fmt.Errorf("build embedding query: %w", err)
	}

	if _, err = r.pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("store embedding: %w", err)
	}
	return nil
}

// FindSemantic returns the owner's tasks whose stored embedding of the given
// model version is closest to the query vector and matches every filter the
// same way FindCandidates does. The result is ordered by cosine distance.
func (r *TaskRepository) FindSemantic(
	ctx context.Context,
	userID uuid.UUID,
	query []float32,
	modelName, version string,
	dateFrom, dateTo *time.Time,
	timeFrom, timeTo *string,
	types []string,
) ([]model.Task, error) {
	if !validVector(query) {
		return nil, fmt.Errorf("invalid query embedding")
	}
	// The vector is interpolated directly: vectorLiteral() produces digits,
	// dots and commas only, so the string is injection-safe.
	distance := fmt.Sprintf("(e.embedding <=> '%s'::vector)", vectorLiteral(query))
	q := r.sb.
		Select("t.id", "t.user_id", "t.type", "t.title", "t.date", "to_char(t.time, 'HH24:MI') AS time_text", "t.status", "t.created_at", distance+" AS distance").
		From("tasks AS t").
		Join("task_embeddings AS e ON e.task_id = t.id").
		Where(sq.Eq{"t.user_id": userID, "e.model": modelName, "e.model_version": version}).
		Where(fmt.Sprintf("%s <= %v", distance, semanticMaxCosineDistance)).
		OrderBy(distance, "t.date", "t.time NULLS FIRST", "t.created_at", "t.id").
		Limit(semanticLimit)
	if dateFrom != nil {
		q = q.Where(sq.Expr("t.date >= ?::date", dateFrom.Format(time.DateOnly)))
	}
	if dateTo != nil {
		q = q.Where(sq.Expr("t.date < ?::date", dateTo.Format(time.DateOnly)))
	}
	if timeFrom != nil {
		q = q.Where(sq.Expr("t.time >= ?::time", *timeFrom))
	}
	if timeTo != nil {
		q = q.Where(sq.Expr("t.time < ?::time", *timeTo))
	}
	if len(types) > 0 {
		q = q.Where(sq.Eq{"t.type": types})
	}
	innerSQL, args, err := q.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build semantic query: %w", err)
	}
	querySQL := fmt.Sprintf(`WITH ranked AS (%s)
		SELECT id, user_id, type, title, date, time_text, status, created_at
		FROM ranked
		WHERE distance <= (SELECT MIN(distance) + %v FROM ranked)
		ORDER BY distance, date, time_text NULLS FIRST, created_at, id`, innerSQL, semanticMaxDistanceFromBest)
	return r.fetchTasksSQL(ctx, querySQL, args...)
}

func validVector(vector []float32) bool {
	if len(vector) != model.EmbeddingDimension {
		return false
	}
	nonzero := false
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
		nonzero = nonzero || value != 0
	}
	return nonzero
}

// vectorLiteral formats an embedding as the pgvector literal "[v1,v2,...]".
func vectorLiteral(vector []float32) string {
	parts := make([]string, len(vector))
	for i, value := range vector {
		parts[i] = strconv.FormatFloat(float64(value), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// FindCandidates filters the user's tasks for on_delete by title substring,
// dates, times and record types: every filled filter must match (AND).
// Date bounds are inclusive and exclusive respectively; the time bounds
// repeat the same rule for the time column, so tasks without a time never
// match a time filter. A date filter excludes undated tasks.
func (r *TaskRepository) FindCandidates(
	ctx context.Context,
	userID uuid.UUID,
	title string,
	dateFrom, dateTo *time.Time,
	timeFrom, timeTo *string,
	types []string,
) ([]model.Task, error) {
	q := r.sb.
		Select("id", "user_id", "type", "title", "date", "to_char(time, 'HH24:MI')", "status", "created_at").
		From("tasks").
		Where(sq.Eq{"user_id": userID})
	if title != "" {
		q = q.Where(sq.Expr("title ILIKE ? ESCAPE '!'", "%"+escapeLike(title)+"%"))
	}
	if dateFrom != nil {
		q = q.Where(sq.Expr("date >= ?::date", dateFrom.Format(time.DateOnly)))
	}
	if dateTo != nil {
		q = q.Where(sq.Expr("date < ?::date", dateTo.Format(time.DateOnly)))
	}
	if timeFrom != nil {
		q = q.Where(sq.Expr("time >= ?::time", *timeFrom))
	}
	if timeTo != nil {
		q = q.Where(sq.Expr("time < ?::time", *timeTo))
	}
	if len(types) > 0 {
		q = q.Where(sq.Eq{"type": types})
	}
	q = q.OrderBy("date", "time NULLS FIRST", "created_at", "id")
	return r.fetchTasks(ctx, q)
}

// DeleteTasks removes every ID in one transaction, but only while each ID
// belongs to the user. When any row is missing or owned by someone else, the
// transaction is rolled back and model.ErrStaleSelection is returned.
func (r *TaskRepository) DeleteTasks(ctx context.Context, userID uuid.UUID, ids []uuid.UUID, expected []model.Task) ([]model.Task, error) {
	if len(ids) == 0 {
		return nil, model.ErrStaleSelection
	}
	query, args, err := r.sb.
		Delete("tasks").
		Where(sq.Eq{"user_id": userID}).
		Where(sq.Eq{"id": ids}).
		Suffix("RETURNING id, user_id, type, title, date, to_char(time, 'HH24:MI'), status, created_at").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("build delete query: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin delete transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	current, err := r.lockTaskRows(ctx, tx, userID, ids)
	if err != nil {
		return nil, err
	}
	snapshots := make(map[uuid.UUID]model.Task, len(expected))
	for _, item := range expected {
		snapshots[item.ID] = item
	}
	if len(current) != len(ids) {
		return nil, model.ErrStaleSelection
	}
	for _, id := range ids {
		old, shown := snapshots[id]
		if !shown || !sameTaskContents(current[id], old) {
			return nil, model.ErrStaleSelection
		}
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("delete tasks: %w", err)
	}
	tasks := make([]model.Task, 0, len(ids))
	for rows.Next() {
		var task model.Task
		if err = rows.Scan(
			&task.ID,
			&task.UserID,
			&task.Type,
			&task.Title,
			&task.Date,
			&task.Time,
			&task.Status,
			&task.CreatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan deleted task: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read deleted tasks: %w", err)
	}
	rows.Close()

	if len(tasks) != len(ids) {
		return nil, model.ErrStaleSelection
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit delete transaction: %w", err)
	}
	return tasks, nil
}

// Lock rows in the same order for all UPDATE/DELETE operations so overlapping
// multi-record confirmations cannot acquire locks in opposite orders.
func (r *TaskRepository) lockTaskRows(ctx context.Context, tx pgx.Tx, userID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]model.Task, error) {
	query, args, err := r.sb.
		Select("id", "user_id", "type", "title", "date", "to_char(time, 'HH24:MI')", "status", "created_at").
		From("tasks").Where(sq.Eq{"user_id": userID, "id": ids}).
		OrderBy("id").Suffix("FOR UPDATE").ToSql()
	if err != nil {
		return nil, fmt.Errorf("build candidate lock query: %w", err)
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lock candidates: %w", err)
	}
	defer rows.Close()
	current := make(map[uuid.UUID]model.Task, len(ids))
	for rows.Next() {
		var task model.Task
		if err := rows.Scan(&task.ID, &task.UserID, &task.Type, &task.Title, &task.Date, &task.Time, &task.Status, &task.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		current[task.ID] = task
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read candidates: %w", err)
	}
	return current, nil
}

// UpdateTasks checks the shown row snapshots under row locks, then updates all
// selected records and their title embeddings in a single transaction.
func (r *TaskRepository) UpdateTasks(
	ctx context.Context, userID uuid.UUID, ids []uuid.UUID, expected []model.Task,
	changes model.TaskChanges, embedding *model.EmbeddingResult,
) ([]model.Task, error) {
	if len(ids) == 0 {
		return nil, model.ErrStaleSelection
	}
	if changes.Title != nil && (embedding == nil || !validVector(embedding.Vector)) {
		return nil, fmt.Errorf("updated title needs a valid embedding")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin update transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := r.lockTaskRows(ctx, tx, userID, ids)
	if err != nil {
		return nil, err
	}
	snapshots := make(map[uuid.UUID]model.Task, len(expected))
	for _, item := range expected {
		snapshots[item.ID] = item
	}
	if len(current) != len(ids) {
		return nil, model.ErrStaleSelection
	}
	for _, id := range ids {
		old, shown := snapshots[id]
		if !shown || !sameTaskSnapshot(current[id], old) {
			return nil, model.ErrStaleSelection
		}
	}
	updated := make([]model.Task, 0, len(ids))
	for _, id := range ids {
		item := current[id]
		if changes.Title != nil {
			item.Title = *changes.Title
		}
		if changes.Date != nil {
			date := *changes.Date
			item.Date = &date
		}
		if changes.Time != nil {
			value := *changes.Time
			item.Time = &value
		}
		if changes.Status != nil {
			item.Status = *changes.Status
		}
		if item.Time != nil && item.Date == nil {
			return nil, model.ErrStaleSelection
		}
		query, values, err := r.sb.Update("tasks").
			Set("title", item.Title).Set("date", item.Date).Set("time", item.Time).
			Set("status", item.Status).Where(sq.Eq{"id": id, "user_id": userID}).
			Suffix("RETURNING id, user_id, type, title, date, to_char(time, 'HH24:MI'), status, created_at").ToSql()
		if err != nil {
			return nil, fmt.Errorf("build task update: %w", err)
		}
		var saved model.Task
		if err := tx.QueryRow(ctx, query, values...).Scan(&saved.ID, &saved.UserID, &saved.Type, &saved.Title, &saved.Date, &saved.Time, &saved.Status, &saved.CreatedAt); err != nil {
			return nil, fmt.Errorf("update task: %w", err)
		}
		if changes.Title != nil {
			vector := vectorLiteral(embedding.Vector)
			if _, err := tx.Exec(ctx, `INSERT INTO task_embeddings (task_id, embedding, model, model_version)
				VALUES ($1, $2::vector, $3, $4)
				ON CONFLICT (task_id) DO UPDATE SET embedding=EXCLUDED.embedding,
				model=EXCLUDED.model, model_version=EXCLUDED.model_version`,
				id, vector, embedding.Model, embedding.Version); err != nil {
				return nil, fmt.Errorf("store updated title embedding: %w", err)
			}
		}
		updated = append(updated, saved)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit task updates: %w", err)
	}
	return updated, nil
}

func sameTaskSnapshot(current, shown model.Task) bool {
	return current.CreatedAt.Equal(shown.CreatedAt) && sameTaskContents(current, shown)
}

func sameTaskContents(current, shown model.Task) bool {
	if current.ID != shown.ID || current.UserID != shown.UserID || current.Type != shown.Type ||
		current.Title != shown.Title || current.Status != shown.Status {
		return false
	}
	if (current.Date == nil) != (shown.Date == nil) || (current.Time == nil) != (shown.Time == nil) {
		return false
	}
	if current.Date != nil && !current.Date.Equal(*shown.Date) {
		return false
	}
	return current.Time == nil || *current.Time == *shown.Time
}

// CreateTask inserts a task and fills its ID, creation time and status.
func (r *TaskRepository) CreateTask(ctx context.Context, task *model.Task) error {
	if task == nil || task.Date == nil {
		return fmt.Errorf("task date is required")
	}
	if task.Type != "task" && task.Type != "reminder" && task.Type != "meeting" {
		return fmt.Errorf("unsupported task type %q", task.Type)
	}
	if task.Type != "task" && task.Time == nil {
		return fmt.Errorf("task time is required for %s", task.Type)
	}

	query, args, err := r.sb.
		Insert("tasks").
		Columns("user_id", "type", "title", "date", "time").
		Values(task.UserID, task.Type, task.Title, *task.Date, task.Time).
		Suffix("RETURNING id, created_at, status").
		ToSql()
	if err != nil {
		return err
	}

	err = r.pool.QueryRow(ctx, query, args...).Scan(&task.ID, &task.CreatedAt, &task.Status)
	if err != nil {
		return err
	}

	return nil
}

func escapeLike(query string) string {
	replacer := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")
	return replacer.Replace(query)
}

func (r *TaskRepository) fetchTasks(ctx context.Context, q sq.SelectBuilder) ([]model.Task, error) {
	query, args, err := q.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build tasks query: %w", err)
	}
	return r.fetchTasksSQL(ctx, query, args...)
}

func (r *TaskRepository) fetchTasksSQL(ctx context.Context, query string, args ...any) ([]model.Task, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]model.Task, 0)
	for rows.Next() {
		var task model.Task
		if err = rows.Scan(
			&task.ID,
			&task.UserID,
			&task.Type,
			&task.Title,
			&task.Date,
			&task.Time,
			&task.Status,
			&task.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read tasks: %w", err)
	}

	return tasks, nil
}
