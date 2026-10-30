package service

import (
	"context"
	"errors"
	"fmt"
	"gorkycode/backend/internal/integration/google"
	"gorkycode/backend/internal/lib/logger"
	"gorkycode/backend/internal/model"
	services "gorkycode/backend/internal/service/subservice"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

const (
	// oauthStateTTL limits how long the user may stay on the Google consent page.
	oauthStateTTL = 10 * time.Minute
	// googleCallTimeout bounds every call to Google.
	googleCallTimeout = 3 * time.Second
	// meetingType and reminderType are the record types sent to Google
	// Calendar; tasks stay only in the app.
	meetingType  = "meeting"
	reminderType = "reminder"
)

// GoogleIntegrationStore keeps users' Google Calendar connections.
type GoogleIntegrationStore interface {
	UpsertGoogleIntegration(ctx context.Context, integration *model.GoogleIntegration) error
	GetGoogleIntegration(ctx context.Context, userID uuid.UUID) (*model.GoogleIntegration, error)
	UpdateGoogleTokens(ctx context.Context, userID uuid.UUID, accessToken string, expiry *time.Time) error
	DeleteGoogleIntegration(ctx context.Context, userID uuid.UUID) error
	SetTaskGoogleEventID(ctx context.Context, userID, taskID uuid.UUID, eventID *string) error
}

// OAuthStateStore keeps one-time OAuth states.
type OAuthStateStore interface {
	SaveOAuthState(ctx context.Context, state string, userID uuid.UUID, ttl time.Duration) error
	TakeOAuthState(ctx context.Context, state string) (uuid.UUID, error)
}

// GoogleCalendarClient calls Google OAuth and the Google Calendar API.
type GoogleCalendarClient interface {
	AuthURL(state string) string
	Exchange(ctx context.Context, code string) (*google.Token, error)
	Token(ctx context.Context, integ *model.GoogleIntegration) (google.Credentials, bool, error)
	CreateEvent(ctx context.Context, creds google.Credentials, calendarID string, ev google.Event) (string, error)
	UpdateEvent(ctx context.Context, creds google.Credentials, calendarID, eventID string, ev google.Event) error
	DeleteEvent(ctx context.Context, creds google.Credentials, calendarID, eventID string) error
	FreeBusy(ctx context.Context, creds google.Credentials, calendarID string, start, end time.Time) ([]google.Busy, error)
	Revoke(ctx context.Context, token string) error
}

// CalendarService connects users' Google Calendars and sends meetings to them.
type CalendarService struct {
	store         GoogleIntegrationStore
	states        OAuthStateStore
	timezones     TimezoneStore
	client        GoogleCalendarClient
	log           *slog.Logger
	queryTimeout  time.Duration
	eventDuration time.Duration
}

// NewCalendarService constructs the Google Calendar service. A nil client
// means the integration is disabled: every method returns model.ErrGoogleDisabled.
func NewCalendarService(
	store GoogleIntegrationStore,
	states OAuthStateStore,
	timezones TimezoneStore,
	client GoogleCalendarClient,
	log *slog.Logger,
	queryTimeout, eventDuration time.Duration,
) *CalendarService {
	return &CalendarService{
		store:         store,
		states:        states,
		timezones:     timezones,
		client:        client,
		log:           log,
		queryTimeout:  queryTimeout,
		eventDuration: eventDuration,
	}
}

// Enabled reports whether Google Calendar is configured.
func (s *CalendarService) Enabled() bool {
	return s.client != nil
}

// Connect starts connecting Google Calendar for the user and returns the
// Google consent page URL.
func (s *CalendarService) Connect(ctx context.Context, userID uuid.UUID) (string, error) {
	if !s.Enabled() {
		return "", model.ErrGoogleDisabled
	}
	state, err := newSessionToken()
	if err != nil {
		return "", fmt.Errorf("create oauth state: %w", err)
	}

	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	if err = s.states.SaveOAuthState(queryCtx, state, userID, oauthStateTTL); err != nil {
		return "", fmt.Errorf("store oauth state: %w", err)
	}
	return s.client.AuthURL(state), nil
}

// Callback finishes connecting Google Calendar after the consent page.
// It returns model.ErrOAuthStateNotFound for an unknown, expired or used state.
func (s *CalendarService) Callback(ctx context.Context, code, state, googleErr string) error {
	if !s.Enabled() {
		return model.ErrGoogleDisabled
	}

	// The state is consumed first, so it cannot be replayed even when the
	// user refused access.
	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	userID, err := s.states.TakeOAuthState(queryCtx, state)
	cancel()
	if err != nil {
		if errors.Is(err, model.ErrOAuthStateNotFound) {
			return model.ErrOAuthStateNotFound
		}
		return fmt.Errorf("take oauth state: %w", err)
	}
	if googleErr != "" {
		return fmt.Errorf("google consent failed: %s", googleErr)
	}
	if code == "" {
		return errors.New("google callback without code")
	}

	googleCtx, cancel := context.WithTimeout(ctx, googleCallTimeout)
	tok, err := s.client.Exchange(googleCtx, code)
	cancel()
	if err != nil {
		return fmt.Errorf("exchange google code: %w", err)
	}

	integration := &model.GoogleIntegration{
		UserID:       userID,
		RefreshToken: tok.RefreshToken,
		AccessToken:  tok.AccessToken,
		TokenExpiry:  tok.Expiry,
		CalendarID:   "primary",
	}
	if tok.Email != "" {
		integration.GoogleEmail = &tok.Email
	}

	queryCtx, cancel = context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	if err = s.store.UpsertGoogleIntegration(queryCtx, integration); err != nil {
		return fmt.Errorf("save google integration: %w", err)
	}
	return nil
}

// Status returns the user's Google Calendar connection.
// It returns model.ErrGoogleNotConnected when the user has not connected it.
func (s *CalendarService) Status(ctx context.Context, userID uuid.UUID) (*model.GoogleIntegration, error) {
	if !s.Enabled() {
		return nil, model.ErrGoogleDisabled
	}

	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	integration, err := s.store.GetGoogleIntegration(queryCtx, userID)
	if err != nil {
		if errors.Is(err, model.ErrGoogleNotConnected) {
			return nil, model.ErrGoogleNotConnected
		}
		return nil, fmt.Errorf("get google integration: %w", err)
	}
	return integration, nil
}

// Disconnect revokes and removes the user's Google Calendar connection.
// Disconnecting a user who has not connected Google Calendar is not an error.
func (s *CalendarService) Disconnect(ctx context.Context, userID uuid.UUID) error {
	if !s.Enabled() {
		return model.ErrGoogleDisabled
	}

	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	integration, err := s.store.GetGoogleIntegration(queryCtx, userID)
	cancel()
	if errors.Is(err, model.ErrGoogleNotConnected) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get google integration: %w", err)
	}

	// Revoking is best effort: the user wants the connection gone even when
	// Google is unreachable, and the user can also revoke access in Google.
	googleCtx, cancel := context.WithTimeout(ctx, googleCallTimeout)
	if err = s.client.Revoke(googleCtx, integration.RefreshToken); err != nil {
		s.log.Warn("failed to revoke google token", slog.String("user_id", userID.String()), logger.Err(err))
	}
	cancel()

	return s.deleteIntegration(ctx, userID)
}

// SyncCreatedTask creates a Google Calendar event for a new meeting or
// reminder and saves its event ID. Tasks and users without a connection are
// skipped.
// When Google no longer accepts the token, the connection is removed.
func (s *CalendarService) SyncCreatedTask(ctx context.Context, task *model.Task) error {
	if !s.Enabled() || task == nil || (task.Type != meetingType && task.Type != reminderType) || task.Date == nil {
		return nil
	}

	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	integration, err := s.store.GetGoogleIntegration(queryCtx, task.UserID)
	cancel()
	if errors.Is(err, model.ErrGoogleNotConnected) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get google integration: %w", err)
	}

	event, err := s.buildEvent(ctx, task)
	if err != nil {
		return err
	}

	creds, err := s.credentials(ctx, integration)
	if err != nil {
		return err
	}

	googleCtx, cancel := context.WithTimeout(ctx, googleCallTimeout)
	eventID, err := s.client.CreateEvent(googleCtx, creds, integration.CalendarID, event)
	cancel()
	if err != nil {
		return s.handleGoogleError(ctx, task.UserID, "create google event", err)
	}

	queryCtx, cancel = context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	if err = s.store.SetTaskGoogleEventID(queryCtx, task.UserID, task.ID, &eventID); err != nil {
		return fmt.Errorf("save google event id: %w", err)
	}
	return nil
}

// SyncDeletedTasks deletes the Google events of deleted records. Failures are
// only logged: the records are already deleted and the user gets the result.
func (s *CalendarService) SyncDeletedTasks(ctx context.Context, tasks []model.Task) {
	s.syncTasks(ctx, tasks, "delete google event", func(creds google.Credentials, calendarID string, task model.Task) error {
		googleCtx, cancel := context.WithTimeout(ctx, googleCallTimeout)
		defer cancel()
		return s.client.DeleteEvent(googleCtx, creds, calendarID, *task.GoogleEventID)
	})
}

// SyncUpdatedTasks moves the Google events of updated records to their new
// title, date and time. Failures are only logged, as in SyncDeletedTasks.
func (s *CalendarService) SyncUpdatedTasks(ctx context.Context, tasks []model.Task) {
	s.syncTasks(ctx, tasks, "update google event", func(creds google.Credentials, calendarID string, task model.Task) error {
		event, err := s.buildEvent(ctx, &task)
		if err != nil {
			return err
		}
		googleCtx, cancel := context.WithTimeout(ctx, googleCallTimeout)
		defer cancel()
		return s.client.UpdateEvent(googleCtx, creds, calendarID, *task.GoogleEventID, event)
	})
}

// syncTasks calls sync for every record that has a Google event. All records
// belong to one user, because deletes and updates are owner-scoped.
func (s *CalendarService) syncTasks(
	ctx context.Context,
	tasks []model.Task,
	op string,
	sync func(creds google.Credentials, calendarID string, task model.Task) error,
) {
	if !s.Enabled() {
		return
	}
	synced := make([]model.Task, 0, len(tasks))
	for _, task := range tasks {
		if task.GoogleEventID != nil {
			synced = append(synced, task)
		}
	}
	if len(synced) == 0 {
		return
	}
	userID := synced[0].UserID
	log := s.log.With(slog.String("op", op), slog.String("user_id", userID.String()))

	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	integration, err := s.store.GetGoogleIntegration(queryCtx, userID)
	cancel()
	if errors.Is(err, model.ErrGoogleNotConnected) {
		return
	}
	if err != nil {
		log.Warn("failed to sync tasks with google calendar", logger.Err(err))
		return
	}

	creds, err := s.credentials(ctx, integration)
	if err != nil {
		log.Warn("failed to sync tasks with google calendar", logger.Err(err))
		return
	}
	for _, task := range synced {
		if err = sync(creds, integration.CalendarID, task); err != nil {
			err = s.handleGoogleError(ctx, userID, op, err)
			log.Warn("failed to sync task with google calendar", slog.String("task_id", task.ID.String()), logger.Err(err))
			if errors.Is(err, model.ErrGoogleTokenRevoked) {
				return
			}
		}
	}
}

// GoogleBusy reports whether the user's Google Calendar has events between
// start and end. Without Google or a connection the calendar counts as free.
func (s *CalendarService) GoogleBusy(ctx context.Context, userID uuid.UUID, start, end time.Time) (bool, error) {
	if !s.Enabled() {
		return false, nil
	}

	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	integration, err := s.store.GetGoogleIntegration(queryCtx, userID)
	cancel()
	if errors.Is(err, model.ErrGoogleNotConnected) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get google integration: %w", err)
	}

	creds, err := s.credentials(ctx, integration)
	if err != nil {
		return false, err
	}

	googleCtx, cancel := context.WithTimeout(ctx, googleCallTimeout)
	busy, err := s.client.FreeBusy(googleCtx, creds, integration.CalendarID, start, end)
	cancel()
	if err != nil {
		return false, s.handleGoogleError(ctx, userID, "query google free/busy", err)
	}
	return len(busy) > 0, nil
}

// credentials returns a valid access token and saves it when it was refreshed.
func (s *CalendarService) credentials(ctx context.Context, integration *model.GoogleIntegration) (google.Credentials, error) {
	googleCtx, cancel := context.WithTimeout(ctx, googleCallTimeout)
	creds, refreshed, err := s.client.Token(googleCtx, integration)
	cancel()
	if err != nil {
		return google.Credentials{}, s.handleGoogleError(ctx, integration.UserID, "get google token", err)
	}
	if refreshed {
		queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
		err = s.store.UpdateGoogleTokens(queryCtx, integration.UserID, creds.AccessToken, creds.Expiry)
		cancel()
		if err != nil {
			return google.Credentials{}, fmt.Errorf("save refreshed google token: %w", err)
		}
	}
	return creds, nil
}

// buildEvent places the task's date and time in the user's time zone.
// A task without time becomes an all-day event. A reminder takes no time:
// it ends where it starts.
func (s *CalendarService) buildEvent(ctx context.Context, task *model.Task) (google.Event, error) {
	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	timezone, err := s.timezones.GetUserTimezone(queryCtx, task.UserID)
	cancel()
	if err != nil {
		return google.Event{}, fmt.Errorf("get user timezone: %w", err)
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return google.Event{}, fmt.Errorf("invalid user timezone: %w", err)
	}

	date := *task.Date
	event := google.Event{Title: task.Title, TimeZone: timezone, Reminder: task.Type == reminderType}
	if task.Time == nil {
		event.AllDay = true
		event.Start = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, location)
		return event, nil
	}

	clock, err := time.Parse("15:04", *task.Time)
	if err != nil {
		return google.Event{}, fmt.Errorf("parse task time: %w", err)
	}
	event.Start = time.Date(date.Year(), date.Month(), date.Day(), clock.Hour(), clock.Minute(), 0, 0, location)
	event.End = event.Start
	if !event.Reminder {
		event.End = event.Start.Add(s.eventDuration)
	}
	return event, nil
}

// handleGoogleError removes the connection when Google no longer accepts the token.
func (s *CalendarService) handleGoogleError(ctx context.Context, userID uuid.UUID, op string, err error) error {
	if errors.Is(err, model.ErrGoogleTokenRevoked) {
		if deleteErr := s.deleteIntegration(ctx, userID); deleteErr != nil {
			return fmt.Errorf("%s: %w; %w", op, err, deleteErr)
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

func (s *CalendarService) deleteIntegration(ctx context.Context, userID uuid.UUID) error {
	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	if err := s.store.DeleteGoogleIntegration(queryCtx, userID); err != nil {
		return fmt.Errorf("delete google integration: %w", err)
	}
	return nil
}

// TaskSyncer sends a created task to an external calendar.
type TaskSyncer interface {
	SyncCreatedTask(ctx context.Context, task *model.Task) error
}

// CalendarSyncingCreator creates tasks and then sends them to Google Calendar.
// A failed sync is only logged: the task is already saved and the user gets it.
type CalendarSyncingCreator struct {
	inner  TaskCreator
	syncer TaskSyncer
	log    *slog.Logger
}

// NewCalendarSyncingCreator wraps a task creator with calendar sync.
func NewCalendarSyncingCreator(inner TaskCreator, syncer TaskSyncer, log *slog.Logger) *CalendarSyncingCreator {
	return &CalendarSyncingCreator{inner: inner, syncer: syncer, log: log}
}

// Create saves the task through the inner creator and syncs it on success.
func (c *CalendarSyncingCreator) Create(ctx context.Context, cmd services.CreateTaskCommand) (*model.Task, error) {
	task, err := c.inner.Create(ctx, cmd)
	if err != nil {
		return task, err
	}
	if syncErr := c.syncer.SyncCreatedTask(ctx, task); syncErr != nil {
		c.log.Warn("failed to sync task with google calendar",
			slog.String("user_id", task.UserID.String()),
			slog.String("task_id", task.ID.String()),
			logger.Err(syncErr),
		)
	}
	return task, nil
}
