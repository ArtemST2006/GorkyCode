// Package storage assembles the backend's repositories.
package storage

import (
	"gorkycode/backend/internal/storage/postgres"
	"gorkycode/backend/internal/storage/redis"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

// Repository groups storage dependencies used by the service layer.
//
// Здесь собираются все репозитории воедино
type Repository struct {
	*postgres.HealthRepository
	*postgres.AuthRepository
	*postgres.TaskCreateRepository
	*postgres.TaskRepository
	*redis.SessionRepository
	*redis.TaskProcess
}

// NewRepository creates repositories over the shared PostgreSQL pool.
func NewRepository(pool *pgxpool.Pool, redisClient *goredis.Client) *Repository {
	return &Repository{
		HealthRepository:     postgres.NewHealthRepository(pool),
		AuthRepository:       postgres.NewAuthRepository(pool),
		TaskCreateRepository: postgres.NewTaskCreateRepository(pool),
		TaskRepository:       postgres.NewTaskRepository(pool),
		SessionRepository:    redis.NewSessionRepository(redisClient),
		TaskProcess:          redis.NewTaskProcess(redisClient),
	}
}
