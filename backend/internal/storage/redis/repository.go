// Package redis stores user sessions in Redis.
package redis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"gorkycode/backend/internal/model"
	"time"

	"github.com/google/uuid"

	goredis "github.com/redis/go-redis/v9"
)

// SessionRepository stores user sessions in Redis.
type SessionRepository struct {
	client *goredis.Client
}

// NewSessionRepository creates a session repository over a Redis client.
func NewSessionRepository(client *goredis.Client) *SessionRepository {
	return &SessionRepository{client: client}
}

// Create stores the session for the token with the given lifetime.
func (r *SessionRepository) Create(ctx context.Context, token string, userID uuid.UUID, ttl time.Duration) error {
	err := r.client.Set(ctx, sessionKey(token), userID.String(), ttl).Err()
	if err != nil {
		return fmt.Errorf("set session: %w", err)
	}
	return nil
}

// Get returns the user ID of the session.
// It returns model.ErrSessionNotFound when the session does not exist or has expired.
func (r *SessionRepository) Get(ctx context.Context, token string) (uuid.UUID, error) {
	val, err := r.client.Get(ctx, sessionKey(token)).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return uuid.Nil, model.ErrSessionNotFound
		}
		return uuid.Nil, fmt.Errorf("get session: %w", err)
	}
	userID, err := uuid.Parse(val)
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse user ID: %w", err)
	}
	return userID, nil
}

// Delete removes the session. Deleting a missing session is not an error.
func (r *SessionRepository) Delete(ctx context.Context, token string) error {
	err := r.client.Del(ctx, sessionKey(token)).Err()
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func sessionKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "session:" + hex.EncodeToString(sum[:])
}
