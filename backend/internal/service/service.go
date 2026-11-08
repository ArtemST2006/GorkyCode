// Package service contains application logic between HTTP and storage.
package service

import (
	"context"
	"fmt"
	"gorkycode/backend/internal/config"
	api "gorkycode/backend/internal/generated"
	"gorkycode/backend/internal/service/subservice"
	"gorkycode/backend/internal/voice"
	"log/slog"
	"time"

	"github.com/google/uuid"
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
	TaskList *subservice.Lister
	*CalendarService
}

// New constructs all services from the repository bundle. A nil googleClient
// disables Google Calendar: new tasks are then saved without sync. A nil
// reminders keeps task changes away from the notification scheduling, which
// the scheduler reconciliation heals on the next tick anyway.
func New(
	repo Repositories,
	queryTimeout, sessionTTL time.Duration,
	rec *voice.Recognizer,
	aiServiceURL string,
	googleCfg config.Google,
	googleClient GoogleCalendarClient,
	reminders subservice.ReminderSyncer,
	log *slog.Logger,
) *Services {
	embedder := NewAIEmbedder(aiServiceURL)
	taskCreate := subservice.NewTaskCreateService(repo, repo, embedder).WithReminderSyncer(reminders)
	eventReader := subservice.NewSemanticLister(repo, repo, embedder)
	taskDelete := subservice.NewTaskDeleteService(repo, embedder)
	taskUpdate := subservice.NewTaskUpdateService(repo, taskDelete, embedder).WithReminderSyncer(reminders)
	taskList := subservice.NewLister(repo)
	calendar := NewCalendarService(repo, repo, repo, googleClient, log, queryTimeout, googleCfg.EventDuration)

	var creator TaskCreator = taskCreate
	var googleBusy GoogleBusyChecker
	if calendar.Enabled() {
		creator = NewCalendarSyncingCreator(taskCreate, calendar, log)
		if googleCfg.CheckBusy {
			googleBusy = calendar
		}
		taskDelete.WithCalendarSync(calendar)
		taskUpdate.WithCalendarSync(calendar)
	}
	conflicts := NewMeetingConflicts(repo, googleBusy, log, queryTimeout, googleCfg.EventDuration)
	return &Services{
		HealthService:   NewHealthService(repo, queryTimeout),
		AuthService:     NewAuthService(repo, repo, queryTimeout, sessionTTL),
		TaskProcess:     NewTaskProcess(rec, repo, repo, creator, eventReader, taskDelete, aiServiceURL, taskUpdate).WithMeetingConflicts(conflicts).WithResultNarration(),
		TaskCreate:      taskCreate,
		TaskDelete:      taskDelete,
		TaskUpdate:      taskUpdate,
		TaskList:        taskList,
		CalendarService: calendar,
	}
}

// ListAllTasks exposes the task-read facade without exposing the configured
// lister pointer to the handlers.
func (s *Services) ListAllTasks(ctx context.Context, userID uuid.UUID, query string, types []string, from, to *time.Time, limit, offset int) (api.TaskCollection, error) {
	if s.TaskList == nil {
		return api.TaskCollection{}, fmt.Errorf("task list service is not configured")
	}
	return s.TaskList.ListAllTasks(ctx, userID, query, types, from, to, limit, offset)
}
