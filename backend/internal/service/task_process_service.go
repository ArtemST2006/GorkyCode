package service

import (
	"bytes"
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
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	api "gorkycode/backend/internal/generated"

	openapitypes "github.com/oapi-codegen/runtime/types"
)

// DraftStore reads the saved dialogue context of a task.
type DraftStore interface {
	GetDraft(ctx context.Context, key string) (string, error)
	PutDraft(ctx context.Context, key, value string) error
	DeleteDraft(ctx context.Context, key string) error
	PutDrafts(ctx context.Context, values map[string]string) error
	LockDialogue(ctx context.Context, key string, ttl time.Duration) (func(), error)
}

// The Redis lease outlives the entire request, including upstream/DB calls.
const taskProcessTimeout = 195 * time.Second

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

// UpdateService searches candidates and commits checked changes after a
// numbered selection. Its implementation is owned by the service layer.
type UpdateService interface {
	FindCandidates(ctx context.Context, userID uuid.UUID, action json.RawMessage) (services.UpdateCandidates, error)
	UpdateConfirmed(ctx context.Context, userID uuid.UUID, action json.RawMessage, saved []model.Task, changes api.UpdateChanges) ([]api.TaskItem, error)
}

// TaskProcess turns a text or audio command into an AI-service request.
type TaskProcess struct {
	rec       STT
	draft     DraftStore
	timezones TimezoneStore
	creator   TaskCreator
	reader    EventReader
	deleter   DeleteService
	updater   UpdateService
	aiURL     string
	conflicts ConflictFinder
	pending   PendingTaskStore
}

// NewTaskProcess constructs a task processor with the given dependencies.
func NewTaskProcess(rec STT, draft DraftStore, timezones TimezoneStore, creator TaskCreator, reader EventReader, deleter DeleteService, aiURL string, update ...UpdateService) *TaskProcess {
	processor := &TaskProcess{
		rec:       rec,
		draft:     draft,
		timezones: timezones,
		creator:   creator,
		reader:    reader,
		deleter:   deleter,
		aiURL:     aiURL,
	}
	if len(update) > 0 {
		processor.updater = update[0]
	}
	return processor
}

// aiClient creates a client with a deadline appropriate for the AI operation.
func aiClient(aiURL string, timeout time.Duration) (*ai.Client, error) {
	client, err := ai.NewClient(aiURL, ai.WithHTTPClient(&http.Client{Timeout: timeout}))
	if err != nil {
		return nil, fmt.Errorf("create AI client: %w", err)
	}
	return client, nil
}

// mapAIStatus переводит не-200 статус AI-сервиса в доменную ошибку.
func mapAIStatus(status int, body io.Reader) error {
	switch status {
	case http.StatusBadRequest:
		var problem struct {
			Code string `json:"code"`
		}
		if json.NewDecoder(io.LimitReader(body, 4096)).Decode(&problem) == nil && problem.Code == "unsupported_intent" {
			return model.ErrUnsupportedIntent
		}
		return fmt.Errorf("%w: AI service returned status %d", model.ErrBadGateway, status)
	case http.StatusServiceUnavailable:
		return fmt.Errorf("%w: AI service returned status %d", model.ErrServiceUnavailable, status)
	case http.StatusBadGateway:
		return fmt.Errorf("%w: AI gateway returned status %d", model.ErrServiceUnavailable, status)
	case http.StatusGatewayTimeout:
		return fmt.Errorf("%w: AI service returned status %d", model.ErrGatewayTimeout, status)
	default:
		return fmt.Errorf("%w: AI service returned status %d", model.ErrBadGateway, status)
	}
}

// createReq отправляет запрос AI-сервису.
func createReq(ctx context.Context, aiURL string, req *ai.JobRequest) (model.AiRes, error) {
	var result model.AiRes
	// The AI job may take 150 seconds; allow time for transport and response.
	client, err := aiClient(aiURL, 180*time.Second)
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
		return result, mapAIStatus(resp.StatusCode, resp.Body)
	}

	decoder := json.NewDecoder(resp.Body)
	var body json.RawMessage
	if err := decoder.Decode(&body); err != nil {
		return result, fmt.Errorf("%w: decode AI response: %w", model.ErrBadGateway, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return result, fmt.Errorf("%w: AI response contains trailing data", model.ErrBadGateway)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return result, fmt.Errorf("%w: AI response must be an object", model.ErrBadGateway)
	}
	strict := json.NewDecoder(bytes.NewReader(body))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&result); err != nil {
		return result, fmt.Errorf("%w: decode AI response: %w", model.ErrBadGateway, err)
	}

	// on_delete/on_update return intent only in the X-Task-Intent header; old
	// intents keep the JSON field. The normal result validator handles
	// missing legacy intent; this check rejects misplaced delete intent.
	headerIntent := strings.TrimSpace(resp.Header.Get("X-Task-Intent"))
	_, hasJSONIntent := fields["intent"]
	if headerIntent == "on_delete" || headerIntent == "on_update" {
		if hasJSONIntent {
			return result, fmt.Errorf("%w: %s response must omit JSON intent", model.ErrBadGateway, headerIntent)
		}
		result.Intent = headerIntent
	} else {
		if headerIntent != "" {
			return result, fmt.Errorf("%w: unexpected header intent %q", model.ErrBadGateway, headerIntent)
		}
		if result.Intent == "on_delete" || result.Intent == "on_update" {
			return result, fmt.Errorf("%w: invalid AI response intent placement", model.ErrBadGateway)
		}
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
	RecordType string  `json:"record_type"`
	Title      string  `json:"title"`
	Date       string  `json:"date"`
	Time       *string `json:"time"`
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

	var clock *time.Time
	if action.Time != nil {
		parsed, err := time.ParseInLocation("15:04", *action.Time, location)
		if err != nil {
			return services.CreateTaskCommand{}, fmt.Errorf("parse task time: %w", err)
		}
		clock = &parsed
	} else if action.RecordType != "task" {
		return services.CreateTaskCommand{}, fmt.Errorf("missing task time for %s", action.RecordType)
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

			question, err := t.checkMeetingConflict(ctx, result, cmd, message)
			if err != nil {
				return nil, err
			}
			if question != nil {
				return question, nil
			}
			task, err := t.creator.Create(ctx, cmd)
			if err != nil {
				return nil, fmt.Errorf("create task: %w", err)
			}
			if task == nil || task.Date == nil || (cmd.RecordType != "task" && task.Time == nil) {
				return nil, errors.New("created task is missing required date or time")
			}

			return &model.TaskResult{Intent: result.Intent, Body: &api.TaskCreatedResponse{
				Data: api.TaskCreatedData{
					Event: api.TaskCreatedEvent{
						CreatedAt:  task.CreatedAt,
						Date:       openapitypes.Date{Time: *task.Date},
						Id:         task.ID,
						RecordType: api.TaskCreatedEventRecordType(cmd.RecordType),
						Status:     api.TaskCreatedEventStatus(task.Status),
						Time:       task.Time,
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

		case "on_update":
			if t.updater == nil {
				return nil, errors.New("update handler is not configured")
			}
			return t.processUpdateResult(ctx, result)

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

type savedUpdateSelection struct {
	Snapshots []model.Task      `json:"snapshots"`
	Changes   api.UpdateChanges `json:"changes"`
}

// processUpdateResult mirrors the two-stage delete flow: candidate lookup is
// read-only, while selected IDs and original row snapshots are checked before
// a single transactional update.
func (t *TaskProcess) processUpdateResult(ctx context.Context, result model.AiRes) (*model.TaskResult, error) {
	var envelope struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(result.Action, &envelope); err != nil {
		return nil, fmt.Errorf("%w: decode update action: %w", model.ErrBadGateway, err)
	}
	key := fmt.Sprintf("task_update_candidates:%s:%s", result.UserID, result.TaskID)
	draftKey := fmt.Sprintf("task_draft:%s:%s", result.UserID, result.TaskID)
	switch envelope.Phase {
	case "find_candidates":
		set, err := t.updater.FindCandidates(ctx, result.UserID, result.Action)
		if err != nil {
			return nil, err
		}
		saved, err := json.Marshal(savedUpdateSelection{Snapshots: set.Snapshots, Changes: set.Changes})
		if err != nil {
			return nil, fmt.Errorf("encode update candidates: %w", err)
		}
		var ready ai.ReadyContext
		if err := json.Unmarshal(result.Context, &ready); err != nil {
			return nil, fmt.Errorf("%w: decode update context: %w", model.ErrBadGateway, err)
		}
		message := updateCandidatesMessage(len(set.Items))
		ready.RecentMessages = append(ready.RecentMessages, ai.DialogueMessage{Role: ai.Assistant, Text: message})
		var draft ai.UpdateSearchDraft
		if err := json.Unmarshal(set.Draft, &draft); err != nil {
			return nil, fmt.Errorf("decode update draft: %w", err)
		}
		contextBody, err := json.Marshal(ai.UpdateCandidateContext{
			RecentMessages: ready.RecentMessages, Draft: draft, Candidates: set.Stored,
		})
		if err != nil {
			return nil, fmt.Errorf("encode update context: %w", err)
		}
		if err := t.draft.PutDrafts(ctx, map[string]string{key: string(saved), draftKey: string(contextBody)}); err != nil {
			return nil, fmt.Errorf("save update context: %w", err)
		}
		body := &api.FrontendUpdateCandidates{
			Message: message, Status: "update_candidates", TaskId: result.TaskID, UserId: result.UserID,
		}
		body.Data.Changes = set.Changes
		body.Data.Candidates = make([]struct {
			Event  api.TaskItem `json:"event"`
			Number int          `json:"number"`
		}, 0, len(set.Items))
		for _, item := range set.Items {
			body.Data.Candidates = append(body.Data.Candidates, struct {
				Event  api.TaskItem `json:"event"`
				Number int          `json:"number"`
			}{Event: item.Event, Number: item.Number})
		}
		return &model.TaskResult{Intent: result.Intent, Body: body}, nil

	case "update_confirmed":
		raw, err := t.draft.GetDraft(ctx, key)
		if errors.Is(err, model.ErrDraftNotFound) {
			return nil, model.ErrStaleSelection
		}
		if err != nil {
			return nil, fmt.Errorf("get update candidates: %w", err)
		}
		var saved savedUpdateSelection
		if err := json.Unmarshal([]byte(raw), &saved); err != nil {
			return nil, fmt.Errorf("decode update candidates: %w", err)
		}
		items, err := t.updater.UpdateConfirmed(ctx, result.UserID, result.Action, saved.Snapshots, saved.Changes)
		if err != nil {
			return nil, err
		}
		var message string
		if err := json.Unmarshal(result.Message, &message); err != nil {
			return nil, fmt.Errorf("%w: decode update message: %w", model.ErrBadGateway, err)
		}
		body := &api.FrontendUpdated{
			Message: message,
			Status:  "updated", TaskId: result.TaskID, UserId: result.UserID,
		}
		body.Data.UpdatedEvents = items
		return &model.TaskResult{Intent: result.Intent, Body: body}, nil
	default:
		return nil, fmt.Errorf("%w: unknown update phase %q", model.ErrBadGateway, envelope.Phase)
	}
}

func updateCandidatesMessage(count int) string {
	if count == 0 {
		return "Подходящих записей для изменения не нашлось. Уточните запрос."
	}
	return fmt.Sprintf("Поиск завершён. Найдено записей: %d. Какие изменить?", count)
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

		responseMessage := deleteCandidatesMessage(len(set.Items))
		if err := t.saveDeleteContext(ctx, result, draftKey, set.Draft, set.Stored, responseMessage); err != nil {
			return nil, err
		}

		return &model.TaskResult{Intent: result.Intent, Body: &api.TaskDeleteCandidatesResponse{
			Data:    api.TaskDeleteCandidatesData{Candidates: set.Items},
			Message: responseMessage,
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
	responseMessage string,
) error {
	var ready ai.ReadyContext
	if err := json.Unmarshal(result.Context, &ready); err != nil {
		return fmt.Errorf("%w: decode delete context: %w", model.ErrBadGateway, err)
	}
	ready.RecentMessages = append(ready.RecentMessages, ai.DialogueMessage{
		Role: ai.Assistant,
		Text: responseMessage,
	})
	deleteContext := ai.DeleteCandidateContext{
		RecentMessages: ready.RecentMessages,
		Draft:          draft,
		Candidates:     stored,
	}
	raw, err := json.Marshal(deleteContext)
	if err != nil {
		return fmt.Errorf("encode delete context: %w", err)
	}
	candidates, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("encode delete candidates: %w", err)
	}
	candidatesKey := fmt.Sprintf("task_candidates:%s:%s", result.UserID, result.TaskID)
	if err := t.draft.PutDrafts(ctx, map[string]string{draftKey: string(raw), candidatesKey: string(candidates)}); err != nil {
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
	ctx, cancel := context.WithTimeout(ctx, taskProcessTimeout)
	defer cancel()
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

	newDialogue := taskID == nil
	if newDialogue {
		id := uuid.New()
		taskID = &id
	}
	unlock, err := t.draft.LockDialogue(ctx, fmt.Sprintf("task_lock:%s:%s", userID, *taskID), taskProcessTimeout+time.Minute)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if !newDialogue {
		confirmed, err := t.confirmPending(ctx, userID, *taskID, text, location)
		if err != nil || confirmed != nil {
			return confirmed, err
		}
	}
	if newDialogue {
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
	if err := preserveSearchSubject(&result, text); err != nil {
		return nil, fmt.Errorf("%w: preserve search subject: %w", model.ErrBadGateway, err)
	}
	processed, err := t.processAIResult(ctx, result, location)
	if err != nil {
		return nil, err
	}
	// The candidate list must survive find_candidates. Every other successful
	// ready result closes this dialogue; Redis expiry covers cleanup failures.
	switch processed.Body.(type) {
	case *api.TaskCreatedResponse, *api.TaskCollectionResponse, *api.TasksDeletedResponse, *api.FrontendUpdated:
		_ = t.draft.DeleteDraft(ctx, fmt.Sprintf("task_draft:%s:%s", result.UserID, result.TaskID))
		_ = t.draft.DeleteDraft(ctx, fmt.Sprintf("task_candidates:%s:%s", result.UserID, result.TaskID))
		_ = t.draft.DeleteDraft(ctx, fmt.Sprintf("task_update_candidates:%s:%s", result.UserID, result.TaskID))
	}
	return processed, nil
}

// AI can reduce "встреча с Таней" to just "встреча". Keep the subject
// following "с/со" from the user's original text before querying storage.
func preserveSearchSubject(result *model.AiRes, text string) error {
	if result.Status != "ready" || result.Intent != "search_events" {
		return nil
	}
	words := strings.Fields(text)
	subject := ""
	for i := 1; i < len(words); i++ {
		previous := strings.ToLower(strings.Trim(words[i-1], ".,!?«»\"'"))
		if previous != "с" && previous != "со" {
			continue
		}
		candidate := strings.Trim(words[i], ".,!?«»\"'")
		// «с понедельника» is a date bound, not a participant's name.
		switch strings.ToLower(candidate) {
		case "понедельника", "вторника", "среды", "четверга", "пятницы", "субботы", "воскресенья", "сегодня", "завтра", "послезавтра", "вчера", "утра", "вечера":
			continue
		}
		first, _ := utf8.DecodeRuneInString(candidate)
		if utf8.RuneCountInString(candidate) >= 3 && unicode.IsLetter(first) {
			subject = candidate
			break
		}
	}
	if subject == "" {
		return nil
	}
	var action ai.SearchEventsAction
	if err := json.Unmarshal(result.Action, &action); err != nil {
		return err
	}
	queryWords := strings.Fields(action.Query)
	for i, word := range queryWords {
		trimmed := strings.Trim(word, ".,!?«»\"'")
		if strings.EqualFold(trimmed, subject) {
			queryWords[i] = strings.Replace(word, trimmed, subject, 1)
			action.Query = strings.Join(queryWords, " ")
			return setSearchAction(result, action)
		}
	}
	action.Query = strings.TrimSpace(action.Query + " с " + subject)
	return setSearchAction(result, action)
}

func setSearchAction(result *model.AiRes, action ai.SearchEventsAction) error {
	raw, err := json.Marshal(action)
	if err != nil {
		return err
	}
	result.Action = raw
	return nil
}
