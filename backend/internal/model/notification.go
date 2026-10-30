package model

import (
	"time"

	"github.com/google/uuid"
)

// Notification kinds stored in the notifications table and sent over
// the WebSocket channel; the values match contracts/backend-addition.openapi.yaml.
const (
	// NotificationKindTaskDue marks the deadline moment of a plain task.
	NotificationKindTaskDue = "task_due"
	// NotificationKindReminderDue marks the moment a reminder record fires.
	NotificationKindReminderDue = "reminder_due"
	// NotificationKindMeetingStart marks the moment a meeting starts.
	NotificationKindMeetingStart = "meeting_start"
)

// Notification storage statuses.
const (
	// NotificationStatusPending marks a notification that no client accepted yet.
	NotificationStatusPending = "pending"
	// NotificationStatusDelivered marks an acknowledged notification.
	NotificationStatusDelivered = "delivered"
)

// TaskDueByDefaultHour is the local hour a dated record without an exact
// time fires at, as the WebSocket contract requires.
const TaskDueByDefaultHour = 9

// Notification is a single reminder moment of a task.
type Notification struct {
	ID     uuid.UUID
	UserID uuid.UUID
	TaskID uuid.UUID
	Kind   string
	Title  string
	// Message is the human-readable text shown to the user.
	Message string
	// DueAt is the moment the event happens; the WebSocket schema calls it
	// scheduled_at.
	DueAt time.Time
	// Status is pending or delivered.
	Status string
	// SendAttemptedAt guards the last delivery attempt of a slow subscriber.
	SendAttemptedAt *time.Time
	// EmailSentAt marks that the reminder email was sent.
	EmailSentAt *time.Time
	CreatedAt   time.Time
}

// TaskWithTimezone is a dated task together with the timezone of its owner,
// ready for the reminder moment computation.
type TaskWithTimezone struct {
	Task     Task
	Timezone string
}
