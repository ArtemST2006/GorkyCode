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

// CreateTaskCommand is a domain command built from a validated AI action.
type CreateTaskCommand struct {
	UserID     uuid.UUID
	RecordType string
	Title      string
	Date       time.Time
	Time       time.Time
}

// TaskCreate creates tasks from validated commands.
type TaskCreate struct {
	rep TaskCreateRepository
}

// NewTaskCreateService constructs a task creation service.
func NewTaskCreateService(rep TaskCreateRepository) *TaskCreate {
	return &TaskCreate{rep: rep}
}

// Create saves a task and returns the stored record.
func (s *TaskCreate) Create(ctx context.Context, cmd CreateTaskCommand) (*model.Task, error) {
	clock := cmd.Time.Format("15:04")
	task := &model.Task{
		UserID: cmd.UserID,
		Type:   cmd.RecordType,
		Title:  cmd.Title,
		Date:   &cmd.Date,
		Time:   &clock,
	}

	if err := s.rep.CreateTask(ctx, task); err != nil {
		return nil, err
	}

	return task, nil
}
