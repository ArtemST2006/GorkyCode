package service

import (
	"context"
	"errors"
	"gorkycode/backend/internal/model"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	api "gorkycode/backend/internal/generated"
)

// fakeConflictFinder returns the configured conflict and records the start.
type fakeConflictFinder struct {
	conflict *MeetingConflict
	err      error

	called bool
	start  time.Time
}

func (f *fakeConflictFinder) Find(_ context.Context, _ uuid.UUID, start time.Time) (*MeetingConflict, error) {
	f.called = true
	f.start = start
	return f.conflict, f.err
}

// fakePendingStore keeps pending tasks in a map, like Redis without expiry.
type fakePendingStore struct {
	values map[string]string
	ttl    time.Duration
}

func newFakePendingStore() *fakePendingStore {
	return &fakePendingStore{values: map[string]string{}}
}

func (s *fakePendingStore) SavePendingTask(_ context.Context, key, value string, ttl time.Duration) error {
	s.values[key] = value
	s.ttl = ttl
	return nil
}

func (s *fakePendingStore) TakePendingTask(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", model.ErrPendingTaskNotFound
	}
	delete(s.values, key)
	return value, nil
}

func createMeetingAIResponse(userID, taskID uuid.UUID, recordType string) string {
	return `{"user_id":"` + userID.String() + `","task_id":"` + taskID.String() + `",` +
		`"status":"ready","intent":"create_event","message":"Встреча с командой назначена на завтра в 15:00.",` +
		`"action":{"record_type":"` + recordType + `","title":"Встреча с командой","date":"2026-10-21","time":"15:00"},` +
		`"context":{"recent_messages":[{"role":"user","text":"встреча с командой"}],"draft":null}}`
}

type conflictTest struct {
	process  *TaskProcess
	ai       *aiStub
	creator  *fakeTaskCreator
	finder   *fakeConflictFinder
	pending  *fakePendingStore
	userID   uuid.UUID
	taskID   uuid.UUID
	location *time.Location
}

func newConflictTest(t *testing.T, conflict *MeetingConflict, recordType string) *conflictTest {
	t.Helper()
	tc := &conflictTest{
		creator: &fakeTaskCreator{},
		finder:  &fakeConflictFinder{conflict: conflict},
		pending: newFakePendingStore(),
		userID:  uuid.New(),
		taskID:  uuid.New(),
	}
	var err error
	if tc.location, err = time.LoadLocation("Europe/Moscow"); err != nil {
		t.Fatal(err)
	}
	tc.ai = &aiStub{t: t, status: http.StatusOK, body: createMeetingAIResponse(tc.userID, tc.taskID, recordType)}
	server := httptest.NewServer(tc.ai)
	t.Cleanup(server.Close)
	tc.process = NewTaskProcess(&fakeSTT{}, &fakeDraftStore{value: `{"draft":{},"recent_messages":[]}`},
		&fakeTimezoneStore{timezone: "Europe/Moscow"}, tc.creator, nil, server.URL).
		WithMeetingConflicts(tc.finder, tc.pending)
	return tc
}

func (tc *conflictTest) send(t *testing.T, text string) model.TaskResult {
	t.Helper()
	result, err := tc.process.Process(t.Context(), &api.TaskTextRequest{UserId: tc.userID, TaskId: &tc.taskID, Text: text})
	if err != nil {
		t.Fatalf("Process(%q) error: %v", text, err)
	}
	return result
}

func TestTaskProcessCreatesMeetingWithoutConflict(t *testing.T) {
	tc := newConflictTest(t, nil, "meeting")

	result := tc.send(t, "встреча с командой 21 октября в 15:00")

	if _, ok := result.(*api.TaskCreatedResponse); !ok || !tc.creator.called {
		t.Fatalf("Process() = %T, created=%v; want created meeting", result, tc.creator.called)
	}
	if want := time.Date(2026, 10, 21, 15, 0, 0, 0, tc.location); !tc.finder.start.Equal(want) || tc.finder.start.Location().String() != "Europe/Moscow" {
		t.Fatalf("conflict check start = %s, want %s", tc.finder.start, want)
	}
}

func TestTaskProcessSkipsConflictCheckForOtherTypes(t *testing.T) {
	tc := newConflictTest(t, &MeetingConflict{Title: "Встреча с Иваном"}, "reminder")

	result := tc.send(t, "напомни 21 октября в 15:00")

	if _, ok := result.(*api.TaskCreatedResponse); !ok || tc.finder.called {
		t.Fatalf("Process() = %T, conflict checked=%v; reminders must not be checked", result, tc.finder.called)
	}
}

func TestTaskProcessAsksAboutConflict(t *testing.T) {
	tc := newConflictTest(t, &MeetingConflict{Title: "Встреча с Иваном"}, "meeting")

	result := tc.send(t, "встреча с командой 21 октября в 15:00")

	question, ok := result.(*api.TaskClarificationResponse)
	if !ok {
		t.Fatalf("Process() = %T, want *api.TaskClarificationResponse", result)
	}
	if question.Message != "В это время уже есть встреча «Встреча с Иваном», всё равно поставить?" ||
		question.Status != api.NeedsClarification || question.TaskId != tc.taskID {
		t.Fatalf("question = %+v", question)
	}
	if tc.creator.called {
		t.Fatal("conflicting meeting must not be created before confirmation")
	}
	if len(tc.pending.values) != 1 || tc.pending.ttl != 10*time.Minute {
		t.Fatalf("pending = %v ttl %s, want one meeting for 10m", tc.pending.values, tc.pending.ttl)
	}
}

func TestTaskProcessConfirmsConflict(t *testing.T) {
	for _, answer := range []string{"да", "Да, ставь"} {
		t.Run(answer, func(t *testing.T) {
			tc := newConflictTest(t, &MeetingConflict{Title: "Встреча с Иваном"}, "meeting")
			tc.send(t, "встреча с командой 21 октября в 15:00")

			result := tc.send(t, answer)

			created, ok := result.(*api.TaskCreatedResponse)
			if !ok {
				t.Fatalf("Process() = %T, want *api.TaskCreatedResponse", result)
			}
			if tc.ai.calls != 1 {
				t.Fatalf("AI calls = %d, want 1: the answer is handled by the backend", tc.ai.calls)
			}
			if created.Message != "Встреча с командой назначена на завтра в 15:00." || created.TaskId != tc.taskID {
				t.Fatalf("created = %+v", created)
			}
			cmd := tc.creator.cmd
			if cmd.RecordType != "meeting" || cmd.Title != "Встреча с командой" || cmd.UserID != tc.userID ||
				cmd.Date.Format(time.DateOnly) != "2026-10-21" || cmd.Time.Format("15:04") != "15:00" ||
				cmd.Date.Location().String() != "Europe/Moscow" {
				t.Fatalf("Create() command = %+v", cmd)
			}
			if len(tc.pending.values) != 0 {
				t.Fatal("confirmed meeting must leave the pending store")
			}
		})
	}
}

func TestTaskProcessCancelsConflict(t *testing.T) {
	tc := newConflictTest(t, &MeetingConflict{Title: "Встреча с Иваном"}, "meeting")
	tc.send(t, "встреча с командой 21 октября в 15:00")

	result := tc.send(t, "нет, не надо")

	cancelled, ok := result.(*api.TaskCancelledResponse)
	if !ok {
		t.Fatalf("Process() = %T, want *api.TaskCancelledResponse", result)
	}
	if cancelled.Status != api.Cancelled || cancelled.TaskId != tc.taskID || cancelled.UserId != tc.userID || cancelled.Message == "" {
		t.Fatalf("cancelled = %+v", cancelled)
	}
	if tc.creator.called || tc.ai.calls != 1 || len(tc.pending.values) != 0 {
		t.Fatalf("created=%v ai calls=%d pending=%v; want nothing created", tc.creator.called, tc.ai.calls, tc.pending.values)
	}
}

func TestTaskProcessRepeatsConflictQuestion(t *testing.T) {
	tc := newConflictTest(t, &MeetingConflict{Title: "Встреча с Иваном"}, "meeting")
	tc.send(t, "встреча с командой 21 октября в 15:00")

	result := tc.send(t, "а во сколько у Ивана встреча?")

	question, ok := result.(*api.TaskClarificationResponse)
	if !ok {
		t.Fatalf("Process() = %T, want *api.TaskClarificationResponse", result)
	}
	if question.Message != "Ответьте «да» или «нет». В это время уже есть встреча «Встреча с Иваном», всё равно поставить?" {
		t.Fatalf("question = %q", question.Message)
	}
	if tc.creator.called || len(tc.pending.values) != 1 {
		t.Fatal("unclear answer must keep the meeting pending")
	}

	if _, ok := tc.send(t, "да").(*api.TaskCreatedResponse); !ok || !tc.creator.called {
		t.Fatal("meeting must be created after a later «да»")
	}
}

func TestTaskProcessConflictCheckFailure(t *testing.T) {
	tc := newConflictTest(t, nil, "meeting")
	tc.finder.err = errors.New("database unavailable")

	_, err := tc.process.Process(t.Context(), &api.TaskTextRequest{UserId: tc.userID, TaskId: &tc.taskID, Text: "встреча"})

	if err == nil || tc.creator.called {
		t.Fatalf("Process() error = %v, created=%v; want error and nothing created", err, tc.creator.called)
	}
}
