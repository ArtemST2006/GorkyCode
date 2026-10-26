package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorkycode/backend/internal/http-server/apierr"
	"gorkycode/backend/internal/lib/logger"
	"gorkycode/backend/internal/model"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	api "gorkycode/backend/internal/generated"

	"github.com/google/uuid"
)

const maxTaskProcessReadSize = 20 << 20 // 20 MiB

// TaskProcess handles text and audio commands.
type TaskProcess interface {
	Process(ctx context.Context, req model.TaskRequest) (*model.TaskResult, error)
}

func parseTaskAudioRequest(r *http.Request) (*api.TaskAudioRequest, error) {
	var req api.TaskAudioRequest
	if r.MultipartForm == nil {
		return nil, errors.New("missing multipart body")
	}
	for name, values := range r.MultipartForm.Value {
		if (name != "user_id" && name != "task_id") || len(values) != 1 {
			return nil, errors.New("expected unique user_id/task_id fields and exactly one file")
		}
	}
	if len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["file"]) != 1 {
		return nil, errors.New("expected exactly one file")
	}
	rawUser := r.MultipartForm.Value["user_id"]
	if len(rawUser) != 1 {
		return nil, errors.New("missing 'user_id'")
	}
	userValue := rawUser[0]
	if userValue == "" {
		return nil, errors.New("missing 'user_id'")
	}
	userID, err := uuid.Parse(userValue)
	if err != nil {
		return nil, fmt.Errorf("invalid 'user_id': %w", err)
	}
	if userID == uuid.Nil {
		return nil, errors.New("invalid 'user_id'")
	}
	req.UserId = userID

	if values := r.MultipartForm.Value["task_id"]; len(values) == 1 {
		rawTask := values[0]
		taskID, err := uuid.Parse(rawTask)
		if err != nil {
			return nil, fmt.Errorf("invalid 'task_id': %w", err)
		}
		if taskID == uuid.Nil {
			return nil, errors.New("invalid 'task_id'")
		}
		req.TaskId = &taskID
	}

	if r.MultipartForm == nil || len(r.MultipartForm.File["file"]) == 0 {
		return nil, errors.New("missing 'file'")
	}
	header := r.MultipartForm.File["file"][0]

	if err := validateFile(header); err != nil {
		return nil, err
	}

	req.File.InitFromMultipart(header)

	return &req, nil
}

func writeJSON[T any](w http.ResponseWriter, status int, v T) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func writeTaskResult(w http.ResponseWriter, r *http.Request, log *slog.Logger, result *model.TaskResult) {
	if result == nil {
		log.ErrorContext(r.Context(), "Missing task result")
		apierr.Write(w, r, apierr.TaskInternal())
		return
	}

	var status int
	switch result.Body.(type) {
	case *api.TaskCancelledResponse:
		status = http.StatusOK
	case *api.TaskClarificationResponse:
		status = http.StatusOK
	case *api.TaskCollectionResponse:
		if result.Intent != "list_events" && result.Intent != "search_events" {
			log.ErrorContext(r.Context(), "Invalid intent for collection", slog.String("intent", result.Intent))
			apierr.Write(w, r, apierr.TaskInternal())
			return
		}
		status = http.StatusOK
	case *api.TaskCreatedResponse:
		if result.Intent != "create_event" {
			log.ErrorContext(r.Context(), "Invalid intent for created event", slog.String("intent", result.Intent))
			apierr.Write(w, r, apierr.TaskInternal())
			return
		}
		status = http.StatusCreated
	case *api.TaskDeleteCandidatesResponse:
		if result.Intent != "on_delete" {
			log.ErrorContext(r.Context(), "Invalid intent for delete candidates", slog.String("intent", result.Intent))
			apierr.Write(w, r, apierr.TaskInternal())
			return
		}
		status = http.StatusOK
	case *api.TasksDeletedResponse:
		if result.Intent != "on_delete" {
			log.ErrorContext(r.Context(), "Invalid intent for deleted tasks", slog.String("intent", result.Intent))
			apierr.Write(w, r, apierr.TaskInternal())
			return
		}
		status = http.StatusOK
	case *api.FrontendUpdateCandidates, *api.FrontendUpdated:
		if result.Intent != "on_update" {
			log.ErrorContext(r.Context(), "Invalid intent for update response", slog.String("intent", result.Intent))
			apierr.Write(w, r, apierr.TaskInternal())
			return
		}
		status = http.StatusOK
	default:
		log.ErrorContext(r.Context(), "Unexpected task result type", slog.Any("type", result.Body))
		apierr.Write(w, r, apierr.TaskInternal())
		return
	}
	if result.Intent != "create_event" && result.Intent != "list_events" &&
		result.Intent != "search_events" && result.Intent != "on_delete" && result.Intent != "on_update" {
		log.ErrorContext(r.Context(), "Unexpected task intent", slog.String("intent", result.Intent))
		apierr.Write(w, r, apierr.TaskInternal())
		return
	}

	w.Header().Set("X-Task-Intent", result.Intent)
	if err := writeJSON(w, status, result.Body); err != nil {
		log.ErrorContext(r.Context(), "Error encoding task response", logger.Err(err))
	}
}

func validateFile(file *multipart.FileHeader) error {
	ext := strings.ToLower(filepath.Ext(file.Filename))

	switch ext {
	case ".wav", ".wave":
		return nil
	default:
		return errors.New("invalid file extension")
	}
}

func writeTaskError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, model.ErrDraftNotFound), errors.Is(err, model.ErrUserNotFound):
		apierr.Write(w, r, apierr.NotFound("Запрос или пользователь не найден."))
	case errors.Is(err, model.ErrStaleSelection):
		apierr.Write(w, r, apierr.Conflict("Выбранные записи устарели или больше недоступны."))
	case errors.Is(err, model.ErrDialogueBusy):
		apierr.Write(w, r, apierr.Conflict("Предыдущий запрос этого диалога ещё обрабатывается. Дождитесь ответа."))
	case errors.Is(err, model.ErrUnsupportedIntent):
		apierr.Write(w, r, apierr.InvalidRequest("Пока можно создавать, искать, показывать, изменять и удалять записи."))
	case errors.Is(err, model.ErrGatewayTimeout):
		apierr.Write(w, r, apierr.GatewayTimeout())
	case errors.Is(err, model.ErrBadGateway), errors.Is(err, model.ErrInvalidAction):
		apierr.Write(w, r, apierr.BadGateway())
	case errors.Is(err, model.ErrServiceUnavailable), errors.Is(err, context.DeadlineExceeded):
		apierr.Write(w, r, apierr.Unavailable("Сервис временно недоступен."))
	default:
		apierr.Write(w, r, apierr.TaskInternal())
	}
}

// ProcessTask handles text (JSON) and voice (multipart WAV) task commands.
func (s *Server) ProcessTask(w http.ResponseWriter, r *http.Request) {
	const op = "task.aggregatorHandler"
	log := s.log.With(slog.String("op", op))
	ctx := r.Context()
	session, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxTaskProcessReadSize)

	ct := r.Header.Get("Content-Type")
	if ct == "" {
		log.ErrorContext(ctx, "Missing required header 'Content-Type'")
		apierr.Write(w, r, apierr.InvalidRequest("Missing required header 'Content-Type'"))
		return
	}

	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		log.ErrorContext(ctx, "Invalid 'Content-Type'", logger.Err(err))
		apierr.Write(w, r, apierr.InvalidRequest("Invalid 'Content-Type'"))
		return
	}

	switch mediaType {
	case "application/json":
		var req api.TaskTextRequest

		if !decodeJSON(w, r, &req) {
			return
		}
		if req.UserId == uuid.Nil || (req.TaskId != nil && *req.TaskId == uuid.Nil) || strings.TrimSpace(req.Text) == "" {
			apierr.Write(w, r, apierr.InvalidRequest("user_id, task_id или text не соответствуют контракту"))
			return
		}
		if req.UserId != session.userID {
			apierr.Write(w, r, apierr.Forbidden("user_id не совпадает с владельцем сессии"))
			return
		}

		result, err := s.services.Process(ctx, &req)
		if err != nil {
			log.ErrorContext(ctx, "Error processing task", logger.Err(err))
			writeTaskError(w, r, err)
			return
		}

		writeTaskResult(w, r, log, result)

	case "multipart/form-data":
		if err := r.ParseMultipartForm(maxTaskProcessReadSize); err != nil {
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				apierr.Write(w, r, apierr.PayloadTooLarge())
				return
			}
			log.ErrorContext(ctx, "Failed to parse multipart form", logger.Err(err))
			apierr.Write(w, r, apierr.InvalidRequest("Invalid multipart body"))
			return
		}
		defer func() {
			_ = r.MultipartForm.RemoveAll()
		}()

		req, err := parseTaskAudioRequest(r)
		if err != nil {
			log.ErrorContext(ctx, "Invalid task audio request", logger.Err(err))
			apierr.Write(w, r, apierr.InvalidRequest(err.Error()))
			return
		}
		if req.UserId != session.userID {
			apierr.Write(w, r, apierr.Forbidden("user_id не совпадает с владельцем сессии"))
			return
		}

		result, err := s.services.Process(ctx, req)
		if err != nil {
			log.ErrorContext(ctx, "Error processing task audio request", logger.Err(err))
			writeTaskError(w, r, err)
			return
		}

		writeTaskResult(w, r, log, result)

	default:
		log.ErrorContext(ctx, "Unsupported 'Content-Type'", slog.String("type", mediaType))
		apierr.Write(w, r, apierr.InvalidRequest("Unsupported Content-Type"))
	}
}
