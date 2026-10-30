package handlers

import (
	"context"
	"fmt"
	"gorkycode/backend/internal/lib/logger"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	api "gorkycode/backend/internal/generated"
	apierr "gorkycode/backend/internal/http-server/apierr"

	"github.com/go-chi/render"
	"github.com/google/uuid"
)

// TaskList is the read-only facade handlers use to show tasks.
type TaskList interface {
	ListAllTasks(ctx context.Context, userID uuid.UUID, query string, types []string, from, to *time.Time, limit, offset int) (api.TaskCollection, error)
}

// ListTasks returns the user's tasks for the screens that just need to show
// them. It is a thin read wrapper over the task storage and does not run the
// AI dialogue.
func (s *Server) ListTasks(w http.ResponseWriter, r *http.Request) {
	log := s.log.With(slog.String("op", "handlers.ListTasks"))
	session, ok := s.authenticate(w, r)
	if !ok {
		log.InfoContext(r.Context(), "authentication failed")
		return
	}

	q := r.URL.Query()
	if raw := q.Get("user_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, apierr.InvalidRequest("invalid user_id"))
			return
		}
		if id != session.userID {
			render.Status(r, http.StatusForbidden)
			render.JSON(w, r, apierr.Forbidden("user_id does not match the session"))
			return
		}
	}

	query := strings.TrimSpace(q.Get("query"))
	if query == "" {
		query = strings.TrimSpace(q.Get("q"))
	}

	var types []string
	for _, raw := range q["type"] {
		for piece := range strings.SplitSeq(raw, ",") {
			piece = strings.TrimSpace(piece)
			if piece == "" {
				continue
			}
			if piece != "meeting" && piece != "task" && piece != "reminder" {
				render.Status(r, http.StatusBadRequest)
				render.JSON(w, r, apierr.InvalidRequest("invalid type"))
				return
			}
			types = append(types, piece)
		}
	}

	var from, to *time.Time
	if raw := q.Get("from"); raw != "" {
		value, err := parseTaskDateFilter(raw)
		if err != nil {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, apierr.InvalidRequest("invalid from date"))
			return
		}
		from = &value
	}
	if raw := q.Get("to"); raw != "" {
		value, err := parseTaskDateFilter(raw)
		if err != nil {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, apierr.InvalidRequest("invalid to date"))
			return
		}
		to = &value
	}

	limit := 200
	if raw := q.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 500 {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, apierr.InvalidRequest("invalid limit"))
			return
		}
		limit = value
	}
	offsetVal := 0
	if raw := q.Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, apierr.InvalidRequest("invalid offset"))
			return
		}
		offsetVal = value
	}

	collection, err := s.services.ListAllTasks(r.Context(), session.userID, query, types, from, to, limit, offsetVal)
	if err != nil {
		log.ErrorContext(r.Context(), "list tasks failed", logger.Err(err))
		render.Status(r, http.StatusInternalServerError)
		render.JSON(w, r, apierr.Internal())
		return
	}
	items := collection.Items
	if items == nil {
		items = []api.TaskItem{}
	}

	response := api.TaskCollectionResponse{
		Status:  api.TaskCollectionResponseStatusOk,
		UserId:  session.userID,
		TaskId:  uuid.New(),
		Message: "ok",
		Data:    api.TaskCollection{Items: items},
	}
	render.Status(r, http.StatusOK)
	render.JSON(w, r, response)
}

func parseTaskDateFilter(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("empty")
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date")
}
