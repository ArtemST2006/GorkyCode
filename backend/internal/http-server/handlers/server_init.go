// Package handlers implements the backend HTTP API defined in OpenAPI.
package handlers

import (
	"log/slog"

	api "gorkycode/backend/internal/generated"
)

// Services lists the operations used by HTTP handlers.
type Services interface {
	ReadinessService
	Auth
}

// Server implements the HTTP operations from the OpenAPI contract.
type Server struct {
	api.Unimplemented

	log      *slog.Logger
	services Services
}

var _ api.ServerInterface = (*Server)(nil)

// NewServer constructs HTTP handlers with all application services.
func NewServer(log *slog.Logger, services Services) *Server {
	return &Server{log: log, services: services}
}
