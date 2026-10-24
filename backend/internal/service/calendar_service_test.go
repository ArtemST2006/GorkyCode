package service

import (
	"context"
	"errors"
	"gorkycode/backend/internal/integration/google"
	"gorkycode/backend/internal/model"
	services "gorkycode/backend/internal/service/subservice"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

const testEventDuration = time.Hour

// fakeGoogleStore keeps one integration per user, like google_integrations.
type fakeGoogleStore struct {
	integrations map[uuid.UUID]*model.GoogleIntegration
	getErr       error
	upsertErr    error

	upserted       *model.GoogleIntegration
	updatedAccess  string
	updatedExpiry  *time.Time
	deleted        []uuid.UUID
	eventTaskID    uuid.UUID
	eventUserID    uuid.UUID
	eventID        *string
	missedDeadline bool
}

func newFakeGoogleStore(integrations ...*model.GoogleIntegration) *fakeGoogleStore {
	store := &fakeGoogleStore{integrations: map[uuid.UUID]*model.GoogleIntegration{}}
	for _, integration := range integrations {
		store.integrations[integration.UserID] = integration
	}
	return store
}

func (s *fakeGoogleStore) checkDeadline(ctx context.Context) {
	if _, ok := ctx.Deadline(); !ok {
		s.missedDeadline = true
	}
}

func (s *fakeGoogleStore) UpsertGoogleIntegration(ctx context.Context, integration *model.GoogleIntegration) error {
	s.checkDeadline(ctx)
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.upserted = integration
	s.integrations[integration.UserID] = integration
	return nil
}

func (s *fakeGoogleStore) GetGoogleIntegration(ctx context.Context, userID uuid.UUID) (*model.GoogleIntegration, error) {
	s.checkDeadline(ctx)
	if s.getErr != nil {
		return nil, s.getErr
	}
	integration, ok := s.integrations[userID]
	if !ok {
		return nil, model.ErrGoogleNotConnected
	}
	return integration, nil
}

func (s *fakeGoogleStore) UpdateGoogleTokens(ctx context.Context, _ uuid.UUID, accessToken string, expiry *time.Time) error {
	s.checkDeadline(ctx)
	s.updatedAccess, s.updatedExpiry = accessToken, expiry
	return nil
}

func (s *fakeGoogleStore) DeleteGoogleIntegration(ctx context.Context, userID uuid.UUID) error {
	s.checkDeadline(ctx)
	s.deleted = append(s.deleted, userID)
	delete(s.integrations, userID)
	return nil
}

func (s *fakeGoogleStore) SetTaskGoogleEventID(ctx context.Context, userID, taskID uuid.UUID, eventID *string) error {
	s.checkDeadline(ctx)
	s.eventUserID, s.eventTaskID, s.eventID = userID, taskID, eventID
	return nil
}

// fakeOAuthStates keeps states in a map; TakeOAuthState removes them.
type fakeOAuthStates struct {
	states map[string]uuid.UUID
	ttl    time.Duration
}

func newFakeOAuthStates() *fakeOAuthStates {
	return &fakeOAuthStates{states: map[string]uuid.UUID{}}
}

func (s *fakeOAuthStates) SaveOAuthState(_ context.Context, state string, userID uuid.UUID, ttl time.Duration) error {
	s.states[state] = userID
	s.ttl = ttl
	return nil
}

func (s *fakeOAuthStates) TakeOAuthState(_ context.Context, state string) (uuid.UUID, error) {
	userID, ok := s.states[state]
	if !ok {
		return uuid.Nil, model.ErrOAuthStateNotFound
	}
	delete(s.states, state)
	return userID, nil
}

// fakeGoogleClient returns what the test configured and records the calls.
type fakeGoogleClient struct {
	token      *google.Token
	exchErr    error
	creds      google.Credentials
	refreshed  bool
	tokenErr   error
	eventID    string
	createErr  error
	revokeErr  error
	busy       []google.Busy
	busyErr    error
	authURLFor string

	exchangedCode string
	createdEvent  *google.Event
	createdIn     string
	revoked       string
}

func (c *fakeGoogleClient) AuthURL(state string) string {
	c.authURLFor = state
	return "https://accounts.example/auth?state=" + state
}

func (c *fakeGoogleClient) Exchange(_ context.Context, code string) (*google.Token, error) {
	c.exchangedCode = code
	return c.token, c.exchErr
}

func (c *fakeGoogleClient) Token(_ context.Context, _ *model.GoogleIntegration) (google.Credentials, bool, error) {
	return c.creds, c.refreshed, c.tokenErr
}

func (c *fakeGoogleClient) CreateEvent(_ context.Context, _ google.Credentials, calendarID string, ev google.Event) (string, error) {
	c.createdIn = calendarID
	c.createdEvent = &ev
	return c.eventID, c.createErr
}

func (c *fakeGoogleClient) UpdateEvent(context.Context, google.Credentials, string, string, google.Event) error {
	return nil
}

func (c *fakeGoogleClient) DeleteEvent(context.Context, google.Credentials, string, string) error {
	return nil
}

func (c *fakeGoogleClient) FreeBusy(context.Context, google.Credentials, string, time.Time, time.Time) ([]google.Busy, error) {
	return c.busy, c.busyErr
}

func (c *fakeGoogleClient) Revoke(_ context.Context, token string) error {
	c.revoked = token
	return c.revokeErr
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newCalendarService(store *fakeGoogleStore, states *fakeOAuthStates, client GoogleCalendarClient) *CalendarService {
	return NewCalendarService(store, states, &fakeTimezoneStore{timezone: "Europe/Moscow"}, client,
		discardLogger(), time.Second, testEventDuration)
}

func TestCalendarServiceDisabled(t *testing.T) {
	calendar := newCalendarService(newFakeGoogleStore(), newFakeOAuthStates(), nil)
	userID := uuid.New()

	if calendar.Enabled() {
		t.Fatal("service without a client must be disabled")
	}
	if _, err := calendar.Connect(t.Context(), userID); !errors.Is(err, model.ErrGoogleDisabled) {
		t.Fatalf("Connect() = %v, want ErrGoogleDisabled", err)
	}
	if err := calendar.Callback(t.Context(), "code", "state", ""); !errors.Is(err, model.ErrGoogleDisabled) {
		t.Fatalf("Callback() = %v, want ErrGoogleDisabled", err)
	}
	if _, err := calendar.Status(t.Context(), userID); !errors.Is(err, model.ErrGoogleDisabled) {
		t.Fatalf("Status() = %v, want ErrGoogleDisabled", err)
	}
	if err := calendar.Disconnect(t.Context(), userID); !errors.Is(err, model.ErrGoogleDisabled) {
		t.Fatalf("Disconnect() = %v, want ErrGoogleDisabled", err)
	}
	if err := calendar.SyncCreatedTask(t.Context(), newMeeting(userID, "15:00")); err != nil {
		t.Fatalf("SyncCreatedTask() = %v, want nil when disabled", err)
	}
}

func TestCalendarServiceConnectSavesState(t *testing.T) {
	states := newFakeOAuthStates()
	client := &fakeGoogleClient{}
	calendar := newCalendarService(newFakeGoogleStore(), states, client)
	userID := uuid.New()

	authURL, err := calendar.Connect(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if states.ttl != 10*time.Minute {
		t.Fatalf("state TTL = %s, want 10m", states.ttl)
	}
	if len(states.states) != 1 || states.states[client.authURLFor] != userID {
		t.Fatalf("state for AuthURL %q was not saved for the user: %v", client.authURLFor, states.states)
	}
	if len(client.authURLFor) < 40 {
		t.Fatalf("state %q is too short for 32 random bytes", client.authURLFor)
	}
	if authURL != "https://accounts.example/auth?state="+client.authURLFor {
		t.Fatalf("authURL = %q", authURL)
	}
}

func TestCalendarServiceCallback(t *testing.T) {
	userID := uuid.New()
	expiry := time.Date(2026, 10, 23, 14, 0, 0, 0, time.UTC)

	t.Run("unknown state", func(t *testing.T) {
		client := &fakeGoogleClient{}
		calendar := newCalendarService(newFakeGoogleStore(), newFakeOAuthStates(), client)

		err := calendar.Callback(t.Context(), "code", "unknown", "")

		if !errors.Is(err, model.ErrOAuthStateNotFound) {
			t.Fatalf("Callback() = %v, want ErrOAuthStateNotFound", err)
		}
		if client.exchangedCode != "" {
			t.Fatal("code must not be exchanged for an unknown state")
		}
	})

	for _, tc := range []struct {
		name      string
		code      string
		googleErr string
	}{
		{name: "user denied access", googleErr: "access_denied"},
		{name: "missing code"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			states := newFakeOAuthStates()
			states.states["state"] = userID
			store := newFakeGoogleStore()
			client := &fakeGoogleClient{}
			calendar := newCalendarService(store, states, client)

			err := calendar.Callback(t.Context(), tc.code, "state", tc.googleErr)

			if err == nil {
				t.Fatal("Callback() = nil, want error")
			}
			if _, ok := states.states["state"]; ok {
				t.Fatal("state must be consumed even when the callback fails")
			}
			if client.exchangedCode != "" || store.upserted != nil {
				t.Fatal("failed callback must not exchange the code or save tokens")
			}
		})
	}

	t.Run("success", func(t *testing.T) {
		states := newFakeOAuthStates()
		states.states["state"] = userID
		store := newFakeGoogleStore()
		client := &fakeGoogleClient{token: &google.Token{
			AccessToken:  "access",
			RefreshToken: "refresh",
			Expiry:       &expiry,
			Email:        "artem@gmail.com",
		}}
		calendar := newCalendarService(store, states, client)

		if err := calendar.Callback(t.Context(), "auth-code", "state", ""); err != nil {
			t.Fatal(err)
		}

		if client.exchangedCode != "auth-code" {
			t.Fatalf("exchanged code = %q", client.exchangedCode)
		}
		got := store.upserted
		if got == nil || got.UserID != userID || got.AccessToken != "access" || got.RefreshToken != "refresh" ||
			got.TokenExpiry != &expiry || got.CalendarID != "primary" || got.GoogleEmail == nil || *got.GoogleEmail != "artem@gmail.com" {
			t.Fatalf("upserted integration = %+v", got)
		}
		if store.missedDeadline {
			t.Fatal("storage calls must have a deadline")
		}
		if err := calendar.Callback(t.Context(), "auth-code", "state", ""); !errors.Is(err, model.ErrOAuthStateNotFound) {
			t.Fatalf("second Callback() = %v, want ErrOAuthStateNotFound", err)
		}
	})

	t.Run("no email", func(t *testing.T) {
		states := newFakeOAuthStates()
		states.states["state"] = userID
		store := newFakeGoogleStore()
		calendar := newCalendarService(store, states, &fakeGoogleClient{token: &google.Token{RefreshToken: "refresh"}})

		if err := calendar.Callback(t.Context(), "code", "state", ""); err != nil {
			t.Fatal(err)
		}
		if store.upserted.GoogleEmail != nil {
			t.Fatalf("GoogleEmail = %q, want nil", *store.upserted.GoogleEmail)
		}
	})
}

func TestCalendarServiceStatus(t *testing.T) {
	userID := uuid.New()
	integration := &model.GoogleIntegration{UserID: userID, CalendarID: "primary"}
	calendar := newCalendarService(newFakeGoogleStore(integration), newFakeOAuthStates(), &fakeGoogleClient{})

	got, err := calendar.Status(t.Context(), userID)
	if err != nil || got != integration {
		t.Fatalf("Status() = %+v, %v; want stored integration", got, err)
	}
	if _, err = calendar.Status(t.Context(), uuid.New()); !errors.Is(err, model.ErrGoogleNotConnected) {
		t.Fatalf("Status() for another user = %v, want ErrGoogleNotConnected", err)
	}
}

func TestCalendarServiceDisconnect(t *testing.T) {
	userID := uuid.New()

	t.Run("not connected", func(t *testing.T) {
		store := newFakeGoogleStore()
		client := &fakeGoogleClient{}
		calendar := newCalendarService(store, newFakeOAuthStates(), client)

		if err := calendar.Disconnect(t.Context(), userID); err != nil {
			t.Fatalf("Disconnect() = %v, want nil", err)
		}
		if client.revoked != "" || len(store.deleted) != 0 {
			t.Fatal("nothing must be revoked or deleted without a connection")
		}
	})

	for _, tc := range []struct {
		name      string
		revokeErr error
	}{
		{name: "revoked"},
		{name: "revoke failure still deletes", revokeErr: errors.New("google unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeGoogleStore(&model.GoogleIntegration{UserID: userID, RefreshToken: "refresh"})
			client := &fakeGoogleClient{revokeErr: tc.revokeErr}
			calendar := newCalendarService(store, newFakeOAuthStates(), client)

			if err := calendar.Disconnect(t.Context(), userID); err != nil {
				t.Fatalf("Disconnect() = %v, want nil", err)
			}
			if client.revoked != "refresh" {
				t.Fatalf("revoked token = %q, want the refresh token", client.revoked)
			}
			if !reflect.DeepEqual(store.deleted, []uuid.UUID{userID}) {
				t.Fatalf("deleted = %v, want [%s]", store.deleted, userID)
			}
		})
	}
}

func newMeeting(userID uuid.UUID, clock string) *model.Task {
	date := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	task := &model.Task{ID: uuid.New(), UserID: userID, Type: "meeting", Title: "Встреча с Иваном", Date: &date}
	if clock != "" {
		task.Time = &clock
	}
	return task
}

func TestCalendarServiceSyncCreatedTask(t *testing.T) {
	userID := uuid.New()
	newIntegration := func() *model.GoogleIntegration {
		return &model.GoogleIntegration{UserID: userID, AccessToken: "old", RefreshToken: "refresh", CalendarID: "work"}
	}

	t.Run("skipped record types", func(t *testing.T) {
		for _, recordType := range []string{"task", "reminder"} {
			client := &fakeGoogleClient{eventID: "event-1"}
			calendar := newCalendarService(newFakeGoogleStore(newIntegration()), newFakeOAuthStates(), client)
			task := newMeeting(userID, "15:00")
			task.Type = recordType

			if err := calendar.SyncCreatedTask(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			if client.createdEvent != nil {
				t.Fatalf("%s must not be sent to Google Calendar", recordType)
			}
		}
	})

	t.Run("not connected", func(t *testing.T) {
		client := &fakeGoogleClient{eventID: "event-1"}
		calendar := newCalendarService(newFakeGoogleStore(), newFakeOAuthStates(), client)

		if err := calendar.SyncCreatedTask(t.Context(), newMeeting(userID, "15:00")); err != nil {
			t.Fatalf("SyncCreatedTask() = %v, want nil", err)
		}
		if client.createdEvent != nil {
			t.Fatal("event must not be created without a connection")
		}
	})

	t.Run("timed meeting", func(t *testing.T) {
		store := newFakeGoogleStore(newIntegration())
		newExpiry := time.Now().Add(time.Hour)
		client := &fakeGoogleClient{
			creds:     google.Credentials{AccessToken: "new", Expiry: &newExpiry},
			refreshed: true,
			eventID:   "event-1",
		}
		calendar := newCalendarService(store, newFakeOAuthStates(), client)
		task := newMeeting(userID, "15:00")

		if err := calendar.SyncCreatedTask(t.Context(), task); err != nil {
			t.Fatal(err)
		}

		if store.updatedAccess != "new" || store.updatedExpiry != &newExpiry {
			t.Fatalf("refreshed token was not saved: %q %v", store.updatedAccess, store.updatedExpiry)
		}
		ev := client.createdEvent
		wantStart := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC) // 15:00 Europe/Moscow
		if ev == nil || !ev.Start.Equal(wantStart) || !ev.End.Equal(wantStart.Add(testEventDuration)) {
			t.Fatalf("event = %+v, want start %s", ev, wantStart)
		}
		if ev.Title != task.Title || ev.TimeZone != "Europe/Moscow" || ev.AllDay || ev.Start.Location().String() != "Europe/Moscow" {
			t.Fatalf("unexpected event fields: %+v", ev)
		}
		if client.createdIn != "work" {
			t.Fatalf("calendar = %q, want the stored calendar", client.createdIn)
		}
		if store.eventTaskID != task.ID || store.eventUserID != userID || store.eventID == nil || *store.eventID != "event-1" {
			t.Fatalf("event id was not saved for the task: %v %v %v", store.eventTaskID, store.eventUserID, store.eventID)
		}
		if store.missedDeadline {
			t.Fatal("storage calls must have a deadline")
		}
	})

	t.Run("token not refreshed", func(t *testing.T) {
		store := newFakeGoogleStore(newIntegration())
		client := &fakeGoogleClient{creds: google.Credentials{AccessToken: "old"}, eventID: "event-1"}
		calendar := newCalendarService(store, newFakeOAuthStates(), client)

		if err := calendar.SyncCreatedTask(t.Context(), newMeeting(userID, "15:00")); err != nil {
			t.Fatal(err)
		}
		if store.updatedAccess != "" {
			t.Fatal("unchanged token must not be saved")
		}
	})

	t.Run("meeting without time is all-day", func(t *testing.T) {
		client := &fakeGoogleClient{eventID: "event-1"}
		calendar := newCalendarService(newFakeGoogleStore(newIntegration()), newFakeOAuthStates(), client)

		if err := calendar.SyncCreatedTask(t.Context(), newMeeting(userID, "")); err != nil {
			t.Fatal(err)
		}
		ev := client.createdEvent
		if ev == nil || !ev.AllDay || ev.Start.Format(time.DateOnly) != "2026-10-21" {
			t.Fatalf("event = %+v, want all-day on 2026-10-21", ev)
		}
	})

	for _, tc := range []struct {
		name      string
		tokenErr  error
		createErr error
	}{
		{name: "refresh token revoked", tokenErr: model.ErrGoogleTokenRevoked},
		{name: "calendar rejects token", createErr: model.ErrGoogleTokenRevoked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeGoogleStore(newIntegration())
			client := &fakeGoogleClient{tokenErr: tc.tokenErr, createErr: tc.createErr}
			calendar := newCalendarService(store, newFakeOAuthStates(), client)

			err := calendar.SyncCreatedTask(t.Context(), newMeeting(userID, "15:00"))

			if !errors.Is(err, model.ErrGoogleTokenRevoked) {
				t.Fatalf("SyncCreatedTask() = %v, want ErrGoogleTokenRevoked", err)
			}
			if !reflect.DeepEqual(store.deleted, []uuid.UUID{userID}) {
				t.Fatalf("revoked integration must be deleted, deleted = %v", store.deleted)
			}
			if store.eventID != nil {
				t.Fatal("event id must not be saved after a failure")
			}
		})
	}

	t.Run("other google error keeps integration", func(t *testing.T) {
		store := newFakeGoogleStore(newIntegration())
		calendar := newCalendarService(store, newFakeOAuthStates(), &fakeGoogleClient{createErr: errors.New("google unavailable")})

		if err := calendar.SyncCreatedTask(t.Context(), newMeeting(userID, "15:00")); err == nil {
			t.Fatal("SyncCreatedTask() = nil, want error")
		}
		if len(store.deleted) != 0 {
			t.Fatal("integration must stay after a temporary failure")
		}
	})
}

// fakeTaskSyncer records the synced task and returns the configured error.
type fakeTaskSyncer struct {
	err    error
	synced *model.Task
}

func (s *fakeTaskSyncer) SyncCreatedTask(_ context.Context, task *model.Task) error {
	s.synced = task
	return s.err
}

func TestCalendarSyncingCreator(t *testing.T) {
	cmd := services.CreateTaskCommand{
		UserID:     uuid.New(),
		RecordType: "meeting",
		Title:      "Встреча с Иваном",
		Date:       time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC),
		Time:       time.Date(0, 1, 1, 15, 0, 0, 0, time.UTC),
	}

	t.Run("sync error does not fail create", func(t *testing.T) {
		syncer := &fakeTaskSyncer{err: errors.New("google unavailable")}
		creator := NewCalendarSyncingCreator(&fakeTaskCreator{}, syncer, discardLogger())

		task, err := creator.Create(t.Context(), cmd)
		if err != nil {
			t.Fatalf("Create() = %v, want nil", err)
		}
		if task == nil || task.Title != cmd.Title || syncer.synced != task {
			t.Fatalf("Create() = %+v, synced %+v; want the created task", task, syncer.synced)
		}
	})

	t.Run("create error skips sync", func(t *testing.T) {
		syncer := &fakeTaskSyncer{}
		createErr := errors.New("database unavailable")
		creator := NewCalendarSyncingCreator(&fakeTaskCreator{err: createErr}, syncer, discardLogger())

		task, err := creator.Create(t.Context(), cmd)

		if !errors.Is(err, createErr) || task != nil {
			t.Fatalf("Create() = %+v, %v; want the create error", task, err)
		}
		if syncer.synced != nil {
			t.Fatal("failed create must not be synced")
		}
	})
}

func TestCalendarServiceGoogleBusy(t *testing.T) {
	userID := uuid.New()
	start := time.Date(2026, 10, 21, 15, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	connected := func() *fakeGoogleStore {
		return newFakeGoogleStore(&model.GoogleIntegration{UserID: userID, RefreshToken: "refresh", CalendarID: "primary"})
	}

	for _, tc := range []struct {
		name        string
		store       *fakeGoogleStore
		client      *fakeGoogleClient
		disabled    bool
		wantBusy    bool
		wantErr     bool
		wantDeleted bool
	}{
		{name: "busy", store: connected(), client: &fakeGoogleClient{busy: []google.Busy{{Start: start, End: end}}}, wantBusy: true},
		{name: "free", store: connected(), client: &fakeGoogleClient{}},
		{name: "not connected", store: newFakeGoogleStore(), client: &fakeGoogleClient{busy: []google.Busy{{Start: start, End: end}}}},
		{name: "disabled", store: connected(), disabled: true},
		{name: "google failure", store: connected(), client: &fakeGoogleClient{busyErr: errors.New("google unavailable")}, wantErr: true},
		{name: "revoked token", store: connected(), client: &fakeGoogleClient{tokenErr: model.ErrGoogleTokenRevoked}, wantErr: true, wantDeleted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var client GoogleCalendarClient
			if !tc.disabled {
				client = tc.client
			}
			calendar := newCalendarService(tc.store, newFakeOAuthStates(), client)

			busy, err := calendar.GoogleBusy(t.Context(), userID, start, end)

			if (err != nil) != tc.wantErr || busy != tc.wantBusy {
				t.Fatalf("GoogleBusy() = %v, %v; want busy=%v err=%v", busy, err, tc.wantBusy, tc.wantErr)
			}
			if deleted := len(tc.store.deleted) > 0; deleted != tc.wantDeleted {
				t.Fatalf("integration deleted = %v, want %v", deleted, tc.wantDeleted)
			}
		})
	}
}
