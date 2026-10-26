package postgres

import (
	"errors"
	"gorkycode/backend/internal/model"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGoogleRepositoryWithPostgres(t *testing.T) {
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
	if err = pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	auth := NewAuthRepository(pool)
	repo := NewGoogleRepository(pool)
	owner := createTaskTestUser(t, auth, pool)
	stranger := createTaskTestUser(t, auth, pool)

	t.Run("get without connection", func(t *testing.T) {
		if _, err := repo.GetGoogleIntegration(ctx, owner); !errors.Is(err, model.ErrGoogleNotConnected) {
			t.Fatalf("GetGoogleIntegration() = %v, want ErrGoogleNotConnected", err)
		}
	})

	expiry := time.Date(2026, 10, 23, 14, 0, 0, 0, time.UTC)
	email := "artem@gmail.com"
	first := &model.GoogleIntegration{
		UserID:       owner,
		RefreshToken: "refresh-1",
		AccessToken:  "access-1",
		TokenExpiry:  &expiry,
		GoogleEmail:  &email,
	}

	t.Run("insert", func(t *testing.T) {
		if err := repo.UpsertGoogleIntegration(ctx, first); err != nil {
			t.Fatal(err)
		}
		if first.CalendarID != "primary" || first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() {
			t.Fatalf("UpsertGoogleIntegration() did not fill defaults: %+v", first)
		}
		got, err := repo.GetGoogleIntegration(ctx, owner)
		if err != nil {
			t.Fatal(err)
		}
		if got.RefreshToken != "refresh-1" || got.AccessToken != "access-1" || got.CalendarID != "primary" ||
			got.TokenExpiry == nil || !got.TokenExpiry.Equal(expiry) || got.GoogleEmail == nil || *got.GoogleEmail != email {
			t.Fatalf("stored integration = %+v", got)
		}
	})

	t.Run("update keeps refresh token when google omits it", func(t *testing.T) {
		second := &model.GoogleIntegration{UserID: owner, AccessToken: "access-2", CalendarID: "work"}
		if err := repo.UpsertGoogleIntegration(ctx, second); err != nil {
			t.Fatal(err)
		}
		got, err := repo.GetGoogleIntegration(ctx, owner)
		if err != nil {
			t.Fatal(err)
		}
		if got.RefreshToken != "refresh-1" || got.AccessToken != "access-2" || got.CalendarID != "work" ||
			got.TokenExpiry != nil || got.GoogleEmail != nil {
			t.Fatalf("updated integration = %+v", got)
		}
		if !got.CreatedAt.Equal(first.CreatedAt) || got.UpdatedAt.Before(first.UpdatedAt) {
			t.Fatalf("created_at must stay and updated_at must move: %+v, first %+v", got, first)
		}
	})

	t.Run("update replaces refresh token", func(t *testing.T) {
		if err := repo.UpsertGoogleIntegration(ctx, &model.GoogleIntegration{UserID: owner, RefreshToken: "refresh-3"}); err != nil {
			t.Fatal(err)
		}
		got, err := repo.GetGoogleIntegration(ctx, owner)
		if err != nil || got.RefreshToken != "refresh-3" {
			t.Fatalf("GetGoogleIntegration() = %+v, %v; want refresh-3", got, err)
		}
	})

	t.Run("insert without refresh token is rejected", func(t *testing.T) {
		if err := repo.UpsertGoogleIntegration(ctx, &model.GoogleIntegration{UserID: stranger}); err == nil {
			t.Fatal("UpsertGoogleIntegration() without refresh token = nil, want error")
		}
	})

	t.Run("update tokens", func(t *testing.T) {
		newExpiry := time.Date(2026, 10, 23, 15, 0, 0, 0, time.UTC)
		if err := repo.UpdateGoogleTokens(ctx, owner, "access-4", &newExpiry); err != nil {
			t.Fatal(err)
		}
		got, err := repo.GetGoogleIntegration(ctx, owner)
		if err != nil {
			t.Fatal(err)
		}
		if got.AccessToken != "access-4" || got.TokenExpiry == nil || !got.TokenExpiry.Equal(newExpiry) || got.RefreshToken != "refresh-3" {
			t.Fatalf("integration after UpdateGoogleTokens() = %+v", got)
		}
		if err := repo.UpdateGoogleTokens(ctx, stranger, "access", nil); !errors.Is(err, model.ErrGoogleNotConnected) {
			t.Fatalf("UpdateGoogleTokens() without connection = %v, want ErrGoogleNotConnected", err)
		}
	})

	t.Run("task google event id is scoped by user", func(t *testing.T) {
		var taskID uuid.UUID
		if err := pool.QueryRow(ctx,
			"INSERT INTO tasks (user_id, type, title, date, time) VALUES ($1, 'meeting', 'Встреча', '2026-10-21', '15:00') RETURNING id",
			owner,
		).Scan(&taskID); err != nil {
			t.Fatal(err)
		}
		eventID := "event-1"

		if err := repo.SetTaskGoogleEventID(ctx, stranger, taskID, &eventID); err != nil {
			t.Fatal(err)
		}
		if got := taskGoogleEventID(t, pool, taskID); got != nil {
			t.Fatalf("another user's update changed google_event_id to %q", *got)
		}

		if err := repo.SetTaskGoogleEventID(ctx, owner, taskID, &eventID); err != nil {
			t.Fatal(err)
		}
		if got := taskGoogleEventID(t, pool, taskID); got == nil || *got != eventID {
			t.Fatalf("google_event_id = %v, want %q", got, eventID)
		}

		if err := repo.SetTaskGoogleEventID(ctx, owner, taskID, nil); err != nil {
			t.Fatal(err)
		}
		if got := taskGoogleEventID(t, pool, taskID); got != nil {
			t.Fatalf("google_event_id = %q, want NULL", *got)
		}
	})

	t.Run("delete is idempotent", func(t *testing.T) {
		if err := repo.DeleteGoogleIntegration(ctx, owner); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.GetGoogleIntegration(ctx, owner); !errors.Is(err, model.ErrGoogleNotConnected) {
			t.Fatalf("GetGoogleIntegration() after delete = %v, want ErrGoogleNotConnected", err)
		}
		if err := repo.DeleteGoogleIntegration(ctx, owner); err != nil {
			t.Fatalf("second DeleteGoogleIntegration() = %v, want nil", err)
		}
	})
}

func taskGoogleEventID(t *testing.T, pool *pgxpool.Pool, taskID uuid.UUID) *string {
	t.Helper()
	var eventID *string
	if err := pool.QueryRow(t.Context(), "SELECT google_event_id FROM tasks WHERE id = $1", taskID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	return eventID
}
