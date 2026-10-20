package service

import (
	"bytes"
	"context"
	"encoding/json"
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

// STT converts an audio stream into text.
type STT interface {
	Transcribe(ctx context.Context, file io.Reader) (string, error)
}

// TaskProcess turns a text or audio command into an AI-service request.
type TaskProcess struct {
	rec   STT
	draft DraftStore
	aiURL string
}

// NewTaskProcess constructs a task processor with the given dependencies.
func NewTaskProcess(rec STT, draft DraftStore, aiURL string) *TaskProcess {
	return &TaskProcess{
		rec:   rec,
		draft: draft,
		aiURL: aiURL,
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

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return result, fmt.Errorf("request AI service: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("AI service returned status %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return result, fmt.Errorf("decode AI response: %w", err)
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
			return "", req.UserId, req.TaskId,
				fmt.Errorf("transcribe audio: %w", err)
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
) *ai.JobRequest {
	return &ai.JobRequest{
		UserId:      userID,
		TaskId:      taskID,
		Text:        text,
		Context:     taskContext,
		RequestTime: time.Now(),
		Timezone:    "Europe/Moscow",
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

	case "need_clarification":
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

	var taskContext ai.JobRequestContext

	if taskID == nil {
		id := uuid.New()
		taskID = &id
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
	)

	result, err := createReq(ctx, t.aiURL, aiReq)
	if err != nil {
		return nil, fmt.Errorf("create AI request: %w", err)
	}

	return t.processAIResult(result)
}
