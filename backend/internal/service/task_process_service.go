package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"
	services "gorkycode/backend/internal/service/subservice"
	"gorkycode/backend/internal/service/utils"
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
	PutDraft(ctx context.Context, key, value string) error
}

// TimezoneStore supplies the timezone saved during registration.
type TimezoneStore interface {
	GetUserTimezone(ctx context.Context, userID uuid.UUID) (string, error)
}

// STT converts an audio stream into text.
type STT interface {
	Transcribe(ctx context.Context, file io.Reader) (string, error)
}

// TaskCreator persists a task assembled from a validated AI action.
type TaskCreator interface {
	Create(ctx context.Context, cmd services.CreateTaskCommand) (*model.Task, error)
}

// EventReader lists and searches the user's stored events.
type EventReader interface {
	ListEvents(ctx context.Context, userID uuid.UUID, action json.RawMessage) (api.TaskCollection, error)
	SearchEvents(ctx context.Context, userID uuid.UUID, action json.RawMessage) (api.TaskCollection, error)
}

// DeleteService implements the on_delete dialog: candidate search and
// confirmed deletion over the user's storage.
type DeleteService interface {
	FindCandidates(ctx context.Context, userID uuid.UUID, action json.RawMessage) (services.DeleteCandidates, error)
	DeleteConfirmed(ctx context.Context, userID uuid.UUID, action json.RawMessage, saved []ai.DeleteCandidate) ([]api.TaskItem, error)
}

// TaskProcess turns a text or audio command into an AI-service request.
type TaskProcess struct {
	rec       STT
	draft     DraftStore
	timezones TimezoneStore
	creator   TaskCreator
	reader    EventReader
	deleter   DeleteService
	aiURL     string
}

// NewTaskProcess constructs a task processor with the given dependencies.
func NewTaskProcess(rec STT, draft DraftStore, timezones TimezoneStore, creator TaskCreator, reader EventReader, deleter DeleteService, aiURL string) *TaskProcess {
	return &TaskProcess{
		rec:       rec,
		draft:     draft,
		timezones: timezones,
		creator:   creator,
		reader:    reader,
		deleter:   deleter,
		aiURL:     aiURL,
	}
}

// aiClient создаёт клиента AI-сервиса с единым таймаутом.
func aiClient(aiURL string) (*ai.Client, error) {
	client, err := ai.NewClient(aiURL, ai.WithHTTPClient(&http.Client{Timeout: 20 * time.Second}))
	if err != nil {
		return nil, fmt.Errorf("create AI client: %w", err)
	}
	return client, nil
}

// mapAIStatus переводит не-200 статус AI-сервиса в доменную ошибку.
func mapAIStatus(status int) error {
	switch status {
	case http.StatusServiceUnavailable:
		return fmt.Errorf("%w: AI service returned status %d", model.ErrServiceUnavailable, status)
	case http.StatusGatewayTimeout:
		return fmt.Errorf("%w: AI service returned status %d", model.ErrGatewayTimeout, status)
	default:
		return fmt.Errorf("%w: AI service returned status %d", model.ErrBadGateway, status)
	}
}

// createReq отправляет запрос AI-сервису.
func createReq(ctx context.Context, aiURL string, req *ai.JobRequest) (model.AiRes, error) {
	var result model.AiRes
	client, err := aiClient(aiURL)
	if err != nil {
		return result, err
	}
	resp, err := client.ProcessJob(ctx, *req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return result, fmt.Errorf("%w: request AI service: %w", model.ErrGatewayTimeout, err)
		}
		return result, fmt.Errorf("%w: request AI service: %w", model.ErrBadGateway, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return result, mapAIStatus(resp.StatusCode)
	}

	decoder := json.NewDecoder(resp.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, fmt.Errorf("%w: decode AI response: %w", model.ErrBadGateway, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return result, fmt.Errorf("%w: AI response contains trailing data", model.ErrBadGateway)
	}

	// on_delete returns its intent only in the X-Task-Intent header; old
	// intents keep the JSON field. A header with anything but on_delete, or
	// both header and JSON intent, breaks the contract.
	headerIntent := strings.TrimSpace(resp.Header.Get("X-Task-Intent"))
	if headerIntent != "" {
		if headerIntent != "on_delete" {
			return result, fmt.Errorf("%w: unexpected header intent %q", model.ErrBadGateway, headerIntent)
		}
		if result.Intent != "" && result.Intent != headerIntent {
			return result, fmt.Errorf("%w: AI response intent conflicts with header", model.ErrBadGateway)
		}
		result.Intent = headerIntent
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
				return "", req.UserId, req.TaskId, fmt.Errorf("%w: transcribe audio: %w", model.ErrGatewayTimeout, err)
			}
			return "", req.UserId, req.TaskId,
				fmt.Errorf("%w: transcribe audio: %w", model.ErrBadGateway, err)
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

type createAction struct {
	RecordType string `json:"record_type"`
	Title      string `json:"title"`
	Date       string `json:"date"`
	Time       string `json:"time"`
}

// parseCreateTaskCommand converts an AI action into a domain command in the user's timezone.
func parseCreateTaskCommand(result model.AiRes, location *time.Location) (services.CreateTaskCommand, error) {
	var action createAction
	if err := json.Unmarshal(result.Action, &action); err != nil {
		return services.CreateTaskCommand{}, fmt.Errorf("decode create action: %w", err)
	}

	date, err := time.ParseInLocation("2006-01-02", action.Date, location)
	if err != nil {
		return services.CreateTaskCommand{}, fmt.Errorf("parse task date: %w", err)
	}

	clock, err := time.ParseInLocation("15:04", action.Time, location)
	if err != nil {
		return services.CreateTaskCommand{}, fmt.Errorf("parse task time: %w", err)
	}

	return services.CreateTaskCommand{
		UserID:     result.UserID,
		RecordType: action.RecordType,
		Title:      action.Title,
		Date:       date,
		Time:       clock,
	}, nil
}

// processAIResult обрабатывает результат AI-сервиса.
func (t *TaskProcess) processAIResult(
	ctx context.Context,
	result model.AiRes,
	location *time.Location,
) (*model.TaskResult, error) {
	var message string
	if err := json.Unmarshal(result.Message, &message); err != nil || strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("%w: invalid AI message", model.ErrBadGateway)
	}

	switch result.Status {
	case "ready":
		switch result.Intent {
		case "create_event":
			if t.creator == nil {
				return nil, errors.New("event creation handler is not configured")
			}
			cmd, err := parseCreateTaskCommand(result, location)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", model.ErrBadGateway, err)
			}

			task, err := t.creator.Create(ctx, cmd)
			if err != nil {
				return nil, fmt.Errorf("create task: %w", err)
			}
			if task == nil || task.Date == nil || task.Time == nil {
				return nil, errors.New("created task is missing date or time")
			}

			return &model.TaskResult{Intent: result.Intent, Body: &api.TaskCreatedResponse{
				Data: api.TaskCreatedData{
					Event: api.TaskCreatedEvent{
						CreatedAt:  task.CreatedAt,
						Date:       openapitypes.Date{Time: *task.Date},
						Id:         task.ID,
						RecordType: api.TaskCreatedEventRecordType(cmd.RecordType),
						Status:     api.TaskCreatedEventStatus(task.Status),
						Time:       *task.Time,
						Title:      task.Title,
						UserId:     task.UserID,
					},
				},
				Message: message,
				Status:  api.TaskCreatedResponseStatusOk,
				TaskId:  result.TaskID,
				UserId:  result.UserID,
			}}, nil

		case "list_events":
			if t.reader == nil {
				return nil, errors.New("event listing handler is not configured")
			}
			collection, err := t.reader.ListEvents(ctx, result.UserID, result.Action)
			if err != nil {
				return nil, fmt.Errorf("list events: %w", err)
			}
			return &model.TaskResult{Intent: result.Intent, Body: &api.TaskCollectionResponse{
				Data:    collection,
				UserId:  result.UserID,
				TaskId:  result.TaskID,
				Status:  api.TaskCollectionResponseStatusOk,
				Message: message,
			}}, nil

		case "search_events":
			if t.reader == nil {
				return nil, errors.New("event search handler is not configured")
			}
			collection, err := t.reader.SearchEvents(ctx, result.UserID, result.Action)
			if err != nil {
				return nil, fmt.Errorf("search events: %w", err)
			}
			return &model.TaskResult{Intent: result.Intent, Body: &api.TaskCollectionResponse{
				Data:    collection,
				UserId:  result.UserID,
				TaskId:  result.TaskID,
				Status:  api.TaskCollectionResponseStatusOk,
				Message: message,
			}}, nil

		case "on_delete": // on_delete ищется в заголовке X-Task-Intent
			if t.deleter == nil {
				return nil, errors.New("delete handler is not configured")
			}
			return t.processDeleteResult(ctx, result)

		default:
			return nil, fmt.Errorf("invalid intent %s", result.Intent)
		}

	case "needs_clarification":
		key := fmt.Sprintf("task_draft:%s:%s", result.UserID, result.TaskID)
		if err := t.draft.PutDraft(ctx, key, string(result.Context)); err != nil {
			return nil, fmt.Errorf("save draft context: %w", err)
		}
		return &model.TaskResult{Intent: result.Intent, Body: &api.TaskClarificationResponse{
			UserId:  result.UserID,
			TaskId:  result.TaskID,
			Status:  api.NeedsClarification,
			Message: message,
		}}, nil

	default:
		return nil, fmt.Errorf("invalid AI status %s", result.Status)
	}
}

// processDeleteResult исполняет проверенную фазу действия on_delete.
// find_candidates только ищет и сохраняет список кандидатов; удаление
// возможно лишь после подтверждения пользователя (delete_confirmed).
func (t *TaskProcess) processDeleteResult(
	ctx context.Context,
	result model.AiRes,
) (*model.TaskResult, error) {
	var envelope struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(result.Action, &envelope); err != nil {
		return nil, fmt.Errorf("%w: decode delete action: %w", model.ErrBadGateway, err)
	}

	candidatesKey := fmt.Sprintf("task_candidates:%s:%s", result.UserID, result.TaskID)
	draftKey := fmt.Sprintf("task_draft:%s:%s", result.UserID, result.TaskID)

	switch envelope.Phase {
	case "find_candidates":
		set, err := t.deleter.FindCandidates(ctx, result.UserID, result.Action)
		if err != nil {
			return nil, err
		}

		stored, err := json.Marshal(set.Stored)
		if err != nil {
			return nil, fmt.Errorf("encode delete candidates: %w", err)
		}
		if err := t.draft.PutDraft(ctx, candidatesKey, string(stored)); err != nil {
			return nil, fmt.Errorf("save delete candidates: %w", err)
		}
		if err := t.saveDeleteContext(ctx, result, draftKey, set.Draft, set.Stored); err != nil {
			return nil, err
		}

		return &model.TaskResult{Intent: result.Intent, Body: &api.TaskDeleteCandidatesResponse{
			Data:    api.TaskDeleteCandidatesData{Candidates: set.Items},
			Message: deleteCandidatesMessage(len(set.Items)),
			Status:  api.Candidates,
			TaskId:  result.TaskID,
			UserId:  result.UserID,
		}}, nil

	case "delete_confirmed":
		raw, err := t.draft.GetDraft(ctx, candidatesKey)
		if errors.Is(err, model.ErrDraftNotFound) {
			return nil, model.ErrStaleSelection
		}
		if err != nil {
			return nil, fmt.Errorf("get delete candidates: %w", err)
		}
		var saved []ai.DeleteCandidate
		if decodeErr := json.Unmarshal([]byte(raw), &saved); decodeErr != nil {
			return nil, fmt.Errorf("decode delete candidates: %w", decodeErr)
		}
		items, err := t.deleter.DeleteConfirmed(ctx, result.UserID, result.Action, saved)
		if err != nil {
			return nil, err
		}

		return &model.TaskResult{Intent: result.Intent, Body: &api.TasksDeletedResponse{
			Data:    api.TasksDeletedData{DeletedEvents: items},
			Message: deleteDoneMessage(len(items)),
			Status:  api.Deleted,
			TaskId:  result.TaskID,
			UserId:  result.UserID,
		}}, nil

	default:
		return nil, fmt.Errorf("%w: unknown delete phase %q", model.ErrBadGateway, envelope.Phase)
	}
}

// saveDeleteContext собирает контекст следующего запроса AI-сервису:
// recent_messages текущего ответа, признаки поиска и показанные кандидаты.
func (t *TaskProcess) saveDeleteContext(
	ctx context.Context,
	result model.AiRes,
	draftKey string,
	draft ai.DeleteSearchDraft,
	stored []ai.DeleteCandidate,
) error {
	var ready ai.ReadyContext
	if err := json.Unmarshal(result.Context, &ready); err != nil {
		return fmt.Errorf("%w: decode delete context: %w", model.ErrBadGateway, err)
	}
	deleteContext := ai.DeleteCandidateContext{
		RecentMessages: ready.RecentMessages,
		Draft:          draft,
		Candidates:     stored,
	}
	raw, err := json.Marshal(deleteContext)
	if err != nil {
		return fmt.Errorf("encode delete context: %w", err)
	}
	if err := t.draft.PutDraft(ctx, draftKey, string(raw)); err != nil {
		return fmt.Errorf("save delete context: %w", err)
	}
	return nil
}

// deleteCandidatesMessage формирует ответ после поиска кандидатов.
func deleteCandidatesMessage(count int) string {
	if count == 0 {
		return "Подходящих записей не нашлось. Уточните запрос."
	}
	return fmt.Sprintf("Поиск завершён. Найдено записей: %d. Какие удалить?", count)
}

// deleteDoneMessage формирует ответ после фактического удаления.
func deleteDoneMessage(count int) string {
	return fmt.Sprintf("Успешно удалено записей: %d.", count)
}

// Process handles a text or audio command and returns the HTTP-facing result.
func (t *TaskProcess) Process(
	ctx context.Context,
	req model.TaskRequest,
) (*model.TaskResult, error) {
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
		if err = taskContext.FromEmptyContext(ai.EmptyContext{}); err != nil {
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

	if err := utils.ValidateAIResult(result, aiReq); err != nil {
		return nil, fmt.Errorf("%w: %w", model.ErrBadGateway, err)
	}
	return t.processAIResult(ctx, result, location)
}
