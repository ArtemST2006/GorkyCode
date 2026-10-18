package service

import (
	"context"
	"errors"
	"fmt"
	"gorkycode/backend/internal/model"
	"time"

	"github.com/alexedwards/argon2id"
)

// AuthRepository is the storage operation used by the auth service.
type AuthRepository interface {
	GetUserByEmail(ctx context.Context, email string) (*model.User, error)
}

// AuthService authenticates users.
type AuthService struct {
	repo         AuthRepository
	queryTimeout time.Duration
	dummyHash    string
}

// NewAuthService constructs the auth service.
func NewAuthService(repo AuthRepository, queryTimeout time.Duration) *AuthService {
	dummyHash, _ := argon2id.CreateHash("any", argon2id.DefaultParams)
	return &AuthService{repo: repo, queryTimeout: queryTimeout, dummyHash: dummyHash}
}

// SignIn checks the email and password of a user.
// It returns model.ErrInvalidCredentials for an unknown email or a wrong password.
func (s *AuthService) SignIn(ctx context.Context, email, password string) (*model.User, error) {
	queryContext, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()
	user, err := s.repo.GetUserByEmail(queryContext, email)
	if err != nil {
		if errors.Is(err, model.ErrUserNotFound) {
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

	return user, nil
}
