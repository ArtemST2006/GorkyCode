package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ExampleAuthRepository demonstrates a PostgreSQL call for the example endpoint.
type ExampleAuthRepository struct {
	pool *pgxpool.Pool
}

// NewExampleAuthRepository constructs the example repository.
func NewExampleAuthRepository(pool *pgxpool.Pool) *ExampleAuthRepository {
	return &ExampleAuthRepository{pool: pool}
}

// SignIn runs a test query and does not authenticate a user.
func (r *ExampleAuthRepository) SignIn(ctx context.Context) (bool, error) {
	var reached bool
	if err := r.pool.QueryRow(ctx, "SELECT TRUE").Scan(&reached); err != nil {
		return false, fmt.Errorf("query example sign-in: %w", err)
	}
	return reached, nil
}
