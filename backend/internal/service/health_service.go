package service

import (
	"context"
	"fmt"
	"time"
)

// HealthRepository is the storage operation needed by the readiness service.
type HealthRepository interface {
	Ping(context.Context) error
}

// HealthService checks readiness through the repository.
type HealthService struct {
	repo         HealthRepository
	queryTimeout time.Duration
}

// NewHealthService constructs the readiness service.
func NewHealthService(repo HealthRepository, queryTimeout time.Duration) *HealthService {
	return &HealthService{repo: repo, queryTimeout: queryTimeout}
}

// Ready checks whether the dependencies required to serve requests are available.
func (s *HealthService) Ready(ctx context.Context) error {
	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	if err := s.repo.Ping(queryCtx); err != nil {
		return fmt.Errorf("check PostgreSQL readiness: %w", err)
	}

	return nil
}
