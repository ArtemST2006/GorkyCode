package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorkycode/backend/internal/model"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	api "gorkycode/backend/internal/generated"
)

// fakeTaskProcess returns what the test configured and records its arguments.
type fakeTaskProcess struct {
	result model.TaskResult
	err    error

	called bool
	req    model.TaskRequest
}

var _ TaskProcess = (*fakeTaskProcess)(nil)

func (f *fakeTaskProcess) Process(_ context.Context, req model.TaskRequest) (model.TaskResult, error) {
	f.called = true
	f.req = req
	return f.result, f.err
}

type taskTestServices struct {
	fakeReadinessService
	*fakeAuthService
	*fakeTaskProcess
}

func newTaskServer(process *fakeTaskProcess, auth *fakeAuthService) *Server {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(log, taskTestServices{
		fakeReadinessService: fakeReadinessService{},
		fakeAuthService:      auth,
		fakeTaskProcess:      process,
	})
}

func doTaskJSON(t *testing.T, process *fakeTaskProcess, auth *fakeAuthService, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/task", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	newTaskServer(process, auth).ProcessTask(response, request)
	return response
}

func newMultipartTaskRequest(
	t *testing.T,
	fields map[string]string,
	filename string,
	content []byte,
	withFile bool,
) (*http.Request, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatalf("write field %q: %v", name, err)
		}
	}
	if withFile {
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			t.Fatalf("create file part: %v", err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatalf("write file part: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/task", &body)
	return request, writer.FormDataContentType()
}

func TestProcessTaskTextSuccess(t *testing.T) {
	userID := uuid.New()
	taskID := uuid.New()
	process := &fakeTaskProcess{
		result: &api.TaskClarificationResponse{
			UserId:  userID,
			TaskId:  taskID,
			Status:  "needs_clarification",
			Message: "В какое время?",
		},
	}

	auth := &fakeAuthService{userID: userID}
	response := doTaskJSON(t, process, auth, fmt.Sprintf(`{"user_id":%q,"text":"Назначь встречу"}`, userID))

	if response.Code != http.StatusOK {
		t.Fatalf("POST /api/task: got %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	var body api.TaskClarificationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Message != "В какое время?" || body.UserId != userID || body.TaskId != taskID {
		t.Fatalf("unexpected response: %+v", body)
	}

	textReq, ok := process.req.(*api.TaskTextRequest)
	if !ok {
		t.Fatalf("service got %T, want *api.TaskTextRequest", process.req)
	}
	if textReq.Text != "Назначь встречу" || textReq.UserId != userID {
		t.Fatalf("service got %+v", textReq)
	}
	if auth.token != "test-token" {
		t.Fatalf("authenticated token = %q", auth.token)
	}
}

func TestProcessTaskRequiresSession(t *testing.T) {
	userID := uuid.New()
	for _, tc := range []struct {
		name       string
		header     string
		authErr    error
		wantStatus int
		wantCode   string
	}{
		{name: "missing token", wantStatus: http.StatusUnauthorized, wantCode: "unauthorized"},
		{name: "wrong scheme", header: "Basic test-token", wantStatus: http.StatusUnauthorized, wantCode: "unauthorized"},
		{name: "expired session", header: "Bearer test-token", authErr: model.ErrSessionNotFound, wantStatus: http.StatusUnauthorized, wantCode: "unauthorized"},
		{name: "session store error", header: "Bearer test-token", authErr: errors.New("redis unavailable"), wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			process := &fakeTaskProcess{}
			auth := &fakeAuthService{userID: userID, authErr: tc.authErr}
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/task", strings.NewReader(fmt.Sprintf(`{"user_id":%q,"text":"привет"}`, userID)))
			request.Header.Set("Content-Type", "application/json")
			if tc.header != "" {
				request.Header.Set("Authorization", tc.header)
			}
			response := httptest.NewRecorder()
			newTaskServer(process, auth).ProcessTask(response, request)
			if response.Code != tc.wantStatus {
				t.Fatalf("POST /api/task: got %d, want %d: %s", response.Code, tc.wantStatus, response.Body)
			}
			assertProblemResponse(t, response, tc.wantCode)
			if process.called {
				t.Fatal("task service must not be called without a valid session")
			}
		})
	}
}

func TestProcessTaskRejectsOtherUser(t *testing.T) {
	otherUserID := uuid.New()
	auth := &fakeAuthService{userID: uuid.New()}

	t.Run("text", func(t *testing.T) {
		process := &fakeTaskProcess{}
		response := doTaskJSON(t, process, auth, fmt.Sprintf(`{"user_id":%q,"text":"привет"}`, otherUserID))
		if response.Code != http.StatusForbidden {
			t.Fatalf("POST /api/task: got %d, want %d: %s", response.Code, http.StatusForbidden, response.Body)
		}
		assertProblemResponse(t, response, "forbidden")
		if process.called {
			t.Fatal("task service must not be called for another user")
		}
	})

	t.Run("audio", func(t *testing.T) {
		process := &fakeTaskProcess{}
		request, contentType := newMultipartTaskRequest(t, map[string]string{"user_id": otherUserID.String()}, "voice.wav", []byte("audio-bytes"), true)
		request.Header.Set("Content-Type", contentType)
		request.Header.Set("Authorization", "Bearer test-token")
		response := httptest.NewRecorder()
		newTaskServer(process, auth).ProcessTask(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("POST /api/task: got %d, want %d: %s", response.Code, http.StatusForbidden, response.Body)
		}
		assertProblemResponse(t, response, "forbidden")
		if process.called {
			t.Fatal("task service must not be called for another user")
		}
	})
}

func TestProcessTaskMapsResultTypes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		result   model.TaskResult
		wantCode int
	}{
		{name: "clarification", result: &api.TaskClarificationResponse{}, wantCode: http.StatusOK},
		{name: "collection", result: &api.TaskCollectionResponse{}, wantCode: http.StatusOK},
		{name: "created", result: &api.TaskCreatedResponse{}, wantCode: http.StatusCreated},
		{name: "unknown", result: struct{}{}, wantCode: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID := uuid.New()
			response := doTaskJSON(t, &fakeTaskProcess{result: tc.result}, &fakeAuthService{userID: userID}, fmt.Sprintf(`{"user_id":%q,"text":"привет"}`, userID))
			if response.Code != tc.wantCode {
				t.Fatalf("POST /api/task: got %d, want %d: %s", response.Code, tc.wantCode, response.Body)
			}
			if tc.wantCode == http.StatusInternalServerError {
				assertProblemResponse(t, response, "internal_error")
			}
		})
	}
}

func TestProcessTaskRejectsInvalidJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "malformed", body: `{"user_id":`},
		{name: "missing required fields", body: `{}`},
		{name: "blank text", body: fmt.Sprintf(`{"user_id":%q,"text":"   "}`, uuid.New())},
		{name: "unknown field", body: fmt.Sprintf(`{"user_id":%q,"text":"привет","debug":true}`, uuid.New())},
		{name: "trailing data", body: fmt.Sprintf(`{"user_id":%q,"text":"привет"}{}`, uuid.New())},
		{name: "too large", body: fmt.Sprintf(`{"text":%q}`, strings.Repeat("a", maxAuthBodyBytes))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			process := &fakeTaskProcess{}
			response := doTaskJSON(t, process, &fakeAuthService{userID: uuid.New()}, tc.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("POST /api/task: got %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body)
			}
			assertProblemResponse(t, response, "invalid_request")
			if process.called {
				t.Fatal("service must not be called for an invalid request")
			}
		})
	}
}

func TestProcessTaskRequiresKnownContentType(t *testing.T) {
	process := &fakeTaskProcess{}
	auth := &fakeAuthService{userID: uuid.New()}

	t.Run("missing content type", func(t *testing.T) {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/task", strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer test-token")
		response := httptest.NewRecorder()
		newTaskServer(process, auth).ProcessTask(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/task: got %d, want %d", response.Code, http.StatusBadRequest)
		}
		assertProblemResponse(t, response, "invalid_request")
	})

	t.Run("unsupported content type", func(t *testing.T) {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/task", strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "text/plain")
		request.Header.Set("Authorization", "Bearer test-token")
		response := httptest.NewRecorder()
		newTaskServer(process, auth).ProcessTask(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/task: got %d, want %d", response.Code, http.StatusBadRequest)
		}
		assertProblemResponse(t, response, "invalid_request")
	})

	if process.called {
		t.Fatal("service must not be called")
	}
}

func TestProcessTaskMapsServiceError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"internal", errors.New("unexpected"), http.StatusInternalServerError, "internal_error"},
		{"upstream", model.ErrBadGateway, http.StatusBadGateway, "bad_gateway"},
		{"invalid AI action", model.ErrInvalidAction, http.StatusBadGateway, "bad_gateway"},
		{"timeout", model.ErrGatewayTimeout, http.StatusGatewayTimeout, "gateway_timeout"},
		{"unavailable", model.ErrServiceUnavailable, http.StatusServiceUnavailable, "service_unavailable"},
		{"missing draft", model.ErrDraftNotFound, http.StatusNotFound, "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			process := &fakeTaskProcess{err: tc.err}
			userID := uuid.New()
			response := doTaskJSON(t, process, &fakeAuthService{userID: userID}, fmt.Sprintf(`{"user_id":%q,"text":"привет"}`, userID))
			if response.Code != tc.status {
				t.Fatalf("POST /api/task: got %d, want %d", response.Code, tc.status)
			}
			assertProblemResponse(t, response, tc.code)
			if tc.status == http.StatusInternalServerError && !strings.Contains(response.Body.String(), "Извините, возникли внутренние проблемы.") {
				t.Fatalf("500 response lacks neutral detail: %s", response.Body.String())
			}
		})
	}
}

func TestProcessTaskAudioSuccess(t *testing.T) {
	userID := uuid.New()
	taskID := uuid.New()
	process := &fakeTaskProcess{result: &api.TaskClarificationResponse{}}
	request, contentType := newMultipartTaskRequest(t, map[string]string{
		"user_id": userID.String(),
		"task_id": taskID.String(),
	}, "voice.wav", []byte("audio-bytes"), true)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Authorization", "Bearer test-token")

	response := httptest.NewRecorder()
	newTaskServer(process, &fakeAuthService{userID: userID}).ProcessTask(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("POST /api/task: got %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}

	audioReq, ok := process.req.(*api.TaskAudioRequest)
	if !ok {
		t.Fatalf("service got %T, want *api.TaskAudioRequest", process.req)
	}
	if audioReq.UserId != userID {
		t.Fatalf("user_id = %s, want %s", audioReq.UserId, userID)
	}
	if audioReq.TaskId == nil || *audioReq.TaskId != taskID {
		t.Fatalf("task_id = %v, want %s", audioReq.TaskId, taskID)
	}

	file, err := audioReq.File.Reader()
	if err != nil {
		t.Fatalf("open audio file: %v", err)
	}
	defer func() {
		_ = file.Close()
	}()
	content, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read audio file: %v", err)
	}
	if string(content) != "audio-bytes" {
		t.Fatalf("audio content = %q", content)
	}
}

func TestProcessTaskRejectsInvalidMultipart(t *testing.T) {
	userID := uuid.New().String()
	for _, tc := range []struct {
		name     string
		fields   map[string]string
		filename string
		withFile bool
	}{
		{name: "missing user_id", filename: "voice.wav", withFile: true},
		{name: "invalid user_id", fields: map[string]string{"user_id": "not-a-uuid"}, filename: "voice.wav", withFile: true},
		{name: "invalid task_id", fields: map[string]string{"user_id": userID, "task_id": "not-a-uuid"}, filename: "voice.wav", withFile: true},
		{name: "missing file", fields: map[string]string{"user_id": userID}},
		{name: "unsupported extension", fields: map[string]string{"user_id": userID}, filename: "voice.mp3", withFile: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			process := &fakeTaskProcess{}
			request, contentType := newMultipartTaskRequest(t, tc.fields, tc.filename, []byte("audio"), tc.withFile)
			request.Header.Set("Content-Type", contentType)
			request.Header.Set("Authorization", "Bearer test-token")
			response := httptest.NewRecorder()
			newTaskServer(process, &fakeAuthService{userID: uuid.New()}).ProcessTask(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("POST /api/task: got %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body)
			}
			assertProblemResponse(t, response, "invalid_request")
			if process.called {
				t.Fatal("service must not be called for an invalid request")
			}
		})
	}
}

func TestValidateFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		ok   bool
	}{
		{name: "wav", file: "voice.wav", ok: true},
		{name: "wave", file: "voice.wave", ok: true},
		{name: "uppercase", file: "VOICE.WAV", ok: true},
		{name: "mp3", file: "voice.mp3"},
		{name: "no extension", file: "voice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := &multipart.FileHeader{Filename: tc.file}
			err := validateFile(header)
			if tc.ok && err != nil {
				t.Fatalf("validateFile(%q) error: %v", tc.file, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("validateFile(%q) = nil, want error", tc.file)
			}
		})
	}
}
