// Package handlers implements the backend HTTP API defined in OpenAPI.
package handlers

import (
	"log/slog"

	api "gorkycode/backend/internal/generated"
)

// Services lists the operations used by HTTP handlers.
//
// Здесь собираются все сервисы воедино
type Services interface {
	ReadinessService
	Auth
	TaskProcess
	TaskList
	Calendar
}

// Server implements the HTTP operations from the OpenAPI contract.
type Server struct {
	api.Unimplemented

	log      *slog.Logger
	services Services
	// googleRedirect is where the Google OAuth callback sends the browser.
	googleRedirect string
}

// Option configures optional Server settings.
type Option func(*Server)

// WithGoogleFrontendRedirect sets the frontend URL that the Google OAuth
// callback redirects to. It comes only from configuration.
func WithGoogleFrontendRedirect(rawURL string) Option {
	return func(s *Server) { s.googleRedirect = rawURL }
}

var _ api.ServerInterface = (*Server)(nil)

// NewServer constructs HTTP handlers with all application services.
func NewServer(log *slog.Logger, services Services, opts ...Option) *Server {
	s := &Server{log: log, services: services}
	for _, opt := range opts {
		opt(s)
	}
	return s
}
