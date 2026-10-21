package postgres

import (
	"context"
	"fmt"
	"gorkycode/backend/internal/model"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
// date and time. Empty types means all record types; types use the values
// stored in tasks.type. Tasks without a date never match a period.
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

	query, args, err := q.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build list tasks query: %w", err)
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
