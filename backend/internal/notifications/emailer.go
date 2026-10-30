package notifications

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorkycode/backend/internal/lib/logger"
	"gorkycode/backend/internal/model"

	"github.com/google/uuid"
)

// EmailStore reads the recipient address and tracks sent letters.
type EmailStore interface {
	// GetUserEmail returns the address that the user registered with.
	GetUserEmail(ctx context.Context, userID uuid.UUID) (string, error)
	// ClaimEmail marks a notification as taken for emailing; false means
	// another attempt already happened.
	ClaimEmail(ctx context.Context, notificationID uuid.UUID) (bool, error)
	// ReleaseEmail resets a failed claim for the next attempt.
	ReleaseEmail(ctx context.Context, notificationID uuid.UUID) error
}

// EmailClient sends one letter worth both subject and body.
type EmailClient interface {
	Send(ctx context.Context, to, subject, body string) error
}

// Emailer mails every fired notification kind out of the configured list to
// the email a user registered with. The worker consumes one channel of the
// reminder bus sequentially: SMTP latency must not grow the mailbox of the
// WebSocket hub.
type Emailer struct {
	store  EmailStore
	client EmailClient
	kinds  map[string]struct{}
	log    *slog.Logger
}

// NewEmailer constructs the email worker over the configured kinds.
func NewEmailer(store EmailStore, client EmailClient, kinds []string, log *slog.Logger) *Emailer {
	enabled := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		if kind != "" {
			enabled[kind] = struct{}{}
		}
	}
	return &Emailer{store: store, client: client, kinds: enabled, log: log}
}

// emailSendTimeout bounds one letter including the SMTP conversation.
const emailSendTimeout = 45 * time.Second

// Run consumes the fired notification channel until it closes or the
// context is done.
func (e *Emailer) Run(ctx context.Context, in <-chan model.Notification) {
	for {
		select {
		case <-ctx.Done():
			return
		case n, ok := <-in:
			if !ok {
				return
			}
			if len(e.kinds) > 0 {
				if _, kindEnabled := e.kinds[n.Kind]; !kindEnabled {
					continue
				}
			}
			e.send(ctx, n)
		}
	}
}

// send claims and delivers one letter; a failure only unblocks the next one.
func (e *Emailer) send(ctx context.Context, n model.Notification) {
	queryCtx, cancel := context.WithTimeout(ctx, emailSendTimeout)
	defer cancel()

	claimed, err := e.store.ClaimEmail(queryCtx, n.ID)
	if err != nil {
		e.log.ErrorContext(ctx, "email claim failed",
			slog.String("notification_id", n.ID.String()), logger.Err(err))
		return
	}
	if !claimed {
		return
	}

	// The release must survive an SMTP call that consumed the whole send
	// budget: a fresh detached context keeps the claim from sticking and
	// silencing the retry.
	releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer releaseCancel()
	release := func() { _ = e.store.ReleaseEmail(releaseCtx, n.ID) }

	to, err := e.store.GetUserEmail(queryCtx, n.UserID)
	if err != nil {
		e.log.ErrorContext(ctx, "user email lookup failed",
			slog.String("user_id", n.UserID.String()), logger.Err(err))
		release()
		return
	}

	if err = e.client.Send(queryCtx, to, emailSubject(n), emailBody(n)); err != nil {
		e.log.ErrorContext(ctx, "reminder email failed",
			slog.String("notification_id", n.ID.String()),
			slog.String("email", to), logger.Err(err))
		release()
		return
	}
	e.log.InfoContext(ctx, "reminder email sent",
		slog.String("notification_id", n.ID.String()), slog.String("email", to))
}

// emailSubject renders the mail title.
func emailSubject(n model.Notification) string {
	return fmt.Sprintf("Напоминание: %s", n.Title)
}

// emailBody renders the plain letter the user receives.
func emailBody(n model.Notification) string {
	return fmt.Sprintf(
		"%s\n\nМомент напоминания: %s\n\nУслуга напоминаний GorkyCode.",
		n.Message,
		n.DueAt.Format("02.01.2006 в 15:04 MST"),
	)
}
