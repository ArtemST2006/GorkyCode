package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorkycode/backend/internal/generated/ai"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	return NewTaskProcess(stt, draft, &fakeTimezoneStore{timezone: "Europe/Moscow"}, server.URL)
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
	process := NewTaskProcess(&fakeSTT{}, &fakeDraftStore{}, &fakeTimezoneStore{timezone: "Asia/Tokyo"}, server.URL)

	_, _ = process.Process(t.Context(), &api.TaskTextRequest{UserId: uuid.New(), Text: "привет"})
	if got.Timezone != "Asia/Tokyo" {
		t.Fatalf("AI timezone = %q, want Asia/Tokyo", got.Timezone)
	}
	if _, offset := got.RequestTime.Zone(); offset != 9*60*60 {
		t.Fatalf("AI request_time offset = %d, want +09:00", offset)
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
	process := NewTaskProcess(&fakeSTT{}, &fakeDraftStore{}, &fakeTimezoneStore{timezone: "Europe/Moscow"}, "http://127.0.0.1:1")

	_, err := process.Process(t.Context(), "plain text")

	if err == nil || !strings.Contains(err.Error(), "unsupported request type") {
		t.Fatalf("Process() error = %v, want unsupported request type", err)
	}
}
