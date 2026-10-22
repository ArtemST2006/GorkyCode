// Package service contains internal application services.
package service

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
	task := &model.Task{
		UserId:   cmd.UserID,
		TaskType: cmd.RecordType,
		Title:    cmd.Title,
		Date:     cmd.Date,
		Time:     cmd.Time,
	}

	if err := s.rep.CreateTask(ctx, task); err != nil {
		return nil, err
	}

	return task, nil
}
