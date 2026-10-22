package postgres

import (
	"context"
	"fmt"
	"gorkycode/backend/internal/model"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TaskCreateRepository stores tasks in PostgreSQL.
type TaskCreateRepository struct {
	pool *pgxpool.Pool
	sb   sq.StatementBuilderType
}

// NewTaskCreateRepository constructs a task repository.
func NewTaskCreateRepository(pool *pgxpool.Pool) *TaskCreateRepository {
	return &TaskCreateRepository{pool: pool, sb: sq.StatementBuilder.PlaceholderFormat(sq.Dollar)}
}

// CreateTask inserts a task and fills its ID, creation time and status.
func (r *TaskCreateRepository) CreateTask(ctx context.Context, task *model.Task) error {
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
