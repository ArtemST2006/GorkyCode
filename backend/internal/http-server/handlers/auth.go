package handlers

import (
	"context"
	"encoding/json"
	"errors"
	api "gorkycode/backend/internal/generated"
	"gorkycode/backend/internal/http-server/apierr"
	"gorkycode/backend/internal/model"
	"net/http"

	"github.com/go-chi/render"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Auth is the application operation used by the auth handlers.
type Auth interface {
	SignIn(ctx context.Context, email, password string) (*model.User, error)
}

// AuthSignIn authenticates a user by email and password (POST /api/v1/auth/sign-in).
func (s *Server) AuthSignIn(w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(r.Body)
	var req api.SignInRequest
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		if errors.Is(err, openapi_types.ErrValidationEmail) {
			apierr.Write(w, r, apierr.InvalidRequest("invalid email format"))
			return
		}
		apierr.Write(w, r, apierr.InvalidRequest("invalid JSON body"))
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

	user, err := s.services.SignIn(r.Context(), email, *password)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrInvalidCredentials):
			apierr.Write(w, r, apierr.InvalidCredentials("invalid email or password"))
		case errors.Is(err, context.DeadlineExceeded):
			apierr.Write(w, r, apierr.Unavailable("request timed out"))
		default:
			apierr.Write(w, r, apierr.Internal())
		}
		return
	}
	render.JSON(w, r, api.AuthResponse{User: toAPIUser(user)})
}

func toAPIUser(user *model.User) api.User {
	return api.User{
		ID:        user.ID,
		Name:      user.Name,
		Email:     openapi_types.Email(user.Email),
		Timezone:  user.Timezone,
		CreatedAt: user.CreatedAt,
	}
}
