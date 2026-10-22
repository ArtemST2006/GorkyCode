// Package service contains application logic between HTTP and storage.
package service

import (
	services "gorkycode/backend/internal/service/internal_services"
	"gorkycode/backend/internal/voice"
	"time"
)

// Repositories lists the storage operations needed to build all services.
type Repositories interface {
	HealthRepository
	AuthRepository
	SessionStore
	DraftStore
	TimezoneStore
	services.TaskCreateRepository
}

// Services groups application services for HTTP wiring.
type Services struct {
	*HealthService
	*AuthService
	*TaskProcess
	*services.TaskCreate
}

// New constructs all services from the repository bundle.
func New(repo Repositories, queryTimeout, sessionTTL time.Duration, rec *voice.Recognizer, aiServiceURL string) *Services {
	taskCreate := services.NewTaskCreateService(repo)
	return &Services{
		HealthService: NewHealthService(repo, queryTimeout),
		AuthService:   NewAuthService(repo, repo, queryTimeout, sessionTTL),
		TaskProcess:   NewTaskProcess(rec, repo, repo, taskCreate, aiServiceURL),
		TaskCreate:    taskCreate,
	}
}
