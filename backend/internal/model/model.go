// Package model contains domain entities and errors shared between layers.
package model

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrUserNotFound is returned by storage when no user matches the query.
	ErrUserNotFound = errors.New("user not found")
	// ErrInvalidCredentials is returned when the email or the password is wrong.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrSessionNotFound is returned when a session token does not exist or has expired.
	ErrSessionNotFound = errors.New("session not found")
	// ErrEmailTaken is returned when a user with the same email already exists.
	ErrEmailTaken = errors.New("email already taken")
)

// User is a registered user.
type User struct {
	ID           uuid.UUID
	Name         string
	Email        string
	PasswordHash string
	Timezone     string
	CreatedAt    time.Time
}

// AuthResult is a signed-in user with the access token of the new session.
type AuthResult struct {
	User        User
	AccessToken string
	ExpiresIn   time.Duration
}
