// Package subservice contains internal application services.
package subservice

import (
	"context"
	"gorkycode/backend/internal/model"
	"time"

	"github.com/google/uuid"
)

// TaskCreateRepository persists new tasks.
type TaskCreateRepository interface {
	CreateTask(ctx context.Context, task *model.Task) error
}

// EmbeddingStore persists computed embeddings for semantic search.
type EmbeddingStore interface {
	StoreEmbedding(ctx context.Context, taskID uuid.UUID, result model.EmbeddingResult) error
}

// CreateTaskCommand is a domain command built from a validated AI action.
type CreateTaskCommand struct {
	UserID     uuid.UUID
	RecordType string
	Title      string
	Date       time.Time
	Time       *time.Time
}

// TaskCreate creates tasks from validated commands and keeps their
// embeddings findable.
type TaskCreate struct {
	rep        TaskCreateRepository
	embeddings EmbeddingStore
	embedder   model.Embedder
}

// NewTaskCreateService constructs a task creation service.
func NewTaskCreateService(rep TaskCreateRepository, embeddings EmbeddingStore, embedder model.Embedder) *TaskCreate {
	return &TaskCreate{rep: rep, embeddings: embeddings, embedder: embedder}
}

// Create saves a task and returns the stored record.
func (s *TaskCreate) Create(ctx context.Context, cmd CreateTaskCommand) (*model.Task, error) {
	var clock *string
	if cmd.Time != nil {
		value := cmd.Time.Format("15:04")
		clock = &value
	}
	task := &model.Task{
		UserID: cmd.UserID,
		Type:   cmd.RecordType,
		Title:  cmd.Title,
		Date:   &cmd.Date,
		Time:   clock,
	}

	if err := s.rep.CreateTask(ctx, task); err != nil {
		return nil, err
	}

	s.embedTask(ctx, task)

	return task, nil
}

// embedTask best-effort stores the title embedding: without it the task is
// just invisible to semantic search, which must not fail the creation.
func (s *TaskCreate) embedTask(ctx context.Context, task *model.Task) {
	if s.embedder == nil || s.embeddings == nil || task == nil {
		return
	}
	result, err := s.embedder.Embed(ctx, "document", task.Title)
	if err != nil {
		return
	}
	_ = s.embeddings.StoreEmbedding(ctx, task.ID, result)
}
