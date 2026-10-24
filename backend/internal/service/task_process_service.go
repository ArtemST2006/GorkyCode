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

// ConflictFinder finds what a new meeting overlaps.
type ConflictFinder interface {
	Find(ctx context.Context, userID uuid.UUID, start time.Time) (*MeetingConflict, error)
}

// PendingTaskStore keeps meetings that wait for the user's confirmation.
type PendingTaskStore interface {
	SavePendingTask(ctx context.Context, key, value string, ttl time.Duration) error
	TakePendingTask(ctx context.Context, key string) (string, error)
}

// pendingTaskTTL limits how long a conflicting meeting waits for confirmation.
const pendingTaskTTL = 10 * time.Minute

// TaskProcess turns a text or audio command into an AI-service request.
type TaskProcess struct {
	rec       STT
	draft     DraftStore
	timezones TimezoneStore
	creator   TaskCreator
	reader    EventReader
	aiURL     string
	conflicts ConflictFinder
	pending   PendingTaskStore
}

// NewTaskProcess constructs a task processor with the given dependencies.
func NewTaskProcess(rec STT, draft DraftStore, timezones TimezoneStore, creator TaskCreator, reader EventReader, aiURL string) *TaskProcess {
	return &TaskProcess{
		rec:       rec,
		draft:     draft,
		timezones: timezones,
		creator:   creator,
		reader:    reader,
		aiURL:     aiURL,
	}
}

// WithMeetingConflicts makes the processor ask for confirmation before it
// creates a meeting that overlaps another one.
func (t *TaskProcess) WithMeetingConflicts(conflicts ConflictFinder, pending PendingTaskStore) *TaskProcess {
	t.conflicts = conflicts
	t.pending = pending
	return t
}

// createReq отправляет запрос AI-сервису.
func createReq(ctx context.Context, aiURL string, req *ai.JobRequest) (model.AiRes, error) {
	var result model.AiRes
	client, err := ai.NewClient(aiURL, ai.WithHTTPClient(&http.Client{Timeout: 20 * time.Second}))
	if err != nil {
		return result, fmt.Errorf("create AI client: %w", err)
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
		switch resp.StatusCode {
		case http.StatusServiceUnavailable:
			return result, fmt.Errorf("%w: AI service returned status %d", model.ErrServiceUnavailable, resp.StatusCode)
		case http.StatusGatewayTimeout:
			return result, fmt.Errorf("%w: AI service returned status %d", model.ErrGatewayTimeout, resp.StatusCode)
		default:
			return result, fmt.Errorf("%w: AI service returned status %d", model.ErrBadGateway, resp.StatusCode)
		}
	}

	decoder := json.NewDecoder(resp.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, fmt.Errorf("%w: decode AI response: %w", model.ErrBadGateway, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return result, fmt.Errorf("%w: AI response contains trailing data", model.ErrBadGateway)
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
) (model.TaskResult, error) {
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
			return t.createTask(ctx, cmd, result.UserID, result.TaskID, message)

		case "list_events":
			if t.reader == nil {
				return nil, errors.New("event listing handler is not configured")
			}
			collection, err := t.reader.ListEvents(ctx, result.UserID, result.Action)
			if err != nil {
				return nil, fmt.Errorf("list events: %w", err)
			}
			return &api.TaskCollectionResponse{
				Data:    collection,
				UserId:  result.UserID,
				TaskId:  result.TaskID,
				Status:  api.TaskCollectionResponseStatusOk,
				Message: message,
			}, nil

		case "search_events":
			if t.reader == nil {
				return nil, errors.New("event search handler is not configured")
			}
			collection, err := t.reader.SearchEvents(ctx, result.UserID, result.Action)
			if err != nil {
				return nil, fmt.Errorf("search events: %w", err)
			}
			return &api.TaskCollectionResponse{
				Data:    collection,
				UserId:  result.UserID,
				TaskId:  result.TaskID,
				Status:  api.TaskCollectionResponseStatusOk,
				Message: message,
			}, nil

		default:
			return nil, fmt.Errorf("invalid intent %s", result.Intent)
		}

	case "needs_clarification":
		key := fmt.Sprintf("task_draft:%s:%s", result.UserID, result.TaskID)
		if err := t.draft.PutDraft(ctx, key, string(result.Context)); err != nil {
			return nil, fmt.Errorf("save draft context: %w", err)
		}
		return &api.TaskClarificationResponse{
			UserId:  result.UserID,
			TaskId:  result.TaskID,
			Status:  api.NeedsClarification,
			Message: message,
		}, nil

	default:
		return nil, fmt.Errorf("invalid AI status %s", result.Status)
	}
}

// createTask saves the task and builds the 201 response with the AI message.
func (t *TaskProcess) createTask(
	ctx context.Context,
	cmd services.CreateTaskCommand,
	userID, taskID uuid.UUID,
	message string,
) (model.TaskResult, error) {
	task, err := t.creator.Create(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}
	if task == nil || task.Date == nil || task.Time == nil {
		return nil, errors.New("created task is missing date or time")
	}

	return &api.TaskCreatedResponse{
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
		TaskId:  taskID,
		UserId:  userID,
	}, nil
}

// pendingTask is a meeting that waits for the user's confirmation.
type pendingTask struct {
	createAction
	Message  string `json:"message"`
	Question string `json:"question"`
}

func pendingTaskKey(userID, taskID uuid.UUID) string {
	return fmt.Sprintf("task_confirm:%s:%s", userID, taskID)
}

// checkMeetingConflict saves a meeting that overlaps another one and returns
// the confirmation question. It returns nil when the task can be created.
func (t *TaskProcess) checkMeetingConflict(
	ctx context.Context,
	result model.AiRes,
	cmd services.CreateTaskCommand,
	message string,
) (*api.TaskClarificationResponse, error) {
	if t.conflicts == nil || t.pending == nil || cmd.RecordType != "meeting" {
		return nil, nil
	}

	start := time.Date(cmd.Date.Year(), cmd.Date.Month(), cmd.Date.Day(),
		cmd.Time.Hour(), cmd.Time.Minute(), 0, 0, cmd.Date.Location())
	conflict, err := t.conflicts.Find(ctx, cmd.UserID, start)
	if err != nil {
		return nil, fmt.Errorf("check meeting conflicts: %w", err)
	}
	if conflict == nil {
		return nil, nil
	}

	pending := pendingTask{
		createAction: createAction{
			RecordType: cmd.RecordType,
			Title:      cmd.Title,
			Date:       cmd.Date.Format(time.DateOnly),
			Time:       cmd.Time.Format("15:04"),
		},
		Message:  message,
		Question: conflict.Question(),
	}
	if err = t.savePending(ctx, result.UserID, result.TaskID, pending); err != nil {
		return nil, err
	}
	return &api.TaskClarificationResponse{
		UserId:  result.UserID,
		TaskId:  result.TaskID,
		Status:  api.NeedsClarification,
		Message: pending.Question,
	}, nil
}

func (t *TaskProcess) savePending(ctx context.Context, userID, taskID uuid.UUID, pending pendingTask) error {
	raw, err := json.Marshal(pending)
	if err != nil {
		return fmt.Errorf("encode pending task: %w", err)
	}
	if err = t.pending.SavePendingTask(ctx, pendingTaskKey(userID, taskID), string(raw), pendingTaskTTL); err != nil {
		return fmt.Errorf("save pending task: %w", err)
	}
	return nil
}

// confirmPending answers a confirmation question without the AI service.
// It returns nil, nil when no meeting waits for this task_id.
func (t *TaskProcess) confirmPending(
	ctx context.Context,
	userID, taskID uuid.UUID,
	text string,
	location *time.Location,
) (model.TaskResult, error) {
	if t.pending == nil {
		return nil, nil
	}
	raw, err := t.pending.TakePendingTask(ctx, pendingTaskKey(userID, taskID))
	if errors.Is(err, model.ErrPendingTaskNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("take pending task: %w", err)
	}
	var pending pendingTask
	if err = json.Unmarshal([]byte(raw), &pending); err != nil {
		return nil, fmt.Errorf("decode pending task: %w", err)
	}

	switch utils.ParseConfirmation(text) {
	case utils.ConfirmationYes:
		action, encodeErr := json.Marshal(pending.createAction)
		if encodeErr != nil {
			return nil, fmt.Errorf("encode pending action: %w", encodeErr)
		}
		cmd, parseErr := parseCreateTaskCommand(model.AiRes{UserID: userID, Action: action}, location)
		if parseErr != nil {
			return nil, fmt.Errorf("parse pending task: %w", parseErr)
		}
		return t.createTask(ctx, cmd, userID, taskID, pending.Message)
	case utils.ConfirmationNo:
		return &api.TaskCancelledResponse{
			UserId:  userID,
			TaskId:  taskID,
			Status:  api.Cancelled,
			Message: "Хорошо, встречу не ставлю.",
		}, nil
	case utils.ConfirmationUnknown:
		if err = t.savePending(ctx, userID, taskID, pending); err != nil {
			return nil, err
		}
		return &api.TaskClarificationResponse{
			UserId:  userID,
			TaskId:  taskID,
			Status:  api.NeedsClarification,
			Message: "Ответьте «да» или «нет». " + pending.Question,
		}, nil
	default:
		return nil, errors.New("unknown confirmation answer")
	}
}

// Process handles a text or audio command and returns the HTTP-facing result.
func (t *TaskProcess) Process(
	ctx context.Context,
	req model.TaskRequest,
) (model.TaskResult, error) {
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

	if taskID != nil {
		confirmed, confirmErr := t.confirmPending(ctx, userID, *taskID, text, location)
		if confirmErr != nil || confirmed != nil {
			return confirmed, confirmErr
		}
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
