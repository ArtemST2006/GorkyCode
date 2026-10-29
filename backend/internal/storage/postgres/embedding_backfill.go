package postgres

import (
	"context"
	"errors"
	"fmt"
	"gorkycode/backend/internal/model"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MissingEmbeddingTasks uses keyset pagination. A restarted run retries failures.
func (r *TaskRepository) MissingEmbeddingTasks(ctx context.Context, modelName, version string, after uuid.UUID, limit int) ([]model.Task, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("batch size must be in 1..1000")
	}
	rows, err := r.pool.Query(ctx, `SELECT t.id, t.title FROM tasks t
		LEFT JOIN task_embeddings e ON e.task_id=t.id
		WHERE t.id > $1 AND (e.task_id IS NULL OR e.model <> $2 OR e.model_version <> $3)
		ORDER BY t.id LIMIT $4`, after, modelName, version, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]model.Task, 0)
	for rows.Next() {
		var task model.Task
		if err := rows.Scan(&task.ID, &task.Title); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// StoreEmbeddingIfCurrent never writes an embedding of a superseded title.
// The network/CPU work must be done before taking this short row lock.
func (r *TaskRepository) StoreEmbeddingIfCurrent(ctx context.Context, taskID uuid.UUID, sourceTitle string, result model.EmbeddingResult) (bool, error) {
	if !validVector(result.Vector) || strings.TrimSpace(result.Model) == "" || strings.TrimSpace(result.Version) == "" {
		return false, fmt.Errorf("invalid document embedding")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	var title string
	err = tx.QueryRow(ctx, "SELECT title FROM tasks WHERE id=$1 FOR UPDATE", taskID).Scan(&title)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if title != sourceTitle {
		return false, nil
	}
	tag, err := tx.Exec(ctx, `INSERT INTO task_embeddings(task_id,embedding,model,model_version)
		VALUES($1,$2::vector,$3,$4)
		ON CONFLICT(task_id) DO UPDATE SET embedding=EXCLUDED.embedding,
		model=EXCLUDED.model, model_version=EXCLUDED.model_version
		WHERE task_embeddings.model <> EXCLUDED.model OR task_embeddings.model_version <> EXCLUDED.model_version`,
		taskID, vectorLiteral(result.Vector), result.Model, result.Version)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
