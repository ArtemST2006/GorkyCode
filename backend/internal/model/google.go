package model

import (
	"time"

	"github.com/google/uuid"
)

// GoogleIntegration is a user's Google Calendar connection from the
// google_integrations table. TokenExpiry and GoogleEmail are nil when unknown.
type GoogleIntegration struct {
	UserID       uuid.UUID
	RefreshToken string
	AccessToken  string
	TokenExpiry  *time.Time
	CalendarID   string
	GoogleEmail  *string
	ConnectionID uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// GoogleEventDeletion is a durable request to remove a task's calendar copy.
// It survives deletion of the local task and can be retried after a failure.
type GoogleEventDeletion struct {
	TaskID       uuid.UUID
	UserID       uuid.UUID
	CalendarID   string
	EventID      string
	GoogleEmail  *string
	ConnectionID uuid.UUID
}
