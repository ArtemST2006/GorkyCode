package service

import (
	"context"
	"errors"
	"gorkycode/backend/internal/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	api "gorkycode/backend/internal/generated"
)

type fakeConflictFinder struct {
	conflict *MeetingConflict
	err      error
	called   bool
	start    time.Time
}

func (f *fakeConflictFinder) Find(_ context.Context, _ uuid.UUID, start time.Time) (*MeetingConflict, error) {
	f.called = true
	f.start = start
	return f.conflict, f.err
}
func createMeetingAIResponse(userID, taskID uuid.UUID, recordType string) string {
	return `{"user_id":"` + userID.String() + `","task_id":"` + taskID.String() + `",` +
		`"status":"ready","intent":"create_event","message":"Встреча назначена.",` +
		`"action":{"record_type":"` + recordType + `","title":"Встреча с командой","date":"2026-10-21","time":"15:00"},` +
		`"context":{"recent_messages":[{"role":"user","text":"встреча с командой"}],"draft":null}}`
}

type conflictTest struct {
	process  *TaskProcess
	creator  *fakeTaskCreator
	finder   *fakeConflictFinder
	userID   uuid.UUID
	taskID   uuid.UUID
	location *time.Location
}

func newConflictTest(t *testing.T, conflict *MeetingConflict, recordType string) *conflictTest {
	t.Helper()
	tc := &conflictTest{creator: &fakeTaskCreator{}, finder: &fakeConflictFinder{conflict: conflict}, userID: uuid.New(), taskID: uuid.New()}
	var err error
	tc.location, err = time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(&aiStub{t: t, status: http.StatusOK, body: createMeetingAIResponse(tc.userID, tc.taskID, recordType)})
	t.Cleanup(server.Close)
	tc.process = NewTaskProcess(&fakeSTT{}, &fakeDraftStore{value: `{"draft":{},"recent_messages":[{"role":"user","text":"Предыдущий запрос"}]}`},
		&fakeTimezoneStore{timezone: "Europe/Moscow"}, tc.creator, nil, nil, server.URL).WithMeetingConflicts(tc.finder)
	return tc
}
func (tc *conflictTest) send(t *testing.T, text string) (any, error) {
	t.Helper()
	result, err := tc.process.Process(t.Context(), &api.TaskTextRequest{UserId: tc.userID, TaskId: &tc.taskID, Text: text})
	if err != nil {
		return nil, err
	}
	return result.Body, nil
}
func TestTaskProcessCreatesMeetingWithoutConflict(t *testing.T) {
	tc := newConflictTest(t, nil, "meeting")
	result, err := tc.send(t, "встреча с командой 21 октября в 15:00")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.(*api.TaskCreatedResponse); !ok || !tc.creator.called {
		t.Fatalf("result=%T created=%v", result, tc.creator.called)
	}
	want := time.Date(2026, 10, 21, 15, 0, 0, 0, tc.location)
	if !tc.finder.start.Equal(want) {
		t.Fatalf("start=%s want=%s", tc.finder.start, want)
	}
}
func TestTaskProcessSkipsConflictCheckForOtherTypes(t *testing.T) {
	tc := newConflictTest(t, &MeetingConflict{Title: "Встреча с Иваном"}, "reminder")
	result, err := tc.send(t, "напомни 21 октября в 15:00")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.(*api.TaskCreatedResponse); !ok || tc.finder.called {
		t.Fatalf("result=%T checked=%v", result, tc.finder.called)
	}
}
func TestTaskProcessRejectsOccupiedTime(t *testing.T) {
	for _, conflict := range []MeetingConflict{{Title: "Встреча с Иваном"}, {}} {
		tc := newConflictTest(t, &conflict, "meeting")
		_, err := tc.send(t, "встреча с командой 21 октября в 15:00")
		var occupied *model.MeetingConflictError
		if !errors.As(err, &occupied) || !strings.Contains(occupied.Detail, "занято") || tc.creator.called ||
			occupied.UserID != tc.userID || occupied.TaskID != tc.taskID {
			t.Fatalf("error=%v created=%v", err, tc.creator.called)
		}
		draft := tc.process.draft.(*fakeDraftStore)
		key := "task_draft:" + tc.userID.String() + ":" + tc.taskID.String()
		saved := draft.saved[key]
		if !strings.Contains(saved, "Встреча с командой") || !strings.Contains(saved, "Выберите другое время") || strings.Contains(saved, `"time":"15:00"`) {
			t.Fatalf("saved conflict context=%s", saved)
		}
		// A follow-up with the same dialogue cannot bypass the conflict check.
		_, err = tc.send(t, "да, всё равно поставь")
		if !errors.As(err, &occupied) || tc.creator.called {
			t.Fatalf("confirmation bypassed conflict: error=%v created=%v", err, tc.creator.called)
		}
	}
}
func TestTaskProcessConflictCheckFailure(t *testing.T) {
	tc := newConflictTest(t, nil, "meeting")
	tc.finder.err = errors.New("database unavailable")
	_, err := tc.send(t, "встреча")
	if err == nil || tc.creator.called {
		t.Fatalf("error=%v created=%v", err, tc.creator.called)
	}
}
