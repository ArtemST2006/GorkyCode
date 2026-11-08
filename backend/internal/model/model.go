// Package model contains domain entities and errors shared between layers.
package model

import (
	"context"
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
	// ErrDialogueBusy means another request is processing this user's dialogue.
	ErrDialogueBusy = errors.New("dialogue is already being processed")
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
	// ErrUnsupportedIntent means the user asked for an operation the product does not support.
	ErrUnsupportedIntent = errors.New("unsupported intent")
	// ErrDialogueContextRequired means a bare reply arrived without a dialogue.
	ErrDialogueContextRequired = errors.New("dialogue context required")
	// ErrModelInvalidOutput means the AI model could not produce a valid action
	// after the service's correction attempt; no user action was performed.
	ErrModelInvalidOutput = errors.New("model output did not match contract")
	// ErrStaleSelection marks a delete selection that no longer matches the
	// shown candidates or the session owner.
	ErrStaleSelection = errors.New("selected tasks are missing or outdated")
	// ErrGoogleNotConnected is returned when the user has not connected Google Calendar.
	ErrGoogleNotConnected = errors.New("google calendar not connected")
	// ErrOAuthStateNotFound is returned when an OAuth state does not exist, has expired or was already used.
	ErrOAuthStateNotFound = errors.New("oauth state not found")
	// ErrGoogleTokenRevoked is returned when Google no longer accepts the user's token.
	ErrGoogleTokenRevoked = errors.New("google token revoked")
	// ErrGoogleDisabled is returned when Google Calendar credentials are not configured.
	ErrGoogleDisabled = errors.New("google calendar integration disabled")
	// ErrGoogleScopeMissing is returned when the user unchecked a calendar
	// permission on the Google consent page.
	ErrGoogleScopeMissing = errors.New("google calendar permission not granted")
	// ErrPendingTaskNotFound is returned when no task waits for the user's confirmation.
	ErrPendingTaskNotFound = errors.New("pending task not found")
	// ErrNotificationNotFound is returned when a notification ID does not exist
	// or belongs to another user.
	ErrNotificationNotFound = errors.New("notification not found")
)

// MeetingConflictError reports occupied meeting time without inserting a record.
type MeetingConflictError struct {
	Detail string
	UserID uuid.UUID
	TaskID uuid.UUID
}

func (e *MeetingConflictError) Error() string { return e.Detail }

// EmbeddingDimension must match the vector(384) column in task_embeddings
// and the /api/embed contract.
const EmbeddingDimension = 384

// EmbeddingResult is a computed embedding with the model identity that makes
// vectors comparable.
type EmbeddingResult struct {
	Vector  []float32
	Model   string
	Version string
}

// TaskChanges contains only fields the user asked to change. Nil preserves
// the corresponding value already stored in PostgreSQL.
type TaskChanges struct {
	Title  *string
	Date   *time.Time
	Time   *string
	Status *string
}

// Embedder computes embeddings for semantic task search. The values of
// purpose are the /api/embed roles: "document" for stored task text and
// "query" for user search strings.
type Embedder interface {
	Embed(ctx context.Context, purpose, text string) (EmbeddingResult, error)
}

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

// TaskResult carries a client response and its validated AI intent.
// Intent is sent in an HTTP header, not in the JSON body.
type TaskResult struct {
	Intent string
	Body   any
}

// TaskRequest is a parsed text or audio task request.
type TaskRequest any
