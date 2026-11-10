package postgres

import (
	"context"
	"fmt"
	"gorkycode/backend/internal/model"

	"github.com/google/uuid"
)

// ClaimGoogleEventDeletions leases due deletions. SKIP LOCKED allows backend
// replicas to work on separate rows. The lease grows after each failure.
func (r *GoogleRepository) ClaimGoogleEventDeletions(ctx context.Context, limit int) ([]model.GoogleEventDeletion, error) {
	rows, err := r.pool.Query(ctx, `
		WITH due AS (
			SELECT task_id FROM google_event_deletions
			WHERE next_attempt_at <= now()
			ORDER BY next_attempt_at, task_id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE google_event_deletions AS d
		SET next_attempt_at = now() + make_interval(secs => LEAST(3600, 60 * (1 << LEAST(d.attempts, 6)))),
			attempts = d.attempts + 1
		FROM due WHERE d.task_id = due.task_id
		RETURNING d.task_id, d.user_id, d.calendar_id, d.google_event_id,
			d.google_email, d.connection_id`, limit)
	if err != nil {
		return nil, fmt.Errorf("claim google event deletions: %w", err)
	}
	defer rows.Close()

	deletions := make([]model.GoogleEventDeletion, 0)
	for rows.Next() {
		var item model.GoogleEventDeletion
		if err := rows.Scan(&item.TaskID, &item.UserID, &item.CalendarID, &item.EventID,
			&item.GoogleEmail, &item.ConnectionID); err != nil {
			return nil, fmt.Errorf("scan google event deletion: %w", err)
		}
		deletions = append(deletions, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read google event deletions: %w", err)
	}
	return deletions, nil
}

// CompleteGoogleEventDeletion removes a request after Google accepted the
// deletion (including an event that was already absent).
func (r *GoogleRepository) CompleteGoogleEventDeletion(ctx context.Context, taskID uuid.UUID) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM google_event_deletions WHERE task_id = $1`, taskID); err != nil {
		return fmt.Errorf("complete google event deletion: %w", err)
	}
	return nil
}
