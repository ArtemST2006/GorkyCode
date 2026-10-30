package postgres

import (
	"context"
	"gorkycode/backend/internal/model"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Capture the repository's actual SQL so EXPLAIN verifies the production query.
type semanticQueryTrace struct {
	sql  string
	args []any
}

func (tr *semanticQueryTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "WITH ranked AS MATERIALIZED") {
		tr.sql, tr.args = data.SQL, slices.Clone(data.Args)
	}
	return ctx
}

func (*semanticQueryTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestHNSWRepositoryQuery(t *testing.T) {
	url := os.Getenv("POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("set POSTGRES_TEST_URL for a migrated isolated database")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	trace := &semanticQueryTrace{}
	cfg.ConnConfig.Tracer = trace
	// On tiny fixtures PostgreSQL normally prefers an exact scan. Force the
	// approximate plan here to exercise filtering and iterative scans.
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, setupErr := conn.Exec(ctx, `SET enable_seqscan = off; SET enable_bitmapscan = off;
            SET enable_sort = off; SET enable_hashjoin = off; SET enable_mergejoin = off`)
		return setupErr
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ctx := t.Context()
	repo := NewTaskRepository(pool)
	owner := createTaskTestUser(t, NewAuthRepository(pool), pool)
	other := createTaskTestUser(t, NewAuthRepository(pool), pool)
	vector := func(distance float64) []float32 {
		v := make([]float32, model.EmbeddingDimension)
		v[0] = float32(1 - distance)
		v[1] = float32(math.Sqrt(1 - (1-distance)*(1-distance)))
		return v
	}
	insert := func(user uuid.UUID, typ, date, clock, name, version string, distance float64) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		insertErr := pool.QueryRow(ctx, `INSERT INTO tasks(user_id,type,title,date,time)
            VALUES($1,$2,'hnsw fixture',$3::date,$4::time) RETURNING id`, user, typ, date, clock).Scan(&id)
		if insertErr != nil {
			t.Fatal(insertErr)
		}
		if storeErr := repo.StoreEmbedding(ctx, id, model.EmbeddingResult{Vector: vector(distance), Model: name, Version: version}); storeErr != nil {
			t.Fatal(storeErr)
		}
		return id
	}
	// Closer but ineligible vectors outnumber ef_search: iterative scanning
	// must continue rather than returning only another user's candidates.
	for range 200 {
		insert(other, "meeting", "2026-10-20", "15:00", "e5", "v1", 0)
	}
	insert(owner, "task", "2026-10-20", "15:00", "e5", "v1", 0)
	insert(owner, "meeting", "2026-10-21", "15:00", "e5", "v1", 0)
	insert(owner, "meeting", "2026-10-19", "15:00", "e5", "v1", 0)
	insert(owner, "meeting", "2026-10-20", "14:59", "e5", "v1", 0)
	insert(owner, "meeting", "2026-10-20", "16:00", "e5", "v1", 0)
	insert(owner, "meeting", "2026-10-20", "15:00", "other-model", "v1", 0)
	insert(owner, "meeting", "2026-10-20", "15:00", "e5", "old", 0)
	want := make([]uuid.UUID, 0, 20)
	for i := range 25 {
		id := insert(owner, "meeting", "2026-10-20", "15:00", "e5", "v1", 0.001+float64(i)*0.0001)
		if i < 20 {
			want = append(want, id)
		}
	}
	from := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	start, end := "15:00", "16:00"
	var before, after string
	const settings = `SELECT current_setting('hnsw.ef_search') || '/' || current_setting('hnsw.iterative_scan')`
	if err = pool.QueryRow(ctx, settings).Scan(&before); err != nil {
		t.Fatal(err)
	}
	got, err := repo.FindSemantic(ctx, owner, vector(0), "e5", "v1", &from, &to, &start, &end, []string{"meeting"})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]uuid.UUID, 0, len(got))
	for _, task := range got {
		ids = append(ids, task.ID)
	}
	if !slices.Equal(ids, want) {
		t.Fatalf("filtered nearest IDs = %v, want %v", ids, want)
	}
	if err = pool.QueryRow(ctx, settings).Scan(&after); err != nil || before != after {
		t.Fatalf("local HNSW settings leaked: %s -> %s, error=%v", before, after, err)
	}
	if trace.sql == "" {
		t.Fatal("repository query was not captured")
	}
	querySQL, queryArgs := trace.sql, slices.Clone(trace.args)
	rows, err := pool.Query(ctx, "EXPLAIN "+querySQL, queryArgs...)
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, line)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan, "\n"), "task_embeddings_embedding_hnsw_idx") {
		t.Fatalf("production query did not use HNSW:\n%s", strings.Join(plan, "\n"))
	}
	t.Logf("production query uses HNSW; filtered Recall@20=1 on this fixture; settings restored")

	t.Run("distance thresholds", func(t *testing.T) {
		user := createTaskTestUser(t, NewAuthRepository(pool), pool)
		best := insert(user, "meeting", "2026-10-20", "15:00", "e5", "v1", 0.10)
		insert(user, "meeting", "2026-10-20", "15:00", "e5", "v1", 0.14)
		got, err := repo.FindSemantic(t.Context(), user, vector(0), "e5", "v1", nil, nil, nil, nil, nil)
		if err != nil || len(got) != 1 || got[0].ID != best {
			t.Fatalf("relative threshold: %v, %v", got, err)
		}
		isolated := createTaskTestUser(t, NewAuthRepository(pool), pool)
		insert(isolated, "meeting", "2026-10-20", "15:00", "e5", "v1", 0.20)
		got, err = repo.FindSemantic(t.Context(), isolated, vector(0), "e5", "v1", nil, nil, nil, nil, nil)
		if err != nil || len(got) != 0 {
			t.Fatalf("absolute threshold: %v, %v", got, err)
		}
	})
	t.Run("empty result", func(t *testing.T) {
		got, err := repo.FindSemantic(t.Context(), owner, vector(0), "e5", "missing", nil, nil, nil, nil, nil)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("empty search: %v, %v", got, err)
		}
	})
	t.Run("index rollback leaves exact search operational", func(t *testing.T) {
		// Transactional drop is only for testing; rollback restores the index.
		conn, err := pool.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		tx, err := conn.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		if _, err = tx.Exec(t.Context(), "DROP INDEX task_embeddings_embedding_hnsw_idx"); err != nil {
			t.Fatal(err)
		}
		rows, err := tx.Query(t.Context(), querySQL, queryArgs...)
		if err != nil {
			t.Fatal(err)
		}
		tasks, err := scanTasks(rows)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]uuid.UUID, 0, len(tasks))
		for _, task := range tasks {
			ids = append(ids, task.ID)
		}
		if !slices.Equal(ids, want) {
			t.Fatalf("exact search after drop: %v, want %v", ids, want)
		}
	})
}
