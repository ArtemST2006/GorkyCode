package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorkycode/backend/internal/http-server/apierr"
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

// TaskResult is a response returned by the task processing service.
type TaskResult any

// TaskRequest is a parsed text or audio task request.
type TaskRequest any

// TaskProcess handles text and audio commands.
type TaskProcess interface {
	Process(ctx context.Context, req TaskRequest) (TaskResult, error)
}

func parseTaskAudioRequest(r *http.Request) (*api.TaskAudioRequest, error) {
	var req api.TaskAudioRequest
	rawUser := r.FormValue("user_id")
	if rawUser == "" {
		return nil, errors.New("missing 'user_id'")
	}
	userID, err := uuid.Parse(rawUser)
	if err != nil {
		return nil, fmt.Errorf("invalid 'user_id': %w", err)
	}
	req.UserId = userID

	if rawTask := r.FormValue("task_id"); rawTask != "" {
		taskID, err := uuid.Parse(rawTask)
		if err != nil {
			return nil, fmt.Errorf("invalid 'task_id': %w", err)
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

func validateFile(file *multipart.FileHeader) error {
	ext := strings.ToLower(filepath.Ext(file.Filename))

	switch ext {
	case ".wav", ".wave":
		return nil
	default:
		return errors.New("invalid file extension")
	}
}

// ProcessTask handles text (JSON) and voice (multipart WAV) task commands.
func (s *Server) ProcessTask(w http.ResponseWriter, r *http.Request) {
	const op = "task.aggregatorHandler"
	log := s.log.With(slog.String("op", op))
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, maxTaskProcessReadSize)

	ct := r.Header.Get("Content-Type")
	if ct == "" {
		log.ErrorContext(ctx, "Missing required header 'Content-Type'")
		apierr.Write(w, r, apierr.InvalidRequest("Missing required header 'Content-Type'"))
		return
	}

	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		log.ErrorContext(ctx, "Invalid 'Content-Type'", slog.Any("err", err))
		apierr.Write(w, r, apierr.InvalidRequest("Invalid 'Content-Type'"))
		return
	}

	switch mediaType {
	case "application/json":
		var req api.TaskTextRequest

		if !decodeJSON(w, r, &req) {
			return
		}

		result, err := s.services.Process(ctx, &req)
		if err != nil {
			log.ErrorContext(ctx, "Error processing task", slog.Any("err", err))
			apierr.Write(w, r, apierr.Internal())
			return
		}

		var encErr error
		switch res := result.(type) {
		case *api.TaskClarificationResponse:
			encErr = writeJSON(w, http.StatusOK, res)
		case *api.TaskCollectionResponse:
			encErr = writeJSON(w, http.StatusOK, res)
		case *api.TaskCreatedResponse:
			encErr = writeJSON(w, http.StatusCreated, res)
		default:
			log.ErrorContext(ctx, "Unexpected result type", slog.Any("type", res))
			apierr.Write(w, r, apierr.Internal())
			return
		}
		if encErr != nil {
			log.ErrorContext(ctx, "Error encoding response", slog.Any("err", encErr))
		}

	case "multipart/form-data":
		if err := r.ParseMultipartForm(maxTaskProcessReadSize); err != nil {
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				apierr.Write(w, r, apierr.InvalidRequest("Request body too large"))
				return
			}
			log.ErrorContext(ctx, "Failed to parse multipart form", slog.Any("err", err))
			apierr.Write(w, r, apierr.InvalidRequest("Invalid multipart body"))
			return
		}
		defer func() {
			_ = r.MultipartForm.RemoveAll()
		}()

		req, err := parseTaskAudioRequest(r)
		if err != nil {
			log.ErrorContext(ctx, "Invalid task audio request", slog.Any("err", err))
			apierr.Write(w, r, apierr.InvalidRequest(err.Error()))
			return
		}

		result, err := s.services.Process(ctx, req)
		if err != nil {
			log.ErrorContext(ctx, "Error processing task audio request", slog.Any("err", err))
			apierr.Write(w, r, apierr.Internal())
			return
		}

		var encErr error
		switch res := result.(type) {
		case *api.TaskClarificationResponse:
			encErr = writeJSON(w, http.StatusOK, res)
		case *api.TaskCollectionResponse:
			encErr = writeJSON(w, http.StatusOK, res)
		case *api.TaskCreatedResponse:
			encErr = writeJSON(w, http.StatusCreated, res)
		default:
			log.ErrorContext(ctx, "Unexpected result type", slog.Any("type", res))
			apierr.Write(w, r, apierr.Internal())
			return
		}
		if encErr != nil {
			log.ErrorContext(ctx, "Error encoding response", slog.Any("err", encErr))
		}

	default:
		log.ErrorContext(ctx, "Unsupported 'Content-Type'", slog.String("type", mediaType))
		w.WriteHeader(http.StatusUnsupportedMediaType)
	}
}
