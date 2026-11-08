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

func TestChatResultSavesHistoryWithoutTaskAction(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	drafts := &fakeDraftStore{}
	process := &TaskProcess{draft: drafts}
	result, err := process.processAIResult(t.Context(), model.AiRes{
		UserID: userID, TaskID: taskID, Status: "ready", Intent: "chat",
		Message: json.RawMessage(`"Привет!"`), Action: json.RawMessage(`null`),
		Context: json.RawMessage(`{"recent_messages":[{"role":"user","text":"Привет"},{"role":"assistant","text":"Привет!"}],"draft":null}`),
	}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	chat, ok := result.Body.(*api.TaskChatResponse)
	if !ok || chat.Message != "Привет!" || chat.TaskId != taskID {
		t.Fatalf("chat result=%#v", result.Body)
	}
	if !strings.Contains(drafts.saved["task_draft:"+userID.String()+":"+taskID.String()], `"draft":{}`) {
		t.Fatalf("chat history not saved: %v", drafts.saved)
	}
}

func TestReadyHistoryCanContinueWithSameTaskID(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	store := &fakeDraftStore{}
	processor := &TaskProcess{draft: store}
	result := model.AiRes{
		UserID: userID, TaskID: taskID, Status: "ready", Intent: "list_events",
		Context: json.RawMessage(`{"recent_messages":[{"role":"user","text":"Покажи мои задачи"}],"draft":null}`),
	}
	if err := processor.saveReadyHistory(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	stored, err := processor.getTaskContext(t.Context(), userID, &taskID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`Покажи мои задачи`)) || !bytes.Contains(raw, []byte(`"draft":{}`)) {
		t.Fatalf("lost history for follow-up: %s", raw)
	}
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
	value  string
	err    error
	getErr error

	called bool
	key    string
	saved  map[string]string
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
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.value, f.err
}

func (f *fakeDraftStore) PutDraft(_ context.Context, key, value string) error {
	f.called = true
	f.key = key
	f.value = value
	if f.saved == nil {
		f.saved = map[string]string{}
	}
	f.saved[key] = value
	return f.err
}

func (f *fakeDraftStore) DeleteDraft(_ context.Context, key string) error {
	delete(f.saved, key)
	return f.err
}

func (f *fakeDraftStore) LockDialogue(context.Context, string, time.Duration) (func(), error) {
	return func() {}, nil
}

func (f *fakeDraftStore) PutDrafts(ctx context.Context, values map[string]string) error {
	for key, value := range values {
		if err := f.PutDraft(ctx, key, value); err != nil {
			return err
		}
	}
	return nil
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
	var clock *string
	if cmd.Time != nil {
		value := cmd.Time.Format("15:04")
		clock = &value
	}
	return &model.Task{
		ID:        uuid.New(),
		UserID:    cmd.UserID,
		Type:      cmd.RecordType,
		Title:     cmd.Title,
		Date:      &cmd.Date,
		Time:      clock,
		CreatedAt: time.Now(),
		Status:    "active",
	}, nil
}

// aiStub records the request body and replies with the configured response.
type aiStub struct {
	t      *testing.T
	status int
	body   string
	header string // X-Task-Intent of the reply, e.g. on_delete
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
	if s.header != "" {
		w.Header().Set("X-Task-Intent", s.header)
	}
	w.WriteHeader(s.status)
	if _, err := io.WriteString(w, s.body); err != nil {
		s.t.Errorf("write AI response: %v", err)
	}
}

func newTestTaskProcess(t *testing.T, stt STT, draft DraftStore, aiHandler http.Handler) *TaskProcess {
	t.Helper()
	server := httptest.NewServer(aiHandler)
	t.Cleanup(server.Close)
	return NewTaskProcess(stt, draft, &fakeTimezoneStore{timezone: "Europe/Moscow"}, &fakeTaskCreator{}, nil, nil, server.URL)
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
	process := newTestTaskProcess(t, &fakeSTT{}, &fakeDraftStore{value: `{"draft":{},"recent_messages":[{"role":"user","text":"Предыдущий запрос"}]}`}, &aiStub{
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
	process := NewTaskProcess(&fakeSTT{}, &fakeDraftStore{}, &fakeTimezoneStore{timezone: "Asia/Tokyo"}, &fakeTaskCreator{}, nil, nil, server.URL)

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
		&fakeDraftStore{value: `{"draft":{},"recent_messages":[{"role":"user","text":"Предыдущий запрос"}]}`},
		&fakeTimezoneStore{timezone: "Asia/Tokyo"},
		creator,
		nil,
		nil,
		server.URL,
	).WithMeetingConflicts(&fakeConflictFinder{})

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
	if creator.cmd.Time == nil || creator.cmd.Time.Format("15:04") != "15:00" {
		t.Fatalf("Create() time = %v, want 15:00", creator.cmd.Time)
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

func TestTaskProcessCreatesDateOnlyTask(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	creator := &fakeTaskCreator{}
	server := httptest.NewServer(&aiStub{
		t: t, status: http.StatusOK,
		body: `{"user_id":"` + userID.String() + `","task_id":"` + taskID.String() + `",` +
			`"status":"ready","intent":"create_event","message":"Задача купить билеты создана.",` +
			`"action":{"record_type":"task","title":"Купить билеты","date":"2026-11-01"},` +
			`"context":{"recent_messages":[{"role":"user","text":"Купить билеты до конца недели"}],"draft":null}}`,
	})
	t.Cleanup(server.Close)
	process := NewTaskProcess(
		&fakeSTT{}, &fakeDraftStore{value: `{"draft":{},"recent_messages":[{"role":"user","text":"Предыдущий запрос"}]}`},
		&fakeTimezoneStore{timezone: "Europe/Moscow"}, creator, nil, nil, server.URL,
	)
	result, err := process.Process(t.Context(), &api.TaskTextRequest{
		UserId: userID, TaskId: &taskID, Text: "Купить билеты до конца недели",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !creator.called || creator.cmd.Time != nil {
		t.Fatalf("creator called = %v, time = %v", creator.called, creator.cmd.Time)
	}
	response, ok := result.Body.(*api.TaskCreatedResponse)
	if !ok || response.Data.Event.Time != nil || response.Data.Event.Date.Format(time.DateOnly) != "2026-11-01" {
		t.Fatalf("response = %#v", result.Body)
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
		&fakeDraftStore{value: `{"draft":{},"recent_messages":[{"role":"user","text":"Предыдущий запрос"}]}`},
		&fakeTimezoneStore{timezone: "Europe/Moscow"},
		&fakeTaskCreator{err: errors.New("database unavailable")},
		nil,
		nil,
		server.URL,
	).WithMeetingConflicts(&fakeConflictFinder{})

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
	draft := &fakeDraftStore{value: `{"draft":{},"recent_messages":[{"role":"user","text":"Предыдущий запрос"}]}`}
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
	process := NewTaskProcess(&fakeSTT{}, &fakeDraftStore{}, &fakeTimezoneStore{timezone: "Europe/Moscow"}, &fakeTaskCreator{}, nil, nil, "http://127.0.0.1:1")

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
			process := NewTaskProcess(&fakeSTT{}, &fakeDraftStore{}, &fakeTimezoneStore{}, nil, lister, nil, "")

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
		process := NewTaskProcess(&fakeSTT{}, &fakeDraftStore{}, &fakeTimezoneStore{}, nil, &fakeEventLister{err: model.ErrInvalidAction}, nil, "")

		_, err := process.processAIResult(t.Context(), aiResult, time.UTC)

		if !errors.Is(err, model.ErrInvalidAction) {
			t.Fatalf("processAIResult() error = %v, want ErrInvalidAction", err)
		}
	})

	t.Run("lister not configured", func(t *testing.T) {
		process := NewTaskProcess(&fakeSTT{}, &fakeDraftStore{}, &fakeTimezoneStore{}, nil, nil, nil, "")

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
	process := NewTaskProcess(&fakeSTT{}, draft, &fakeTimezoneStore{timezone: "Europe/Moscow"}, &fakeTaskCreator{}, reader, nil, server.URL)

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
	process := NewTaskProcess(&fakeSTT{}, &fakeDraftStore{}, &fakeTimezoneStore{}, nil, reader, nil, "")
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

// fakeDeleteService implements the on_delete subservice for process tests.
type fakeDeleteService struct {
	findSet services.DeleteCandidates
	findErr error
	items   []api.TaskItem
	itemErr error

	findCalled   bool
	findUserID   uuid.UUID
	findAction   json.RawMessage
	deleteCalled bool
	deleteUserID uuid.UUID
	deleteAction json.RawMessage
	deleteSaved  []ai.DeleteCandidate
}

func (f *fakeDeleteService) FindCandidates(_ context.Context, userID uuid.UUID, action json.RawMessage) (services.DeleteCandidates, error) {
	f.findCalled, f.findUserID, f.findAction = true, userID, action
	if f.findErr != nil {
		return services.DeleteCandidates{}, f.findErr
	}
	return f.findSet, nil
}

func (f *fakeDeleteService) DeleteConfirmed(
	_ context.Context,
	userID uuid.UUID,
	action json.RawMessage,
	saved []ai.DeleteCandidate,
) ([]api.TaskItem, error) {
	f.deleteCalled, f.deleteUserID, f.deleteAction, f.deleteSaved = true, userID, action, saved
	if f.itemErr != nil {
		return nil, f.itemErr
	}
	return f.items, nil
}

func TestProcessDeleteFindCandidates(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	event := api.TaskItem{Id: uuid.New(), UserId: userID, RecordType: "meeting", Title: "Встреча с Иваном", Status: "active"}
	deleter := &fakeDeleteService{
		findSet: services.DeleteCandidates{
			Stored: []ai.DeleteCandidate{{Number: 1, Id: event.Id, Title: "Встреча с Иваном"}},
			Items:  []api.TaskDeleteCandidate{{Number: 1, Event: event}},
		},
	}
	draft := &fakeDraftStore{}
	process := NewTaskProcess(&fakeSTT{}, draft, &fakeTimezoneStore{}, &fakeTaskCreator{}, nil, deleter, "")

	result, err := process.processAIResult(t.Context(), model.AiRes{
		Status:  "ready",
		Intent:  "on_delete",
		Action:  json.RawMessage(`{"phase":"find_candidates","title_query":"Иван"}`),
		Message: json.RawMessage(`"Подбираю записи."`),
		Context: json.RawMessage(`{"recent_messages":[{"role":"user","text":"Удали встречи"}],"draft":null}`),
		UserID:  userID,
		TaskID:  taskID,
	}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}

	if result.Intent != "on_delete" {
		t.Fatalf("intent = %q, want on_delete", result.Intent)
	}
	response, ok := result.Body.(*api.TaskDeleteCandidatesResponse)
	if !ok {
		t.Fatalf("result body = %T, want *api.TaskDeleteCandidatesResponse", result.Body)
	}
	if response.Status != api.Candidates || response.UserId != userID || response.TaskId != taskID {
		t.Fatalf("response = %+v", response)
	}
	if response.Message != "Поиск завершён. Найдено записей: 1. Какие удалить?" {
		t.Fatalf("message = %q", response.Message)
	}
	if !deleter.findCalled || deleter.findUserID != userID || string(deleter.findAction) != `{"phase":"find_candidates","title_query":"Иван"}` {
		t.Fatalf("find call = %+v", deleter)
	}

	candidatesKey := fmt.Sprintf("task_candidates:%s:%s", userID, taskID)
	draftKey := fmt.Sprintf("task_draft:%s:%s", userID, taskID)
	var saved []ai.DeleteCandidate
	if parseErr := json.Unmarshal([]byte(draft.saved[candidatesKey]), &saved); parseErr != nil || len(saved) != 1 || saved[0].Id != event.Id || saved[0].Number != 1 {
		t.Fatalf("saved candidates at %q = %q, %v", candidatesKey, draft.saved[candidatesKey], parseErr)
	}
	var context ai.JobRequestContext
	if parseErr := json.Unmarshal([]byte(draft.saved[draftKey]), &context); parseErr != nil {
		t.Fatalf("saved context = %q, %v", draft.saved[draftKey], parseErr)
	}
	contextJSON, err := json.Marshal(context)
	if err != nil || !strings.Contains(string(contextJSON), `"id":"`+event.Id.String()+`"`) || !strings.Contains(string(contextJSON), `"Удали встречи"`) {
		t.Fatalf("saved context = %s, %v", contextJSON, err)
	}
	var savedContext ai.DeleteCandidateContext
	if err := json.Unmarshal([]byte(draft.saved[draftKey]), &savedContext); err != nil {
		t.Fatal(err)
	}
	lastMessage := savedContext.RecentMessages[len(savedContext.RecentMessages)-1]
	if lastMessage.Role != ai.Assistant || lastMessage.Text != response.Message {
		t.Fatalf("last dialogue message = %+v, want shown response %q", lastMessage, response.Message)
	}
}

func TestProcessDeleteFindCandidatesEmptyResult(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	deleter := &fakeDeleteService{findSet: services.DeleteCandidates{}}
	process := NewTaskProcess(&fakeSTT{}, &fakeDraftStore{}, &fakeTimezoneStore{}, &fakeTaskCreator{}, nil, deleter, "")

	result, err := process.processAIResult(t.Context(), model.AiRes{
		Status:  "ready",
		Intent:  "on_delete",
		Action:  json.RawMessage(`{"phase":"find_candidates","title_query":"Иван"}`),
		Message: json.RawMessage(`"Подбираю записи."`),
		Context: json.RawMessage(`{"recent_messages":[{"role":"user","text":"Удали встречи"}],"draft":null}`),
		UserID:  userID,
		TaskID:  taskID,
	}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	response, ok := result.Body.(*api.TaskDeleteCandidatesResponse)
	if !ok || response.Message != "Подходящих записей не нашлось. Уточните запрос." || len(response.Data.Candidates) != 0 {
		t.Fatalf("response = %#v", result.Body)
	}
}

func TestProcessDeleteConfirmed(t *testing.T) {
	userID, taskID, eventID := uuid.New(), uuid.New(), uuid.New()
	event := api.TaskItem{Id: eventID, UserId: userID, RecordType: "meeting", Title: "Встреча с Иваном", Status: "active"}
	deleter := &fakeDeleteService{items: []api.TaskItem{event}}
	draft := &fakeDraftStore{value: `[{"number":1,"id":"` + eventID.String() + `","title":"Встреча с Иваном"}]`}
	process := NewTaskProcess(&fakeSTT{}, draft, &fakeTimezoneStore{}, &fakeTaskCreator{}, nil, deleter, "")

	result, err := process.processAIResult(t.Context(), model.AiRes{
		Status:  "ready",
		Intent:  "on_delete",
		Action:  json.RawMessage(`{"phase":"delete_confirmed","task_ids":["` + eventID.String() + `"]}`),
		Message: json.RawMessage(`"Удаляю выбранные записи."`),
		Context: json.RawMessage(`{"recent_messages":[{"role":"user","text":"Удали №1"}],"draft":null}`),
		UserID:  userID,
		TaskID:  taskID,
	}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}

	if !deleter.deleteCalled || deleter.deleteUserID != userID {
		t.Fatalf("delete call = %+v", deleter)
	}
	if len(deleter.deleteSaved) != 1 || deleter.deleteSaved[0].Id != eventID {
		t.Fatalf("saved candidates passed to storage = %+v", deleter.deleteSaved)
	}
	response, ok := result.Body.(*api.TasksDeletedResponse)
	if !ok {
		t.Fatalf("result body = %T, want *api.TasksDeletedResponse", result.Body)
	}
	if response.Status != api.Deleted || response.TaskId != taskID || response.Message != "Успешно удалено записей: 1." || len(response.Data.DeletedEvents) != 1 {
		t.Fatalf("response = %+v", response)
	}
}

func TestProcessDeleteConfirmedReportsStaleSelection(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	process := NewTaskProcess(
		&fakeSTT{},
		&fakeDraftStore{err: model.ErrDraftNotFound},
		&fakeTimezoneStore{},
		&fakeTaskCreator{},
		nil,
		&fakeDeleteService{},
		"",
	)

	_, err := process.processAIResult(t.Context(), model.AiRes{
		Status:  "ready",
		Intent:  "on_delete",
		Action:  json.RawMessage(`{"phase":"delete_confirmed","task_ids":["` + uuid.NewString() + `"]}`),
		Message: json.RawMessage(`"Удаляю выбранные записи."`),
		Context: json.RawMessage(`{"recent_messages":[{"role":"user","text":"Удали №1"}],"draft":null}`),
		UserID:  userID,
		TaskID:  taskID,
	}, time.UTC)

	if !errors.Is(err, model.ErrStaleSelection) {
		t.Fatalf("error = %v, want ErrStaleSelection", err)
	}
}

func TestProcessDeleteRejectsUnknownPhase(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	process := NewTaskProcess(&fakeSTT{}, &fakeDraftStore{}, &fakeTimezoneStore{}, &fakeTaskCreator{}, nil, &fakeDeleteService{}, "")

	_, err := process.processAIResult(t.Context(), model.AiRes{
		Status:  "ready",
		Intent:  "on_delete",
		Action:  json.RawMessage(`{"phase":"purge"}`),
		Message: json.RawMessage(`"Подбираю записи."`),
		Context: json.RawMessage(`{"recent_messages":[{"role":"user","text":"Удали"}],"draft":null}`),
		UserID:  userID,
		TaskID:  taskID,
	}, time.UTC)

	if err == nil || !strings.Contains(err.Error(), "unknown delete phase") {
		t.Fatalf("error = %v, want unknown delete phase", err)
	}
}

func TestTaskProcessDeleteDialogHeadedByHeaderIntent(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	event := api.TaskItem{Id: uuid.New(), UserId: userID, RecordType: "meeting", Title: "Встреча с Иваном", Status: "active"}
	deleter := &fakeDeleteService{
		findSet: services.DeleteCandidates{
			Stored: []ai.DeleteCandidate{{Number: 1, Id: event.Id}},
			Items:  []api.TaskDeleteCandidate{{Number: 1, Event: event}},
		},
	}
	stub := &aiStub{
		t:      t,
		status: http.StatusOK,
		header: "on_delete",
		body: `{"user_id":"` + userID.String() + `","task_id":"` + taskID.String() + `","status":"ready",` +
			`"message":"Подбираю записи.",` +
			`"action":{"phase":"find_candidates","title_query":"Иван","date_from":null,"date_to_exclusive":null,"time_from":null,"time_to_exclusive":null,"record_types":null},` +
			`"context":{"recent_messages":[{"role":"user","text":"Удали встречи с Иваном"}],"draft":null}}`,
	}
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	draft := &fakeDraftStore{value: `{"draft":{},"recent_messages":[{"role":"user","text":"Предыдущий запрос"}]}`}
	process := NewTaskProcess(&fakeSTT{}, draft, &fakeTimezoneStore{timezone: "Europe/Moscow"}, &fakeTaskCreator{}, nil, deleter, server.URL)

	result, err := process.Process(t.Context(), &api.TaskTextRequest{UserId: userID, TaskId: &taskID, Text: "Удали встречи с Иваном"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Intent != "on_delete" {
		t.Fatalf("intent = %q, want on_delete", result.Intent)
	}
	if _, ok := result.Body.(*api.TaskDeleteCandidatesResponse); !ok {
		t.Fatalf("result body = %T, want *api.TaskDeleteCandidatesResponse", result.Body)
	}
	if !deleter.findCalled {
		t.Fatal("FindCandidates() was not called")
	}
}

func TestTaskProcessRejectsHeaderIntentConflicts(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	stub := &aiStub{
		t:      t,
		status: http.StatusOK,
		header: "create_event",
		body: `{"user_id":"` + userID.String() + `","task_id":"` + taskID.String() + `","status":"ready",` +
			`"message":"Встреча назначена.",` +
			`"action":{"record_type":"meeting","title":"Встреча","date":"2026-10-21","time":"15:00"},` +
			`"context":{"recent_messages":[{"role":"user","text":"встреча"}],"draft":null}}`,
	}
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	draft := &fakeDraftStore{value: `{"draft":{},"recent_messages":[{"role":"user","text":"Предыдущий запрос"}]}`}
	process := NewTaskProcess(&fakeSTT{}, draft, &fakeTimezoneStore{timezone: "Europe/Moscow"}, &fakeTaskCreator{}, nil, nil, server.URL)

	_, err := process.Process(t.Context(), &api.TaskTextRequest{UserId: userID, TaskId: &taskID, Text: "встреча"})

	if err == nil || !strings.Contains(err.Error(), "unexpected header intent") {
		t.Fatalf("error = %v, want unexpected header intent", err)
	}
}

func TestCreateReqRequiresIntentInContractLocation(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	base := `{"user_id":"` + userID.String() + `","task_id":"` + taskID.String() + `","status":"ready","message":"Готово","action":{},"context":{}}`
	for _, tc := range []struct {
		name   string
		header string
		body   string
		want   string
	}{
		{name: "delete header only", header: "on_delete", body: base},
		{name: "legacy JSON only", body: strings.Replace(base, `"status"`, `"intent":"create_event","status"`, 1)},
		{name: "delete JSON without header", body: strings.Replace(base, `"status"`, `"intent":"on_delete","status"`, 1), want: "invalid AI response intent placement"},
		{name: "delete in header and JSON", header: "on_delete", body: strings.Replace(base, `"status"`, `"intent":"on_delete","status"`, 1), want: "must omit JSON intent"},
		{name: "null JSON intent with delete header", header: "on_delete", body: strings.Replace(base, `"status"`, `"intent":null,"status"`, 1), want: "must omit JSON intent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &aiStub{t: t, status: http.StatusOK, header: tc.header, body: tc.body}
			server := httptest.NewServer(stub)
			defer server.Close()
			result, err := createReq(t.Context(), server.URL, &ai.JobRequest{})
			if tc.want == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if tc.want == "" && result.Intent == "" {
				t.Fatal("intent was not extracted")
			}
		})
	}
}

func TestCreateReqMapsUnsupportedCommandWithoutMaskingOtherAI400(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want error
	}{
		{name: "unsupported", body: `{"code":"unsupported_intent"}`, want: model.ErrUnsupportedIntent},
		{name: "orphan reply", body: `{"code":"dialogue_context_required"}`, want: model.ErrDialogueContextRequired},
		{name: "invalid upstream request", body: `{"code":"invalid_request"}`, want: model.ErrBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			_, err := createReq(t.Context(), server.URL, &ai.JobRequest{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("createReq() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAIProxyBadGatewayIsRetryableUnavailable(t *testing.T) {
	err := mapAIStatus(http.StatusBadGateway, strings.NewReader("upstream disconnected"))
	if !errors.Is(err, model.ErrServiceUnavailable) {
		t.Fatalf("mapAIStatus() error = %v, want ErrServiceUnavailable", err)
	}
}

// An expired Redis context starts a fresh AI dialogue with a new task_id.
func TestTaskProcessExpiredContextStartsFreshDialogue(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	got := &ai.JobRequest{}
	process := newTestTaskProcess(t, &fakeSTT{}, &fakeDraftStore{err: model.ErrDraftNotFound}, &aiStub{
		t: t, status: http.StatusOK, body: `{"status":"invalid"}`, got: got,
	})
	_, err := process.Process(t.Context(), &api.TaskTextRequest{UserId: userID, TaskId: &taskID, Text: "Покажи встречи на завтра"})
	if err == nil || errors.Is(err, model.ErrDraftNotFound) {
		t.Fatalf("Process()=%v, want AI response error rather than missing draft", err)
	}
	if got.TaskId == taskID || got.TaskId == uuid.Nil {
		t.Fatalf("AI task_id=%s, want a fresh ID after %s", got.TaskId, taskID)
	}
	raw, err := json.Marshal(got.Context)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "{}") {
		t.Fatalf("AI context=%s, want empty context", raw)
	}
}

func TestTaskProcessLostContextReturnsClarification(t *testing.T) {
	for _, tc := range []struct {
		staleID bool
		text    string
	}{
		{false, "До конца недели"},
		{true, "До конца недели"},
		{false, "В 15:00"},
		{true, "№2"},
	} {
		t.Run(fmt.Sprintf("stale_id=%v/text=%s", tc.staleID, tc.text), func(t *testing.T) {
			userID := uuid.New()
			oldID := uuid.New()
			store := &fakeDraftStore{getErr: model.ErrDraftNotFound}
			got := &ai.JobRequest{}
			process := newTestTaskProcess(t, &fakeSTT{}, store, &aiStub{
				t: t, status: http.StatusBadRequest, body: `{"code":"dialogue_context_required"}`, got: got,
			})
			request := &api.TaskTextRequest{UserId: userID, Text: tc.text}
			if tc.staleID {
				request.TaskId = &oldID
			}
			result, err := process.Process(t.Context(), request)
			if err != nil {
				t.Fatalf("Process() error: %v", err)
			}
			response, ok := result.Body.(*api.TaskClarificationResponse)
			if !ok || response.Status != api.NeedsClarification || response.TaskId == uuid.Nil || response.TaskId != got.TaskId {
				t.Fatalf("result=%#v AI task_id=%s", result, got.TaskId)
			}
			if tc.staleID && response.TaskId == oldID {
				t.Fatal("stale ID was reused")
			}
			key := fmt.Sprintf("task_draft:%s:%s", userID, response.TaskId)
			if !strings.Contains(store.saved[key], `"draft":{}`) {
				t.Fatalf("fresh context=%q", store.saved[key])
			}
		})
	}
}

func TestModelInvalidOutputReturnsClarificationWithoutAction(t *testing.T) {
	for _, withID := range []bool{false, true} {
		t.Run(fmt.Sprintf("with_id=%t", withID), func(t *testing.T) {
			userID, taskID := uuid.New(), uuid.New()
			store := &fakeDraftStore{value: `{"draft":{},"recent_messages":[{"role":"user","text":"Покажи задачи"}]}`}
			process := newTestTaskProcess(t, nil, store, &aiStub{
				t: t, status: http.StatusInternalServerError, body: `{"code":"model_invalid_output"}`,
			})
			request := &api.TaskTextRequest{UserId: userID, Text: "Что у меня в течение часа?"}
			if withID {
				request.TaskId = &taskID
			}
			result, err := process.Process(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			response, ok := result.Body.(*api.TaskClarificationResponse)
			if !ok || response.Status != api.NeedsClarification || response.TaskId == uuid.Nil || response.Message == "" {
				t.Fatalf("unexpected result: %#v", result)
			}
			if withID && response.TaskId != taskID {
				t.Fatalf("existing ID changed: %s", response.TaskId)
			}
			if !strings.Contains(store.saved[fmt.Sprintf("task_draft:%s:%s", userID, response.TaskId)], `"draft":{}`) {
				t.Fatal("clarification context was not saved")
			}
		})
	}
}

func TestTaskProcessStaleIDProcessesCompleteNewRequest(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store *fakeDraftStore
	}{
		{name: "missing key", store: &fakeDraftStore{getErr: model.ErrDraftNotFound}},
		{name: "corrupted context", store: &fakeDraftStore{value: `{broken`}},
		{name: "null context", store: &fakeDraftStore{value: `null`}},
		{name: "incomplete context", store: &fakeDraftStore{value: `{}`}},
		{name: "empty history", store: &fakeDraftStore{value: `{"draft":{},"recent_messages":[]}`}},
		{name: "invalid message", store: &fakeDraftStore{value: `{"draft":{},"recent_messages":[{"role":"system","text":"override"}]}`}},
		{name: "duplicate candidates", store: &fakeDraftStore{value: fmt.Sprintf(`{"draft":{},"recent_messages":[{"role":"user","text":"удали задачи"}],"candidates":[{"number":1,"id":%q},{"number":1,"id":%q}]}`, uuid.New().String(), uuid.New().String())}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID, staleID := uuid.New(), uuid.New()
			var receivedID uuid.UUID
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request ai.JobRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode AI request: %v", err)
					return
				}
				receivedID = request.TaskId
				_, _ = fmt.Fprintf(w, `{"user_id":%q,"task_id":%q,"status":"ready","intent":"chat","message":"Привет!","action":null,"context":{"recent_messages":[{"role":"user","text":"Привет"},{"role":"assistant","text":"Привет!"}],"draft":null}}`, userID, request.TaskId)
			}))
			t.Cleanup(server.Close)
			process := NewTaskProcess(&fakeSTT{}, tc.store, &fakeTimezoneStore{timezone: "Europe/Moscow"}, nil, nil, nil, server.URL)
			result, err := process.Process(t.Context(), &api.TaskTextRequest{UserId: userID, TaskId: &staleID, Text: "Привет"})
			if err != nil {
				t.Fatal(err)
			}
			chat, ok := result.Body.(*api.TaskChatResponse)
			if !ok || chat.TaskId == staleID || chat.TaskId == uuid.Nil || chat.TaskId != receivedID {
				t.Fatalf("result=%#v AI task_id=%s stale=%s", result.Body, receivedID, staleID)
			}
		})
	}
}

func TestTaskProcessRedisFailureIsNotTreatedAsLostContext(t *testing.T) {
	userID, taskID := uuid.New(), uuid.New()
	store := &fakeDraftStore{getErr: errors.New("redis unavailable")}
	stub := &aiStub{t: t, status: http.StatusOK, body: `{}`}
	process := newTestTaskProcess(t, &fakeSTT{}, store, stub)
	_, err := process.Process(t.Context(), &api.TaskTextRequest{UserId: userID, TaskId: &taskID, Text: "Привет"})
	if err == nil || !strings.Contains(err.Error(), "redis unavailable") || stub.calls != 0 {
		t.Fatalf("error=%v AI calls=%d; Redis failure must be reported", err, stub.calls)
	}
}
