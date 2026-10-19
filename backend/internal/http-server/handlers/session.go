package handlers

import (
	"errors"
	"gorkycode/backend/internal/http-server/apierr"
	"gorkycode/backend/internal/lib/logger"
	"gorkycode/backend/internal/model"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// session is the authenticated caller of a request.
type session struct {
	userID uuid.UUID
	token  string
}

// authenticate resolves the bearer token of the request to its session.
// It writes 401 and returns false when the token is missing or not valid.
// Call it first in every handler whose operation has security: bearerAuth.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (session, bool) {
	token, ok := bearerToken(r)
	if !ok {
		apierr.Write(w, r, apierr.Unauthorized("missing bearer token"))
		return session{}, false
	}

	userID, err := s.services.Authenticate(r.Context(), token)
	if err != nil {
		if errors.Is(err, model.ErrSessionNotFound) {
			apierr.Write(w, r, apierr.Unauthorized("session expired or ended"))
			return session{}, false
		}
		s.log.ErrorContext(r.Context(), "authentication failed",
			slog.String("op", "handlers.session.authenticate"),
			logger.Err(err),
		)
		apierr.Write(w, r, apierr.Internal())
		return session{}, false
	}
	return session{userID: userID, token: token}, true
}

// bearerToken extracts the token from "Authorization: Bearer <token>".
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}
