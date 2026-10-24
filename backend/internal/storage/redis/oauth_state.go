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

// OAuthStateRepository stores one-time OAuth states in Redis.
type OAuthStateRepository struct {
	client *goredis.Client
}

// NewOAuthStateRepository creates an OAuth state repository over a Redis client.
func NewOAuthStateRepository(client *goredis.Client) *OAuthStateRepository {
	return &OAuthStateRepository{client: client}
}

// SaveOAuthState binds the state to the user for the given lifetime.
func (r *OAuthStateRepository) SaveOAuthState(ctx context.Context, state string, userID uuid.UUID, ttl time.Duration) error {
	err := r.client.Set(ctx, oauthStateKey(state), userID.String(), ttl).Err()
	if err != nil {
		return fmt.Errorf("set oauth state: %w", err)
	}
	return nil
}

// TakeOAuthState returns the user ID of the state and deletes it, so a state
// can be used only once. It returns model.ErrOAuthStateNotFound when the state
// does not exist, has expired or was already used.
func (r *OAuthStateRepository) TakeOAuthState(ctx context.Context, state string) (uuid.UUID, error) {
	val, err := r.client.GetDel(ctx, oauthStateKey(state)).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return uuid.Nil, model.ErrOAuthStateNotFound
		}
		return uuid.Nil, fmt.Errorf("take oauth state: %w", err)
	}
	userID, err := uuid.Parse(val)
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse user ID: %w", err)
	}
	return userID, nil
}

func oauthStateKey(state string) string {
	sum := sha256.Sum256([]byte(state))
	return "oauth_state:" + hex.EncodeToString(sum[:])
}
