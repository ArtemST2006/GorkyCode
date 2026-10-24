package subservice

import (
	"context"
	"errors"
	"gorkycode/backend/internal/model"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeEmbedder returns the configured embedding and records its arguments.
type fakeEmbedder struct {
	called  bool
	purpose string
	text    string
	result  model.EmbeddingResult
	err     error
}

func (f *fakeEmbedder) Embed(_ context.Context, purpose, text string) (model.EmbeddingResult, error) {
	f.called = true
	f.purpose, f.text = purpose, text
	if f.err != nil {
		return model.EmbeddingResult{}, f.err
	}
	return f.result, nil
}

func embeddingResult(vector ...float32) model.EmbeddingResult {
	return model.EmbeddingResult{Vector: vector, Model: "intfloat/multilingual-e5-small", Version: "v1"}
}

type fakeTaskRepository struct {
	task   *model.Task
	err    error
	called bool
}

func (f *fakeTaskRepository) CreateTask(_ context.Context, task *model.Task) error {
	f.called = true
	f.task = task
	if f.err != nil {
		return f.err
	}
	task.ID = uuid.New()
	task.CreatedAt = time.Now()
	task.Status = "active"
	return nil
}

type fakeEmbeddingStore struct {
	called bool
	taskID uuid.UUID
	result model.EmbeddingResult
}

func (f *fakeEmbeddingStore) StoreEmbedding(_ context.Context, taskID uuid.UUID, result model.EmbeddingResult) error {
	f.called = true
	f.taskID, f.result = taskID, result
	return nil
}

func createCommand() CreateTaskCommand {
	date, _ := time.Parse(time.DateOnly, "2026-10-20")
	clock, _ := time.Parse("15:04", "15:30")
	return CreateTaskCommand{
		UserID:     uuid.New(),
		RecordType: "meeting",
		Title:      "Встреча с Иваном",
		Date:       date,
		Time:       clock,
	}
}

func TestCreateStoresTaskEmbedding(t *testing.T) {
	embedder := &fakeEmbedder{result: embeddingResult(1, 2)}
	store := &fakeEmbeddingStore{}
	rep := &fakeTaskRepository{}

	task, err := NewTaskCreateService(rep, store, embedder).Create(t.Context(), createCommand())
	if err != nil {
		t.Fatal(err)
	}

	if !embedder.called || embedder.purpose != "document" || embedder.text != "Встреча с Иваном" {
		t.Fatalf("embedder = %+v", embedder)
	}
	if !store.called || store.taskID != task.ID || len(store.result.Vector) != 2 || store.result.Model != "intfloat/multilingual-e5-small" {
		t.Fatalf("stored embedding = %+v into task %s", store.result, store.taskID)
	}
}

func TestCreateKeepsWorkingWithoutEmbedding(t *testing.T) {
	embedder := &fakeEmbedder{err: errors.New("embed endpoint down")}
	store := &fakeEmbeddingStore{taskID: uuid.New()} // stored must be untouched
	rep := &fakeTaskRepository{}

	task, err := NewTaskCreateService(rep, store, embedder).Create(t.Context(), createCommand())
	if err != nil {
		t.Fatal(err)
	}
	if store.called {
		t.Fatal("StoreEmbedding() must not run without an embedding")
	}
	if task.ID == uuid.Nil {
		t.Fatal("task must be saved")
	}
}

func TestCreateWithoutEmbedderStoresNothingEmbedding(t *testing.T) {
	store := &fakeEmbeddingStore{}
	rep := &fakeTaskRepository{}
	embedder := &fakeEmbedder{}

	if _, err := NewTaskCreateService(rep, store, nil).Create(t.Context(), createCommand()); err != nil {
		t.Fatal(err)
	}
	if embedder.called || store.called {
		t.Fatalf("embedder called = %v, store called = %v", embedder.called, store.called)
	}
}
