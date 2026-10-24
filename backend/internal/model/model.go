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
	// ErrInvalidAction marks an invalid action returned by the AI service.
	ErrInvalidAction = errors.New("invalid action")
	// ErrGoogleNotConnected is returned when the user has not connected Google Calendar.
	ErrGoogleNotConnected = errors.New("google calendar not connected")
	// ErrOAuthStateNotFound is returned when an OAuth state does not exist, has expired or was already used.
	ErrOAuthStateNotFound = errors.New("oauth state not found")
	// ErrGoogleTokenRevoked is returned when Google no longer accepts the user's token.
	ErrGoogleTokenRevoked = errors.New("google token revoked")
	// ErrGoogleDisabled is returned when Google Calendar credentials are not configured.
	ErrGoogleDisabled = errors.New("google calendar integration disabled")
	// ErrPendingTaskNotFound is returned when no task waits for the user's confirmation.
	ErrPendingTaskNotFound = errors.New("pending task not found")
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

// AiRes is a response returned by the AI service.
type AiRes struct {
	Action  json.RawMessage `json:"action"`
	Context json.RawMessage `json:"context"`
	Intent  string          `json:"intent"`
	Message json.RawMessage `json:"message"`
	Status  string          `json:"status"`
	TaskID  uuid.UUID       `json:"task_id"`
	UserID  uuid.UUID       `json:"user_id"`
}

// TaskResult is a response returned by the task processing service.
type TaskResult any

// TaskRequest is a parsed text or audio task request.
type TaskRequest any
