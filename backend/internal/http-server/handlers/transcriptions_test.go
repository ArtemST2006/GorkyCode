package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	api "gorkycode/backend/internal/generated"
)

type fakeTranscriber struct {
	text   string
	err    error
	called bool
}

func (f *fakeTranscriber) Transcribe(_ context.Context, audio io.Reader) (string, error) {
	f.called = true
	data, err := io.ReadAll(audio)
	if err != nil || !bytes.HasPrefix(data, []byte("RIFF")) {
		return "", errors.New("unexpected audio data")
	}
	return f.text, f.err
}

func transcriptionRequest(t *testing.T, filename string, audio []byte) *http.Request {
	t.Helper()
	r, contentType := newMultipartTaskRequest(t, nil, filename, audio, true)
	r.URL.Path = "/api/v1/transcriptions"
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("Authorization", "Bearer test-token")
	return r
}

func transcriptionRouter(auth *fakeAuthService, process *fakeTaskProcess, rec *fakeTranscriber) http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(log, taskTestServices{fakeAuthService: auth, fakeTaskProcess: process}, WithAudioTranscriber(rec))
	return api.Handler(server)
}

func TestTranscriptionReturnsTextWithoutProcessingTask(t *testing.T) {
	process := &fakeTaskProcess{}
	rec := &fakeTranscriber{text: "  Поставь напоминание  "}
	r := transcriptionRequest(t, "voice.wav", []byte("RIFF....WAVEdata"))
	w := httptest.NewRecorder()
	transcriptionRouter(&fakeAuthService{userID: uuid.New()}, process, rec).ServeHTTP(w, r)
	if w.Code != http.StatusOK || !rec.called || process.called {
		t.Fatalf("status=%d, transcribed=%v, task processed=%v, body=%s", w.Code, rec.called, process.called, w.Body.String())
	}
	var body api.TranscriptionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Text != "Поставь напоминание" {
		t.Fatalf("response=%+v, err=%v", body, err)
	}
}

func TestTranscriptionRejectsInvalidInputBeforeRecognition(t *testing.T) {
	for _, tc := range []struct {
		name  string
		file  string
		audio []byte
	}{
		{"empty", "voice.wav", nil},
		{"wrong extension", "voice.mp3", []byte("RIFF....WAVEdata")},
		{"wrong content", "voice.wav", []byte("not a wave")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &fakeTranscriber{text: "text"}
			w := httptest.NewRecorder()
			transcriptionRouter(&fakeAuthService{userID: uuid.New()}, &fakeTaskProcess{}, rec).ServeHTTP(w, transcriptionRequest(t, tc.file, tc.audio))
			if w.Code != http.StatusBadRequest || rec.called {
				t.Fatalf("status=%d, transcribed=%v, body=%s", w.Code, rec.called, w.Body.String())
			}
		})
	}
}

func TestTranscriptionRejectsMissingSession(t *testing.T) {
	rec := &fakeTranscriber{text: "text"}
	r := transcriptionRequest(t, "voice.wav", []byte("RIFF....WAVEdata"))
	r.Header.Del("Authorization")
	w := httptest.NewRecorder()
	transcriptionRouter(&fakeAuthService{}, &fakeTaskProcess{}, rec).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || rec.called {
		t.Fatalf("status=%d, transcribed=%v", w.Code, rec.called)
	}
}

func TestTranscriptionRejectsAdditionalFieldsAndOversizedBody(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]string
		audio  []byte
		want   int
	}{
		{"extra field", map[string]string{"task_id": uuid.NewString()}, []byte("RIFF....WAVEdata"), http.StatusBadRequest},
		{"too large", nil, append([]byte("RIFF....WAVE"), bytes.Repeat([]byte("x"), maxTaskProcessReadSize)...), http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, contentType := newMultipartTaskRequest(t, tc.fields, "voice.wav", tc.audio, true)
			r.URL.Path = "/api/v1/transcriptions"
			r.Header.Set("Content-Type", contentType)
			r.Header.Set("Authorization", "Bearer test-token")
			rec := &fakeTranscriber{text: "text"}
			w := httptest.NewRecorder()
			transcriptionRouter(&fakeAuthService{userID: uuid.New()}, &fakeTaskProcess{}, rec).ServeHTTP(w, r)
			if w.Code != tc.want || rec.called {
				t.Fatalf("status=%d, transcribed=%v, body=%s", w.Code, rec.called, w.Body.String())
			}
		})
	}
}

func TestTranscriptionHandlesSilenceAndRecognizerError(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		err  error
		want int
	}{
		{"silence", "  ", nil, http.StatusUnprocessableEntity},
		{"recognizer error", "", errors.New("decode failed"), http.StatusBadGateway},
		{"recognizer timeout", "", context.DeadlineExceeded, http.StatusGatewayTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &fakeTranscriber{text: tc.text, err: tc.err}
			w := httptest.NewRecorder()
			transcriptionRouter(&fakeAuthService{userID: uuid.New()}, &fakeTaskProcess{}, rec).ServeHTTP(w, transcriptionRequest(t, "voice.wav", []byte("RIFF....WAVEdata")))
			if w.Code != tc.want || !rec.called || !strings.Contains(w.Header().Get("Content-Type"), "application/problem+json") {
				t.Fatalf("status=%d, transcribed=%v, body=%s", w.Code, rec.called, w.Body.String())
			}
		})
	}
}
