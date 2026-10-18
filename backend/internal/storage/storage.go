// Package storage assembles the backend's repositories.
package storage

import (
	"gorkycode/backend/internal/storage/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository groups storage dependencies used by the service layer.
//
// Здесь собираются все репозитории воедино
type Repository struct {
	*postgres.HealthRepository
	*postgres.AuthRepository
}

// NewRepository creates repositories over the shared PostgreSQL pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{
		HealthRepository: postgres.NewHealthRepository(pool),
		AuthRepository:   postgres.NewAuthRepository(pool),
	}
}
