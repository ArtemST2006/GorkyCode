package google

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"gorkycode/backend/internal/config"
	"gorkycode/backend/internal/model"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	testAccessToken  = "test-access-token"
	testRefreshToken = "test-refresh-token"
)

func testConfig() config.Google {
	return config.Google{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  "http://localhost:8088/api/v1/integrations/google/callback",
	}
}

// newTestClient points every Google URL of the client at one test server.
func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return New(testConfig(),
		WithHTTPClient(server.Client()),
		WithOAuthEndpoint(server.URL+"/auth", server.URL+"/token"),
		WithCalendarEndpoint(server.URL+"/calendar/"),
		WithRevokeURL(server.URL+"/revoke"),
	)
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Error(err)
	}
}

// fakeIDToken builds an unsigned JWT with the given claims.
func fakeIDToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString(payload) + ".sig"
}

func assertNoTokens(t *testing.T, err error) {
	t.Helper()
	for _, secret := range []string{testAccessToken, testRefreshToken, "client-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks a secret: %v", err)
		}
	}
}

func TestAuthURL(t *testing.T) {
	client := New(testConfig())

	raw := client.AuthURL("state-123")

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	for key, want := range map[string]string{
		"state":         "state-123",
		"access_type":   "offline",
		"prompt":        "consent",
		"client_id":     "client-id",
		"redirect_uri":  testConfig().RedirectURL,
		"response_type": "code",
	} {
		if got := query.Get(key); got != want {
			t.Fatalf("%s = %q, want %q (url %s)", key, got, want, raw)
		}
	}
	scopes := strings.Fields(query.Get("scope"))
	for _, want := range []string{"https://www.googleapis.com/auth/calendar.events", "https://www.googleapis.com/auth/calendar.events.freebusy", "openid", "email"} {
		if !strings.Contains(" "+strings.Join(scopes, " ")+" ", " "+want+" ") {
			t.Fatalf("scope %q missing in %v", want, scopes)
		}
	}
}

func TestExchange(t *testing.T) {
	for _, tc := range []struct {
		name      string
		idToken   string
		wantEmail string
	}{
		{name: "email from id_token", idToken: "with-email", wantEmail: "artem@gmail.com"},
		{name: "no id_token", wantEmail: ""},
		{name: "malformed id_token", idToken: "not-a-jwt", wantEmail: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/token" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.PostForm.Get("code") != "auth-code" || r.PostForm.Get("grant_type") != "authorization_code" {
					t.Errorf("unexpected token form: %v", r.PostForm)
				}
				body := map[string]any{
					"access_token":  testAccessToken,
					"refresh_token": testRefreshToken,
					"token_type":    "Bearer",
					"expires_in":    3600,
				}
				switch tc.idToken {
				case "with-email":
					body["id_token"] = fakeIDToken(t, map[string]any{"email": tc.wantEmail})
				case "":
				default:
					body["id_token"] = tc.idToken
				}
				writeJSON(t, w, http.StatusOK, body)
			}))

			tok, err := client.Exchange(t.Context(), "auth-code")
			if err != nil {
				t.Fatal(err)
			}
			if tok.AccessToken != testAccessToken || tok.RefreshToken != testRefreshToken {
				t.Fatalf("unexpected tokens: %+v", tok)
			}
			if tok.Expiry == nil || time.Until(*tok.Expiry) <= 0 {
				t.Fatalf("expiry = %v, want a future time", tok.Expiry)
			}
			if tok.Email != tc.wantEmail {
				t.Fatalf("email = %q, want %q", tok.Email, tc.wantEmail)
			}
		})
	}
}

func TestExchangeInvalidGrant(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
	}))

	_, err := client.Exchange(t.Context(), "used-code")

	if !errors.Is(err, model.ErrGoogleTokenRevoked) {
		t.Fatalf("Exchange() = %v, want ErrGoogleTokenRevoked", err)
	}
}

func TestTokenRefreshesExpiredToken(t *testing.T) {
	var calls int
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != testRefreshToken {
			t.Errorf("unexpected refresh form: %v", r.PostForm)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"access_token": "new-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	expired := time.Now().Add(-time.Hour)

	creds, refreshed, err := client.Token(t.Context(), &model.GoogleIntegration{
		AccessToken:  testAccessToken,
		RefreshToken: testRefreshToken,
		TokenExpiry:  &expired,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !refreshed || creds.AccessToken != "new-access-token" || calls != 1 {
		t.Fatalf("Token() = %+v, refreshed=%v, calls=%d", creds, refreshed, calls)
	}
	if creds.Expiry == nil || time.Until(*creds.Expiry) <= 0 {
		t.Fatalf("expiry = %v, want a future time", creds.Expiry)
	}
}

func TestTokenKeepsValidToken(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	valid := time.Now().Add(time.Hour)

	creds, refreshed, err := client.Token(t.Context(), &model.GoogleIntegration{
		AccessToken:  testAccessToken,
		RefreshToken: testRefreshToken,
		TokenExpiry:  &valid,
	})
	if err != nil {
		t.Fatal(err)
	}
	if refreshed || creds.AccessToken != testAccessToken {
		t.Fatalf("Token() = %+v, refreshed=%v; want stored token", creds, refreshed)
	}
}

func TestTokenRevokedRefreshToken(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]string{
			"error":             "invalid_grant",
			"error_description": "Token has been expired or revoked.",
		})
	}))
	expired := time.Now().Add(-time.Hour)

	_, _, err := client.Token(t.Context(), &model.GoogleIntegration{
		AccessToken:  testAccessToken,
		RefreshToken: testRefreshToken,
		TokenExpiry:  &expired,
	})

	if !errors.Is(err, model.ErrGoogleTokenRevoked) {
		t.Fatalf("Token() = %v, want ErrGoogleTokenRevoked", err)
	}
	assertNoTokens(t, err)
}

func TestCreateEvent(t *testing.T) {
	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 21, 15, 0, 0, 0, moscow)

	for _, tc := range []struct {
		name       string
		calendarID string
		event      Event
		wantPath   string
		wantStart  map[string]string
		wantEnd    map[string]string
	}{
		{
			name:      "timed event in primary calendar",
			event:     Event{Title: "Встреча с Иваном", Start: start, End: start.Add(time.Hour), TimeZone: "Europe/Moscow"},
			wantPath:  "/calendar/calendars/primary/events",
			wantStart: map[string]string{"dateTime": "2026-10-21T15:00:00+03:00", "timeZone": "Europe/Moscow"},
			wantEnd:   map[string]string{"dateTime": "2026-10-21T16:00:00+03:00", "timeZone": "Europe/Moscow"},
		},
		{
			name:       "all-day event ends next day",
			calendarID: "team@group.calendar.google.com",
			event:      Event{Title: "Встреча с Иваном", Start: start, TimeZone: "Europe/Moscow", AllDay: true},
			wantPath:   "/calendar/calendars/team@group.calendar.google.com/events",
			wantStart:  map[string]string{"date": "2026-10-21", "timeZone": "Europe/Moscow"},
			wantEnd:    map[string]string{"date": "2026-10-22", "timeZone": "Europe/Moscow"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != tc.wantPath {
					t.Errorf("request = %s %s, want POST %s", r.Method, r.URL.Path, tc.wantPath)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer "+testAccessToken {
					t.Errorf("Authorization = %q", got)
				}
				var body struct {
					Summary string            `json:"summary"`
					Start   map[string]string `json:"start"`
					End     map[string]string `json:"end"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Summary != tc.event.Title || !equalMaps(body.Start, tc.wantStart) || !equalMaps(body.End, tc.wantEnd) {
					t.Errorf("event body = %+v", body)
				}
				writeJSON(t, w, http.StatusOK, map[string]string{"id": "event-1"})
			}))

			id, err := client.CreateEvent(t.Context(), Credentials{AccessToken: testAccessToken}, tc.calendarID, tc.event)
			if err != nil {
				t.Fatal(err)
			}
			if id != "event-1" {
				t.Fatalf("event id = %q, want event-1", id)
			}
		})
	}
}

func TestCalendarUnauthorizedIsRevoked(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusUnauthorized, map[string]any{
			"error": map[string]any{"code": 401, "message": "Invalid Credentials"},
		})
	}))
	creds := Credentials{AccessToken: testAccessToken}
	event := Event{Title: "Встреча", Start: time.Now(), End: time.Now().Add(time.Hour), TimeZone: "UTC"}

	_, createErr := client.CreateEvent(t.Context(), creds, "", event)
	updateErr := client.UpdateEvent(t.Context(), creds, "", "event-1", event)
	deleteErr := client.DeleteEvent(t.Context(), creds, "", "event-1")

	for name, err := range map[string]error{"CreateEvent": createErr, "UpdateEvent": updateErr, "DeleteEvent": deleteErr} {
		if !errors.Is(err, model.ErrGoogleTokenRevoked) {
			t.Fatalf("%s() = %v, want ErrGoogleTokenRevoked", name, err)
		}
		assertNoTokens(t, err)
	}
}

func TestUpdateEventPatchesEvent(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/calendar/calendars/primary/events/event-1" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, map[string]string{"id": "event-1"})
	}))
	start := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

	err := client.UpdateEvent(t.Context(), Credentials{AccessToken: testAccessToken}, "", "event-1",
		Event{Title: "Перенесено", Start: start, End: start.Add(time.Hour), TimeZone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeleteEvent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "deleted", status: http.StatusNoContent},
		{name: "not found", status: http.StatusNotFound},
		{name: "already deleted", status: http.StatusGone},
		{name: "server error", status: http.StatusInternalServerError, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete || r.URL.Path != "/calendar/calendars/primary/events/event-1" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
			}))

			err := client.DeleteEvent(t.Context(), Credentials{AccessToken: testAccessToken}, "", "event-1")

			if (err != nil) != tc.wantErr {
				t.Fatalf("DeleteEvent() = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestRevoke(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "revoked", status: http.StatusOK},
		{name: "already invalid", status: http.StatusBadRequest},
		{name: "server error", status: http.StatusInternalServerError, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/revoke" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				if r.URL.RawQuery != "" {
					t.Errorf("token must not be sent in the URL: %s", r.URL.RawQuery)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if string(body) != "token="+testRefreshToken {
					t.Errorf("revoke body = %q", body)
				}
				w.WriteHeader(tc.status)
			}))

			err := client.Revoke(t.Context(), testRefreshToken)

			if (err != nil) != tc.wantErr {
				t.Fatalf("Revoke() = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil {
				assertNoTokens(t, err)
			}
		})
	}
}

func TestFreeBusy(t *testing.T) {
	start := time.Date(2026, 10, 21, 15, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	for _, tc := range []struct {
		name     string
		calendar map[string]any
		wantBusy int
		wantErr  bool
	}{
		{name: "busy", calendar: map[string]any{"busy": []map[string]string{{"start": "2026-10-21T15:30:00Z", "end": "2026-10-21T16:30:00Z"}}}, wantBusy: 1},
		{name: "free", calendar: map[string]any{"busy": []any{}}},
		{name: "calendar error", calendar: map[string]any{"errors": []map[string]string{{"domain": "global", "reason": "notFound"}}}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/calendar/freeBusy" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				var body struct {
					TimeMin string              `json:"timeMin"`
					TimeMax string              `json:"timeMax"`
					Items   []map[string]string `json:"items"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.TimeMin != "2026-10-21T15:00:00Z" || body.TimeMax != "2026-10-21T16:00:00Z" ||
					len(body.Items) != 1 || body.Items[0]["id"] != "primary" {
					t.Errorf("freeBusy body = %+v", body)
				}
				writeJSON(t, w, http.StatusOK, map[string]any{"calendars": map[string]any{"primary": tc.calendar}})
			}))

			busy, err := client.FreeBusy(t.Context(), Credentials{AccessToken: testAccessToken}, "", start, end)

			if (err != nil) != tc.wantErr {
				t.Fatalf("FreeBusy() error = %v, wantErr %v", err, tc.wantErr)
			}
			if len(busy) != tc.wantBusy {
				t.Fatalf("FreeBusy() = %+v, want %d periods", busy, tc.wantBusy)
			}
			if tc.wantBusy > 0 && !busy[0].Start.Equal(time.Date(2026, 10, 21, 15, 30, 0, 0, time.UTC)) {
				t.Fatalf("busy start = %s", busy[0].Start)
			}
		})
	}
}

func TestFreeBusyUnauthorizedIsRevoked(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": 401, "message": "Invalid Credentials"}})
	}))

	_, err := client.FreeBusy(t.Context(), Credentials{AccessToken: testAccessToken}, "", time.Now(), time.Now().Add(time.Hour))

	if !errors.Is(err, model.ErrGoogleTokenRevoked) {
		t.Fatalf("FreeBusy() = %v, want ErrGoogleTokenRevoked", err)
	}
	assertNoTokens(t, err)
}

func equalMaps(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}
