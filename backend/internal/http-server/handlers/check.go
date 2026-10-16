package handlers

import (
	"context"
	"gorkycode/backend/internal/lib/logger"
	"log/slog"
	"net/http"

	"github.com/go-chi/render"

	api "gorkycode/backend/internal/generated"
)

// ReadinessService is the application operation used by the readiness handler.
type ReadinessService interface {
	Ready(context.Context) error
}

// Health reports whether the process is running (GET /health).
func (s *Server) Health(w http.ResponseWriter, r *http.Request) {
	s.log.InfoContext(r.Context(), "health check passed",
		slog.String("op", "handlers.check.Health"),
		slog.Int("status", http.StatusOK),
	)
	render.Status(r, http.StatusOK)
	render.JSON(w, r, api.HealthResponse{Service: "backend", Status: api.HealthResponseStatusOk})
}

// Ready checks application dependencies (GET /ready).
func (s *Server) Ready(w http.ResponseWriter, r *http.Request) {
	const op = "handlers.check.Ready"
	log := s.log.With(slog.String("op", op))

	if err := s.services.Ready(r.Context()); err != nil {
		log.ErrorContext(r.Context(), "readiness check failed", logger.Err(err))
		render.Status(r, http.StatusServiceUnavailable)
		render.JSON(w, r, api.HealthResponse{Service: "backend", Status: api.HealthResponseStatusUnavailable})
		return
	}

	log.InfoContext(r.Context(), "readiness check passed", slog.Int("status", http.StatusOK))
	render.Status(r, http.StatusOK)
	render.JSON(w, r, api.HealthResponse{Service: "backend", Status: api.HealthResponseStatusOk})
}
