package service

import (
	"context"
	"encoding/base64"
	"errors"
	"gorkycode/backend/internal/model"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/google/uuid"
)

// testHashParams keeps argon2id fast in tests; production uses DefaultParams.
var testHashParams = &argon2id.Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

const testSessionTTL = time.Hour

type fakeAuthRepository struct {
	user      *model.User
	err       error
	createErr error

	created     *model.User
	hasDeadline bool
}

func (r *fakeAuthRepository) GetUserByEmail(ctx context.Context, _ string) (*model.User, error) {
	_, r.hasDeadline = ctx.Deadline()
	return r.user, r.err
}

func (r *fakeAuthRepository) CreateUser(ctx context.Context, user *model.User) error {
	_, r.hasDeadline = ctx.Deadline()
	if r.createErr != nil {
		return r.createErr
	}
	user.ID = uuid.New()
	user.CreatedAt = time.Now()
	r.created = user
	return nil
}

// fakeSessionStore keeps sessions in a map, like Redis without expiry.
type fakeSessionStore struct {
	sessions map[string]uuid.UUID
	ttl      time.Duration
	err      error

	hasDeadline bool
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{sessions: map[string]uuid.UUID{}}
}

func (s *fakeSessionStore) Create(ctx context.Context, token string, userID uuid.UUID, ttl time.Duration) error {
	_, s.hasDeadline = ctx.Deadline()
	if s.err != nil {
		return s.err
	}
	s.sessions[token] = userID
	s.ttl = ttl
	return nil
}

func (s *fakeSessionStore) Get(ctx context.Context, token string) (uuid.UUID, error) {
	_, s.hasDeadline = ctx.Deadline()
	if s.err != nil {
		return uuid.Nil, s.err
	}
	userID, ok := s.sessions[token]
	if !ok {
		return uuid.Nil, model.ErrSessionNotFound
	}
	return userID, nil
}

func (s *fakeSessionStore) Delete(ctx context.Context, token string) error {
	_, s.hasDeadline = ctx.Deadline()
	if s.err != nil {
		return s.err
	}
	delete(s.sessions, token)
	return nil
}

func newTestAuthService(repo AuthRepository, sessions SessionStore) *AuthService {
	return NewAuthService(repo, sessions, time.Second, testSessionTTL)
}

func newTestUser(t *testing.T, password string) *model.User {
	t.Helper()
	hash, err := argon2id.CreateHash(password, testHashParams)
	if err != nil {
		t.Fatal(err)
	}
	return &model.User{ID: uuid.New(), Email: "artem@example.com", PasswordHash: hash}
}

func TestSignInStartsSession(t *testing.T) {
	user := newTestUser(t, "correct password")
	repo := &fakeAuthRepository{user: user}
	sessions := newFakeSessionStore()

	result, err := newTestAuthService(repo, sessions).SignIn(t.Context(), "artem@example.com", "correct password")
	if err != nil {
		t.Fatalf("SignIn() error: %v", err)
	}
	if !repo.hasDeadline || !sessions.hasDeadline {
		t.Fatal("SignIn() must call storage with a query deadline")
	}
	if result.User.ID != user.ID || result.ExpiresIn != testSessionTTL || result.AccessToken == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if sessions.sessions[result.AccessToken] != user.ID || sessions.ttl != testSessionTTL {
		t.Fatal("session for the returned token was not stored")
	}
}

func TestSignInHidesWhetherEmailExists(t *testing.T) {
	for _, tc := range []struct {
		name string
		repo *fakeAuthRepository
	}{
		{name: "unknown email", repo: &fakeAuthRepository{err: model.ErrUserNotFound}},
		{name: "wrong password", repo: &fakeAuthRepository{user: newTestUser(t, "correct password")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessions := newFakeSessionStore()
			_, err := newTestAuthService(tc.repo, sessions).SignIn(t.Context(), "artem@example.com", "wrong password")
			if !errors.Is(err, model.ErrInvalidCredentials) {
				t.Fatalf("SignIn() error = %v, want %v", err, model.ErrInvalidCredentials)
			}
			if len(sessions.sessions) != 0 {
				t.Fatal("session must not be started")
			}
		})
	}
}

func TestSignInWrapsUnexpectedErrors(t *testing.T) {
	dbErr := errors.New("database unavailable")
	storeErr := errors.New("redis unavailable")
	for _, tc := range []struct {
		name     string
		repo     *fakeAuthRepository
		sessions *fakeSessionStore
		wantErr  error
	}{
		{name: "repository", repo: &fakeAuthRepository{err: dbErr}, sessions: newFakeSessionStore(), wantErr: dbErr},
		{name: "session store", repo: &fakeAuthRepository{user: newTestUser(t, "pw")}, sessions: &fakeSessionStore{err: storeErr}, wantErr: storeErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newTestAuthService(tc.repo, tc.sessions).SignIn(t.Context(), "artem@example.com", "pw")
			if !errors.Is(err, tc.wantErr) || errors.Is(err, model.ErrInvalidCredentials) {
				t.Fatalf("SignIn() error = %v, want wrapped %v", err, tc.wantErr)
			}
		})
	}
}

func TestSignUpCreatesUserAndSession(t *testing.T) {
	repo := &fakeAuthRepository{}
	sessions := newFakeSessionStore()

	result, err := newTestAuthService(repo, sessions).SignUp(t.Context(), "Артём", "artem@example.com", "correct-horse-battery", "Europe/Moscow")
	if err != nil {
		t.Fatalf("SignUp() error: %v", err)
	}
	if repo.created == nil || repo.created.Name != "Артём" || repo.created.Timezone != "Europe/Moscow" {
		t.Fatalf("unexpected created user: %+v", repo.created)
	}
	if repo.created.PasswordHash == "correct-horse-battery" {
		t.Fatal("password must be stored as a hash")
	}
	match, err := argon2id.ComparePasswordAndHash("correct-horse-battery", repo.created.PasswordHash)
	if err != nil || !match {
		t.Fatalf("stored hash does not match the password: %v", err)
	}
	if result.User.ID != repo.created.ID || sessions.sessions[result.AccessToken] != repo.created.ID {
		t.Fatal("session must be started for the created user")
	}
}

func TestSignUpReportsTakenEmail(t *testing.T) {
	sessions := newFakeSessionStore()
	_, err := newTestAuthService(&fakeAuthRepository{createErr: model.ErrEmailTaken}, sessions).
		SignUp(t.Context(), "Артём", "artem@example.com", "correct-horse-battery", "Europe/Moscow")
	if !errors.Is(err, model.ErrEmailTaken) {
		t.Fatalf("SignUp() error = %v, want %v", err, model.ErrEmailTaken)
	}
	if len(sessions.sessions) != 0 {
		t.Fatal("session must not be started")
	}
}

func TestAuthenticateAndSignOut(t *testing.T) {
	userID := uuid.New()
	sessions := newFakeSessionStore()
	sessions.sessions["token"] = userID
	svc := newTestAuthService(&fakeAuthRepository{}, sessions)

	got, err := svc.Authenticate(t.Context(), "token")
	if err != nil || got != userID {
		t.Fatalf("Authenticate() = %s, %v; want %s", got, err, userID)
	}
	if err = svc.SignOut(t.Context(), "token"); err != nil {
		t.Fatalf("SignOut() error: %v", err)
	}
	if _, err = svc.Authenticate(t.Context(), "token"); !errors.Is(err, model.ErrSessionNotFound) {
		t.Fatalf("Authenticate() after SignOut error = %v, want %v", err, model.ErrSessionNotFound)
	}
}

func TestAuthenticateWrapsStoreErrors(t *testing.T) {
	storeErr := errors.New("redis unavailable")
	svc := newTestAuthService(&fakeAuthRepository{}, &fakeSessionStore{err: storeErr})

	if _, err := svc.Authenticate(t.Context(), "token"); !errors.Is(err, storeErr) || errors.Is(err, model.ErrSessionNotFound) {
		t.Fatalf("Authenticate() error = %v, want wrapped %v", err, storeErr)
	}
	if err := svc.SignOut(t.Context(), "token"); !errors.Is(err, storeErr) {
		t.Fatalf("SignOut() error = %v, want wrapped %v", err, storeErr)
	}
}

func TestNewSessionTokenIsRandomURLSafeString(t *testing.T) {
	first, err := newSessionToken()
	if err != nil {
		t.Fatalf("newSessionToken() error: %v", err)
	}
	second, err := newSessionToken()
	if err != nil {
		t.Fatalf("newSessionToken() error: %v", err)
	}

	if first == second {
		t.Fatal("two tokens must differ")
	}
	if want := base64.RawURLEncoding.EncodedLen(sessionTokenBytes); len(first) != want {
		t.Fatalf("token length = %d, want %d", len(first), want)
	}
	if _, err := base64.RawURLEncoding.DecodeString(first); err != nil {
		t.Fatalf("token is not URL-safe base64: %v", err)
	}
}
