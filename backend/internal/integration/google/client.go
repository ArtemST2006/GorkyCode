// Package google is a client for Google OAuth and the Google Calendar API.
//
// Клиент не знает о хранилище и сервисах: токены передаются в методы явно,
// а обновлённый access token сохраняет вызывающий сервис.
package google

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"gorkycode/backend/internal/config"
	"gorkycode/backend/internal/model"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	defaultRevokeURL  = "https://oauth2.googleapis.com/revoke"
	defaultCalendarID = "primary"
	// transparent marks an event as free, so it does not show up in free/busy.
	transparent = "transparent"
)

// requiredScopes are the calendar permissions the user must not uncheck on
// the consent page: without them every Calendar call fails with 403.
var requiredScopes = []string{calendar.CalendarEventsScope, calendar.CalendarEventsFreebusyScope}

// Token is the result of the OAuth code exchange. Expiry is nil when Google
// did not report it; Email is empty when the id_token has no email.
type Token struct {
	AccessToken  string
	RefreshToken string
	Expiry       *time.Time
	Email        string
}

// Credentials is a valid access token for Calendar API calls.
type Credentials struct {
	AccessToken string
	Expiry      *time.Time
}

// Event is a calendar event. TimeZone is an IANA name. For an all-day event
// only the date of Start is used. A reminder is a free event with a popup
// notification at its start.
type Event struct {
	Title    string
	Start    time.Time
	End      time.Time
	TimeZone string
	AllDay   bool
	Reminder bool
}

// Busy is a period when the calendar already has an event.
type Busy struct {
	Start time.Time
	End   time.Time
}

// Client calls Google OAuth and the Google Calendar API.
type Client struct {
	oauth            *oauth2.Config
	httpClient       *http.Client
	calendarEndpoint string
	revokeURL        string
}

// Option changes Client defaults, mainly to point it at test servers.
type Option func(*Client)

// WithHTTPClient sets the HTTP client for OAuth, Calendar and revoke requests.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) { c.httpClient = httpClient }
}

// WithCalendarEndpoint sets the base URL of the Calendar API.
func WithCalendarEndpoint(endpoint string) Option {
	return func(c *Client) { c.calendarEndpoint = endpoint }
}

// WithOAuthEndpoint sets the OAuth consent page and token URLs.
func WithOAuthEndpoint(authURL, tokenURL string) Option {
	return func(c *Client) {
		c.oauth.Endpoint = oauth2.Endpoint{AuthURL: authURL, TokenURL: tokenURL, AuthStyle: oauth2.AuthStyleInParams}
	}
}

// WithRevokeURL sets the token revocation URL.
func WithRevokeURL(revokeURL string) Option {
	return func(c *Client) { c.revokeURL = revokeURL }
}

// New creates a client from the Google settings. The caller checks cfg.Enabled().
func New(cfg config.Google, opts ...Option) *Client {
	c := &Client{
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     googleoauth.Endpoint,
			Scopes:       []string{calendar.CalendarEventsScope, calendar.CalendarEventsFreebusyScope, "openid", "email"},
		},
		httpClient: http.DefaultClient,
		revokeURL:  defaultRevokeURL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// AuthURL returns the Google consent page URL for the state. Offline access
// and the forced consent prompt make Google return a refresh token.
func (c *Client) AuthURL(state string) string {
	return c.oauth.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
}

// Exchange trades the authorization code from the callback for tokens.
// It returns model.ErrGoogleScopeMissing when the user unchecked a calendar
// permission on the consent page.
func (c *Client) Exchange(ctx context.Context, code string) (*Token, error) {
	tok, err := c.oauth.Exchange(c.oauthContext(ctx), code)
	if err != nil {
		return nil, wrapOAuthError("exchange google code", err)
	}
	if missing := missingScopes(tok); len(missing) > 0 {
		return nil, fmt.Errorf("%w: %s", model.ErrGoogleScopeMissing, strings.Join(missing, " "))
	}
	return &Token{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		Expiry:       expiryPtr(tok.Expiry),
		Email:        idTokenEmail(tok),
	}, nil
}

// Token returns a valid access token for the stored integration, refreshing
// it when it has expired. refreshed is true when the access token changed and
// the caller should save it.
func (c *Client) Token(ctx context.Context, integ *model.GoogleIntegration) (Credentials, bool, error) {
	stored := &oauth2.Token{
		AccessToken:  integ.AccessToken,
		RefreshToken: integ.RefreshToken,
		TokenType:    "Bearer",
	}
	if integ.TokenExpiry != nil {
		stored.Expiry = *integ.TokenExpiry
	}

	tok, err := c.oauth.TokenSource(c.oauthContext(ctx), stored).Token()
	if err != nil {
		return Credentials{}, false, wrapOAuthError("refresh google token", err)
	}
	creds := Credentials{AccessToken: tok.AccessToken, Expiry: expiryPtr(tok.Expiry)}
	return creds, tok.AccessToken != integ.AccessToken, nil
}

// CreateEvent creates the event in the calendar and returns its Google event ID.
// An empty calendarID means the user's primary calendar.
func (c *Client) CreateEvent(ctx context.Context, creds Credentials, calendarID string, ev Event) (string, error) {
	srv, err := c.calendarService(ctx, creds)
	if err != nil {
		return "", err
	}
	created, err := srv.Events.Insert(calendarOrPrimary(calendarID), toCalendarEvent(ev)).Context(ctx).Do()
	if err != nil {
		return "", wrapAPIError("create google event", err)
	}
	return created.Id, nil
}

// UpdateEvent replaces the title and time of the event.
func (c *Client) UpdateEvent(ctx context.Context, creds Credentials, calendarID, eventID string, ev Event) error {
	srv, err := c.calendarService(ctx, creds)
	if err != nil {
		return err
	}
	_, err = srv.Events.Patch(calendarOrPrimary(calendarID), eventID, toCalendarEvent(ev)).Context(ctx).Do()
	if err != nil {
		return wrapAPIError("update google event", err)
	}
	return nil
}

// DeleteEvent deletes the event. An event that is already deleted is not an error.
func (c *Client) DeleteEvent(ctx context.Context, creds Credentials, calendarID, eventID string) error {
	srv, err := c.calendarService(ctx, creds)
	if err != nil {
		return err
	}
	err = srv.Events.Delete(calendarOrPrimary(calendarID), eventID).Context(ctx).Do()
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) && (apiErr.Code == http.StatusNotFound || apiErr.Code == http.StatusGone) {
		return nil
	}
	if err != nil {
		return wrapAPIError("delete google event", err)
	}
	return nil
}

// FreeBusy returns the busy periods of the calendar between start and end.
// An empty calendarID means the user's primary calendar.
func (c *Client) FreeBusy(ctx context.Context, creds Credentials, calendarID string, start, end time.Time) ([]Busy, error) {
	srv, err := c.calendarService(ctx, creds)
	if err != nil {
		return nil, err
	}
	calendarID = calendarOrPrimary(calendarID)
	resp, err := srv.Freebusy.Query(&calendar.FreeBusyRequest{
		TimeMin: start.Format(time.RFC3339),
		TimeMax: end.Format(time.RFC3339),
		Items:   []*calendar.FreeBusyRequestItem{{Id: calendarID}},
	}).Context(ctx).Do()
	if err != nil {
		return nil, wrapAPIError("query google free/busy", err)
	}

	cal, ok := resp.Calendars[calendarID]
	if !ok {
		return nil, fmt.Errorf("query google free/busy: calendar %q missing in response", calendarID)
	}
	if len(cal.Errors) > 0 {
		return nil, fmt.Errorf("query google free/busy: calendar %q: %s", calendarID, cal.Errors[0].Reason)
	}
	busy := make([]Busy, 0, len(cal.Busy))
	for _, period := range cal.Busy {
		from, err := time.Parse(time.RFC3339, period.Start)
		if err != nil {
			return nil, fmt.Errorf("parse google busy start: %w", err)
		}
		to, err := time.Parse(time.RFC3339, period.End)
		if err != nil {
			return nil, fmt.Errorf("parse google busy end: %w", err)
		}
		busy = append(busy, Busy{Start: from, End: to})
	}
	return busy, nil
}

// Revoke revokes the token at Google so the app loses access to the calendar.
// Google answers 400 for a token that is already invalid; that is not an error.
func (c *Client) Revoke(ctx context.Context, token string) error {
	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.revokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build google revoke request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("revoke google token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("revoke google token: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// oauthContext makes the oauth2 package use the client's HTTP client.
func (c *Client) oauthContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, c.httpClient)
}

func (c *Client) calendarService(ctx context.Context, creds Credentials) (*calendar.Service, error) {
	tok := &oauth2.Token{AccessToken: creds.AccessToken, TokenType: "Bearer"}
	opts := []option.ClientOption{
		option.WithHTTPClient(oauth2.NewClient(c.oauthContext(ctx), oauth2.StaticTokenSource(tok))),
	}
	if c.calendarEndpoint != "" {
		opts = append(opts, option.WithEndpoint(c.calendarEndpoint))
	}
	srv, err := calendar.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create google calendar service: %w", err)
	}
	return srv, nil
}

func calendarOrPrimary(calendarID string) string {
	if calendarID == "" {
		return defaultCalendarID
	}
	return calendarID
}

func toCalendarEvent(ev Event) *calendar.Event {
	if ev.AllDay {
		// Google treats the end date of an all-day event as exclusive. The
		// event is free: otherwise free/busy reports the whole day as taken.
		return &calendar.Event{
			Summary:      ev.Title,
			Transparency: transparent,
			Start:        &calendar.EventDateTime{Date: ev.Start.Format(time.DateOnly), TimeZone: ev.TimeZone},
			End:          &calendar.EventDateTime{Date: ev.Start.AddDate(0, 0, 1).Format(time.DateOnly), TimeZone: ev.TimeZone},
		}
	}
	event := &calendar.Event{
		Summary: ev.Title,
		Start:   &calendar.EventDateTime{DateTime: ev.Start.Format(time.RFC3339), TimeZone: ev.TimeZone},
		End:     &calendar.EventDateTime{DateTime: ev.End.Format(time.RFC3339), TimeZone: ev.TimeZone},
	}
	if ev.Reminder {
		// The reminder must not take the slot, and it notifies at its start
		// instead of the calendar's default notifications. UseDefault=false
		// is forced because the JSON encoder drops false values.
		event.Transparency = transparent
		event.Reminders = &calendar.EventReminders{
			Overrides:       []*calendar.EventReminder{{Method: "popup", Minutes: 0, ForceSendFields: []string{"Minutes"}}},
			ForceSendFields: []string{"UseDefault"},
		}
	}
	return event
}

// missingScopes lists the required scopes absent from the granted ones. Google
// always reports the granted scopes; a token without them is trusted as is.
func missingScopes(tok *oauth2.Token) []string {
	raw, _ := tok.Extra("scope").(string)
	if raw == "" {
		return nil
	}
	granted := make(map[string]bool)
	for scope := range strings.FieldsSeq(raw) {
		granted[scope] = true
	}
	var missing []string
	for _, scope := range requiredScopes {
		if !granted[scope] {
			missing = append(missing, scope)
		}
	}
	return missing
}

// idTokenEmail reads the email claim of the id_token without checking its
// signature: the token came straight from Google's token endpoint over TLS,
// so it is trusted. A missing id_token or email gives an empty string.
func idTokenEmail(tok *oauth2.Token) string {
	idToken, ok := tok.Extra("id_token").(string)
	if !ok {
		return ""
	}
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err = json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.Email
}

func expiryPtr(expiry time.Time) *time.Time {
	if expiry.IsZero() {
		return nil
	}
	return &expiry
}

// wrapOAuthError marks a refresh token or code that Google no longer accepts,
// for example after the 7-day limit of an app in testing mode.
func wrapOAuthError(op string, err error) error {
	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) && retrieveErr.ErrorCode == "invalid_grant" {
		return fmt.Errorf("%w: %s: %w", model.ErrGoogleTokenRevoked, op, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}

func wrapAPIError(op string, err error) error {
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) && apiErr.Code == http.StatusUnauthorized {
		return fmt.Errorf("%w: %s: %w", model.ErrGoogleTokenRevoked, op, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}
