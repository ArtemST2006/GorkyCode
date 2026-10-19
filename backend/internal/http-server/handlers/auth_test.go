package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorkycode/backend/internal/model"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	api "gorkycode/backend/internal/generated"
)

// fakeAuthService returns what the test configured and records its arguments.
type fakeAuthService struct {
	result  *model.AuthResult
	err     error
	userID  uuid.UUID
	authErr error

	called   bool
	name     string
	email    string
	password string
	timezone string
	token    string
	signOut  bool
}

var _ Auth = (*fakeAuthService)(nil)

func (f *fakeAuthService) SignUp(_ context.Context, name, email, password, timezone string) (*model.AuthResult, error) {
	f.called = true
	f.name, f.email, f.password, f.timezone = name, email, password, timezone
	return f.result, f.err
}

func (f *fakeAuthService) SignIn(_ context.Context, email, password string) (*model.AuthResult, error) {
	f.called = true
	f.email, f.password = email, password
	return f.result, f.err
}

func (f *fakeAuthService) Authenticate(_ context.Context, token string) (uuid.UUID, error) {
	f.token = token
	return f.userID, f.authErr
}

func (f *fakeAuthService) SignOut(_ context.Context, token string) error {
	f.signOut = true
	f.token = token
	return f.err
}

const (
	signUpPath  = "/api/v1/auth/sign-up"
	signInPath  = "/api/v1/auth/sign-in"
	signOutPath = "/api/v1/auth/sign-out"
	strongPass  = "correct-horse-battery"
)

func newAuthResult() *model.AuthResult {
	return &model.AuthResult{
		User: model.User{
			ID:        uuid.New(),
			Name:      "Артём",
			Email:     "artem@example.com",
			Timezone:  "Europe/Moscow",
			CreatedAt: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
		},
		AccessToken: "token",
		ExpiresIn:   24 * time.Hour,
	}
}

func doRequest(t *testing.T, auth *fakeAuthService, path, body string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	router := newTestRouterWithAuth(nil, auth)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	maps.Copy(request.Header, header)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func assertAuthResponse(t *testing.T, response *httptest.ResponseRecorder, want *model.AuthResult) {
	t.Helper()
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var body api.AuthResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.AccessToken != want.AccessToken || body.TokenType != api.Bearer || body.ExpiresIn != 86400 {
		t.Fatalf("unexpected token fields: %+v", body)
	}
	if body.User.ID != want.User.ID || string(body.User.Email) != want.User.Email || !body.User.CreatedAt.Equal(want.User.CreatedAt) {
		t.Fatalf("unexpected user: %+v", body.User)
	}
	if strings.Contains(response.Body.String(), "password") {
		t.Fatalf("response leaks password fields: %s", response.Body)
	}
}

func TestAuthSignInSuccess(t *testing.T) {
	want := newAuthResult()
	auth := &fakeAuthService{result: want}

	response := doRequest(t, auth, signInPath, `{"email":" artem@example.com ","password":"secret"}`, nil)

	if response.Code != http.StatusOK {
		t.Fatalf("POST sign-in: got %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}
	if auth.email != "artem@example.com" || auth.password != "secret" {
		t.Fatalf("service got email %q, password %q", auth.email, auth.password)
	}
	assertAuthResponse(t, response, want)
}

func TestAuthSignInRejectsInvalidRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "empty body", body: ""},
		{name: "not json", body: "email=a@b.c"},
		{name: "invalid email", body: `{"email":"not-an-email","password":"secret"}`},
		{name: "missing email", body: `{"password":"secret"}`},
		{name: "missing password", body: `{"email":"artem@example.com"}`},
		{name: "unknown field", body: `{"email":"artem@example.com","password":"secret","admin":true}`},
		{name: "trailing data", body: `{"email":"artem@example.com","password":"secret"}{}`},
		{name: "too large", body: fmt.Sprintf(`{"email":"artem@example.com","password":%q}`, strings.Repeat("a", maxAuthBodyBytes))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &fakeAuthService{}
			response := doRequest(t, auth, signInPath, tc.body, nil)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("POST sign-in: got %d, want %d", response.Code, http.StatusBadRequest)
			}
			assertProblemResponse(t, response, "invalid_request")
			if auth.called {
				t.Fatal("service must not be called for an invalid request")
			}
		})
	}
}

func TestAuthSignInMapsServiceErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantCode int
		wantType string
	}{
		{name: "invalid credentials", err: model.ErrInvalidCredentials, wantCode: http.StatusUnauthorized, wantType: "invalid_credentials"},
		{name: "timeout", err: fmt.Errorf("get user: %w", context.DeadlineExceeded), wantCode: http.StatusServiceUnavailable, wantType: "service_unavailable"},
		{name: "unexpected", err: errors.New("database unavailable"), wantCode: http.StatusInternalServerError, wantType: "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := doRequest(t, &fakeAuthService{err: tc.err}, signInPath, `{"email":"artem@example.com","password":"secret"}`, nil)
			if response.Code != tc.wantCode {
				t.Fatalf("POST sign-in: got %d, want %d", response.Code, tc.wantCode)
			}
			assertProblemResponse(t, response, tc.wantType)
		})
	}
}

func signUpBody(name, email, password, timezone string) string {
	return fmt.Sprintf(`{"name":%q,"email":%q,"password":%q,"timezone":%q}`, name, email, password, timezone)
}

func TestAuthSignUpSuccess(t *testing.T) {
	want := newAuthResult()
	auth := &fakeAuthService{result: want}

	response := doRequest(t, auth, signUpPath, signUpBody("  Артём ", "artem@example.com", strongPass, "Europe/Moscow"), nil)

	if response.Code != http.StatusCreated {
		t.Fatalf("POST sign-up: got %d, want %d: %s", response.Code, http.StatusCreated, response.Body)
	}
	if auth.name != "Артём" || auth.email != "artem@example.com" || auth.password != strongPass || auth.timezone != "Europe/Moscow" {
		t.Fatalf("service got name %q, email %q, timezone %q", auth.name, auth.email, auth.timezone)
	}
	assertAuthResponse(t, response, want)
}

func TestAuthSignUpRejectsInvalidRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "not json", body: "abc"},
		{name: "blank name", body: signUpBody("   ", "artem@example.com", strongPass, "Europe/Moscow")},
		{name: "too long name", body: signUpBody(strings.Repeat("я", maxNameLen+1), "artem@example.com", strongPass, "Europe/Moscow")},
		{name: "invalid email", body: signUpBody("Артём", "not-an-email", strongPass, "Europe/Moscow")},
		{name: "missing password", body: `{"name":"Артём","email":"artem@example.com","timezone":"Europe/Moscow"}`},
		{name: "short password", body: signUpBody("Артём", "artem@example.com", "short", "Europe/Moscow")},
		{name: "too long password", body: signUpBody("Артём", "artem@example.com", strings.Repeat("a", maxPasswordLen+1), "Europe/Moscow")},
		{name: "unknown timezone", body: signUpBody("Артём", "artem@example.com", strongPass, "Mars/Olympus")},
		{name: "local timezone", body: signUpBody("Артём", "artem@example.com", strongPass, "Local")},
		{name: "unknown field", body: `{"name":"Артём","email":"artem@example.com","password":"correct-horse-battery","timezone":"Europe/Moscow","admin":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &fakeAuthService{}
			response := doRequest(t, auth, signUpPath, tc.body, nil)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("POST sign-up: got %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body)
			}
			assertProblemResponse(t, response, "invalid_request")
			if auth.called {
				t.Fatal("service must not be called for an invalid request")
			}
		})
	}
}

func TestAuthSignUpMapsServiceErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantCode int
		wantType string
	}{
		{name: "email taken", err: model.ErrEmailTaken, wantCode: http.StatusConflict, wantType: "conflict"},
		{name: "timeout", err: fmt.Errorf("create user: %w", context.DeadlineExceeded), wantCode: http.StatusServiceUnavailable, wantType: "service_unavailable"},
		{name: "unexpected", err: errors.New("database unavailable"), wantCode: http.StatusInternalServerError, wantType: "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := signUpBody("Артём", "artem@example.com", strongPass, "Europe/Moscow")
			response := doRequest(t, &fakeAuthService{err: tc.err}, signUpPath, body, nil)
			if response.Code != tc.wantCode {
				t.Fatalf("POST sign-up: got %d, want %d", response.Code, tc.wantCode)
			}
			assertProblemResponse(t, response, tc.wantType)
		})
	}
}

func bearer(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
}

func TestAuthSignOutSuccess(t *testing.T) {
	auth := &fakeAuthService{userID: uuid.New()}

	response := doRequest(t, auth, signOutPath, "", bearer("token"))

	if response.Code != http.StatusNoContent {
		t.Fatalf("POST sign-out: got %d, want %d: %s", response.Code, http.StatusNoContent, response.Body)
	}
	if !auth.signOut || auth.token != "token" {
		t.Fatalf("session was not ended: signOut=%v token=%q", auth.signOut, auth.token)
	}
	if response.Body.Len() != 0 {
		t.Fatalf("204 must have no body, got %q", response.Body)
	}
}

func TestAuthSignOutRequiresSession(t *testing.T) {
	for _, tc := range []struct {
		name     string
		header   http.Header
		authErr  error
		wantCode int
		wantType string
	}{
		{name: "no header", wantCode: http.StatusUnauthorized, wantType: "unauthorized"},
		{name: "wrong scheme", header: http.Header{"Authorization": {"Basic dXNlcjpwYXNz"}}, wantCode: http.StatusUnauthorized, wantType: "unauthorized"},
		{name: "empty token", header: http.Header{"Authorization": {"Bearer "}}, wantCode: http.StatusUnauthorized, wantType: "unauthorized"},
		{name: "unknown session", header: bearer("expired"), authErr: model.ErrSessionNotFound, wantCode: http.StatusUnauthorized, wantType: "unauthorized"},
		{name: "store failure", header: bearer("token"), authErr: errors.New("redis unavailable"), wantCode: http.StatusInternalServerError, wantType: "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &fakeAuthService{authErr: tc.authErr}
			response := doRequest(t, auth, signOutPath, "", tc.header)
			if response.Code != tc.wantCode {
				t.Fatalf("POST sign-out: got %d, want %d", response.Code, tc.wantCode)
			}
			assertProblemResponse(t, response, tc.wantType)
			if auth.signOut {
				t.Fatal("session must not be ended without authentication")
			}
		})
	}
}

func TestAuthSignOutStoreFailure(t *testing.T) {
	auth := &fakeAuthService{userID: uuid.New(), err: errors.New("redis unavailable")}

	response := doRequest(t, auth, signOutPath, "", bearer("token"))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("POST sign-out: got %d, want %d", response.Code, http.StatusInternalServerError)
	}
	assertProblemResponse(t, response, "internal_error")
}

func TestBearerToken(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   string
		ok     bool
	}{
		{header: "Bearer abc", want: "abc", ok: true},
		{header: "bearer abc", want: "abc", ok: true},
		{header: "Bearer   abc  ", want: "abc", ok: true},
		{header: "", ok: false},
		{header: "Bearer", ok: false},
		{header: "Token abc", ok: false},
	} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, signOutPath, nil)
		request.Header.Set("Authorization", tc.header)
		got, ok := bearerToken(request)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("bearerToken(%q) = %q, %v; want %q, %v", tc.header, got, ok, tc.want, tc.ok)
		}
	}
}
