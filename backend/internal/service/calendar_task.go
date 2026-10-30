package service

import (
	"context"
	"encoding/json"
	"fmt"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"
	services "gorkycode/backend/internal/service/subservice"
	"time"

	"github.com/google/uuid"
)

type ConflictFinder interface {
	Find(context.Context, uuid.UUID, time.Time) (*MeetingConflict, error)
}

func (t *TaskProcess) WithMeetingConflicts(c ConflictFinder) *TaskProcess {
	t.conflicts = c
	return t
}

// checkMeetingConflict rejects occupied time before any insert.
func (t *TaskProcess) checkMeetingConflict(ctx context.Context, cmd services.CreateTaskCommand, taskID uuid.UUID) error {
	if cmd.RecordType != "meeting" {
		return nil
	}
	if t.conflicts == nil {
		return model.ErrServiceUnavailable
	}
	if cmd.Time == nil {
		return model.ErrInvalidAction
	}
	start := time.Date(cmd.Date.Year(), cmd.Date.Month(), cmd.Date.Day(), cmd.Time.Hour(), cmd.Time.Minute(), 0, 0, cmd.Date.Location())
	conflict, err := t.conflicts.Find(ctx, cmd.UserID, start)
	if err != nil {
		return err
	}
	if conflict != nil {
		return &model.MeetingConflictError{Detail: conflict.Message(), UserID: cmd.UserID, TaskID: taskID}
	}
	return nil
}

// saveMeetingConflictContext keeps the original meeting details so the user
// can provide a new time using the same task_id.
func (t *TaskProcess) saveMeetingConflictContext(ctx context.Context, result model.AiRes, cmd services.CreateTaskCommand, message string) error {
	var ready ai.ReadyContext
	if err := json.Unmarshal(result.Context, &ready); err != nil {
		return fmt.Errorf("%w: decode meeting context: %w", model.ErrBadGateway, err)
	}
	history := append(ready.RecentMessages, ai.DialogueMessage{Role: ai.Assistant, Text: message})
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	contextValue := struct {
		RecentMessages ai.RecentMessages `json:"recent_messages"`
		Draft          map[string]string `json:"draft"`
	}{
		RecentMessages: history,
		Draft:          map[string]string{"record_type": "meeting", "title": cmd.Title, "date": cmd.Date.Format(time.DateOnly)},
	}
	raw, err := json.Marshal(contextValue)
	if err != nil {
		return fmt.Errorf("encode meeting context: %w", err)
	}
	key := fmt.Sprintf("task_draft:%s:%s", result.UserID, result.TaskID)
	if err := t.draft.PutDraft(ctx, key, string(raw)); err != nil {
		return fmt.Errorf("save meeting context: %w", err)
	}
	return nil
}
