package service

import (
	"context"
	"fmt"
	"time"
)

// AuthRepository is the storage operation used by the example auth service.
//
// 4) аналогично прописываем интерфейс для репозитория
type AuthRepository interface {
	SignIn(context.Context) (bool, error)
}

// AuthService handles the example sign-in operation.
type AuthService struct {
	repo         AuthRepository
	queryTimeout time.Duration
}

// NewAuthService constructs the example auth service.
func NewAuthService(repo AuthRepository, queryTimeout time.Duration) *AuthService {
	return &AuthService{repo: repo, queryTimeout: queryTimeout}
}

// SignIn runs a demonstration query without authenticating a user.
//
// 5) прописываем сервис с доменной логикой.
func (s *AuthService) SignIn(ctx context.Context) (bool, error) {
	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	reached, err := s.repo.SignIn(queryCtx)
	if err != nil {
		return false, fmt.Errorf("run example sign-in query: %w", err)
	}
	return reached, nil
}
