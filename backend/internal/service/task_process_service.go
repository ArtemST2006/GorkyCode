package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/http-server/handlers"
	"gorkycode/backend/internal/model"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	api "gorkycode/backend/internal/generated"

	openapitypes "github.com/oapi-codegen/runtime/types"
)

// DraftStore reads the saved dialogue context of a task.
type DraftStore interface {
	GetDraft(ctx context.Context, key string) (string, error)
}

// TimezoneStore supplies the timezone saved during registration.
type TimezoneStore interface {
	GetUserTimezone(ctx context.Context, userID uuid.UUID) (string, error)
}

// STT converts an audio stream into text.
type STT interface {
	Transcribe(ctx context.Context, file io.Reader) (string, error)
}

// TaskProcess turns a text or audio command into an AI-service request.
type TaskProcess struct {
	rec       STT
	draft     DraftStore
	timezones TimezoneStore
	aiURL     string
}

// NewTaskProcess constructs a task processor with the given dependencies.
func NewTaskProcess(rec STT, draft DraftStore, timezones TimezoneStore, aiURL string) *TaskProcess {
	return &TaskProcess{
		rec:       rec,
		draft:     draft,
		timezones: timezones,
		aiURL:     aiURL,
	}
}

// createReq отправляет запрос AI-сервису.
func createReq(ctx context.Context, aiURL string, req *ai.JobRequest) (model.AiRes, error) {
	var result model.AiRes

	body, err := json.Marshal(req)
	if err != nil {
		return result, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(aiURL, "/")+"/api/job",
		bytes.NewReader(body),
	)
	if err != nil {
		return result, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return result, fmt.Errorf("%w: request AI service: %v", model.ErrGatewayTimeout, err)
		}
		return result, fmt.Errorf("%w: request AI service: %v", model.ErrBadGateway, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusServiceUnavailable:
			return result, fmt.Errorf("%w: AI service returned status %d", model.ErrServiceUnavailable, resp.StatusCode)
		case http.StatusGatewayTimeout:
			return result, fmt.Errorf("%w: AI service returned status %d", model.ErrGatewayTimeout, resp.StatusCode)
		default:
			return result, fmt.Errorf("%w: AI service returned status %d", model.ErrBadGateway, resp.StatusCode)
		}
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return result, fmt.Errorf("%w: decode AI response: %v", model.ErrBadGateway, err)
	}

	return result, nil
}

func (t *TaskProcess) extractText(
	ctx context.Context,
	req any,
) (string, openapitypes.UUID, *openapitypes.UUID, error) {
	switch req := req.(type) {
	case *api.TaskTextRequest:
		return req.Text, req.UserId, req.TaskId, nil

	case *api.TaskAudioRequest:
		file, err := req.File.Reader()
		if err != nil {
			return "", req.UserId, req.TaskId,
				fmt.Errorf("read audio file: %w", err)
		}
		defer func() {
			_ = file.Close()
		}()

		text, err := t.rec.Transcribe(ctx, file)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return "", req.UserId, req.TaskId, fmt.Errorf("%w: transcribe audio: %v", model.ErrGatewayTimeout, err)
			}
			return "", req.UserId, req.TaskId,
				fmt.Errorf("%w: transcribe audio: %v", model.ErrBadGateway, err)
		}

		return text, req.UserId, req.TaskId, nil

	default:
		return "", openapitypes.UUID{}, nil,
			fmt.Errorf("unsupported request type %T", req)
	}
}

// getTaskContext получает контекст существующего черновика.
func (t *TaskProcess) getTaskContext(
	ctx context.Context,
	userID openapitypes.UUID,
	taskID *openapitypes.UUID,
) (ai.JobRequestContext, error) {
	var taskContext ai.JobRequestContext

	if taskID == nil {
		return taskContext, nil
	}

	key := fmt.Sprintf("task_draft:%s:%s", userID, *taskID)

	raw, err := t.draft.GetDraft(ctx, key)
	if err != nil {
		return taskContext, fmt.Errorf("get draft: %w", err)
	}

	if err := json.Unmarshal([]byte(raw), &taskContext); err != nil {
		return taskContext, fmt.Errorf("decode draft context: %w", err)
	}

	return taskContext, nil
}

// prepareAIRequest собирает запрос для AI-сервиса.
func prepareAIRequest(
	userID openapitypes.UUID,
	taskID openapitypes.UUID,
	text string,
	taskContext ai.JobRequestContext,
	timezone string,
	location *time.Location,
) *ai.JobRequest {
	return &ai.JobRequest{
		UserId:      userID,
		TaskId:      taskID,
		Text:        text,
		Context:     taskContext,
		RequestTime: time.Now().In(location),
		Timezone:    timezone,
	}
}

// processAIResult обрабатывает результат AI-сервиса.
func (t *TaskProcess) processAIResult(
	result model.AiRes,
) (handlers.TaskResult, error) {
	switch result.Status {
	case "ready":
		switch result.Intent {
		case "create_event":
			// TODO: вызвать обработчик создания события.
			panic("not implemented")

		case "list_events":
			// TODO: вызвать обработчик получения списка событий.
			panic("not implemented")

		case "search_events":
			// TODO: вызвать обработчик поиска событий.
			panic("not implemented")

		default:
			return nil, fmt.Errorf("invalid intent %s", result.Intent)
		}

	case "needs_clarification":
		// TODO: вернуть клиенту сообщение с просьбой уточнить данные.
		panic("not implemented")

	default:
		return nil, fmt.Errorf("invalid AI status %s", result.Status)
	}
}

// Process handles a text or audio command and returns the HTTP-facing result.
func (t *TaskProcess) Process(
	ctx context.Context,
	req handlers.TaskRequest,
) (handlers.TaskResult, error) {
	text, userID, taskID, err := t.extractText(ctx, req)
	if err != nil {
		return nil, err
	}
	if userID == uuid.Nil || strings.TrimSpace(text) == "" {
		return nil, errors.New("invalid task request")
	}
	timezone, err := t.timezones.GetUserTimezone(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user timezone: %w", err)
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid user timezone: %w", err)
	}

	var taskContext ai.JobRequestContext

	if taskID == nil {
		id := uuid.New()
		taskID = &id
		if err := taskContext.FromEmptyContext(ai.EmptyContext{}); err != nil {
			return nil, fmt.Errorf("prepare empty context: %w", err)
		}
	} else {
		taskContext, err = t.getTaskContext(ctx, userID, taskID)
		if err != nil {
			return nil, err
		}
	}

	aiReq := prepareAIRequest(
		userID,
		*taskID,
		text,
		taskContext,
		timezone,
		location,
	)

	result, err := createReq(ctx, t.aiURL, aiReq)
	if err != nil {
		return nil, fmt.Errorf("create AI request: %w", err)
	}

	if err := validateAIResult(result, aiReq); err != nil {
		return nil, fmt.Errorf("%w: %v", model.ErrBadGateway, err)
	}
	return t.processAIResult(result)
}
