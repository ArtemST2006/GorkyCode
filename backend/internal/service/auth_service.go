package service

import (
	"context"
	"errors"
	"fmt"
	"gorkycode/backend/internal/model"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/google/uuid"
)

// AuthRepository is the storage operation used by the auth service.
type AuthRepository interface {
	GetUserByEmail(ctx context.Context, email string) (*model.User, error)
	CreateUser(ctx context.Context, user *model.User) error
}

// SessionStore keeps user sessions.
type SessionStore interface {
	Create(ctx context.Context, token string, userID uuid.UUID, ttl time.Duration) error
	Get(ctx context.Context, token string) (uuid.UUID, error)
	Delete(ctx context.Context, token string) error
}

// AuthService authenticates users.
type AuthService struct {
	repo         AuthRepository
	sessions     SessionStore
	queryTimeout time.Duration
	sessionTTL   time.Duration
	dummyHash    string
}

// NewAuthService constructs the auth service.
func NewAuthService(repo AuthRepository, sessions SessionStore, queryTimeout, sessionTTL time.Duration) *AuthService {
	dummyHash, _ := argon2id.CreateHash("any", argon2id.DefaultParams)
	return &AuthService{
		repo:         repo,
		sessions:     sessions,
		queryTimeout: queryTimeout,
		sessionTTL:   sessionTTL,
		dummyHash:    dummyHash,
	}
}

// SignUp registers a user and starts a session for them.
// It returns model.ErrEmailTaken when the email is already registered.
func (s *AuthService) SignUp(ctx context.Context, name, email, password, timezone string) (*model.AuthResult, error) {
	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	user := &model.User{Name: name, Email: email, PasswordHash: hash, Timezone: timezone}
	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	if err = s.repo.CreateUser(queryCtx, user); err != nil {
		if errors.Is(err, model.ErrEmailTaken) {
			return nil, model.ErrEmailTaken
		}
		return nil, fmt.Errorf("create user: %w", err)
	}

	return s.startSession(ctx, user)
}

// SignIn checks the email and password of a user and starts a session for them.
// It returns model.ErrInvalidCredentials for an unknown email or a wrong password.
func (s *AuthService) SignIn(ctx context.Context, email, password string) (*model.AuthResult, error) {
	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	user, err := s.repo.GetUserByEmail(queryCtx, email)
	if err != nil {
		if errors.Is(err, model.ErrUserNotFound) {
			// An unknown email must take as long as a wrong password,
			// otherwise response time reveals which emails are registered.
			_, _ = argon2id.ComparePasswordAndHash(password, s.dummyHash)
			return nil, model.ErrInvalidCredentials
		}
		return nil, fmt.Errorf("get user by email: %w", err)
	}

	match, err := argon2id.ComparePasswordAndHash(password, user.PasswordHash)
	if err != nil {
		return nil, fmt.Errorf("check password: %w", err)
	}
	if !match {
		return nil, model.ErrInvalidCredentials
	}

	return s.startSession(ctx, user)
}

// Authenticate returns the ID of the user who owns the session token.
// It returns model.ErrSessionNotFound when the session does not exist or has expired.
func (s *AuthService) Authenticate(ctx context.Context, token string) (uuid.UUID, error) {
	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	userID, err := s.sessions.Get(queryCtx, token)
	if err != nil {
		if errors.Is(err, model.ErrSessionNotFound) {
			return uuid.Nil, model.ErrSessionNotFound
		}
		return uuid.Nil, fmt.Errorf("get session: %w", err)
	}
	return userID, nil
}

// SignOut ends the session of the token.
func (s *AuthService) SignOut(ctx context.Context, token string) error {
	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	if err := s.sessions.Delete(queryCtx, token); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// startSession issues a new access token for the user and stores the session.
func (s *AuthService) startSession(ctx context.Context, user *model.User) (*model.AuthResult, error) {
	token, err := newSessionToken()
	if err != nil {
		return nil, fmt.Errorf("create session token: %w", err)
	}

	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	if err = s.sessions.Create(queryCtx, token, user.ID, s.sessionTTL); err != nil {
		return nil, fmt.Errorf("store session: %w", err)
	}
	return &model.AuthResult{User: *user, AccessToken: token, ExpiresIn: s.sessionTTL}, nil
}
