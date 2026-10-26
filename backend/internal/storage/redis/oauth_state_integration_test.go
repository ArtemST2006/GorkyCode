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

func TestOAuthStateRepositoryWithRedis(t *testing.T) {
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

	repo := NewOAuthStateRepository(client)
	state := uuid.NewString()
	userID := uuid.New()
	t.Cleanup(func() { _ = client.Del(context.Background(), oauthStateKey(state)).Err() })

	if _, err := repo.TakeOAuthState(ctx, state); !errors.Is(err, model.ErrOAuthStateNotFound) {
		t.Fatalf("TakeOAuthState() before save = %v; want ErrOAuthStateNotFound", err)
	}
	if err := repo.SaveOAuthState(ctx, state, userID, time.Minute); err != nil {
		t.Fatal(err)
	}
	if keys := client.Keys(ctx, "*"+state+"*").Val(); len(keys) != 0 {
		t.Fatalf("oauth state appears in Redis keys: %v", keys)
	}
	if ttl := client.TTL(ctx, oauthStateKey(state)).Val(); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("oauth state TTL = %s; want positive TTL <= 1m", ttl)
	}
	if got, err := repo.TakeOAuthState(ctx, state); err != nil || got != userID {
		t.Fatalf("TakeOAuthState() = %s, %v; want %s", got, err, userID)
	}
	if _, err := repo.TakeOAuthState(ctx, state); !errors.Is(err, model.ErrOAuthStateNotFound) {
		t.Fatalf("second TakeOAuthState() = %v; want ErrOAuthStateNotFound", err)
	}
}
