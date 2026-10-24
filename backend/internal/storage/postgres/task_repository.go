package postgres

import (
	"context"
	"fmt"
	"gorkycode/backend/internal/model"
	"strconv"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// semanticMaxCosineDistance admits only reasonably close title matches;
	// L2-normalized vectors make the cosine distance range 0..2.
	semanticMaxCosineDistance = 0.6
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
	if len(result.Vector) != model.EmbeddingDimension {
		return fmt.Errorf("embedding has %d values, want %d", len(result.Vector), model.EmbeddingDimension)
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
	// The vector is interpolated directly: vectorLiteral() produces digits,
	// dots and commas only, so the string is injection-safe.
	distance := fmt.Sprintf("(e.embedding <=> '%s'::vector)", vectorLiteral(query))
	q := r.sb.
		Select("t.id", "t.user_id", "t.type", "t.title", "t.date", "to_char(t.time, 'HH24:MI')", "t.status", "t.created_at").
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
	return r.fetchTasks(ctx, q)
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
func (r *TaskRepository) DeleteTasks(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) ([]model.Task, error) {
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

// CreateTask inserts a task and fills its ID, creation time and status.
func (r *TaskRepository) CreateTask(ctx context.Context, task *model.Task) error {
	if task == nil || task.Date == nil || task.Time == nil {
		return fmt.Errorf("task date and time are required")
	}
	if task.Type != "task" && task.Type != "reminder" && task.Type != "meeting" {
		return fmt.Errorf("unsupported task type %q", task.Type)
	}

	query, args, err := r.sb.
		Insert("tasks").
		Columns("user_id", "type", "title", "date", "time").
		Values(task.UserID, task.Type, task.Title, *task.Date, *task.Time).
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
