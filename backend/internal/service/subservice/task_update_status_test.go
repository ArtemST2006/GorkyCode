package subservice

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	api "gorkycode/backend/internal/generated"
	"gorkycode/backend/internal/model"

	"github.com/google/uuid"
)

type statusUpdateRepository struct {
	called bool
}

func (r *statusUpdateRepository) UpdateTasks(_ context.Context, _ uuid.UUID, _ []uuid.UUID, _ []model.Task, _ model.TaskChanges, _ *model.EmbeddingResult) ([]model.Task, error) {
	r.called = true
	return nil, nil
}

func TestTaskUpdateProgressFiltersMixedCandidates(t *testing.T) {
	userID := uuid.New()
	meeting := deleteTask(t, userID, "Проектная встреча")
	task := deleteTask(t, userID, "Проект")
	task.Type = "task"
	reminder := deleteTask(t, userID, "Проектное напоминание")
	reminder.Type = "reminder"
	store := &fakeTaskDeleter{tasks: []model.Task{meeting, task, reminder}}
	service := NewTaskUpdateService(&statusUpdateRepository{}, NewTaskDeleteService(store, nil), nil)
	action := json.RawMessage(`{"phase":"find_candidates","title_query":"Проект","changes":{"status":"in_progress"}}`)

	set, err := service.FindCandidates(t.Context(), userID, action)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Items) != 1 || len(set.Stored) != 1 || len(set.Snapshots) != 1 {
		t.Fatalf("candidate sets have wrong sizes: items=%d stored=%d snapshots=%d", len(set.Items), len(set.Stored), len(set.Snapshots))
	}
	if set.Items[0].Number != 1 || set.Stored[0].Number != 1 || set.Items[0].Event.Id != task.ID || set.Stored[0].Id != task.ID || set.Snapshots[0].ID != task.ID {
		t.Fatalf("candidate sets diverged: %+v", set)
	}
}

func TestTaskUpdateProgressRejectsNonTaskConfirmation(t *testing.T) {
	userID := uuid.New()
	meeting := deleteTask(t, userID, "Проектная встреча")
	repository := &statusUpdateRepository{}
	service := NewTaskUpdateService(repository, nil, nil)
	status := api.TaskStatusInProgress
	action := json.RawMessage(`{"phase":"update_confirmed","record_ids":["` + meeting.ID.String() + `"]}`)
	_, err := service.UpdateConfirmed(t.Context(), userID, action, []model.Task{meeting}, api.UpdateChanges{Status: &status})
	if !errors.Is(err, model.ErrInvalidAction) || repository.called {
		t.Fatalf("error=%v, repository called=%v", err, repository.called)
	}
}
