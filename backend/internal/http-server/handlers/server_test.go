package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"gorkycode/backend/internal/http-server/apierr"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	api "gorkycode/backend/internal/generated"
)

type fakeReadinessService struct {
	err error
}

func (f fakeReadinessService) Ready(context.Context) error {
	return f.err
}

type fakeServices struct {
	fakeReadinessService
	*fakeAuthService
}

func TestHealthDoesNotDependOnDatabase(t *testing.T) {
	router := newTestRouter(errors.New("database unavailable"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET /health: got %d, want %d", response.Code, http.StatusOK)
	}
	assertHealthResponse(t, response, api.HealthResponseStatusOk)
}

func TestReadyReportsServiceStatus(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantCode   int
		wantStatus api.HealthResponseStatus
	}{
		{name: "available", wantCode: http.StatusOK, wantStatus: api.HealthResponseStatusOk},
		{name: "unavailable", err: errors.New("database unavailable"), wantCode: http.StatusServiceUnavailable, wantStatus: api.HealthResponseStatusUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := newTestRouter(tc.err)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ready", nil))
			if response.Code != tc.wantCode {
				t.Fatalf("GET /ready: got %d, want %d", response.Code, tc.wantCode)
			}
			assertHealthResponse(t, response, tc.wantStatus)
		})
	}
}

func newTestRouter(err error) http.Handler {
	return newTestRouterWithAuth(err, &fakeAuthService{})
}

func newTestRouterWithAuth(err error, auth *fakeAuthService) http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(log, fakeServices{
		fakeReadinessService: fakeReadinessService{err: err},
		fakeAuthService:      auth,
	})
	router := chi.NewRouter()
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		apierr.Write(w, r, apierr.NotFound("route not found"))
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		apierr.Write(w, r, apierr.MethodNotAllowed("method not allowed for this route"))
	})
	return api.HandlerWithOptions(server, api.ChiServerOptions{BaseRouter: router})
}

func TestUnknownRouteUsesProblemDetails(t *testing.T) {
	router := newTestRouter(nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/missing", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("GET /missing: got %d, want %d", response.Code, http.StatusNotFound)
	}
	assertProblemResponse(t, response, "not_found")
}

func TestWrongMethodUsesProblemDetails(t *testing.T) {
	router := newTestRouter(nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/health", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /health: got %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	assertProblemResponse(t, response, "method_not_allowed")
}

func assertHealthResponse(t *testing.T, response *httptest.ResponseRecorder, status api.HealthResponseStatus) {
	t.Helper()
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}
	var body api.HealthResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Service != "backend" || body.Status != status {
		t.Fatalf("unexpected health response: %+v", body)
	}
}

func assertProblemResponse(t *testing.T, response *httptest.ResponseRecorder, code string) {
	t.Helper()
	if got := response.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("content type = %q", got)
	}
	var problem api.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != code || int(problem.Status) != response.Code {
		t.Fatalf("unexpected problem: %+v", problem)
	}
}
