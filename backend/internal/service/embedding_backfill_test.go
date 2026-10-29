package service

import (
	"context"
	"errors"
	"gorkycode/backend/internal/model"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type backfillFake struct {
	mu                       sync.Mutex
	tasks                    []model.Task
	saved                    map[uuid.UUID]bool
	maxActive, active, calls int
}

func (f *backfillFake) MissingEmbeddingTasks(_ context.Context, _, _ string, after uuid.UUID, limit int) ([]model.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]model.Task, 0)
	for _, task := range f.tasks {
		if task.ID.String() > after.String() && !f.saved[task.ID] {
			result = append(result, task)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func (f *backfillFake) StoreEmbeddingIfCurrent(_ context.Context, id uuid.UUID, _ string, _ model.EmbeddingResult) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved[id] = true
	return true, nil
}

func (f *backfillFake) Embed(ctx context.Context, purpose, text string) (model.EmbeddingResult, error) {
	f.mu.Lock()
	f.calls++
	f.active++
	f.maxActive = max(f.active, f.maxActive)
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.active--; f.mu.Unlock() }()
	if purpose != "document" {
		return model.EmbeddingResult{}, errors.New("wrong purpose")
	}
	if text == "fail" {
		return model.EmbeddingResult{}, errors.New("encoder failed")
	}
	select {
	case <-ctx.Done():
		return model.EmbeddingResult{}, ctx.Err()
	case <-time.After(time.Millisecond):
	}
	vector := make([]float32, 384)
	vector[0] = 1
	version := "v1"
	if text == "wrong version" {
		version = "v2"
	}
	return model.EmbeddingResult{Vector: vector, Model: "e5", Version: version}, nil
}

func TestBackfillDryRunLimitsRetryAndConcurrency(t *testing.T) {
	f := &backfillFake{saved: make(map[uuid.UUID]bool)}
	for i, title := range []string{"ok", "fail", "wrong version", "ok", "ok"} {
		id := uuid.Nil
		id[15] = byte(i + 1)
		f.tasks = append(f.tasks, model.Task{ID: id, Title: title})
	}
	opt := BackfillOptions{Model: "e5", Version: "v1", BatchSize: 2, Limit: 3, Workers: 2, DryRun: true, RequestTimeout: time.Second}
	report, err := BackfillEmbeddings(t.Context(), f, nil, opt)
	if err != nil || report.Scanned != 3 || report.Stored != 0 || f.calls != 0 {
		t.Fatalf("dry run: %+v %v calls=%d", report, err, f.calls)
	}
	opt.DryRun = false
	opt.Limit = 100
	report, err = BackfillEmbeddings(t.Context(), f, f, opt)
	if err != nil || report.Scanned != 5 || report.Stored != 3 || report.Failed != 2 || f.maxActive > 2 {
		t.Fatalf("first: %+v %v max=%d", report, err, f.maxActive)
	}
	// Only failed records are retried, successful records are not re-encoded.
	f.tasks[1].Title = "ok"
	f.tasks[2].Title = "ok"
	report, err = BackfillEmbeddings(t.Context(), f, f, opt)
	if err != nil || report.Scanned != 2 || report.Stored != 2 {
		t.Fatalf("retry: %+v %v", report, err)
	}
	report, err = BackfillEmbeddings(t.Context(), f, f, opt)
	if err != nil || report.Scanned != 0 {
		t.Fatalf("idempotent: %+v %v", report, err)
	}
}
