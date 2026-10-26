package redis

import (
	"context"
	"errors"
	"gorkycode/backend/internal/model"
	"os"
	"testing"
	"time"

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
	ttl, err := client.TTL(ctx, key).Result()
	if err != nil || ttl <= 0 || ttl > taskDraftTTL {
		t.Fatalf("draft TTL = %s, %v; want (0, %s]", ttl, err, taskDraftTTL)
	}
	if err := store.DeleteDraft(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetDraft(ctx, key); !errors.Is(err, model.ErrDraftNotFound) {
		t.Fatalf("GetDraft() after DeleteDraft() = %v; want ErrDraftNotFound", err)
	}

	other := key + ":candidates"
	t.Cleanup(func() { _ = client.Del(context.Background(), other).Err() })
	if err := store.PutDrafts(ctx, map[string]string{key: "context", other: "candidates"}); err != nil {
		t.Fatal(err)
	}
	values, err := client.MGet(ctx, key, other).Result()
	if err != nil || values[0] != "context" || values[1] != "candidates" {
		t.Fatalf("saved pair = %v, %v", values, err)
	}
	for _, k := range []string{key, other} {
		if ttl := client.TTL(ctx, k).Val(); ttl <= 0 || ttl > taskDraftTTL {
			t.Fatalf("invalid TTL %v", ttl)
		}
	}

	lock := key + ":lock"
	t.Cleanup(func() { _ = client.Del(context.Background(), lock).Err() })
	release, err := store.LockDialogue(ctx, lock, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	secondClient := goredis.NewClient(options)
	t.Cleanup(func() { _ = secondClient.Close() })
	secondStore := NewTaskProcess(secondClient)
	if _, err := secondStore.LockDialogue(ctx, lock, time.Minute); !errors.Is(err, model.ErrDialogueBusy) {
		t.Fatalf("parallel lock = %v", err)
	}
	release()
	releaseNew, err := secondStore.LockDialogue(ctx, lock, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseNew()
	// A late duplicate unlock must never remove a newer request's lease.
	release()
	if _, err := store.LockDialogue(ctx, lock, time.Minute); !errors.Is(err, model.ErrDialogueBusy) {
		t.Fatalf("old release removed new lease: %v", err)
	}
}
