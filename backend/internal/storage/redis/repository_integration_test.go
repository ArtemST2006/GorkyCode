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

func TestSessionRepositoryWithRedis(t *testing.T) {
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
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}

	repo := NewSessionRepository(client)
	token := uuid.NewString()
	userID := uuid.New()
	t.Cleanup(func() { _ = repo.Delete(context.Background(), token) })
	if err := repo.Create(ctx, token, userID, time.Minute); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Get(ctx, token); err != nil || got != userID {
		t.Fatalf("Get() = %s, %v; want %s", got, err, userID)
	}
	if keys := client.Keys(ctx, "*"+token+"*").Val(); len(keys) != 0 {
		t.Fatalf("access token appears in Redis keys: %v", keys)
	}
	if ttl := client.TTL(ctx, sessionKey(token)).Val(); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("session TTL = %s; want positive TTL <= 1m", ttl)
	}
	if err := repo.Delete(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, token); !errors.Is(err, model.ErrSessionNotFound) {
		t.Fatalf("Get() after Delete() = %v; want ErrSessionNotFound", err)
	}
}
