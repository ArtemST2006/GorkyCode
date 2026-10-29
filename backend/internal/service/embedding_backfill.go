package service

import (
	"context"
	"fmt"
	"gorkycode/backend/internal/model"
	"sync"
	"time"

	"github.com/google/uuid"
)

// EmbeddingBackfillStore selects missing vectors and conditionally stores repairs.
type EmbeddingBackfillStore interface {
	MissingEmbeddingTasks(context.Context, string, string, uuid.UUID, int) ([]model.Task, error)
	StoreEmbeddingIfCurrent(context.Context, uuid.UUID, string, model.EmbeddingResult) (bool, error)
}

// BackfillOptions bounds a single repair run and identifies its embedding space.
type BackfillOptions struct {
	Model, Version            string
	BatchSize, Limit, Workers int
	DryRun                    bool
	RequestTimeout            time.Duration
}

// BackfillReport contains counts only, without user titles or queries.
type BackfillReport struct {
	Scanned int  `json:"scanned"`
	Stored  int  `json:"stored"`
	Skipped int  `json:"skipped"`
	Failed  int  `json:"failed"`
	DryRun  bool `json:"dry_run"`
}

// BackfillEmbeddings only calls the local encoder. It does not call /api/job.
func BackfillEmbeddings(ctx context.Context, store EmbeddingBackfillStore, embedder model.Embedder, opt BackfillOptions) (BackfillReport, error) {
	report := BackfillReport{DryRun: opt.DryRun}
	if opt.Model == "" || opt.Version == "" || opt.BatchSize < 1 || opt.BatchSize > 1000 || opt.Limit < 1 || opt.Workers < 1 || opt.Workers > 16 || opt.RequestTimeout <= 0 {
		return report, fmt.Errorf("invalid backfill options")
	}
	if !opt.DryRun && embedder == nil {
		return report, fmt.Errorf("embedder is required")
	}
	after := uuid.Nil
	for report.Scanned < opt.Limit {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		n := min(opt.BatchSize, opt.Limit-report.Scanned)
		tasks, err := store.MissingEmbeddingTasks(ctx, opt.Model, opt.Version, after, n)
		if err != nil {
			return report, err
		}
		if len(tasks) == 0 {
			break
		}
		after = tasks[len(tasks)-1].ID
		report.Scanned += len(tasks)
		if opt.DryRun {
			report.Skipped += len(tasks)
			continue
		}
		jobs := make(chan model.Task)
		results := make(chan int, len(tasks)) // 1 stored, 0 stale/already current, -1 failed
		var wg sync.WaitGroup
		for range min(opt.Workers, len(tasks)) {
			wg.Go(func() {
				for task := range jobs {
					workCtx, cancel := context.WithTimeout(ctx, opt.RequestTimeout)
					result, err := embedder.Embed(workCtx, "document", task.Title)
					if err != nil || result.Model != opt.Model || result.Version != opt.Version {
						cancel()
						results <- -1
						continue
					}
					stored, err := store.StoreEmbeddingIfCurrent(workCtx, task.ID, task.Title, result)
					cancel()
					switch {
					case err != nil:
						results <- -1
					case stored:
						results <- 1
					default:
						results <- 0
					}
				}
			})
		}
		for _, task := range tasks {
			jobs <- task
		}
		close(jobs)
		wg.Wait()
		close(results)
		for status := range results {
			switch status {
			case 1:
				report.Stored++
			case 0:
				report.Skipped++
			default:
				report.Failed++
			}
		}
	}
	return report, ctx.Err()
}
