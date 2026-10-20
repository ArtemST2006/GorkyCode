// Package model contains domain entities and errors shared between layers.
package model

import (
	"encoding/json"
	"errors"
	"gorkycode/backend/internal/generated/ai"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

var (
	// ErrUserNotFound is returned by storage when no user matches the query.
	ErrUserNotFound = errors.New("user not found")
	// ErrInvalidCredentials is returned when the email or the password is wrong.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrSessionNotFound is returned when a session token does not exist or has expired.
	ErrSessionNotFound = errors.New("session not found")
	// ErrDraftNotFound is returned when a task draft does not exist or has expired.
	ErrDraftNotFound = errors.New("draft not found")
	// ErrEmailTaken is returned when a user with the same email already exists.
	ErrEmailTaken = errors.New("email already taken")
)

// User is a registered user.
type User struct {
	ID           uuid.UUID
	Name         string
	Email        string
	PasswordHash string
	Timezone     string
	CreatedAt    time.Time
}

// AuthResult is a signed-in user with the access token of the new session.
type AuthResult struct {
	User        User
	AccessToken string
	ExpiresIn   time.Duration
}

type AiRes struct {
	Action  json.RawMessage           `json:"action"`
	Context ai.ReadyContext           `json:"context"`
	Intent  ai.ReadyCreateEventIntent `json:"intent"`

	// Message Финальное сообщение формирует backend после выполнения action.
	Message *ai.ReadyCreateEventMessage `json:"message"`
	Status  ai.ReadyCreateEventStatus   `json:"status"`
	TaskId  openapi_types.UUID          `json:"task_id"`
	UserId  openapi_types.UUID          `json:"user_id"`
}
