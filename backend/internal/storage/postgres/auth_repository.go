package postgres

import (
	"context"
	"errors"
	"fmt"
	"gorkycode/backend/internal/model"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// uniqueViolation is the PostgreSQL error code for a broken unique constraint.
const uniqueViolation = "23505"

// AuthRepository reads and creates users for authentication.
type AuthRepository struct {
	pool *pgxpool.Pool
	sb   sq.StatementBuilderType
}

// NewAuthRepository creates an auth repository over a connection pool.
func NewAuthRepository(pool *pgxpool.Pool) *AuthRepository {
	return &AuthRepository{
		pool: pool,
		sb:   sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
	}
}

// GetUserByEmail finds a user by email ignoring case.
// It returns model.ErrUserNotFound when no user has this email.
func (r *AuthRepository) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	user := &model.User{}

	query, args, err := r.sb.
		Select("id", "name", "email", "password_hash", "timezone", "created_at").
		From("users").
		Where("lower(email) = lower(?)", email).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("build select user query: %w", err)
	}

	err = r.pool.QueryRow(ctx, query, args...).
		Scan(&user.ID,
			&user.Name,
			&user.Email,
			&user.PasswordHash,
			&user.Timezone,
			&user.CreatedAt,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, model.ErrUserNotFound
		}
		return nil, fmt.Errorf("select user by email: %w", err)
	}

	return user, nil
}

// GetUserTimezone returns the IANA timezone stored for a user.
func (r *AuthRepository) GetUserTimezone(ctx context.Context, userID uuid.UUID) (string, error) {
	var timezone string
	err := r.pool.QueryRow(ctx, "SELECT timezone FROM users WHERE id = $1", userID).Scan(&timezone)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", model.ErrUserNotFound
	}
	if err != nil {
		return "", fmt.Errorf("select user timezone: %w", err)
	}
	return timezone, nil
}

// GetUserEmail returns the address the user registered with.
func (r *AuthRepository) GetUserEmail(ctx context.Context, userID uuid.UUID) (string, error) {
	var email string
	err := r.pool.QueryRow(ctx, "SELECT email FROM users WHERE id = $1", userID).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", model.ErrUserNotFound
	}
	if err != nil {
		return "", fmt.Errorf("select user email: %w", err)
	}
	return email, nil
}

// CreateUser inserts the user and fills its generated ID and creation time.
// It returns model.ErrEmailTaken when a user with the same email already exists.
func (r *AuthRepository) CreateUser(ctx context.Context, user *model.User) error {
	query, args, err := r.sb.
		Insert("users").
		Columns("name", "email", "password_hash", "timezone").
		Values(user.Name, user.Email, user.PasswordHash, user.Timezone).
		Suffix("RETURNING id, created_at").
		ToSql()
	if err != nil {
		return fmt.Errorf("build insert user query: %w", err)
	}

	err = r.pool.QueryRow(ctx, query, args...).Scan(&user.ID, &user.CreatedAt)
	if err != nil {
		// users_email_unique compares lower(email), so the race between two
		// sign-ups with the same email is settled by the database.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return model.ErrEmailTaken
		}
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}
