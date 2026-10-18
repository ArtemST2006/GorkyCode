package postgres

import (
	"context"
	"errors"
	"fmt"
	"gorkycode/backend/internal/model"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AuthRepository reads users for authentication.
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
		return nil, fmt.Errorf("build query: %w", err)
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
		return nil, fmt.Errorf("run query: %w", err)
	}

	return user, nil
}
