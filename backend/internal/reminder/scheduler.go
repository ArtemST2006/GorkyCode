package reminder

import (
	"context"
	"gorkycode/backend/internal/config"
	"gorkycode/backend/internal/lib/logger"
	"gorkycode/backend/internal/model"
	"log/slog"
	"time"
)

// ClaimStore is the dispatched part of notifications storage.
type ClaimStore interface {
	// ClaimDue marks due pending notifications as delivery attempts and
	// returns their current content.
	ClaimDue(ctx context.Context, now time.Time, limit int, claimTTL time.Duration) ([]model.Notification, error)
	// PurgeDelivered deletes records outside the retention window.
	PurgeDelivered(ctx context.Context, olderThan time.Time) (int64, error)
}

// Reconciler lists upcoming tasks and re-derives their reminder moments.
type Reconciler interface {
	Reconcile(ctx context.Context) error
}

// Scheduler is the central dispatch loop. One goroutine claims due
// notifications from storage and fans them out to the subscribers, so the
// process memory stays independent of the number of reminders. Every tick
// uses SKIP LOCKED claims: several backend instances cannot double-send the
// same notification within one claim period.
type Scheduler struct {
	store        ClaimStore
	bus          Publisher
	syncer       Reconciler
	queryTimeout time.Duration
	log          *slog.Logger
	cfg          config.Reminder
}

// NewScheduler constructs the reminder scheduler.
func NewScheduler(store ClaimStore, bus Publisher, syncer Reconciler, queryTimeout time.Duration, cfg config.Reminder, log *slog.Logger) *Scheduler {
	return &Scheduler{store: store, bus: bus, syncer: syncer, queryTimeout: queryTimeout, log: log, cfg: cfg}
}

// Run keeps the loop running until the context is done.
func (s *Scheduler) Run(ctx context.Context) {
	// The first reconciliation fills notifications storage for the tasks
	// created before this process started.
	if err := s.syncer.Reconcile(ctx); err != nil {
		s.log.ErrorContext(ctx, "first reminder reconciliation failed", logger.Err(err))
	}

	scan := time.NewTicker(s.cfg.ScanInterval)
	defer scan.Stop()
	reconcile := time.NewTicker(s.cfg.SyncInterval)
	defer reconcile.Stop()
	purge := time.NewTicker(time.Hour)
	defer purge.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-scan.C:
			s.dispatch(ctx)
		case <-reconcile.C:
			if err := s.syncer.Reconcile(ctx); err != nil {
				s.log.ErrorContext(ctx, "reminder reconciliation failed", logger.Err(err))
			}
		case <-purge.C:
			s.purge(ctx)
		}
	}
}

// dispatch claims every due notification and publishes it to the bus. The
// rows stay pending in storage until a client acknowledges them, so a
// failed publish is retried when the claim expires.
func (s *Scheduler) dispatch(ctx context.Context) {
	now := time.Now()
	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	claimed, err := s.store.ClaimDue(queryCtx, now, s.cfg.BatchLimit, s.cfg.ClaimTTL)
	if err != nil {
		s.log.ErrorContext(ctx, "claim due notifications failed", logger.Err(err))
		return
	}
	for _, n := range claimed {
		if err = s.bus.Publish(ctx, n); err != nil {
			s.log.ErrorContext(ctx, "publish notification failed",
				slog.String("notification_id", n.ID.String()),
				slog.String("user_id", n.UserID.String()),
				logger.Err(err),
			)
		}
	}
}

// purge deletes notifications outside the retention window.
func (s *Scheduler) purge(ctx context.Context) {
	olderThan := time.Now().Add(-s.cfg.Retention)
	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	removed, err := s.store.PurgeDelivered(queryCtx, olderThan)
	if err != nil {
		s.log.ErrorContext(ctx, "notification retention purge failed", logger.Err(err))
		return
	}
	if removed > 0 {
		s.log.InfoContext(ctx, "notification retention purge",
			slog.Int64("removed", removed),
			slog.String("older_than", olderThan.Format(time.RFC3339)),
		)
	}
}
