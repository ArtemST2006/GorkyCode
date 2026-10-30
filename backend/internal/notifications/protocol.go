// Package notifications keeps the connected clients: the WebSocket hub with
// the protocol of contracts/backend-addition.openapi.yaml and the reminder
// email worker.
package notifications

import (
	"encoding/json"
	"gorkycode/backend/internal/model"
	"time"

	"github.com/google/uuid"
)

// Client message types.
const (
	msgAuth = "auth"
	msgAck  = "ack"
)

// Server message types.
const (
	msgReady        = "ready"
	msgNotification = "notification"
	msgError        = "error"
)

// Error message codes.
const (
	errInvalidMessage      = "invalid_message"
	errUnknownNotification = "unknown_notification"
)

// Close codes of the WebSocket contract.
const (
	closeBadToken  = 4401
	closeNoAuth    = 4408
	closePolicy    = 1008
	closeBadData   = 1003
	closeTooBig    = 1009
	closeSlowQueue = 1008
)

// authMessage is the first frame a client sends after the upgrade.
type authMessage struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

// ackMessage confirms that one notification reached the user.
type ackMessage struct {
	Type           string    `json:"type"`
	NotificationID uuid.UUID `json:"notification_id"`
}

// readyMessage opens the protocol after a successful auth.
type readyMessage struct {
	Type   string    `json:"type"`
	UserID uuid.UUID `json:"user_id"`
}

// notificationMessage wraps one Notification record.
type notificationMessage struct {
	Type         string       `json:"type"`
	Notification notification `json:"notification"`
}

// errorMessage answers a malformed client message.
type errorMessage struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// notification is the payload of contracts/backend-addition.openapi.yaml:
// due_at is sent as scheduled_at, times are RFC 3339 with a zone.
type notification struct {
	ID          uuid.UUID `json:"id"`
	Kind        string    `json:"kind"`
	TaskID      uuid.UUID `json:"task_id"`
	Title       string    `json:"title"`
	Message     string    `json:"message"`
	ScheduledAt time.Time `json:"scheduled_at"`
	CreatedAt   time.Time `json:"created_at"`
}

func toPayload(n model.Notification) notification {
	return notification{
		ID:          n.ID,
		Kind:        n.Kind,
		TaskID:      n.TaskID,
		Title:       n.Title,
		Message:     n.Message,
		ScheduledAt: n.DueAt,
		CreatedAt:   n.CreatedAt,
	}
}

// parseClientType extracts only the type field of a client frame.
func parseClientType(data []byte) (string, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return "", err
	}
	return probe.Type, nil
}
