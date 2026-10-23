package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"
	services "gorkycode/backend/internal/service/subservice"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapitypes "github.com/oapi-codegen/runtime/types"

	api "gorkycode/backend/internal/generated"
)

type fakeSTT struct {
	text string
	err  error

	called bool
	got    string
}

func (f *fakeSTT) Transcribe(_ context.Context, file io.Reader) (string, error) {
	f.called = true
	content, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	f.got = string(content)
	return f.text, f.err
}

type fakeDraftStore struct {
	value string
	err   error

	called bool
	key    string
}

type fakeTimezoneStore struct {
	timezone string
	err      error
}

func (f *fakeTimezoneStore) GetUserTimezone(_ context.Context, _ uuid.UUID) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.timezone, nil
}

func (f *fakeDraftStore) GetDraft(_ context.Context, key string) (string, error) {
	f.called = true
	f.key = key
	return f.value, f.err
}

func (f *fakeDraftStore) PutDraft(_ context.Context, key, value string) error {
	f.called = true
	f.key = key
	f.value = value
	return f.err
}

type fakeTaskCreator struct {
	err error

	called bool
	cmd    services.CreateTaskCommand
}

func (f *fakeTaskCreator) Create(_ context.Context, cmd services.CreateTaskCommand) (*model.Task, error) {
	f.called = true
	f.cmd = cmd
	if f.err != nil {
		return nil, f.err
	}
	clock := cmd.Time.Format("15:04")
	return &model.Task{
		ID:        uuid.New(),
		UserID:    cmd.UserID,
		Type:      cmd.RecordType,
		Title:     cmd.Title,
		Date:      &cmd.Date,
		Time:      &clock,
		CreatedAt: time.Now(),
		Status:    "active",
	}, nil
}

// aiStub records the request body and replies with the configured response.
type aiStub struct {
	t      *testing.T
	status int
	body   string
	got    *ai.JobRequest
	calls  int
}

func (s *aiStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.calls++
	if r.Method != http.MethodPost {
		s.t.Errorf("AI method = %s, want %s", r.Method, http.MethodPost)
	}
	if r.URL.Path != "/api/job" {
		s.t.Errorf("AI path = %s, want /api/job", r.URL.Path)
	}
	if s.got != nil {
		if err := json.NewDecoder(r.Body).Decode(s.got); err != nil {
			s.t.Errorf("decode AI request: %v", err)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s.status)
	if _, err := io.WriteString(w, s.body); err != nil {
		s.t.Errorf("write AI response: %v", err)
	}
}

func newTestTaskProcess(t *testing.T, stt STT, draft DraftStore, aiHandler http.Handler) *TaskProcess {
	t.Helper()
	server := httptest.NewServer(aiHandler)
	t.Cleanup(server.Close)
	return NewTaskProcess(stt, draft, &fakeTimezoneStore{timezone: "Europe/Moscow"}, &fakeTaskCreator{}, nil, server.URL)
}

// newAudioRequest builds a TaskAudioRequest whose file is backed by a multipart header.
func newAudioRequest(t *testing.T, userID uuid.UUID, content string) *api.TaskAudioRequest {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "voice.wav")
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, writeErr := part.Write([]byte(content)); writeErr != nil {
		t.Fatalf("write file part: %v", writeErr)
	}
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatalf("close multipart writer: %v", closeErr)
	}

	reader := multipart.NewReader(&body, writer.Boundary())
	form, err := reader.ReadForm(1 << 20)
	if err != nil {
		t.Fatalf("read multipart form: %v", err)
	}
	t.Cleanup(func() {
		_ = form.RemoveAll()
	})

	var file openapitypes.File
	file.InitFromMultipart(form.File["file"][0])
	return &api.TaskAudioRequest{UserId: userID, File: file}
}

func TestTaskProcessBuildsAIRequest(t *testing.T) {
	userID := uuid.New()
	taskID := uuid.New()
	got := &ai.JobRequest{}
	process := newTestTaskProcess(t, &fakeSTT{}, &fakeDraftStore{value: `{"draft":{},"recent_messages":[]}`}, &aiStub{
		t:      t,
		status: http.StatusOK,
		body:   `{"status":"invalid"}`,
		got:    got,
	})

	_, err := process.Process(t.Context(), &api.TaskTextRequest{
		UserId: userID,
		TaskId: &taskID,
		Text:   "назначь встречу",
	})

	if err == nil || !strings.Contains(err.Error(), "invalid AI status") {
		t.Fatalf("Process() error = %v, want invalid AI status", err)
	}
	if got.UserId != userID {
		t.Fatalf("AI user_id = %s, want %s", got.UserId, userID)
	}
	if got.TaskId != taskID {
		t.Fatalf("AI task_id = %s, want %s", got.TaskId, taskID)
	}
	if got.Text != "назначь встречу" {
		t.Fatalf("AI text = %q", got.Text)
	}
	if got.Timezone != "Europe/Moscow" {
		t.Fatalf("AI timezone = %q", got.Timezone)
	}
	if got.RequestTime.IsZero() {
		t.Fatal("AI request_time is zero")
	}
}

func TestTaskProcessGeneratesTaskIDForFirstRequest(t *testing.T) {
	got := &ai.JobRequest{}
	process := newTestTaskProcess(t, &fakeSTT{}, &fakeDraftStore{}, &aiStub{
		t:      t,
		status: http.StatusOK,
		body:   `{"status":"invalid"}`,
		got:    got,
	})

	_, _ = process.Process(t.Context(), &api.TaskTextRequest{UserId: uuid.New(), Text: "привет"})

	if got.TaskId == uuid.Nil {
		t.Fatal("AI task_id must be generated for the first request")
	}
	contextJSON, err := json.Marshal(got.Context)
	if err != nil || string(contextJSON) != "{}" {
		t.Fatalf("AI context = %s, %v; want {}", contextJSON, err)
	}
}

func TestTaskProcessUsesSavedTimezone(t *testing.T) {
	got := &ai.JobRequest{}
	stub := &aiStub{t: t, status: http.StatusOK, body: `{"status":"invalid"}`, got: got}
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	process := NewTaskProcess(&fakeSTT{}, &fakeDraftStore{}, &fakeTimezoneStore{timezone: "Asia/Tokyo"}, &fakeTaskCreator{}, nil, server.URL)

	_, _ = process.Process(t.Context(), &api.TaskTextRequest{UserId: uuid.New(), Text: "привет"})
	if got.Timezone != "Asia/Tokyo" {
		t.Fatalf("AI timezone = %q, want Asia/Tokyo", got.Timezone)
	}
	if _, offset := got.RequestTime.Zone(); offset != 9*60*60 {
		t.Fatalf("AI request_time offset = %d, want +09:00", offset)
	}
}

func TestTaskProcessCreatesEvent(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	creator := &fakeTaskCreator{}
	server := httptest.NewServer(&aiStub{
		t:      t,
		status: http.StatusOK,
		body: `{"user_id":"` + userID.String() + `","task_id":"` + taskID.String() + `",` +
			`"status":"ready","intent":"create_event","message":"Встреча с Иваном назначена на завтра в 15:00.",` +
			`"action":{"record_type":"meeting","title":"Встреча с Иваном","date":"2026-10-21","time":"15:00"},` +
			`"context":{"recent_messages":[{"role":"user","text":"встреча с Иваном"}],"draft":null}}`,
	})
	t.Cleanup(server.Close)

	process := NewTaskProcess(
		&fakeSTT{},
		&fakeDraftStore{value: `{"draft":{},"recent_messages":[]}`},
		&fakeTimezoneStore{timezone: "Asia/Tokyo"},
		creator,
		nil,
		server.URL,
	)

	result, err := process.Process(t.Context(), &api.TaskTextRequest{
		UserId: userID,
		TaskId: &taskID,
		Text:   "встреча с Иваном 21 октября в 15:00",
	})
	if err != nil {
		t.Fatalf("Process() error: %v", err)
	}
	if !creator.called {
		t.Fatal("Create() was not called")
	}
	if creator.cmd.UserID != userID || creator.cmd.RecordType != "meeting" || creator.cmd.Title != "Встреча с Иваном" {
		t.Fatalf("Create() command = %+v", creator.cmd)
	}
	if got := creator.cmd.Date.Format("2006-01-02"); got != "2026-10-21" {
		t.Fatalf("Create() date = %s, want 2026-10-21", got)
	}
	if _, offset := creator.cmd.Date.Zone(); offset != 9*60*60 {
		t.Fatalf("Create() date offset = %d, want +09:00", offset)
	}
	if got := creator.cmd.Time.Format("15:04"); got != "15:00" {
		t.Fatalf("Create() time = %s, want 15:00", got)
	}

	if result.Intent != "create_event" {
		t.Fatalf("intent = %q, want create_event", result.Intent)
	}
	response, ok := result.Body.(*api.TaskCreatedResponse)
	if !ok {
		t.Fatalf("Process() body = %T, want *api.TaskCreatedResponse", result.Body)
	}
	if response.UserId != userID || response.TaskId != taskID {
		t.Fatalf("response ids = %s/%s, want %s/%s", response.UserId, response.TaskId, userID, taskID)
	}
	if response.Data.Event.RecordType != api.TaskCreatedEventRecordTypeMeeting {
		t.Fatalf("response record_type = %s, want meeting", response.Data.Event.RecordType)
	}
	if response.Data.Event.Title != "Встреча с Иваном" {
		t.Fatalf("response title = %s", response.Data.Event.Title)
	}
	if response.Message != "Встреча с Иваном назначена на завтра в 15:00." {
		t.Fatalf("response message = %q", response.Message)
	}
}

func TestTaskProcessReportsCreateErrors(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	server := httptest.NewServer(&aiStub{
		t:      t,
		status: http.StatusOK,
		body: `{"user_id":"` + userID.String() + `","task_id":"` + taskID.String() + `",` +
			`"status":"ready","intent":"create_event","message":"Встреча назначена.",` +
			`"action":{"record_type":"meeting","title":"Встреча","date":"2026-10-21","time":"15:00"},` +
			`"context":{"recent_messages":[{"role":"user","text":"встреча"}],"draft":null}}`,
	})
	t.Cleanup(server.Close)

	process := NewTaskProcess(
		&fakeSTT{},
		&fakeDraftStore{value: `{"draft":{},"recent_messages":[]}`},
		&fakeTimezoneStore{timezone: "Europe/Moscow"},
		&fakeTaskCreator{err: errors.New("database unavailable")},
		nil,
		server.URL,
	)

	_, err := process.Process(t.Context(), &api.TaskTextRequest{
		UserId: userID,
		TaskId: &taskID,
		Text:   "встреча 21 октября в 15:00",
	})

	if err == nil || !strings.Contains(err.Error(), "create task") {
		t.Fatalf("Process() error = %v, want create task", err)
	}
}

func TestTaskProcessTranscribesAudio(t *testing.T) {
	stt := &fakeSTT{text: "позвони маме"}
	got := &ai.JobRequest{}
	process := newTestTaskProcess(t, stt, &fakeDraftStore{}, &aiStub{
		t:      t,
		status: http.StatusOK,
		body:   `{"status":"invalid"}`,
		got:    got,
	})

	_, err := process.Process(t.Context(), newAudioRequest(t, uuid.New(), "RIFF-audio"))

	if err == nil || !strings.Contains(err.Error(), "invalid AI status") {
		t.Fatalf("Process() error = %v, want invalid AI status", err)
	}
	if !stt.called || stt.got != "RIFF-audio" {
		t.Fatalf("STT got %q, called = %v", stt.got, stt.called)
	}
	if got.Text != "позвони маме" {
		t.Fatalf("AI text = %q, want transcript", got.Text)
	}
}

func TestTaskProcessReportsSTTError(t *testing.T) {
	stub := &aiStub{t: t, status: http.StatusOK, body: `{}`}
	process := newTestTaskProcess(t, &fakeSTT{err: errors.New("model failed")}, &fakeDraftStore{}, stub)

	_, err := process.Process(t.Context(), newAudioRequest(t, uuid.New(), "RIFF-audio"))

	if err == nil || !strings.Contains(err.Error(), "transcribe audio") {
		t.Fatalf("Process() error = %v, want transcribe audio", err)
	}
	if stub.calls != 0 {
		t.Fatalf("AI service calls = %d, want 0", stub.calls)
	}
}

func TestTaskProcessLoadsDraftContext(t *testing.T) {
	userID := uuid.New()
	taskID := uuid.New()
	draft := &fakeDraftStore{value: `{"draft":{},"recent_messages":[]}`}
	process := newTestTaskProcess(t, &fakeSTT{}, draft, &aiStub{
		t:      t,
		status: http.StatusOK,
		body:   `{"status":"invalid"}`,
	})

	_, err := process.Process(t.Context(), &api.TaskTextRequest{
		UserId: userID,
		TaskId: &taskID,
		Text:   "в 15:00",
	})

	if err == nil || !strings.Contains(err.Error(), "invalid AI status") {
		t.Fatalf("Process() error = %v, want invalid AI status", err)
	}
	wantKey := fmt.Sprintf("task_draft:%s:%s", userID, taskID)
	if !draft.called || draft.key != wantKey {
		t.Fatalf("draft key = %q, called = %v; want %q", draft.key, draft.called, wantKey)
	}
}

func TestTaskProcessReportsDraftErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		draft *fakeDraftStore
		want  string
	}{
		{name: "store failure", draft: &fakeDraftStore{err: errors.New("redis unavailable")}, want: "get draft"},
		{name: "invalid JSON", draft: &fakeDraftStore{value: `{"draft":`}, want: "decode draft context"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &aiStub{t: t, status: http.StatusOK, body: `{}`}
			process := newTestTaskProcess(t, &fakeSTT{}, tc.draft, stub)
			taskID := uuid.New()

			_, err := process.Process(t.Context(), &api.TaskTextRequest{
				UserId: uuid.New(),
				TaskId: &taskID,
				Text:   "в 15:00",
			})

			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Process() error = %v, want %q", err, tc.want)
			}
			if stub.calls != 0 {
				t.Fatalf("AI service calls = %d, want 0", stub.calls)
			}
		})
	}
}

func TestTaskProcessReportsAIErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "non-200", status: http.StatusServiceUnavailable, body: `{}`, want: "AI service returned status 503"},
		{name: "invalid JSON", status: http.StatusOK, body: `not json`, want: "decode AI response"},
		{name: "unknown status", status: http.StatusOK, body: `{"status":"mystery"}`, want: "invalid AI status mystery"},
		{name: "unknown intent", status: http.StatusOK, body: `{"status":"ready","intent":"dance"}`, want: "invalid intent dance"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			process := newTestTaskProcess(t, &fakeSTT{}, &fakeDraftStore{}, &aiStub{
				t:      t,
				status: tc.status,
				body:   tc.body,
			})

			_, err := process.Process(t.Context(), &api.TaskTextRequest{UserId: uuid.New(), Text: "привет"})

			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Process() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestTaskProcessRejectsUnsupportedRequest(t *testing.T) {
	process := NewTaskProcess(&fakeSTT{}, &fakeDraftStore{}, &fakeTimezoneStore{timezone: "Europe/Moscow"}, &fakeTaskCreator{}, nil, "http://127.0.0.1:1")

	_, err := process.Process(t.Context(), "plain text")

	if err == nil || !strings.Contains(err.Error(), "unsupported request type") {
		t.Fatalf("Process() error = %v, want unsupported request type", err)
	}
}

// fakeEventLister returns what the test configured and records its arguments.
type fakeEventLister struct {
	collection api.TaskCollection
	err        error

	userID uuid.UUID
	action json.RawMessage
}

func (f *fakeEventLister) SearchEvents(_ context.Context, userID uuid.UUID, action json.RawMessage) (api.TaskCollection, error) {
	f.userID, f.action = userID, action
	return f.collection, f.err
}

func (f *fakeEventLister) ListEvents(_ context.Context, userID uuid.UUID, action json.RawMessage) (api.TaskCollection, error) {
	f.userID, f.action = userID, action
	return f.collection, f.err
}

func TestProcessAIResultListEvents(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	action := json.RawMessage(`{"date_from":"2026-10-20","date_to_exclusive":"2026-10-21"}`)
	aiResult := model.AiRes{Status: "ready", Intent: "list_events", Action: action, Message: json.RawMessage(`"Проверил события на завтра."`), UserID: userID, TaskID: taskID}
	item := api.TaskItem{Id: uuid.New(), UserId: userID, RecordType: api.TaskItemRecordTypeMeeting, Title: "Встреча", Status: api.TaskItemStatusActive}

	for _, tc := range []struct {
		name        string
		items       []api.TaskItem
		wantMessage string
	}{
		{name: "found", items: []api.TaskItem{item}, wantMessage: "Проверил события на завтра."},
		{name: "empty", items: []api.TaskItem{}, wantMessage: "Проверил события на завтра."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lister := &fakeEventLister{collection: api.TaskCollection{Items: tc.items}}
			process := NewTaskProcess(&fakeSTT{}, &fakeDraftStore{}, &fakeTimezoneStore{}, nil, lister, "")

			result, err := process.processAIResult(t.Context(), aiResult, time.UTC)
			if err != nil {
				t.Fatalf("processAIResult() error = %v", err)
			}

			if lister.userID != userID || string(lister.action) != string(action) {
				t.Fatalf("lister got user %s, action %s", lister.userID, lister.action)
			}
			if result.Intent != "list_events" {
				t.Fatalf("intent = %q, want list_events", result.Intent)
			}
			response, ok := result.Body.(*api.TaskCollectionResponse)
			if !ok {
				t.Fatalf("result body type = %T, want *api.TaskCollectionResponse", result.Body)
			}
			if response.UserId != userID || response.TaskId != taskID || response.Status != api.TaskCollectionResponseStatusOk {
				t.Fatalf("response = %+v", response)
			}
			if response.Message != tc.wantMessage {
				t.Fatalf("message = %q, want %q", response.Message, tc.wantMessage)
			}
			if len(response.Data.Items) != len(tc.items) {
				t.Fatalf("items = %v, want %v", response.Data.Items, tc.items)
			}
		})
	}
}

func TestProcessAIResultListEventsErrors(t *testing.T) {
	aiResult := model.AiRes{Status: "ready", Intent: "list_events", Action: json.RawMessage(`{}`), Message: json.RawMessage(`"Проверил события."`), UserID: uuid.New(), TaskID: uuid.New()}

	t.Run("lister error", func(t *testing.T) {
		process := NewTaskProcess(&fakeSTT{}, &fakeDraftStore{}, &fakeTimezoneStore{}, nil, &fakeEventLister{err: model.ErrInvalidAction}, "")

		_, err := process.processAIResult(t.Context(), aiResult, time.UTC)

		if !errors.Is(err, model.ErrInvalidAction) {
			t.Fatalf("processAIResult() error = %v, want ErrInvalidAction", err)
		}
	})

	t.Run("lister not configured", func(t *testing.T) {
		process := NewTaskProcess(&fakeSTT{}, &fakeDraftStore{}, &fakeTimezoneStore{}, nil, nil, "")

		_, err := process.processAIResult(t.Context(), aiResult, time.UTC)

		if err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Fatalf("processAIResult() error = %v, want not configured", err)
		}
	})
}

func TestTaskProcessClarificationRoundTrip(t *testing.T) {
	userID := uuid.New()
	draft := &fakeDraftStore{}
	reader := &fakeEventLister{collection: api.TaskCollection{Items: []api.TaskItem{}}}
	var taskID uuid.UUID
	var secondContext ai.JobRequestContext
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request ai.JobRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			taskID = request.TaskId
			_, _ = fmt.Fprintf(w, `{"user_id":%q,"task_id":%q,"status":"needs_clarification","intent":"create_event","message":"Во сколько?","action":null,"context":{"recent_messages":[{"role":"user","text":"Встреча с Иваном завтра"},{"role":"assistant","text":"Во сколько?"}],"draft":{"record_type":"meeting","title":"Встреча с Иваном"}}}`, userID, taskID)
			return
		}
		secondContext = request.Context
		_, _ = fmt.Fprintf(w, `{"user_id":%q,"task_id":%q,"status":"ready","intent":"list_events","message":"Проверил события на завтра.","action":{"date_from":"2026-10-20","date_to_exclusive":"2026-10-21"},"context":{"recent_messages":[{"role":"user","text":"Что у меня завтра?"}],"draft":null}}`, userID, taskID)
	}))
	t.Cleanup(server.Close)
	process := NewTaskProcess(&fakeSTT{}, draft, &fakeTimezoneStore{timezone: "Europe/Moscow"}, &fakeTaskCreator{}, reader, server.URL)

	first, err := process.Process(t.Context(), &api.TaskTextRequest{UserId: userID, Text: "Встреча с Иваном завтра"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Intent != "create_event" {
		t.Fatalf("clarification intent = %q, want create_event", first.Intent)
	}
	clarification, ok := first.Body.(*api.TaskClarificationResponse)
	if !ok || clarification.TaskId == uuid.Nil || clarification.Message != "Во сколько?" {
		t.Fatalf("clarification = %#v", first)
	}
	if draft.value == "" || draft.key != fmt.Sprintf("task_draft:%s:%s", userID, taskID) {
		t.Fatalf("saved draft = %q at %q", draft.value, draft.key)
	}
	second, err := process.Process(t.Context(), &api.TaskTextRequest{UserId: userID, TaskId: &taskID, Text: "Что у меня завтра?"})
	if err != nil {
		t.Fatal(err)
	}
	if response, ok := second.Body.(*api.TaskCollectionResponse); !ok || second.Intent != "list_events" || calls != 2 || response.Message != "Проверил события на завтра." {
		t.Fatalf("second result = %+v, AI calls = %d", second, calls)
	}
	contextJSON, err := json.Marshal(secondContext)
	if err != nil || !strings.Contains(string(contextJSON), `"title":"Встреча с Иваном"`) {
		t.Fatalf("AI continuation context = %s, %v", contextJSON, err)
	}
}

func TestProcessAIResultSearchEvents(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	action := json.RawMessage(`{"query":"Иван"}`)
	reader := &fakeEventLister{collection: api.TaskCollection{Items: []api.TaskItem{{Title: "Встреча с Иваном"}}}}
	process := NewTaskProcess(&fakeSTT{}, &fakeDraftStore{}, &fakeTimezoneStore{}, nil, reader, "")
	result, err := process.processAIResult(t.Context(), model.AiRes{Status: "ready", Intent: "search_events", Action: action, Message: json.RawMessage(`"Поиск встреч с Иваном завершён."`), UserID: userID, TaskID: taskID}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	response, ok := result.Body.(*api.TaskCollectionResponse)
	if !ok || response.Message != "Поиск встреч с Иваном завершён." || reader.userID != userID || string(reader.action) != string(action) {
		t.Fatalf("search response = %#v, reader = %+v", result, reader)
	}
	if result.Intent != "search_events" {
		t.Fatalf("intent = %q, want search_events", result.Intent)
	}
}
