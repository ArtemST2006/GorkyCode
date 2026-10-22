// Package service contains application logic between HTTP and storage.
package service

import (
	"gorkycode/backend/internal/service/subservice"
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
	subservice.TaskCreateRepository
	subservice.TaskLister
	subservice.TaskSearcher
}

// Services groups application services for HTTP wiring.
type Services struct {
	*HealthService
	*AuthService
	*TaskProcess
	*subservice.TaskCreate
}

// New constructs all services from the repository bundle.
func New(repo Repositories, queryTimeout, sessionTTL time.Duration, rec *voice.Recognizer, aiServiceURL string) *Services {
	taskCreate := subservice.NewTaskCreateService(repo)
	eventReader := subservice.NewLister(repo)
	return &Services{
		HealthService: NewHealthService(repo, queryTimeout),
		AuthService:   NewAuthService(repo, repo, queryTimeout, sessionTTL),
		TaskProcess:   NewTaskProcess(rec, repo, repo, taskCreate, eventReader, aiServiceURL),
		TaskCreate:    taskCreate,
	}
}
