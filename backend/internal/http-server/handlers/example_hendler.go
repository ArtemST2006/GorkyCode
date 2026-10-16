package handlers

import (
	"context"
	"gorkycode/backend/internal/http-server/apierr"
	"gorkycode/backend/internal/lib/logger"
	"log/slog"
	"net/http"

	"github.com/go-chi/render"

	api "gorkycode/backend/internal/generated"
)

// Пример нового хэндлера:
// 1) сначала описать endpoint и ответ в
// contracts/openapi/backend.openapi.yaml (если я успею сделать спецификацию до того как вы начнёте делать таску, то я напишу об этом в jira)
// 2) выполнить make generate,

// Auth предоставляет методы для работы с Auth cервисами.
//
// 3) писываем интерфейс в месте его использования
type Auth interface {
	SignIn(context.Context) (bool, error)
}

// не забыть зарегестрировать новый интерфейс в Server

// SignIn оказывает HTTP → service → PostgreSQL
func (s *Server) SignIn(w http.ResponseWriter, r *http.Request) {
	// логгирование с инфой о месте
	const op = "handlers.example.SignIn"
	log := s.log.With(slog.String("op", op))

	reached, err := s.services.SignIn(r.Context())
	if err != nil {
		log.ErrorContext(r.Context(), "example sign-in failed", logger.Err(err))
		apierr.Write(w, r, apierr.Internal()) // оборот в ошибку в формате RFCхуйхуйхуй типо круто
		return
	}

	log.InfoContext(r.Context(), "example sign-in completed",
		slog.Int("status", http.StatusOK),
		slog.Bool("repository_reached", reached),
	)
	render.Status(r, http.StatusOK)
	render.JSON(w, r, api.ExampleSignInResponse{
		RepositoryReached: reached,
		Authenticated:     false,
	})
}

// Новые хэндлеры не забыть зарегистрировать в contracts/openapi/backend.openapi.yaml(если буду успевать, то буду регистрировать сам и писать в таску)
