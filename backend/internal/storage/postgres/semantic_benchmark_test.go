package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"gorkycode/backend/internal/config"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	benchModel   = "semantic-benchmark-e5"
	benchVersion = "normalized-384-v1"
)

func benchOwner(label string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("gorky-semantic-benchmark/"+label))
}

func benchVector(seed, cluster int) []float32 {
	rng := rand.New(rand.NewPCG(uint64(seed+1), 42)) //nolint:gosec // Reproducible synthetic vectors, not security-sensitive randomness.
	vector := make([]float32, 384)
	vector[cluster] = 1
	var sum float64
	for i := range vector {
		vector[i] += float32(rng.NormFloat64() * .012)
		sum += float64(vector[i]) * float64(vector[i])
	}
	norm := math.Sqrt(sum)
	for i := range vector {
		vector[i] = float32(float64(vector[i]) / norm)
	}
	return vector
}

func prepareSemanticFixture(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()
	ctx := t.Context()
	for _, label := range []string{"dense", "medium", "sparse", "stranger"} {
		_, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,timezone)
			VALUES($1,'semantic benchmark fixture',$2,'not-a-real-password','Europe/Moscow')`, benchOwner(label), label+"@semantic-benchmark.invalid")
		if err != nil {
			t.Fatalf("fixture already exists or cannot be created; use cleanup phase: %v", err)
		}
	}
	for start := 0; start < n; start += 250 {
		var tasks [][]any
		var ids []uuid.UUID
		var vectors, versions []string
		for i := start; i < min(start+250, n); i++ {
			label := "stranger"
			switch {
			case i < 20:
				label = "sparse"
			case i < n*65/100:
				label = "dense"
			case i < n*70/100:
				label = "medium"
			}
			id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("gorky-semantic-benchmark/task/%d", i)))
			cluster := i % 8
			if label == "stranger" && i >= n-500 {
				cluster = 0
			}
			version := benchVersion
			if i%19 == 0 {
				version = "old-version"
			}
			typ := "meeting"
			if i%3 == 0 {
				typ = "task"
			}
			date := time.Date(2026, 10, 20+i%4, 0, 0, 0, 0, time.UTC)
			tasks = append(tasks, []any{id, benchOwner(label), typ, fmt.Sprintf("synthetic fixture %d", i), date})
			ids = append(ids, id)
			vectors = append(vectors, vectorLiteral(benchVector(i, cluster)))
			versions = append(versions, version)
		}
		_, err := pool.CopyFrom(ctx, pgx.Identifier{"tasks"}, []string{"id", "user_id", "type", "title", "date"}, pgx.CopyFromRows(tasks))
		if err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(ctx, `INSERT INTO task_embeddings(task_id,embedding,model,model_version)
			SELECT task_id,vec::vector,$4,version FROM unnest($1::uuid[],$2::text[],$3::text[]) AS v(task_id,vec,version)`, ids, vectors, versions, benchModel)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, "ANALYZE tasks"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "ANALYZE task_embeddings"); err != nil {
		t.Fatal(err)
	}
}

func cleanupSemanticFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, label := range []string{"dense", "medium", "sparse", "stranger"} {
		if _, err := pool.Exec(t.Context(), "DELETE FROM users WHERE id=$1 AND name='semantic benchmark fixture'", benchOwner(label)); err != nil {
			t.Fatal(err)
		}
	}
}

func semanticPlan(ctx context.Context, pool *pgxpool.Pool, f semanticFilter, vector []float32, exact bool, limit int, cfg config.SemanticSearch) ([]string, error) {
	sql, args, err := semanticSQL(f, vector, exact, limit)
	if err != nil {
		return nil, err
	}
	var db semanticQuerier = pool
	if !exact {
		tx, beginErr := pool.Begin(ctx)
		if beginErr != nil {
			return nil, beginErr
		}
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx, `SELECT set_config('hnsw.ef_search',$1,true),
			set_config('hnsw.max_scan_tuples',$2,true),
			set_config('hnsw.iterative_scan','strict_order',true)`,
			strconv.Itoa(cfg.EFSearch), strconv.Itoa(cfg.MaxScanTuples))
		if err != nil {
			return nil, err
		}
		db = tx
	}
	rows, err := db.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}
	return lines, rows.Err()
}

func latencySummary(values []float64) map[string]float64 {
	slices.Sort(values)
	return map[string]float64{"p50_ms": values[len(values)/2], "p95_ms": values[min(len(values)-1, int(math.Ceil(float64(len(values))*.95))-1)]}
}

func candidateRecall(want, got []semanticCandidate) float64 {
	if len(want) == 0 {
		return 1
	}
	set := make(map[uuid.UUID]bool)
	for _, c := range got {
		set[c.task.ID] = true
	}
	count := 0
	for _, c := range want {
		if set[c.task.ID] {
			count++
		}
	}
	return float64(count) / float64(len(want))
}

// Explicit phases allow measuring the real goose index build between runs.
// Run only against a disposable migrated database; fixtures are synthetic.
func TestSemanticBenchmark(t *testing.T) {
	phase := os.Getenv("SEMANTIC_BENCHMARK_PHASE")
	if phase == "" {
		t.Skip("opt-in benchmark: baseline, indexed or cleanup phase")
	}
	if phase != "baseline" && phase != "indexed" && phase != "cleanup" {
		t.Fatal("invalid benchmark phase")
	}
	pool := semanticTestPool(t)
	ctx := t.Context()
	if phase == "cleanup" {
		cleanupSemanticFixture(t, pool)
		return
	}
	output := os.Getenv("SEMANTIC_REPORT_DIR")
	if output == "" {
		t.Fatal("SEMANTIC_REPORT_DIR required")
	}
	if err := os.MkdirAll(output, 0o750); err != nil { //nolint:gosec // Operator-selected output directory for this opt-in local benchmark.
		t.Fatal(err)
	}
	rows := 10000
	if raw := os.Getenv("SEMANTIC_BENCHMARK_ROWS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1000 || n > 100000 {
			t.Fatal("rows must be 1000..100000")
		}
		rows = n
	}
	if phase == "baseline" && os.Getenv("SEMANTIC_BENCHMARK_REUSE") != "1" {
		prepareSemanticFixture(t, pool, rows)
	}
	var actual int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM task_embeddings WHERE model=$1", benchModel).Scan(&actual); err != nil || actual != rows {
		t.Fatalf("fixture count %d want %d: %v", actual, rows, err)
	}
	cfg := config.DefaultSemanticSearch()
	cfg.Mode = "hnsw"
	if raw := os.Getenv("SEMANTIC_BENCHMARK_EF_SEARCH"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatal(err)
		}
		cfg.EFSearch = n
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	annRepo := NewTaskRepository(pool, WithSemanticSearch(cfg, nil))
	var version string
	if err := pool.QueryRow(ctx, "SELECT extversion FROM pg_extension WHERE extname='vector'").Scan(&version); err != nil {
		t.Fatal(err)
	}
	report := map[string]any{"phase": phase, "rows": rows, "dimensions": 384, "vector_version": version, "config": cfg, "synthetic": true, "latency_scope": "SQL retrieval including pool/transaction; no encoder or LLM"}
	var indexSize int64
	if phase == "indexed" {
		if err := pool.QueryRow(ctx, "SELECT pg_relation_size('task_embeddings_embedding_hnsw_idx')").Scan(&indexSize); err != nil {
			t.Fatal(err)
		}
		report["index_bytes"] = indexSize
	}
	from := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	scenarios := []struct {
		name   string
		filter semanticFilter
	}{
		{"dense", semanticFilter{userID: benchOwner("dense"), model: benchModel, version: benchVersion}},
		{"medium", semanticFilter{userID: benchOwner("medium"), model: benchModel, version: benchVersion}},
		{"sparse", semanticFilter{userID: benchOwner("sparse"), model: benchModel, version: benchVersion}},
		{"date_and_type", semanticFilter{userID: benchOwner("dense"), model: benchModel, version: benchVersion, dateFrom: &from, dateTo: &to, types: []string{"meeting"}}},
	}
	var measurements []map[string]any
	for _, scenario := range scenarios {
		var exactTimes, annTimes, productionTimes []float64
		recall, finalRecall, nonempty := 0.0, 0.0, 0
		falseEmpty, finalDifferences := 0, 0
		for i := range 40 {
			vector := benchVector(100000+i, i%8)
			sql, args, err := semanticSQL(scenario.filter, vector, true, 20)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			exact, err := querySemantic(ctx, pool, sql, args...)
			exactTimes = append(exactTimes, float64(time.Since(start).Microseconds())/1000)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "baseline" {
				continue
			}
			start = time.Now()
			ann, err := annRepo.nearestHNSW(ctx, scenario.filter, vector)
			annTimes = append(annTimes, float64(time.Since(start).Microseconds())/1000)
			if err != nil {
				t.Fatal(err)
			}
			recall += candidateRecall(exact, ann[:min(20, len(ann))])
			want, got := selectSemantic(exact), selectSemantic(ann)
			set := make(map[uuid.UUID]bool)
			for _, task := range got {
				set[task.ID] = true
			}
			found := 0
			for _, task := range want {
				if set[task.ID] {
					found++
				}
			}
			if len(want) > 0 {
				nonempty++
				finalRecall += float64(found) / float64(len(want))
				if len(got) == 0 {
					falseEmpty++
				}
			}
			if len(want) != len(got) || found != len(want) {
				finalDifferences++
			}
			f := scenario.filter
			start = time.Now()
			production, err := annRepo.FindSemantic(ctx, f.userID, vector, f.model, f.version, f.dateFrom, f.dateTo, f.timeFrom, f.timeTo, f.types)
			productionTimes = append(productionTimes, float64(time.Since(start).Microseconds())/1000)
			if err != nil {
				t.Fatal(err)
			}
			for _, task := range production {
				if task.UserID != f.userID {
					t.Fatal("owner leak")
				}
			}
		}
		measurement := map[string]any{"scenario": scenario.name, "queries": 40, "exact": latencySummary(exactTimes)}
		vector := benchVector(100000, 0)
		exactPlan, err := semanticPlan(ctx, pool, scenario.filter, vector, true, 20, cfg)
		if err != nil {
			t.Fatal(err)
		}
		measurement["exact_plan"] = exactPlan
		if strings.Contains(strings.Join(exactPlan, "\n"), "task_embeddings_embedding_hnsw_idx") {
			t.Fatal("exact baseline uses ANN")
		}
		if phase == "indexed" {
			measurement["ann"] = latencySummary(annTimes)
			measurement["production_hnsw_mode"] = latencySummary(productionTimes)
			measurement["raw_recall_at_20"] = recall / 40
			measurement["false_empty_queries"] = falseEmpty
			measurement["final_set_differences"] = finalDifferences
			if nonempty > 0 {
				measurement["final_recall"] = finalRecall / float64(nonempty)
			}
			annPlan, err := semanticPlan(ctx, pool, scenario.filter, vector, false, cfg.Candidates, cfg)
			if err != nil {
				t.Fatal(err)
			}
			measurement["ann_plan"] = annPlan
			measurement["planner_used_hnsw"] = strings.Contains(strings.Join(annPlan, "\n"), "task_embeddings_embedding_hnsw_idx")
		}
		measurements = append(measurements, measurement)
	}
	report["scenarios"] = measurements
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "benchmark-"+phase+".json"), append(data, '\n'), 0o600); err != nil { //nolint:gosec // Operator-selected report destination, never HTTP request input.
		t.Fatal(err)
	}
	t.Logf("saved benchmark %s with %d vectors", phase, rows)
}
