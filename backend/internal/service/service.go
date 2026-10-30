// Package service contains application logic between HTTP and storage.
package service

import (
	"gorkycode/backend/internal/config"
	"gorkycode/backend/internal/service/subservice"
	"gorkycode/backend/internal/voice"
	"log/slog"
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
	subservice.EmbeddingStore
	subservice.TaskDeleteRepository
	subservice.TaskUpdateRepository
	GoogleIntegrationStore
	OAuthStateStore
	MeetingOverlapFinder
}

// Services groups application services for HTTP wiring.
type Services struct {
	*HealthService
	*AuthService
	*TaskProcess
	*subservice.TaskCreate
	*subservice.TaskDelete
	*subservice.TaskUpdate
	*CalendarService
}

// New constructs all services from the repository bundle. A nil googleClient
// disables Google Calendar: new tasks are then saved without sync.
func New(
	repo Repositories,
	queryTimeout, sessionTTL time.Duration,
	rec *voice.Recognizer,
	aiServiceURL string,
	googleCfg config.Google,
	googleClient GoogleCalendarClient,
	log *slog.Logger,
) *Services {
	embedder := NewAIEmbedder(aiServiceURL)
	taskCreate := subservice.NewTaskCreateService(repo, repo, embedder)
	eventReader := subservice.NewSemanticLister(repo, repo, embedder)
	taskDelete := subservice.NewTaskDeleteService(repo, embedder)
	taskUpdate := subservice.NewTaskUpdateService(repo, taskDelete, embedder)
	calendar := NewCalendarService(repo, repo, repo, googleClient, log, queryTimeout, googleCfg.EventDuration)

	var creator TaskCreator = taskCreate
	var googleBusy GoogleBusyChecker
	if calendar.Enabled() {
		creator = NewCalendarSyncingCreator(taskCreate, calendar, log)
		googleBusy = calendar
	}
	conflicts := NewMeetingConflicts(repo, googleBusy, log, queryTimeout, googleCfg.EventDuration)
	return &Services{
		HealthService:   NewHealthService(repo, queryTimeout),
		AuthService:     NewAuthService(repo, repo, queryTimeout, sessionTTL),
		TaskProcess:     NewTaskProcess(rec, repo, repo, creator, eventReader, taskDelete, aiServiceURL, taskUpdate).WithMeetingConflicts(conflicts).WithResultNarration(),
		TaskCreate:      taskCreate,
		TaskDelete:      taskDelete,
		TaskUpdate:      taskUpdate,
		CalendarService: calendar,
	}
}
