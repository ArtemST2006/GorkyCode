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
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
