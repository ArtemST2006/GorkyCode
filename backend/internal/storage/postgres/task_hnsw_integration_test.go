package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"gorkycode/backend/internal/config"
	"gorkycode/backend/internal/model"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func semanticTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("set POSTGRES_TEST_URL for a migrated isolated database")
	}
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestHNSWFiltersFallbackAndSettings(t *testing.T) {
	pool := semanticTestPool(t)
	ctx := t.Context()
	owner := createTaskTestUser(t, NewAuthRepository(pool), pool)
	other := createTaskTestUser(t, NewAuthRepository(pool), pool)
	vector := make([]float32, model.EmbeddingDimension)
	vector[0] = 1
	insert := func(user uuid.UUID, typ, date, clock, version string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO tasks(user_id,type,title,date,time)
			VALUES($1,$2,'hnsw fixture',$3::date,$4::time) RETURNING id`, user, typ, date, clock).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if err := NewTaskRepository(pool).StoreEmbedding(ctx, id, model.EmbeddingResult{Vector: vector, Model: "e5", Version: version}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	allowed := make(map[uuid.UUID]bool)
	for range 25 {
		allowed[insert(owner, "meeting", "2026-10-20", "15:00", "v1")] = true
	}
	insert(owner, "meeting", "2026-10-20", "14:59", "v1")
	insert(owner, "meeting", "2026-10-20", "16:00", "v1")
	insert(owner, "meeting", "2026-10-21", "15:00", "v1")
	insert(owner, "task", "2026-10-20", "15:00", "v1")
	insert(owner, "meeting", "2026-10-20", "15:00", "old")
	insert(other, "meeting", "2026-10-20", "15:00", "v1")
	from := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	start, end := "15:00", "16:00"
	cfg := config.DefaultSemanticSearch()
	cfg.Mode = "hnsw"
	cfg.ExactThreshold = 0
	var log bytes.Buffer
	repo := NewTaskRepository(pool, WithSemanticSearch(cfg, slog.New(slog.NewJSONHandler(&log, nil))))
	var before, after string
	if err := pool.QueryRow(ctx, "SHOW hnsw.ef_search").Scan(&before); err != nil {
		t.Fatal(err)
	}
	items, err := repo.FindSemantic(ctx, owner, vector, "e5", "v1", &from, &to, &start, &end, []string{"meeting"})
	if err != nil || len(items) != 20 {
		t.Fatalf("search = %d, %v", len(items), err)
	}
	for _, task := range items {
		if !allowed[task.ID] || task.UserID != owner || task.Type != "meeting" || task.Time == nil || *task.Time != "15:00" || !task.Date.Equal(from) {
			t.Fatalf("filter leak: %+v", task)
		}
	}
	if err := pool.QueryRow(ctx, "SHOW hnsw.ef_search").Scan(&after); err != nil || before != after {
		t.Fatalf("settings leaked: %s -> %s, %v", before, after, err)
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(log.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event["mode"] != "hnsw" {
		t.Fatalf("ANN not exercised: %s", log.String())
	}
	if strings.Contains(log.String(), "hnsw fixture") {
		t.Fatal("title in log")
	}

	t.Run("exact cannot use HNSW", func(t *testing.T) {
		f := semanticFilter{userID: owner, model: "e5", version: "v1"}
		sql, args, err := semanticSQL(f, vector, true, 20)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := pool.Query(ctx, "EXPLAIN "+sql, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(line, "task_embeddings_embedding_hnsw_idx") {
				t.Fatal("exact used ANN: " + line)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("small set", func(t *testing.T) {
		cfg.ExactThreshold = 500
		var output bytes.Buffer
		r := NewTaskRepository(pool, WithSemanticSearch(cfg, slog.New(slog.NewJSONHandler(&output, nil))))
		if _, err := r.FindSemantic(ctx, owner, vector, "e5", "v1", nil, nil, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), `"mode":"exact_small"`) {
			t.Fatal(output.String())
		}
	})
	t.Run("no semantic match", func(t *testing.T) {
		settings := config.DefaultSemanticSearch()
		settings.Mode = "hnsw"
		settings.ExactThreshold = 0
		r := NewTaskRepository(pool, WithSemanticSearch(settings, nil))
		negative := make([]float32, model.EmbeddingDimension)
		negative[0] = -1
		if items, err := r.FindSemantic(ctx, owner, negative, "e5", "v1", nil, nil, nil, nil, nil); err != nil || len(items) != 0 {
			t.Fatalf("unrelated vector matched: %d %v", len(items), err)
		}
	})
	t.Run("ANN timeout falls back", func(t *testing.T) {
		cfg.ExactThreshold = 0
		cfg.ANNTimeout = time.Nanosecond
		var output bytes.Buffer
		r := NewTaskRepository(pool, WithSemanticSearch(cfg, slog.New(slog.NewJSONHandler(&output, nil))))
		if items, err := r.FindSemantic(ctx, owner, vector, "e5", "v1", nil, nil, nil, nil, nil); err != nil || len(items) == 0 {
			t.Fatalf("fallback: %d %v", len(items), err)
		}
		if !strings.Contains(output.String(), `"mode":"exact_fallback"`) {
			t.Fatal(output.String())
		}
	})
}

func TestEmbeddingBackfillGuardsAndLifecycle(t *testing.T) {
	pool := semanticTestPool(t)
	ctx := t.Context()
	repo := NewTaskRepository(pool)
	owner := createTaskTestUser(t, NewAuthRepository(pool), pool)
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO tasks(user_id,type,title) VALUES($1,'task','old title') RETURNING id`, owner).Scan(&id); err != nil {
		t.Fatal(err)
	}
	vector := make([]float32, 384)
	vector[0] = 1
	result := model.EmbeddingResult{Vector: vector, Model: "e5", Version: "v1"}
	if _, err := pool.Exec(ctx, "UPDATE tasks SET title='new title' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if stored, err := repo.StoreEmbeddingIfCurrent(ctx, id, "old title", result); err != nil || stored {
		t.Fatalf("stale title: %v %v", stored, err)
	}
	// Two concurrent workers must store at most once.
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			stored, err := repo.StoreEmbeddingIfCurrent(ctx, id, "new title", result)
			results <- stored
			errs <- err
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	count := 0
	for stored := range results {
		if stored {
			count++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 {
		t.Fatalf("stored %d times", count)
	}
	result.Version = "v2"
	if stored, err := repo.StoreEmbeddingIfCurrent(ctx, id, "new title", result); err != nil || !stored {
		t.Fatalf("version repair: %v %v", stored, err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM tasks WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM task_embeddings WHERE task_id=$1)", id).Scan(&exists); err != nil || exists {
		t.Fatalf("cascade: %v %v", exists, err)
	}
	if stored, err := repo.StoreEmbeddingIfCurrent(context.Background(), id, "new title", result); err != nil || stored {
		t.Fatalf("deleted task: %v %v", stored, err)
	}
}
