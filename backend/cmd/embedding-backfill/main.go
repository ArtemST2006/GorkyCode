// embedding-backfill repairs missing or incompatible embeddings in bounded runs.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"gorkycode/backend/internal/service"
	"gorkycode/backend/internal/storage/postgres"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	dbURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL URL; prefer DATABASE_URL environment")
	aiURL := flag.String("ai-url", os.Getenv("AI_SERVICE_URL"), "AI service URL (local encoder only)")
	modelName := flag.String("model", "", "target model; discovered from encoder if empty")
	version := flag.String("model-version", "", "target version; discovered from encoder if empty")
	batch := flag.Int("batch-size", 32, "keyset page size (1..1000)")
	limit := flag.Int("limit", 1000, "maximum tasks inspected in this run")
	workers := flag.Int("workers", 1, "concurrent encoder requests (1..16)")
	dry := flag.Bool("dry-run", false, "count missing embeddings without writes")
	timeout := flag.Duration("request-timeout", 15*time.Second, "timeout per task including storage")
	flag.Parse()
	if *dbURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if *aiURL == "" {
		*aiURL = "http://localhost:8090"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	embedder := service.NewAIEmbedder(*aiURL)
	if *modelName == "" || *version == "" {
		probeCtx, cancel := context.WithTimeout(ctx, *timeout)
		identity, err := embedder.Embed(probeCtx, "query", "проверка модели")
		cancel()
		if err != nil {
			return fmt.Errorf("encoder identity probe failed (or supply --model and --model-version): %w", err)
		}
		if *modelName != "" && *modelName != identity.Model || *version != "" && *version != identity.Version {
			return fmt.Errorf("requested model identity differs from encoder")
		}
		*modelName, *version = identity.Model, identity.Version
	}
	pool, err := pgxpool.New(ctx, *dbURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()
	report, err := service.BackfillEmbeddings(ctx, postgres.NewTaskRepository(pool), embedder, service.BackfillOptions{
		Model: *modelName, Version: *version, BatchSize: *batch, Limit: *limit, Workers: *workers, DryRun: *dry, RequestTimeout: *timeout,
	})
	if encodeErr := json.NewEncoder(os.Stdout).Encode(report); encodeErr != nil {
		return encodeErr
	}
	if err != nil {
		return err
	}
	if report.Failed > 0 {
		return fmt.Errorf("%d embeddings failed; rerun to retry", report.Failed)
	}
	return nil
}
