package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"gorkycode/backend/internal/model"
)

func TestAuthRepositoryWithPostgres(t *testing.T) {
	url := os.Getenv("POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("set POSTGRES_TEST_URL for a migrated test database")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	repo := NewAuthRepository(pool)
	email := uuid.NewString() + "@example.com"
	user := &model.User{Name: "Auth test", Email: email, PasswordHash: "test hash", Timezone: "Europe/Moscow"}
	if err := repo.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", user.ID) })
	if user.ID == uuid.Nil || user.CreatedAt.IsZero() {
		t.Fatalf("CreateUser() did not populate ID and created_at: %+v", user)
	}
	got, err := repo.GetUserByEmail(ctx, strings.ToUpper(email))
	if err != nil || got.ID != user.ID {
		t.Fatalf("GetUserByEmail() = %+v, %v; want ID %s", got, err, user.ID)
	}
	duplicate := &model.User{Name: "Duplicate", Email: strings.ToUpper(email), PasswordHash: "test hash", Timezone: "Europe/Moscow"}
	if err := repo.CreateUser(ctx, duplicate); !errors.Is(err, model.ErrEmailTaken) {
		t.Fatalf("CreateUser() duplicate email = %v; want ErrEmailTaken", err)
	}
}
