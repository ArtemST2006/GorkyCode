package subservice

import (
	"context"
	"encoding/json"
	"errors"
	"gorkycode/backend/internal/model"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	api "gorkycode/backend/internal/generated"
)

// fakeTaskLister returns what the test configured and records its arguments.
type fakeTaskLister struct {
	tasks []model.Task
	err   error

	called      bool
	userID      uuid.UUID
	from        time.Time
	to          time.Time
	types       []string
	statuses    []string
	searchQuery string
	searchFrom  *time.Time
	searchTo    *time.Time
}

func (f *fakeTaskLister) ListEvents(_ context.Context, userID uuid.UUID, from, to time.Time, types []string) ([]model.Task, error) {
	f.called = true
	f.userID, f.from, f.to, f.types = userID, from, to, types
	return f.tasks, f.err
}

func (f *fakeTaskLister) ListEventsFiltered(_ context.Context, userID uuid.UUID, from, to *time.Time, types, statuses []string) ([]model.Task, error) {
	f.called = true
	f.userID, f.types, f.statuses = userID, types, statuses
	if from != nil {
		f.from = *from
	}
	if to != nil {
		f.to = *to
	}
	return f.tasks, f.err
}

func (f *fakeTaskLister) SearchEvents(_ context.Context, userID uuid.UUID, query string, from, to *time.Time, types []string) ([]model.Task, error) {
	f.called = true
	f.userID, f.searchQuery, f.searchFrom, f.searchTo, f.types = userID, query, from, to, types
	return f.tasks, f.err
}

func date(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestListEventsPassesQueryToStore(t *testing.T) {
	userID := uuid.New()
	for _, tc := range []struct {
		name      string
		action    string
		wantTypes []string
	}{
		{
			name:   "all types when record_types is absent",
			action: `{"date_from":"2026-10-20","date_to_exclusive":"2026-10-21"}`,
		},
		{
			name:      "record types passed to storage",
			action:    `{"date_from":"2026-10-20","date_to_exclusive":"2026-10-21","record_types":["meeting","task","reminder"]}`,
			wantTypes: []string{"meeting", "task", "reminder"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeTaskLister{}

			if _, err := NewLister(store).ListEvents(t.Context(), userID, json.RawMessage(tc.action)); err != nil {
				t.Fatalf("ListEvents() error = %v", err)
			}

			if store.userID != userID {
				t.Fatalf("userID = %s, want %s", store.userID, userID)
			}
			if !store.from.Equal(date(t, "2026-10-20")) || !store.to.Equal(date(t, "2026-10-21")) {
				t.Fatalf("period = [%s, %s), want [2026-10-20, 2026-10-21)", store.from, store.to)
			}
			if tc.wantTypes == nil && store.types != nil {
				t.Fatalf("types = %v, want nil", store.types)
			}
			if !slices.Equal(store.types, tc.wantTypes) {
				t.Fatalf("types = %v, want %v", store.types, tc.wantTypes)
			}
		})
	}
}

func TestListEventsFiltersStatusWithoutPeriod(t *testing.T) {
	store := &fakeTaskLister{}
	_, err := NewLister(store).ListEvents(t.Context(), uuid.New(), json.RawMessage(`{"record_types":["task"],"statuses":["active"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !store.called || !store.from.IsZero() || !store.to.IsZero() || !slices.Equal(store.types, []string{"task"}) || !slices.Equal(store.statuses, []string{"active"}) {
		t.Fatalf("filters: called=%v from=%s to=%s types=%v statuses=%v", store.called, store.from, store.to, store.types, store.statuses)
	}
}

func TestListEventsConvertsTasks(t *testing.T) {
	userID := uuid.New()
	meetingDate := date(t, "2026-10-20")
	meetingTime := "15:00"
	createdAt := time.Date(2026, 10, 18, 12, 0, 20, 0, time.UTC)
	tasks := []model.Task{
		{ID: uuid.New(), UserID: userID, Type: "meeting", Title: "Встреча с Иваном", Date: &meetingDate, Time: &meetingTime, Status: "active", CreatedAt: createdAt},
		{ID: uuid.New(), UserID: userID, Type: "task", Title: "Отчёт", Status: "completed", CreatedAt: createdAt},
		{ID: uuid.New(), UserID: userID, Type: "task", Title: "Проект", Status: "in_progress", CreatedAt: createdAt},
		{ID: uuid.New(), UserID: userID, Type: "reminder", Title: "Позвонить", Date: &meetingDate, Status: "active", CreatedAt: createdAt},
	}
	store := &fakeTaskLister{tasks: tasks}

	got, err := NewLister(store).ListEvents(t.Context(), userID, json.RawMessage(`{"date_from":"2026-10-20","date_to_exclusive":"2026-10-21"}`))
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}

	if len(got.Items) != len(tasks) {
		t.Fatalf("len(items) = %d, want %d", len(got.Items), len(tasks))
	}
	wantTypes := []api.TaskItemRecordType{api.TaskItemRecordTypeMeeting, api.TaskItemRecordTypeTask, api.TaskItemRecordTypeTask, api.TaskItemRecordTypeReminder}
	wantStatuses := []api.TaskItemStatus{api.TaskItemStatusActive, api.TaskItemStatusCompleted, api.TaskItemStatusInProgress, api.TaskItemStatusActive}
	for i, item := range got.Items {
		task := tasks[i]
		if item.Id != task.ID || item.UserId != task.UserID || item.Title != task.Title || !item.CreatedAt.Equal(task.CreatedAt) {
			t.Fatalf("items[%d] = %+v, want fields from %+v", i, item, task)
		}
		if item.RecordType != wantTypes[i] {
			t.Fatalf("items[%d].RecordType = %q, want %q", i, item.RecordType, wantTypes[i])
		}
		if item.Status != wantStatuses[i] {
			t.Fatalf("items[%d].Status = %q, want %q", i, item.Status, wantStatuses[i])
		}
	}

	if got.Items[0].Date == nil || !got.Items[0].Date.Equal(meetingDate) {
		t.Fatalf("items[0].Date = %v, want %s", got.Items[0].Date, meetingDate)
	}
	if got.Items[0].Time == nil || *got.Items[0].Time != meetingTime {
		t.Fatalf("items[0].Time = %v, want %q", got.Items[0].Time, meetingTime)
	}
	if got.Items[1].Date != nil || got.Items[1].Time != nil {
		t.Fatalf("items[1] date/time = %v/%v, want nil/nil", got.Items[1].Date, got.Items[1].Time)
	}
	if got.Items[3].Time != nil {
		t.Fatalf("items[3].Time = %v, want nil", got.Items[3].Time)
	}
}

func TestListEventsEmptyResultIsEmptyArray(t *testing.T) {
	store := &fakeTaskLister{}

	got, err := NewLister(store).ListEvents(t.Context(), uuid.New(), json.RawMessage(`{"date_from":"2026-10-20","date_to_exclusive":"2026-10-21"}`))
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}

	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"items":[]}` {
		t.Fatalf("body = %s, want {\"items\":[]}", body)
	}
}

func TestListEventsRejectsInvalidAction(t *testing.T) {
	for _, tc := range []struct {
		name   string
		userID uuid.UUID
		action string
	}{
		{name: "nil user", userID: uuid.Nil, action: `{"date_from":"2026-10-20","date_to_exclusive":"2026-10-21"}`},
		{name: "bad json", action: `{`},
		{name: "null action", action: `null`},
		{name: "invalid date", action: `{"date_from":"20-10-2026","date_to_exclusive":"2026-10-21"}`},
		{name: "equal dates", action: `{"date_from":"2026-10-20","date_to_exclusive":"2026-10-20"}`},
		{name: "reversed dates", action: `{"date_from":"2026-10-21","date_to_exclusive":"2026-10-20"}`},
		{name: "empty record_types", action: `{"date_from":"2026-10-20","date_to_exclusive":"2026-10-21","record_types":[]}`},
		{name: "unknown record type", action: `{"date_from":"2026-10-20","date_to_exclusive":"2026-10-21","record_types":["birthday"]}`},
		{name: "duplicate record type", action: `{"date_from":"2026-10-20","date_to_exclusive":"2026-10-21","record_types":["meeting","meeting"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID := tc.userID
			if tc.name != "nil user" {
				userID = uuid.New()
			}
			store := &fakeTaskLister{}

			_, err := NewLister(store).ListEvents(t.Context(), userID, json.RawMessage(tc.action))

			if !errors.Is(err, model.ErrInvalidAction) {
				t.Fatalf("error = %v, want ErrInvalidAction", err)
			}
			if store.called {
				t.Fatal("store must not be called for an invalid action")
			}
		})
	}
}

func TestListEventsPropagatesStoreError(t *testing.T) {
	storeErr := errors.New("database is down")
	store := &fakeTaskLister{err: storeErr}

	_, err := NewLister(store).ListEvents(t.Context(), uuid.New(), json.RawMessage(`{"date_from":"2026-10-20","date_to_exclusive":"2026-10-21"}`))

	if !errors.Is(err, storeErr) {
		t.Fatalf("error = %v, want %v", err, storeErr)
	}
	if errors.Is(err, model.ErrInvalidAction) {
		t.Fatal("storage error must not be reported as an invalid action")
	}
}

func TestListEventsRejectsUnknownStoredValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		task model.Task
	}{
		{name: "unknown type", task: model.Task{Type: "birthday", Status: "active"}},
		{name: "unknown status", task: model.Task{Type: "task", Status: "deleted"}},
		{name: "meeting in progress", task: model.Task{Type: "meeting", Status: "in_progress"}},
		{name: "reminder in progress", task: model.Task{Type: "reminder", Status: "in_progress"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeTaskLister{tasks: []model.Task{tc.task}}

			_, err := NewLister(store).ListEvents(t.Context(), uuid.New(), json.RawMessage(`{"date_from":"2026-10-20","date_to_exclusive":"2026-10-21"}`))

			if err == nil {
				t.Fatal("ListEvents() error = nil, want error")
			}
		})
	}
}
