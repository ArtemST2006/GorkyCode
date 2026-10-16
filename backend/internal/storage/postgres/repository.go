package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// HealthRepository checks PostgreSQL availability.
type HealthRepository struct {
	pool *pgxpool.Pool
}

// NewHealthRepository creates a health repository over a connection pool.
func NewHealthRepository(pool *pgxpool.Pool) *HealthRepository {
	return &HealthRepository{pool: pool}
}

// Ping reports whether PostgreSQL is reachable.
func (r *HealthRepository) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}
