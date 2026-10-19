package handlers

import (
	"context"
	"encoding/json"
	"errors"
	api "gorkycode/backend/internal/generated"
	"gorkycode/backend/internal/http-server/apierr"
	"gorkycode/backend/internal/lib/logger"
	"gorkycode/backend/internal/model"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/render"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Limits from the OpenAPI contract.
const (
	maxAuthBodyBytes = 4 << 10
	maxNameLen       = 100
	maxEmailLen      = 320
	minPasswordLen   = 15
	maxPasswordLen   = 128
)

// Auth is the application operation used by the auth handlers.
type Auth interface {
	SignUp(ctx context.Context, name, email, password, timezone string) (*model.AuthResult, error)
	SignIn(ctx context.Context, email, password string) (*model.AuthResult, error)
	Authenticate(ctx context.Context, token string) (uuid.UUID, error)
	SignOut(ctx context.Context, token string) error
}

// AuthSignUp registers a user and returns an access token (POST /api/v1/auth/sign-up).
func (s *Server) AuthSignUp(w http.ResponseWriter, r *http.Request) {
	const op = "handlers.auth.AuthSignUp"
	log := s.log.With(slog.String("op", op))

	var req api.SignUpRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	name := strings.TrimSpace(req.Name)
	email := string(req.Email)
	if detail := validateSignUp(name, email, req.Password, req.Timezone); detail != "" {
		apierr.Write(w, r, apierr.InvalidRequest(detail))
		return
	}

	result, err := s.services.SignUp(r.Context(), name, email, *req.Password, req.Timezone)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrEmailTaken):
			apierr.Write(w, r, apierr.Conflict("email is already registered"))
		case errors.Is(err, context.DeadlineExceeded):
			log.ErrorContext(r.Context(), "sign-up timed out", logger.Err(err))
			apierr.Write(w, r, apierr.Unavailable("request timed out"))
		default:
			log.ErrorContext(r.Context(), "sign-up failed", logger.Err(err))
			apierr.Write(w, r, apierr.Internal())
		}
		return
	}

	log.InfoContext(r.Context(), "user signed up", slog.String("user_id", result.User.ID.String()))
	writeAuthResponse(w, r, http.StatusCreated, result)
}

// AuthSignIn authenticates a user by email and password (POST /api/v1/auth/sign-in).
func (s *Server) AuthSignIn(w http.ResponseWriter, r *http.Request) {
	const op = "handlers.auth.AuthSignIn"
	log := s.log.With(slog.String("op", op))

	var req api.SignInRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	email := string(req.Email)
	if email == "" {
		apierr.Write(w, r, apierr.InvalidRequest("email is required"))
		return
	}
	password := req.Password
	if password == nil || *password == "" {
		apierr.Write(w, r, apierr.InvalidRequest("password is required"))
		return
	}

	result, err := s.services.SignIn(r.Context(), email, *password)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrInvalidCredentials):
			apierr.Write(w, r, apierr.InvalidCredentials("invalid email or password"))
		case errors.Is(err, context.DeadlineExceeded):
			log.ErrorContext(r.Context(), "sign-in timed out", logger.Err(err))
			apierr.Write(w, r, apierr.Unavailable("request timed out"))
		default:
			log.ErrorContext(r.Context(), "sign-in failed", logger.Err(err))
			apierr.Write(w, r, apierr.Internal())
		}
		return
	}

	log.InfoContext(r.Context(), "user signed in", slog.String("user_id", result.User.ID.String()))
	writeAuthResponse(w, r, http.StatusOK, result)
}

// AuthSignOut ends the session of the access token (POST /api/v1/auth/sign-out).
func (s *Server) AuthSignOut(w http.ResponseWriter, r *http.Request) {
	const op = "handlers.auth.AuthSignOut"
	log := s.log.With(slog.String("op", op))

	session, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	if err := s.services.SignOut(r.Context(), session.token); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			log.ErrorContext(r.Context(), "sign-out timed out", logger.Err(err))
			apierr.Write(w, r, apierr.Unavailable("request timed out"))
			return
		}
		log.ErrorContext(r.Context(), "sign-out failed", logger.Err(err))
		apierr.Write(w, r, apierr.Internal())
		return
	}

	log.InfoContext(r.Context(), "user signed out", slog.String("user_id", session.userID.String()))
	w.WriteHeader(http.StatusNoContent)
}

// decodeJSON reads a single JSON object into dst and writes 400 when it cannot.
// Unknown fields are rejected because the contract sets additionalProperties: false.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAuthBodyBytes))
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil && !errors.Is(dec.Decode(&struct{}{}), io.EOF) {
		err = errors.New("request body must contain a single JSON object")
	}
	if err == nil {
		return true
	}

	detail := "invalid JSON body"
	if errors.Is(err, openapi_types.ErrValidationEmail) {
		detail = "invalid email format"
	}
	apierr.Write(w, r, apierr.InvalidRequest(detail))
	return false
}

func validateSignUp(name, email string, password *string, timezone string) string {
	switch {
	case name == "":
		return "name is required"
	case utf8.RuneCountInString(name) > maxNameLen:
		return "name is too long"
	case email == "":
		return "email is required"
	case len(email) > maxEmailLen:
		return "email is too long"
	case password == nil || utf8.RuneCountInString(*password) < minPasswordLen:
		return "password must be at least 15 characters"
	case utf8.RuneCountInString(*password) > maxPasswordLen:
		return "password is too long"
	case !validTimezone(timezone):
		return "timezone must be an IANA time zone, e.g. Europe/Moscow"
	}
	return ""
}

// validTimezone accepts IANA names only: time.LoadLocation also accepts ""
// and "Local", which do not name a real time zone.
func validTimezone(name string) bool {
	if name == "" || name == "Local" {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

func writeAuthResponse(w http.ResponseWriter, r *http.Request, status int, result *model.AuthResult) {
	w.Header().Set("Cache-Control", "no-store")
	render.Status(r, status)
	render.JSON(w, r, api.AuthResponse{
		User:        toAPIUser(&result.User),
		AccessToken: result.AccessToken,
		TokenType:   api.Bearer,
		ExpiresIn:   expiresInSeconds(result.ExpiresIn),
	})
}

// expiresInSeconds converts the session lifetime for the int32 expires_in field,
// which the contract requires to be at least 1.
func expiresInSeconds(ttl time.Duration) int32 {
	seconds := int64(ttl / time.Second)
	switch {
	case seconds < 1:
		return 1
	case seconds > math.MaxInt32:
		return math.MaxInt32
	default:
		return int32(seconds)
	}
}

func toAPIUser(user *model.User) api.User {
	return api.User{
		Id:        user.ID,
		Name:      user.Name,
		Email:     openapi_types.Email(user.Email),
		Timezone:  user.Timezone,
		CreatedAt: user.CreatedAt,
	}
}
