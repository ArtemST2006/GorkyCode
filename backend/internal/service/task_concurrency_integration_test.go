package service

import (
	"context"
	"errors"
	"fmt"
	api "gorkycode/backend/internal/generated"
	"gorkycode/backend/internal/model"
	redisstore "gorkycode/backend/internal/storage/redis"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

func TestParallelDialogueCallsDoNotReachAITwice(t *testing.T) {
	url := os.Getenv("REDIS_TEST_URL")
	if url == "" {
		t.Skip("set REDIS_TEST_URL")
	}
	options, err := goredis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := goredis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	store := redisstore.NewTaskProcess(client)
	owner, dialogue := uuid.New(), uuid.New()
	key := fmt.Sprintf("task_draft:%s:%s", owner, dialogue)
	if err := store.PutDraft(t.Context(), key, `{}`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Del(context.Background(), key).Err() })
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	// Always release the first upstream request, including on assertion failure.
	defer close(release)
	process := NewTaskProcess(&fakeSTT{}, store, &fakeTimezoneStore{timezone: "Europe/Moscow"}, nil, nil, nil, server.URL)
	req := &api.TaskTextRequest{UserId: owner, TaskId: &dialogue, Text: "Назначь встречу"}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := process.Process(ctx, req); done <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first call did not reach AI")
	}
	if _, err := process.Process(ctx, req); !errors.Is(err, model.ErrDialogueBusy) {
		t.Fatalf("parallel turn: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d", calls.Load())
	}
	// Cancellation also releases the lease (rather than blocking the next turn).
	cancel()
	<-done
	if _, err := process.Process(t.Context(), req); !errors.Is(err, model.ErrServiceUnavailable) {
		t.Fatalf("lease after failure: %v", err)
	}
}
