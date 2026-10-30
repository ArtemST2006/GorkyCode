package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	api "gorkycode/backend/internal/generated"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"

	"github.com/google/uuid"
	openapitypes "github.com/oapi-codegen/runtime/types"
)

func windowItem(userID uuid.UUID, at time.Time, status api.TaskItemStatus) api.TaskItem {
	clock := at.Format("15:04")
	return api.TaskItem{
		Id: uuid.New(), UserId: userID, Title: "Дело", RecordType: api.TaskItemRecordTypeTask,
		Date: &openapitypes.Date{Time: at}, Time: &clock, Status: status,
	}
}

func TestModelFilledWindowHandlesMissingExistingAndStaleTaskID(t *testing.T) {
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		oldID  bool
		getErr error
	}{
		{name: "missing task_id"},
		{name: "existing task_id", oldID: true},
		{name: "stale task_id", oldID: true, getErr: model.ErrDraftNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID, oldID := uuid.New(), uuid.New()
			drafts := &fakeDraftStore{value: `{"draft":{},"recent_messages":[{"role":"user","text":"Предыдущий запрос"}]}`, getErr: tc.getErr}
			soon := time.Now().In(location).Truncate(time.Minute).Add(15 * time.Minute)
			lister := &fakeEventLister{collection: api.TaskCollection{Items: []api.TaskItem{
				windowItem(userID, soon, api.TaskItemStatusActive),
				windowItem(userID, soon.Add(90*time.Minute), api.TaskItemStatusActive),
				windowItem(userID, soon, api.TaskItemStatusCompleted),
			}}}
			var received ai.JobRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Errorf("decode AI request: %v", err)
					return
				}
				_, _ = fmt.Fprintf(w, `{"user_id":%q,"task_id":%q,"status":"ready","intent":"list_events","message":"Показываю дела на ближайший час.","action":{"upcoming_minutes":60,"statuses":["active"]},"context":{"recent_messages":[{"role":"user","text":"а напомни что я должен сделать в течении часа?"}],"draft":null}}`, userID, received.TaskId)
			}))
			t.Cleanup(server.Close)
			process := NewTaskProcess(nil, drafts, &fakeTimezoneStore{timezone: "Europe/Moscow"}, nil, lister, nil, server.URL)
			request := &api.TaskTextRequest{UserId: userID, Text: "а напомни что я должен сделать в течении часа?"}
			if tc.oldID {
				request.TaskId = &oldID
			}
			result, err := process.Process(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			response, ok := result.Body.(*api.TaskCollectionResponse)
			if !ok || result.Intent != "list_events" {
				t.Fatalf("unexpected result: %#v", result)
			}
			if response.TaskId == uuid.Nil || response.TaskId != received.TaskId || response.UserId != userID || len(response.Data.Items) != 1 {
				t.Fatalf("unexpected response: %#v", response)
			}
			if tc.oldID && tc.getErr == nil && response.TaskId != oldID {
				t.Fatalf("existing ID changed: %s", response.TaskId)
			}
			if tc.oldID && tc.getErr != nil && response.TaskId == oldID {
				t.Fatalf("stale ID reused: %s", response.TaskId)
			}
			if received.Text != request.Text || lister.userID != userID {
				t.Fatalf("AI request or list user mismatched: %#v %s", received, lister.userID)
			}
			if strings.Contains(string(lister.action), "upcoming_minutes") || !strings.Contains(string(lister.action), `"statuses":["active"]`) {
				t.Fatalf("invalid storage filter: %s", lister.action)
			}
		})
	}
}

func TestListTimeWindowCrossesMidnightAndExcludesDateOnly(t *testing.T) {
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 30, 23, 30, 0, 0, location)
	userID := uuid.New()
	dateOnly := windowItem(userID, start, api.TaskItemStatusActive)
	dateOnly.Time = nil
	lister := &fakeEventLister{collection: api.TaskCollection{Items: []api.TaskItem{
		windowItem(userID, start.Add(-time.Minute), api.TaskItemStatusActive),
		windowItem(userID, start.Add(15*time.Minute), api.TaskItemStatusActive),
		windowItem(userID, start.Add(20*time.Minute), api.TaskItemStatusInProgress),
		windowItem(userID, start.Add(45*time.Minute), api.TaskItemStatusActive),
		windowItem(userID, start.Add(61*time.Minute), api.TaskItemStatusActive),
		dateOnly,
	}}}
	process := &TaskProcess{reader: lister}
	collection, err := process.listEventsWithWindow(context.Background(), userID, json.RawMessage(`{"upcoming_minutes":60}`), location, start)
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Items) != 3 {
		t.Fatalf("items = %v, want three within the hour", collection.Items)
	}
	if !strings.Contains(string(lister.action), `"statuses":["active","in_progress"]`) {
		t.Fatalf("default status filter excludes tasks in progress: %s", lister.action)
	}
	if !strings.Contains(string(lister.action), `"date_to_exclusive":"2026-11-01"`) {
		t.Fatalf("date range does not cross midnight: %s", lister.action)
	}
}

func TestListTimeWindowUsesActualRequestSecond(t *testing.T) {
	location := time.UTC
	start := time.Date(2026, 10, 30, 12, 0, 59, 0, location)
	userID := uuid.New()
	lister := &fakeEventLister{collection: api.TaskCollection{Items: []api.TaskItem{
		windowItem(userID, start.Add(-59*time.Second), api.TaskItemStatusActive),
		windowItem(userID, start.Add(time.Second), api.TaskItemStatusActive),
		windowItem(userID, start.Add(59*time.Minute+time.Second), api.TaskItemStatusActive),
		windowItem(userID, start.Add(60*time.Minute+time.Second), api.TaskItemStatusActive),
	}}}
	process := &TaskProcess{reader: lister}
	collection, err := process.listEventsWithWindow(t.Context(), userID, json.RawMessage(`{"upcoming_minutes":60}`), location, start)
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Items) != 2 {
		t.Fatalf("got %d items, want the two actually inside the hour", len(collection.Items))
	}
}
