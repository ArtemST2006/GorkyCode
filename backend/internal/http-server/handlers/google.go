package handlers

import (
	"context"
	"errors"
	api "gorkycode/backend/internal/generated"
	"gorkycode/backend/internal/http-server/apierr"
	"gorkycode/backend/internal/lib/logger"
	"gorkycode/backend/internal/model"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/go-chi/render"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// fallbackGoogleRedirect is used when GOOGLE_FRONTEND_REDIRECT is empty: a
// relative path keeps the browser on this site instead of an arbitrary host.
const fallbackGoogleRedirect = "/"

// Calendar is the application operation used by the Google Calendar handlers.
type Calendar interface {
	Connect(ctx context.Context, userID uuid.UUID) (string, error)
	Callback(ctx context.Context, code, state, googleErr string) error
	Status(ctx context.Context, userID uuid.UUID) (*model.GoogleIntegration, error)
	Disconnect(ctx context.Context, userID uuid.UUID) error
}

// GoogleConnect returns the Google consent page URL (POST /api/v1/integrations/google/connect).
func (s *Server) GoogleConnect(w http.ResponseWriter, r *http.Request) {
	const op = "handlers.google.GoogleConnect"
	log := s.log.With(slog.String("op", op))

	session, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	authURL, err := s.services.Connect(r.Context(), session.userID)
	if err != nil {
		s.writeGoogleError(w, r, log, "google connect", err)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	render.Status(r, http.StatusOK)
	render.JSON(w, r, api.GoogleConnectResponse{AuthUrl: authURL})
}

// GoogleCallback finishes the OAuth flow and redirects the browser to the
// frontend (GET /api/v1/integrations/google/callback). The redirect target
// comes only from configuration, never from the request.
func (s *Server) GoogleCallback(w http.ResponseWriter, r *http.Request, params api.GoogleCallbackParams) {
	const op = "handlers.google.GoogleCallback"
	log := s.log.With(slog.String("op", op))

	var code, googleErr string
	if params.Code != nil {
		code = *params.Code
	}
	if params.Error != nil {
		googleErr = *params.Error
	}

	result := "connected"
	if err := s.services.Callback(r.Context(), code, params.State, googleErr); err != nil {
		// The error never contains the code or tokens.
		log.WarnContext(r.Context(), "google callback failed", logger.Err(err))
		result = "error"
	}

	http.Redirect(w, r, s.googleRedirectURL(log, result), http.StatusFound)
}

// GoogleStatus reports whether the user connected Google Calendar (GET /api/v1/integrations/google).
func (s *Server) GoogleStatus(w http.ResponseWriter, r *http.Request) {
	const op = "handlers.google.GoogleStatus"
	log := s.log.With(slog.String("op", op))

	session, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	integration, err := s.services.Status(r.Context(), session.userID)
	if errors.Is(err, model.ErrGoogleNotConnected) {
		render.Status(r, http.StatusOK)
		render.JSON(w, r, api.GoogleStatusResponse{Connected: false})
		return
	}
	if err != nil {
		s.writeGoogleError(w, r, log, "google status", err)
		return
	}

	resp := api.GoogleStatusResponse{Connected: true, ConnectedAt: &integration.CreatedAt}
	if integration.GoogleEmail != nil {
		email := openapi_types.Email(*integration.GoogleEmail)
		resp.GoogleEmail = &email
	}
	render.Status(r, http.StatusOK)
	render.JSON(w, r, resp)
}

// GoogleDisconnect removes the user's Google Calendar connection (DELETE /api/v1/integrations/google).
func (s *Server) GoogleDisconnect(w http.ResponseWriter, r *http.Request) {
	const op = "handlers.google.GoogleDisconnect"
	log := s.log.With(slog.String("op", op))

	session, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	if err := s.services.Disconnect(r.Context(), session.userID); err != nil {
		s.writeGoogleError(w, r, log, "google disconnect", err)
		return
	}

	log.InfoContext(r.Context(), "google calendar disconnected", slog.String("user_id", session.userID.String()))
	w.WriteHeader(http.StatusNoContent)
}

// writeGoogleError maps service errors of the Google endpoints to problem responses.
func (s *Server) writeGoogleError(w http.ResponseWriter, r *http.Request, log *slog.Logger, action string, err error) {
	switch {
	case errors.Is(err, model.ErrGoogleDisabled):
		apierr.Write(w, r, apierr.GoogleDisabled())
	case errors.Is(err, context.DeadlineExceeded):
		log.ErrorContext(r.Context(), action+" timed out", logger.Err(err))
		apierr.Write(w, r, apierr.Unavailable("request timed out"))
	default:
		log.ErrorContext(r.Context(), action+" failed", logger.Err(err))
		apierr.Write(w, r, apierr.Internal())
	}
}

// googleRedirectURL adds google=<result> to the configured frontend URL and
// keeps its existing query parameters.
func (s *Server) googleRedirectURL(log *slog.Logger, result string) string {
	raw := s.googleRedirect
	if raw == "" {
		raw = fallbackGoogleRedirect
	}
	target, err := url.Parse(raw)
	if err != nil {
		log.Error("invalid google frontend redirect", logger.Err(err))
		target = &url.URL{Path: fallbackGoogleRedirect}
	}
	query := target.Query()
	query.Set("google", result)
	target.RawQuery = query.Encode()
	return target.String()
}
