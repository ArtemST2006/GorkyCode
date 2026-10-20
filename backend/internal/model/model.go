// Package model contains domain entities and errors shared between layers.
package model

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
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
	// ErrBadGateway marks an invalid response from STT or AI-service.
	ErrBadGateway = errors.New("upstream service failure")
	// ErrGatewayTimeout marks an upstream timeout.
	ErrGatewayTimeout = errors.New("upstream service timeout")
	// ErrServiceUnavailable marks an unavailable upstream service.
	ErrServiceUnavailable = errors.New("upstream service unavailable")
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
	Action  json.RawMessage `json:"action"`
	Context json.RawMessage `json:"context"`
	Intent  string          `json:"intent"`
	Message json.RawMessage `json:"message"`
	Status  string          `json:"status"`
	TaskId  uuid.UUID       `json:"task_id"`
	UserId  uuid.UUID       `json:"user_id"`
}
