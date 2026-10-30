package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"gorkycode/backend/internal/model"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	api "gorkycode/backend/internal/generated"
)

const (
	googlePath        = "/api/v1/integrations/google"
	googleConnectPath = "/api/v1/integrations/google/connect"
	googleCallback    = "/api/v1/integrations/google/callback"
)

// fakeGoogleCalendar returns what the test configured and records its arguments.
type fakeGoogleCalendar struct {
	authURL     string
	integration *model.GoogleIntegration
	err         error

	userID    uuid.UUID
	code      string
	state     string
	googleErr string
}

var _ Calendar = (*fakeGoogleCalendar)(nil)

func (f *fakeGoogleCalendar) Connect(_ context.Context, userID uuid.UUID) (string, error) {
	f.userID = userID
	return f.authURL, f.err
}

func (f *fakeGoogleCalendar) Callback(_ context.Context, code, state, googleErr string) error {
	f.code, f.state, f.googleErr = code, state, googleErr
	return f.err
}

func (f *fakeGoogleCalendar) Status(_ context.Context, userID uuid.UUID) (*model.GoogleIntegration, error) {
	f.userID = userID
	return f.integration, f.err
}

func (f *fakeGoogleCalendar) Disconnect(_ context.Context, userID uuid.UUID) error {
	f.userID = userID
	return f.err
}

type googleTestServices struct {
	fakeReadinessService
	*fakeAuthService
	*fakeGoogleCalendar
}

func (googleTestServices) Process(context.Context, model.TaskRequest) (*model.TaskResult, error) {
	return nil, errors.New("task processing is not configured")
}

func doGoogleRequest(
	t *testing.T,
	calendar *fakeGoogleCalendar,
	auth *fakeAuthService,
	redirect, method, target string,
	header http.Header,
) *httptest.ResponseRecorder {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(log, googleTestServices{fakeAuthService: auth, fakeGoogleCalendar: calendar},
		WithGoogleFrontendRedirect(redirect))
	router := api.HandlerWithOptions(server, api.ChiServerOptions{BaseRouter: chi.NewRouter()})

	request := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	maps.Copy(request.Header, header)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestGoogleBearerEndpointsRequireSession(t *testing.T) {
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, googleConnectPath},
		{http.MethodGet, googlePath},
		{http.MethodDelete, googlePath},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			calendar := &fakeGoogleCalendar{}
			response := doGoogleRequest(t, calendar, &fakeAuthService{}, "", tc.method, tc.path, nil)

			if response.Code != http.StatusUnauthorized {
				t.Fatalf("got %d, want %d", response.Code, http.StatusUnauthorized)
			}
			assertProblemResponse(t, response, "unauthorized")
			if calendar.userID != uuid.Nil {
				t.Fatal("service must not be called without a session")
			}
		})
	}
}

func TestGoogleBearerEndpointsMapErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantCode int
		wantType string
	}{
		{name: "disabled", err: model.ErrGoogleDisabled, wantCode: http.StatusServiceUnavailable, wantType: "google_disabled"},
		{name: "timeout", err: context.DeadlineExceeded, wantCode: http.StatusServiceUnavailable, wantType: "service_unavailable"},
		{name: "failure", err: errors.New("database unavailable"), wantCode: http.StatusInternalServerError, wantType: "internal_error"},
	} {
		for _, endpoint := range []struct {
			method string
			path   string
		}{
			{http.MethodPost, googleConnectPath},
			{http.MethodGet, googlePath},
			{http.MethodDelete, googlePath},
		} {
			t.Run(tc.name+" "+endpoint.method+" "+endpoint.path, func(t *testing.T) {
				auth := &fakeAuthService{userID: uuid.New()}
				calendar := &fakeGoogleCalendar{err: tc.err}

				response := doGoogleRequest(t, calendar, auth, "", endpoint.method, endpoint.path, bearer("token"))

				if response.Code != tc.wantCode {
					t.Fatalf("got %d, want %d: %s", response.Code, tc.wantCode, response.Body)
				}
				assertProblemResponse(t, response, tc.wantType)
			})
		}
	}
}

func TestGoogleConnect(t *testing.T) {
	auth := &fakeAuthService{userID: uuid.New()}
	calendar := &fakeGoogleCalendar{authURL: "https://accounts.google.com/o/oauth2/auth?state=abc"}

	response := doGoogleRequest(t, calendar, auth, "", http.MethodPost, googleConnectPath, bearer("token"))

	if response.Code != http.StatusOK {
		t.Fatalf("got %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var body api.GoogleConnectResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.AuthUrl != calendar.authURL || calendar.userID != auth.userID {
		t.Fatalf("auth_url = %q, user = %s", body.AuthUrl, calendar.userID)
	}
}

func TestGoogleCallbackRedirects(t *testing.T) {
	for _, tc := range []struct {
		name     string
		redirect string
		query    string
		err      error
		want     string
	}{
		{name: "connected", redirect: "http://localhost:8088/", query: "code=c&state=s", want: "http://localhost:8088/?google=connected"},
		{name: "service error", redirect: "http://localhost:8088/", query: "code=c&state=s", err: model.ErrOAuthStateNotFound, want: "http://localhost:8088/?google=error"},
		{name: "disabled", redirect: "http://localhost:8088/", query: "code=c&state=s", err: model.ErrGoogleDisabled, want: "http://localhost:8088/?google=error"},
		{name: "keeps existing query", redirect: "http://localhost:8088/settings?tab=calendar", query: "code=c&state=s", want: "http://localhost:8088/settings?google=connected&tab=calendar"},
		{name: "empty redirect falls back to root", query: "code=c&state=s", want: "/?google=connected"},
		{name: "ignores redirect params from the request", redirect: "http://localhost:8088/", query: "code=c&state=s&redirect_uri=https://evil.example", want: "http://localhost:8088/?google=connected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calendar := &fakeGoogleCalendar{err: tc.err}

			response := doGoogleRequest(t, calendar, &fakeAuthService{}, tc.redirect, http.MethodGet, googleCallback+"?"+tc.query, nil)

			if response.Code != http.StatusFound {
				t.Fatalf("got %d, want %d: %s", response.Code, http.StatusFound, response.Body)
			}
			if got := response.Header().Get("Location"); got != tc.want {
				t.Fatalf("Location = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGoogleCallbackPassesParams(t *testing.T) {
	calendar := &fakeGoogleCalendar{err: errors.New("google consent failed")}
	query := url.Values{"state": {"s"}, "error": {"access_denied"}}

	response := doGoogleRequest(t, calendar, &fakeAuthService{}, "http://localhost:8088/", http.MethodGet,
		googleCallback+"?"+query.Encode(), nil)

	if response.Code != http.StatusFound {
		t.Fatalf("got %d, want %d", response.Code, http.StatusFound)
	}
	if calendar.code != "" || calendar.state != "s" || calendar.googleErr != "access_denied" {
		t.Fatalf("Callback(%q, %q, %q)", calendar.code, calendar.state, calendar.googleErr)
	}
}

func TestGoogleStatus(t *testing.T) {
	connectedAt := time.Date(2026, 10, 22, 16, 0, 0, 0, time.UTC)
	email := "artem@gmail.com"

	for _, tc := range []struct {
		name        string
		integration *model.GoogleIntegration
		err         error
		want        api.GoogleStatusResponse
	}{
		{
			name:        "connected",
			integration: &model.GoogleIntegration{GoogleEmail: &email, CreatedAt: connectedAt},
			want:        api.GoogleStatusResponse{Connected: true},
		},
		{name: "not connected", err: model.ErrGoogleNotConnected, want: api.GoogleStatusResponse{Connected: false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &fakeAuthService{userID: uuid.New()}
			calendar := &fakeGoogleCalendar{integration: tc.integration, err: tc.err}

			response := doGoogleRequest(t, calendar, auth, "", http.MethodGet, googlePath, bearer("token"))

			if response.Code != http.StatusOK {
				t.Fatalf("got %d, want %d: %s", response.Code, http.StatusOK, response.Body)
			}
			var body api.GoogleStatusResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Connected != tc.want.Connected {
				t.Fatalf("connected = %v, want %v", body.Connected, tc.want.Connected)
			}
			if !tc.want.Connected {
				if body.GoogleEmail != nil || body.ConnectedAt != nil {
					t.Fatalf("not connected status must have no details: %s", response.Body)
				}
				return
			}
			if body.GoogleEmail == nil || string(*body.GoogleEmail) != email ||
				body.ConnectedAt == nil || !body.ConnectedAt.Equal(connectedAt) {
				t.Fatalf("unexpected status: %s", response.Body)
			}
		})
	}
}

func TestGoogleDisconnect(t *testing.T) {
	auth := &fakeAuthService{userID: uuid.New()}
	calendar := &fakeGoogleCalendar{}

	response := doGoogleRequest(t, calendar, auth, "", http.MethodDelete, googlePath, bearer("token"))

	if response.Code != http.StatusNoContent {
		t.Fatalf("got %d, want %d: %s", response.Code, http.StatusNoContent, response.Body)
	}
	if response.Body.Len() != 0 || calendar.userID != auth.userID {
		t.Fatalf("body = %q, user = %s", response.Body, calendar.userID)
	}
}

func (f googleTestServices) ListAllTasks(ctx context.Context, userID uuid.UUID, query string, types []string, from, to *time.Time, limit, offset int) (api.TaskCollection, error) {
	return api.TaskCollection{Items: []api.TaskItem{}}, nil
}
