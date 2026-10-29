package postgres

import (
	"context"
	"fmt"
	"gorkycode/backend/internal/model"
	"strconv"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const semanticColumns = "id, user_id, type, title, date, time_text, status, created_at"

type semanticFilter struct {
	userID           uuid.UUID
	model, version   string
	dateFrom, dateTo *time.Time
	timeFrom, timeTo *string
	types            []string
}

type semanticCandidate struct {
	task     model.Task
	distance float64
}

func semanticTasks(f semanticFilter) sq.SelectBuilder {
	q := sq.Select().From("tasks AS t").Where(sq.Eq{"t.user_id": f.userID})
	if f.dateFrom != nil {
		q = q.Where(sq.Expr("t.date >= ?::date", f.dateFrom.Format(time.DateOnly)))
	}
	if f.dateTo != nil {
		q = q.Where(sq.Expr("t.date < ?::date", f.dateTo.Format(time.DateOnly)))
	}
	if f.timeFrom != nil {
		q = q.Where(sq.Expr("t.time >= ?::time", *f.timeFrom))
	}
	if f.timeTo != nil {
		q = q.Where(sq.Expr("t.time < ?::time", *f.timeTo))
	}
	if len(f.types) > 0 {
		q = q.Where(sq.Eq{"t.type": f.types})
	}
	return q
}

func semanticBase(f semanticFilter) sq.SelectBuilder {
	return semanticTasks(f).Join("task_embeddings AS e ON e.task_id = t.id").
		Where(sq.Eq{"e.model": f.model, "e.model_version": f.version})
}

// semanticSQL keeps all ownership/compatibility filters inside retrieval.
// MATERIALIZED eligible makes exact independent of the planner's ANN choice.
func semanticSQL(f semanticFilter, vector []float32, exact bool, limit int) (string, []any, error) {
	base := semanticBase(f).Columns("t.id", "t.user_id", "t.type", "t.title", "t.date",
		"to_char(t.time, 'HH24:MI') AS time_text", "t.status", "t.created_at")
	var sql string
	var args []any
	var err error
	if exact {
		// Materialize scalar distances, not the 384-dimensional vectors.
		// Without an ORDER BY in this CTE, HNSW cannot replace exact scoring.
		inner, values, buildErr := base.Column("e.embedding <=> ?::vector AS distance", vectorLiteral(vector)).ToSql()
		if buildErr != nil {
			return "", nil, buildErr
		}
		sql = `WITH eligible AS MATERIALIZED (` + inner + `), nearest AS MATERIALIZED (
			SELECT ` + semanticColumns + `, distance
			FROM eligible ORDER BY distance, date, time_text NULLS FIRST, created_at, id LIMIT ?)
			SELECT ` + semanticColumns + `, distance FROM nearest
			ORDER BY distance, date, time_text NULLS FIRST, created_at, id`
		args = values
		args = append(args, limit)
	} else {
		// No secondary sort keys here: the inner ORDER BY must match the HNSW
		// distance operator. Deterministic presentation is applied outside.
		inner, values, buildErr := base.Column("e.embedding <=> ?::vector AS distance", vectorLiteral(vector)).
			OrderBy("distance").Suffix("LIMIT ?", limit).ToSql()
		if buildErr != nil {
			return "", nil, buildErr
		}
		sql = `WITH nearest AS MATERIALIZED (` + inner + `)
			SELECT ` + semanticColumns + `, distance FROM nearest
			ORDER BY distance, date, time_text NULLS FIRST, created_at, id`
		args = values
	}
	sql, err = sq.Dollar.ReplacePlaceholders(sql)
	return sql, args, err
}

func selectSemantic(candidates []semanticCandidate) []model.Task {
	result := make([]model.Task, 0, semanticLimit)
	if len(candidates) == 0 {
		return result
	}
	best := candidates[0].distance
	for _, c := range candidates {
		if c.distance <= semanticMaxCosineDistance && c.distance <= best+semanticMaxDistanceFromBest {
			result = append(result, c.task)
			if len(result) == semanticLimit {
				break
			}
		}
	}
	return result
}

type semanticQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func querySemantic(ctx context.Context, db semanticQuerier, sql string, args ...any) ([]semanticCandidate, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]semanticCandidate, 0)
	for rows.Next() {
		var c semanticCandidate
		t := &c.task
		if err = rows.Scan(&t.ID, &t.UserID, &t.Type, &t.Title, &t.Date, &t.Time, &t.Status, &t.CreatedAt, &c.distance); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (r *TaskRepository) smallSemanticSet(ctx context.Context, f semanticFilter) (bool, error) {
	if r.search.ExactThreshold == 0 {
		return false, nil
	}
	// Task count is a cheap upper bound on compatible embeddings. It avoids
	// reading the vector table just to decide that a small user needs exact.
	inner, args, err := semanticTasks(f).Column("1").Suffix("LIMIT ?", r.search.ExactThreshold+1).
		PlaceholderFormat(sq.Dollar).ToSql()
	if err != nil {
		return false, err
	}
	var count int
	err = r.pool.QueryRow(ctx, "SELECT count(*) FROM ("+inner+") AS eligible", args...).Scan(&count)
	return count <= r.search.ExactThreshold, err
}

func (r *TaskRepository) nearestHNSW(ctx context.Context, f semanticFilter, vector []float32) ([]semanticCandidate, error) {
	annCtx, cancel := context.WithTimeout(ctx, r.search.ANNTimeout)
	defer cancel()
	tx, err := r.pool.Begin(annCtx)
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	_, err = tx.Exec(annCtx, `SELECT set_config('hnsw.ef_search', $1, true),
		set_config('hnsw.max_scan_tuples', $2, true),
		set_config('hnsw.iterative_scan', 'strict_order', true)`,
		strconv.Itoa(r.search.EFSearch), strconv.Itoa(r.search.MaxScanTuples))
	if err != nil {
		return nil, err
	}
	sql, args, err := semanticSQL(f, vector, false, r.search.Candidates)
	if err != nil {
		return nil, err
	}
	result, err := querySemantic(annCtx, tx, sql, args...)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(annCtx); err != nil {
		return nil, err
	}
	return result, nil
}

// FindSemantic supplements literal search. HNSW may omit neighbors; exact is
// the reference path, the small-set path and a bounded failure fallback.
func (r *TaskRepository) FindSemantic(ctx context.Context, userID uuid.UUID, vector []float32,
	modelName, version string, dateFrom, dateTo *time.Time, timeFrom, timeTo *string, types []string,
) (tasks []model.Task, err error) {
	if !validVector(vector) || strings.TrimSpace(modelName) == "" || strings.TrimSpace(version) == "" {
		return nil, fmt.Errorf("invalid query embedding or model identity")
	}
	if err = r.search.Validate(); err != nil {
		return nil, err
	}
	f := semanticFilter{userID, modelName, version, dateFrom, dateTo, timeFrom, timeTo, types}
	started := time.Now()
	mode, fallback, count := "exact", "", 0
	defer func() {
		if r.log != nil {
			r.log.InfoContext(ctx, "semantic_search", "requested_mode", r.search.Mode, "mode", mode,
				"duration_ms", float64(time.Since(started).Microseconds())/1000,
				"candidates", count, "returned", len(tasks), "fallback_reason", fallback, "failed", err != nil)
		}
	}()
	if r.search.Mode == "hnsw" {
		small, probeErr := r.smallSemanticSet(ctx, f)
		switch {
		case probeErr != nil:
			fallback = "filter_probe_error"
		case small:
			mode = "exact_small"
		default:
			mode = "hnsw"
			candidates, annErr := r.nearestHNSW(ctx, f, vector)
			count = len(candidates)
			switch {
			case annErr == nil && count >= semanticLimit:
				tasks = selectSemantic(candidates)
				if len(tasks) > 0 {
					return tasks, nil
				}
				fallback = "empty_after_threshold"
			case annErr != nil:
				fallback = "ann_error"
			default:
				fallback = "candidate_shortfall"
			}
		}
	}
	if fallback != "" {
		mode = "exact_fallback"
	}
	sql, args, buildErr := semanticSQL(f, vector, true, semanticLimit)
	if buildErr != nil {
		return nil, buildErr
	}
	candidates, queryErr := querySemantic(ctx, r.pool, sql, args...)
	count = len(candidates)
	if queryErr != nil {
		return nil, fmt.Errorf("semantic exact query: %w", queryErr)
	}
	return selectSemantic(candidates), nil
}
