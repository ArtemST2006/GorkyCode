// Package service contains application logic between HTTP and storage.
package service

import "time"

// Repositories lists the storage operations needed to build all services.
type Repositories interface {
	HealthRepository
	AuthRepository
}

// Services groups application services for HTTP wiring.
type Services struct {
	*HealthService
	*AuthService
}

// New constructs all services from the repository bundle.
func New(repo Repositories, queryTimeout time.Duration) *Services {
	return &Services{
		HealthService: NewHealthService(repo, queryTimeout),
		AuthService:   NewAuthService(repo, queryTimeout),
	}
}
