package postgres

import (
	"context"
	"gorkycode/backend/internal/model"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTaskRepositoryFindSemanticWithPostgres(t *testing.T) {
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
	auth := NewAuthRepository(pool)
	owner := createTaskTestUser(t, auth, pool)
	stranger := createTaskTestUser(t, auth, pool)
	repo := NewTaskRepository(pool)
	query := make([]float32, model.EmbeddingDimension)
	query[0] = 1
	unrelated := make([]float32, model.EmbeddingDimension)
	unrelated[1] = 1
	closeButWrong := make([]float32, model.EmbeddingDimension)
	closeButWrong[0], closeButWrong[1] = 0.9, 0.4358899 // cosine distance about 0.10
	insert := func(userID uuid.UUID, typ, title, date string, vector []float32) uuid.UUID {
		t.Helper()
		var taskID uuid.UUID
		if err := pool.QueryRow(ctx,
			"INSERT INTO tasks (user_id, type, title, date, time) VALUES ($1, $2, $3, $4::date, '15:00'::time) RETURNING id",
			userID, typ, title, date,
		).Scan(&taskID); err != nil {
			t.Fatal(err)
		}
		if err := repo.StoreEmbedding(ctx, taskID, model.EmbeddingResult{
			Vector: vector, Model: "intfloat/multilingual-e5-small", Version: "v1",
		}); err != nil {
			t.Fatal(err)
		}
		return taskID
	}
	wantID := insert(owner, "meeting", "Приём у стоматолога", "2026-10-20", query)
	insert(owner, "meeting", "Встреча с другим человеком", "2026-10-20", closeButWrong)
	insert(owner, "meeting", "Обсуждение бюджета", "2026-10-20", unrelated)
	insert(owner, "task", "Записаться к врачу", "2026-10-20", query)
	insert(owner, "meeting", "Врач через месяц", "2026-11-20", query)
	insert(stranger, "meeting", "Чужая запись к врачу", "2026-10-20", query)
	from := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	got, err := repo.FindSemantic(ctx, owner, query, "intfloat/multilingual-e5-small", "v1", &from, &to, nil, nil, []string{"meeting"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != wantID {
		t.Fatalf("semantic matches = %+v, want task %s", got, wantID)
	}
	if got, err = repo.FindSemantic(ctx, owner, query, "intfloat/multilingual-e5-small", "old-version", &from, &to, nil, nil, []string{"meeting"}); err != nil || len(got) != 0 {
		t.Fatalf("incompatible model version returned %d rows: %v", len(got), err)
	}
	if got, err = repo.FindSemantic(context.Background(), owner, []float32{1}, "intfloat/multilingual-e5-small", "v1", nil, nil, nil, nil, nil); err == nil || got != nil {
		t.Fatalf("invalid vector result = %+v, error = %v", got, err)
	}
}
