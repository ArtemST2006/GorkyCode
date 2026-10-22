package postgres

import (
	"context"
	"fmt"
	"gorkycode/backend/internal/model"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgxpool"
)

var recordTypeToDB = map[string]string{
	"meeting":  "встреча",
	"task":     "задача",
	"reminder": "напоминание",
}

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
	recordType, ok := recordTypeToDB[task.TaskType]
	if !ok {
		return fmt.Errorf("unsupported task type %q", task.TaskType)
	}

	query, args, err := r.sb.
		Insert("tasks").
		Columns("user_id", "type", "title", "date", "time").
		Values(task.UserId, recordType, task.Title, task.Date, task.Time).
		Suffix("RETURNING id, created_at, status").
		ToSql()
	if err != nil {
		return err
	}

	err = r.pool.QueryRow(ctx, query, args...).Scan(&task.Id, &task.CreatedAt, &task.Status)
	if err != nil {
		return err
	}

	return nil
}
