package redis

import (
	"context"
	"errors"
	"gorkycode/backend/internal/model"
	"os"
	"testing"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

func TestTaskProcessDraftsWithRedis(t *testing.T) {
	url := os.Getenv("REDIS_TEST_URL")
	if url == "" {
		t.Skip("set REDIS_TEST_URL to run the Redis integration test")
	}
	options, err := goredis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := goredis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	ctx := t.Context()
	if pingErr := client.Ping(ctx).Err(); pingErr != nil {
		t.Fatal(pingErr)
	}

	store := NewTaskProcess(client)
	key := "task_draft:" + uuid.NewString()
	t.Cleanup(func() { _ = client.Del(context.Background(), key).Err() })

	if _, getErr := store.GetDraft(ctx, key); !errors.Is(getErr, model.ErrDraftNotFound) {
		t.Fatalf("GetDraft() before PutDraft() = %v; want ErrDraftNotFound", getErr)
	}

	value := `{"draft":{},"recent_messages":[]}`
	if putErr := store.PutDraft(ctx, key, value); putErr != nil {
		t.Fatal(putErr)
	}

	got, err := store.GetDraft(ctx, key)
	if err != nil || got != value {
		t.Fatalf("GetDraft() = %q, %v; want %q", got, err, value)
	}
}
