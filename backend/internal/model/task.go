package model

import (
	"time"

	"github.com/google/uuid"
)

// Task is a user's record from the tasks table. Date and Time are nil when not set.
type Task struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Type      string
	Title     string
	Date      *time.Time
	Time      *string
	Status    string
	CreatedAt time.Time
}
