package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	openapitypes "github.com/oapi-codegen/runtime/types"
	api "gorkycode/backend/internal/generated"
	"gorkycode/backend/internal/model"
	services "gorkycode/backend/internal/service/subservice"
	"gorkycode/backend/internal/service/utils"
	"time"
)

type ConflictFinder interface {
	Find(context.Context, uuid.UUID, time.Time) (*MeetingConflict, error)
}
type PendingTaskStore interface {
	SavePendingTask(context.Context, string, string, time.Duration) error
	TakePendingTask(context.Context, string) (string, error)
}

const pendingTaskTTL = 10 * time.Minute

type pendingTask struct {
	createAction
	Message  string `json:"message"`
	Question string `json:"question"`
}

func pendingTaskKey(userID, taskID uuid.UUID) string {
	return fmt.Sprintf("task_confirm:%s:%s", userID, taskID)
}
func (t *TaskProcess) WithMeetingConflicts(c ConflictFinder, p PendingTaskStore) *TaskProcess {
	t.conflicts = c
	t.pending = p
	return t
}
func (t *TaskProcess) savePending(ctx context.Context, userID, taskID uuid.UUID, p pendingTask) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return t.pending.SavePendingTask(ctx, pendingTaskKey(userID, taskID), string(raw), pendingTaskTTL)
}
func (t *TaskProcess) checkMeetingConflict(ctx context.Context, result model.AiRes, cmd services.CreateTaskCommand, message string) (*model.TaskResult, error) {
	if t.conflicts == nil || t.pending == nil || cmd.RecordType != "meeting" {
		return nil, nil
	}
	if cmd.Time == nil {
		return nil, model.ErrInvalidAction
	}
	start := time.Date(cmd.Date.Year(), cmd.Date.Month(), cmd.Date.Day(), cmd.Time.Hour(), cmd.Time.Minute(), 0, 0, cmd.Date.Location())
	conflict, err := t.conflicts.Find(ctx, cmd.UserID, start)
	if err != nil {
		return nil, err
	}
	if conflict == nil {
		return nil, nil
	}
	clock := cmd.Time.Format("15:04")
	pending := pendingTask{createAction: createAction{RecordType: cmd.RecordType, Title: cmd.Title, Date: cmd.Date.Format(time.DateOnly), Time: &clock}, Message: message, Question: conflict.Question()}
	if err := t.savePending(ctx, result.UserID, result.TaskID, pending); err != nil {
		return nil, err
	}
	return &model.TaskResult{Intent: "create_event", Body: &api.TaskClarificationResponse{UserId: result.UserID, TaskId: result.TaskID, Status: api.NeedsClarification, Message: pending.Question}}, nil
}
func (t *TaskProcess) confirmPending(ctx context.Context, userID, taskID uuid.UUID, text string, location *time.Location) (*model.TaskResult, error) {
	if t.pending == nil {
		return nil, nil
	}
	raw, err := t.pending.TakePendingTask(ctx, pendingTaskKey(userID, taskID))
	if errors.Is(err, model.ErrPendingTaskNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pending pendingTask
	if err = json.Unmarshal([]byte(raw), &pending); err != nil {
		return nil, err
	}
	switch utils.ParseConfirmation(text) {
	case utils.ConfirmationNo:
		return &model.TaskResult{Intent: "create_event", Body: &api.TaskCancelledResponse{UserId: userID, TaskId: taskID, Status: api.Cancelled, Message: "Хорошо, встречу не ставлю."}}, nil
	case utils.ConfirmationYes:
		action, err := json.Marshal(pending.createAction)
		if err != nil {
			return nil, err
		}
		cmd, err := parseCreateTaskCommand(model.AiRes{UserID: userID, Action: action}, location)
		if err != nil {
			return nil, err
		}
		task, err := t.creator.Create(ctx, cmd)
		if err != nil {
			_ = t.savePending(ctx, userID, taskID, pending)
			return nil, err
		}
		if task == nil || task.Date == nil {
			return nil, errors.New("created meeting missing date")
		}
		return &model.TaskResult{Intent: "create_event", Body: &api.TaskCreatedResponse{
			UserId: userID, TaskId: taskID, Status: api.TaskCreatedResponseStatusOk, Message: pending.Message,
			Data: api.TaskCreatedData{Event: api.TaskCreatedEvent{Id: task.ID, UserId: task.UserID, Title: task.Title, Date: openapitypes.Date{Time: *task.Date}, Time: task.Time, RecordType: api.TaskCreatedEventRecordType(task.Type), Status: api.TaskCreatedEventStatus(task.Status), CreatedAt: task.CreatedAt}},
		}}, nil
	default:
		if err := t.savePending(ctx, userID, taskID, pending); err != nil {
			return nil, err
		}
		return &model.TaskResult{Intent: "create_event", Body: &api.TaskClarificationResponse{UserId: userID, TaskId: taskID, Status: api.NeedsClarification, Message: "Ответьте «да» или «нет». " + pending.Question}}, nil
	}
}
