package postgres

import (
	"context"
	"errors"
	"fmt"
	"gorkycode/backend/internal/model"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultGoogleCalendarID is the user's main calendar in the Google Calendar API.
const defaultGoogleCalendarID = "primary"

// GoogleRepository stores Google Calendar connections and synced event IDs.
type GoogleRepository struct {
	pool *pgxpool.Pool
	sb   sq.StatementBuilderType
}

// NewGoogleRepository creates a Google Calendar repository over a connection pool.
func NewGoogleRepository(pool *pgxpool.Pool) *GoogleRepository {
	return &GoogleRepository{
		pool: pool,
		sb:   sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
	}
}

// UpsertGoogleIntegration saves the user's connection, replacing an existing one,
// and fills its creation and update times. An empty CalendarID is stored as
// "primary". An empty RefreshToken keeps the stored one, because Google
// returns a refresh token only on the first consent; a new connection
// without a refresh token is rejected.
func (r *GoogleRepository) UpsertGoogleIntegration(ctx context.Context, integration *model.GoogleIntegration) error {
	if integration == nil {
		return fmt.Errorf("google integration is required")
	}
	if integration.CalendarID == "" {
		integration.CalendarID = defaultGoogleCalendarID
	}
	if integration.RefreshToken == "" {
		// INSERT ... ON CONFLICT checks the refresh_token constraint on the
		// proposed row before it finds the conflict, so an empty token can
		// only go through a plain UPDATE.
		return r.updateGoogleIntegrationKeepingRefreshToken(ctx, integration)
	}

	query, args, err := r.sb.
		Insert("google_integrations").
		Columns("user_id", "refresh_token", "access_token", "token_expiry", "calendar_id", "google_email").
		Values(
			integration.UserID,
			integration.RefreshToken,
			integration.AccessToken,
			integration.TokenExpiry,
			integration.CalendarID,
			integration.GoogleEmail,
		).
		Suffix(`ON CONFLICT (user_id) DO UPDATE SET
			refresh_token = EXCLUDED.refresh_token,
			access_token = EXCLUDED.access_token,
			token_expiry = EXCLUDED.token_expiry,
			calendar_id = EXCLUDED.calendar_id,
			google_email = EXCLUDED.google_email,
			updated_at = now()
		RETURNING created_at, updated_at`).
		ToSql()
	if err != nil {
		return fmt.Errorf("build upsert google integration query: %w", err)
	}

	err = r.pool.QueryRow(ctx, query, args...).Scan(&integration.CreatedAt, &integration.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert google integration: %w", err)
	}
	return nil
}

// updateGoogleIntegrationKeepingRefreshToken updates every field of an existing
// connection except the refresh token.
func (r *GoogleRepository) updateGoogleIntegrationKeepingRefreshToken(
	ctx context.Context,
	integration *model.GoogleIntegration,
) error {
	query, args, err := r.sb.
		Update("google_integrations").
		Set("access_token", integration.AccessToken).
		Set("token_expiry", integration.TokenExpiry).
		Set("calendar_id", integration.CalendarID).
		Set("google_email", integration.GoogleEmail).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"user_id": integration.UserID}).
		Suffix("RETURNING created_at, updated_at").
		ToSql()
	if err != nil {
		return fmt.Errorf("build update google integration query: %w", err)
	}

	err = r.pool.QueryRow(ctx, query, args...).Scan(&integration.CreatedAt, &integration.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("refresh token is required for a new google integration")
		}
		return fmt.Errorf("update google integration: %w", err)
	}
	return nil
}

// GetGoogleIntegration returns the user's Google Calendar connection.
// It returns model.ErrGoogleNotConnected when the user has not connected it.
func (r *GoogleRepository) GetGoogleIntegration(ctx context.Context, userID uuid.UUID) (*model.GoogleIntegration, error) {
	integration := &model.GoogleIntegration{}

	query, args, err := r.sb.
		Select("user_id", "refresh_token", "access_token", "token_expiry", "calendar_id", "google_email",
			"created_at", "updated_at").
		From("google_integrations").
		Where(sq.Eq{"user_id": userID}).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("build select google integration query: %w", err)
	}

	err = r.pool.QueryRow(ctx, query, args...).
		Scan(&integration.UserID,
			&integration.RefreshToken,
			&integration.AccessToken,
			&integration.TokenExpiry,
			&integration.CalendarID,
			&integration.GoogleEmail,
			&integration.CreatedAt,
			&integration.UpdatedAt,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, model.ErrGoogleNotConnected
		}
		return nil, fmt.Errorf("select google integration: %w", err)
	}

	return integration, nil
}

// UpdateGoogleTokens saves a refreshed access token and its expiry.
// It returns model.ErrGoogleNotConnected when the user has not connected Google Calendar.
func (r *GoogleRepository) UpdateGoogleTokens(
	ctx context.Context,
	userID uuid.UUID,
	accessToken string,
	expiry *time.Time,
) error {
	query, args, err := r.sb.
		Update("google_integrations").
		Set("access_token", accessToken).
		Set("token_expiry", expiry).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"user_id": userID}).
		ToSql()
	if err != nil {
		return fmt.Errorf("build update google tokens query: %w", err)
	}

	tag, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update google tokens: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return model.ErrGoogleNotConnected
	}
	return nil
}

// DeleteGoogleIntegration removes the user's connection. Deleting a missing
// connection is not an error.
func (r *GoogleRepository) DeleteGoogleIntegration(ctx context.Context, userID uuid.UUID) error {
	query, args, err := r.sb.
		Delete("google_integrations").
		Where(sq.Eq{"user_id": userID}).
		ToSql()
	if err != nil {
		return fmt.Errorf("build delete google integration query: %w", err)
	}

	if _, err = r.pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("delete google integration: %w", err)
	}
	return nil
}

// SetTaskGoogleEventID stores the Google Calendar event ID of the user's task.
// A nil eventID marks the task as not sent to Google Calendar.
func (r *GoogleRepository) SetTaskGoogleEventID(ctx context.Context, userID, taskID uuid.UUID, eventID *string) error {
	query, args, err := r.sb.
		Update("tasks").
		Set("google_event_id", eventID).
		Where(sq.Eq{"id": taskID, "user_id": userID}).
		ToSql()
	if err != nil {
		return fmt.Errorf("build update task google event query: %w", err)
	}

	if _, err = r.pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("update task google event: %w", err)
	}
	return nil
}
