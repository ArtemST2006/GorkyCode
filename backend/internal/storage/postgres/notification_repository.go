package postgres

import (
	"context"
	"fmt"
	"gorkycode/backend/internal/model"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// notificationColumns is the shared projection of the notifications table.
// due_at keeps its time zone; the WebSocket schema calls it scheduled_at.
const notificationColumns = "id, user_id, task_id, kind, title, message, due_at, status, send_attempted_at, email_sent_at, created_at"

// notificationReturning projects the same columns as an UPDATE ... RETURNING
// statement does.
const notificationReturning = "n.id, n.user_id, n.task_id, n.kind, n.title, n.message, n.due_at, n.status, n.send_attempted_at, n.email_sent_at, n.created_at"

// NotificationRepository reads and maintains reminder notifications.
type NotificationRepository struct {
	pool *pgxpool.Pool
	sb   sq.StatementBuilderType
}

// NewNotificationRepository creates a notification repository over a pool.
func NewNotificationRepository(pool *pgxpool.Pool) *NotificationRepository {
	return &NotificationRepository{
		pool: pool,
		sb:   sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
	}
}

// ScheduleTask atomically replaces the future pending notifications of one
// task: stale moments are dropped and the supplied moments are inserted only
// when no record of the same task, kind and moment exists yet. Past pending
// moments are preserved because a user may still want the late information.
// The caller supplies fully built moments; the id, status and timestamps are
// left to the defaults of the table.
func (r *NotificationRepository) ScheduleTask(
	ctx context.Context,
	userID, taskID uuid.UUID,
	moments []model.Notification,
) error {
	_ = userID
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin notification schedule: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	query, args, err := r.sb.Delete("notifications").
		Where(sq.Eq{"task_id": taskID}).
		Where(sq.Eq{"status": model.NotificationStatusPending}).
		Where(sq.Gt{"due_at": time.Now()}).
		ToSql()
	if err != nil {
		return fmt.Errorf("build notification cleanup: %w", err)
	}
	if _, err = tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("delete stale notifications: %w", err)
	}

	for _, moment := range moments {
		query, args, err = r.sb.
			Insert("notifications").
			Columns("user_id", "task_id", "kind", "title", "message", "due_at").
			Values(moment.UserID, moment.TaskID, moment.Kind, moment.Title, moment.Message, moment.DueAt).
			Suffix("ON CONFLICT (task_id, kind, due_at) DO NOTHING").
			ToSql()
		if err != nil {
			return fmt.Errorf("build notification insert: %w", err)
		}
		if _, err = tx.Exec(ctx, query, args...); err != nil {
			return fmt.Errorf("insert notification: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit notification schedule: %w", err)
	}
	return nil
}

// DuePendingByUser lists the unacked notifications of a user whose moment
// already passed, in the creation order of the WebSocket contract.
func (r *NotificationRepository) DuePendingByUser(
	ctx context.Context,
	userID uuid.UUID,
	now time.Time,
	limit int,
) ([]model.Notification, error) {
	if limit < 0 {
		limit = 0
	}
	query, args, err := r.sb.
		Select(notificationColumns).
		From("notifications").
		Where(sq.Eq{"user_id": userID, "status": model.NotificationStatusPending}).
		Where(sq.LtOrEq{"due_at": now}).
		OrderBy("created_at", "id").
		Limit(uint64(limit)).
		ToSql() //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("build pending notifications query: %w", err)
	}
	return r.fetchNotifications(ctx, query, args...)
}

// ClaimDue marks up to limit shared-due notifications as delivery attempts
// and returns their current content. A claim expires after claimTTL so a
// crashed process does not silence a notification forever. SKIP LOCKED keeps
// concurrent schedulers from sending the same rows at once.
func (r *NotificationRepository) ClaimDue(
	ctx context.Context,
	now time.Time,
	limit int,
	claimTTL time.Duration,
) ([]model.Notification, error) {
	// Squirrel cannot wrap a plain SELECT into an UPDATE ... RETURNING, so the
	// claim keeps one raw query instead.
	const claimQuery = `
WITH due AS (
    SELECT id FROM notifications
    WHERE status = 'pending'
      AND due_at <= $1
      AND (send_attempted_at IS NULL OR send_attempted_at < $2)
    ORDER BY due_at
    LIMIT $3
    FOR UPDATE SKIP LOCKED
)
UPDATE notifications n
SET send_attempted_at = $1
FROM due
WHERE n.id = due.id
RETURNING ` + notificationReturning

	rows, err := r.pool.Query(ctx, claimQuery, now, now.Add(-claimTTL), limit)
	if err != nil {
		return nil, fmt.Errorf("claim due notifications: %w", err)
	}
	defer rows.Close()
	return scanNotifications(rows)
}

// MarkDelivered stores a user's acknowledgement. It succeeds idempotently
// for an already delivered notification of the same user; false means the
// ID is unknown or belongs to another user.
func (r *NotificationRepository) MarkDelivered(
	ctx context.Context,
	userID, notificationID uuid.UUID,
) (bool, error) {
	query, args, err := r.sb.
		Update("notifications").
		Set("status", model.NotificationStatusDelivered).
		Where(sq.Eq{"id": notificationID, "user_id": userID}).
		Where(sq.Expr("status <> ?", model.NotificationStatusDelivered)).
		ToSql()
	if err != nil {
		return false, fmt.Errorf("build delivered notification update: %w", err)
	}
	tag, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("mark delivered notification: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}

	// No row changed: either it was delivered already or the ID is foreign.
	var exists bool
	if err = r.pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM notifications WHERE id = $1 AND user_id = $2)",
		notificationID, userID,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check notification ownership: %w", err)
	}
	return exists, nil
}

// ClaimEmail marks a notification as attempted to email; false means another
// attempt already happened and this one must skip. A failed SMTP call resets
// the claim through ReleaseEmail so the next try can send again.
func (r *NotificationRepository) ClaimEmail(ctx context.Context, notificationID uuid.UUID) (bool, error) {
	query, args, err := r.sb.
		Update("notifications").
		Set("email_sent_at", time.Now()).
		Where(sq.Eq{"id": notificationID}).
		Where(sq.Expr("email_sent_at IS NULL")).
		ToSql()
	if err != nil {
		return false, fmt.Errorf("build email claim update: %w", err)
	}
	tag, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("claim email: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ReleaseEmail resets the email claim after a failed SMTP attempt, so the
// next run of an expired delivery attempt can try to send again.
func (r *NotificationRepository) ReleaseEmail(ctx context.Context, notificationID uuid.UUID) error {
	query, args, err := r.sb.
		Update("notifications").
		Set("email_sent_at", nil).
		Where(sq.Eq{"id": notificationID}).
		ToSql()
	if err != nil {
		return fmt.Errorf("build email release update: %w", err)
	}
	if _, err = r.pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("release email: %w", err)
	}
	return nil
}

// PurgeDelivered removes notifications older than the retention window from
// the WebSocket contract: delivered ones and never acknowledged records.
func (r *NotificationRepository) PurgeDelivered(ctx context.Context, olderThan time.Time) (int64, error) {
	query, args, err := r.sb.Delete("notifications").
		Where(sq.Lt{"created_at": olderThan}).
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("build retention purge: %w", err)
	}
	tag, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("purge notifications: %w", err)
	}
	return tag.RowsAffected(), nil
}

// UpcomingTasksWithTimezone lists dated active tasks with the owner timezone
// inside the date window; the reconciliation scan derives reminder moments
// out of it. Tasks without a date never reach this projection.
func (r *NotificationRepository) UpcomingTasksWithTimezone(
	ctx context.Context,
	from, to time.Time,
	limit int,
) ([]model.TaskWithTimezone, error) {
	if limit < 0 {
		limit = 0
	}
	// Dates are passed as YYYY-MM-DD, as in TaskRepository.ListEvents, so the
	// server time zone cannot shift the day boundaries.
	query, args, err := r.sb.
		Select(`t.id`, `t.user_id`, `t.type`, `t.title`, `t.date`,
			`to_char(t.time, 'HH24:MI')`, `t.status`, `u.timezone`).
		From("tasks t").
		Join("users u ON u.id = t.user_id").
		Where(sq.Eq{"t.status": "active"}).
		Where(sq.Expr("t.date >= ?::date", from.Format(time.DateOnly))).
		Where(sq.Expr("t.date <= ?::date", to.Format(time.DateOnly))).
		OrderBy("t.date", "t.time NULLS FIRST", "t.created_at", "t.id").
		Limit(uint64(limit)).
		ToSql() //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("build upcoming tasks query: %w", err)
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select upcoming tasks: %w", err)
	}
	defer rows.Close()

	var tasks []model.TaskWithTimezone
	for rows.Next() {
		var row model.TaskWithTimezone
		if err = rows.Scan(&row.Task.ID, &row.Task.UserID, &row.Task.Type, &row.Task.Title,
			&row.Task.Date, &row.Task.Time, &row.Task.Status, &row.Timezone); err != nil {
			return nil, fmt.Errorf("scan upcoming task: %w", err)
		}
		tasks = append(tasks, row)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate upcoming tasks: %w", err)
	}
	return tasks, nil
}

func (r *NotificationRepository) fetchNotifications(ctx context.Context, query string, args ...any) ([]model.Notification, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select notifications: %w", err)
	}
	defer rows.Close()
	return scanNotifications(rows)
}

func scanNotifications(rows pgx.Rows) ([]model.Notification, error) {
	var result []model.Notification
	for rows.Next() {
		var n model.Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.TaskID, &n.Kind, &n.Title,
			&n.Message, &n.DueAt, &n.Status, &n.SendAttemptedAt, &n.EmailSentAt, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		result = append(result, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notifications: %w", err)
	}
	return result, nil
}
