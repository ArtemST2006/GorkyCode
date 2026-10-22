// Package events lists and searches user events stored in the tasks table.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	api "gorkycode/backend/internal/generated"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// TaskLister reads user tasks from storage. from is inclusive, to is exclusive;
// empty types means all record types.
type TaskLister interface {
	ListEvents(ctx context.Context, userID uuid.UUID, from, to time.Time, types []string) ([]model.Task, error)
}

// TaskSearcher finds user tasks by title with optional date and type filters.
type TaskSearcher interface {
	SearchEvents(ctx context.Context, userID uuid.UUID, query string, from, to *time.Time, types []string) ([]model.Task, error)
}

// TaskReader supports both list_events and search_events.
type TaskReader interface {
	TaskLister
	TaskSearcher
}

// Lister returns user events for list and search actions.
type Lister struct {
	store TaskReader
}

// NewLister constructs an event lister over the given task storage.
func NewLister(store TaskReader) *Lister {
	return &Lister{store: store}
}

// ListEvents returns a collection of tasks for the given user and action.
func (l *Lister) ListEvents(ctx context.Context, userID uuid.UUID, action json.RawMessage) (api.TaskCollection, error) {
	if userID == uuid.Nil {
		return api.TaskCollection{}, fmt.Errorf("%w: empty user_id", model.ErrInvalidAction)
	}
	var act ai.ListEventsAction
	if err := json.Unmarshal(action, &act); err != nil {
		return api.TaskCollection{}, fmt.Errorf("%w: decode: %w", model.ErrInvalidAction, err)
	}

	from := act.DateFrom.Time
	to := act.DateToExclusive.Time
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return api.TaskCollection{}, fmt.Errorf("%w: invalid period", model.ErrInvalidAction)
	}

	types, err := parseRecordTypes(act.RecordTypes)
	if err != nil {
		return api.TaskCollection{}, err
	}

	tasks, err := l.store.ListEvents(ctx, userID, from, to, types)
	if err != nil {
		return api.TaskCollection{}, fmt.Errorf("list tasks: %w", err)
	}
	items := make([]api.TaskItem, 0, len(tasks))

	for _, t := range tasks {
		item, err := toItem(t)
		if err != nil {
			return api.TaskCollection{}, err
		}
		items = append(items, item)
	}

	return api.TaskCollection{Items: items}, nil
}

func parseRecordTypes(values *ai.RecordTypes) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	if len(*values) == 0 {
		return nil, fmt.Errorf("%w: empty record_types", model.ErrInvalidAction)
	}
	seen := make(map[ai.RecordType]bool, len(*values))
	types := make([]string, 0, len(*values))
	for _, rt := range *values {
		tp, err := toDBType(rt)
		if err != nil {
			return nil, err
		}
		if seen[rt] {
			return nil, fmt.Errorf("%w: duplicate record type %q", model.ErrInvalidAction, rt)
		}
		seen[rt] = true
		types = append(types, tp)
	}
	return types, nil
}

// toDBType validates a record type from the AI action. tasks.type stores the
// same values as the API, so a known type is passed to storage unchanged.
func toDBType(rt ai.RecordType) (string, error) {
	if !rt.Valid() {
		return "", fmt.Errorf("%w: unknown record type %q", model.ErrInvalidAction, rt)
	}
	return string(rt), nil
}

// toAPIType validates a record type read from tasks.type.
func toAPIType(dbType string) (api.TaskItemRecordType, error) {
	recordType := api.TaskItemRecordType(dbType)
	if !recordType.Valid() {
		return "", fmt.Errorf("unknown task type %q in storage", dbType)
	}
	return recordType, nil
}

// toItem converts a stored task into its API representation.
func toItem(t model.Task) (api.TaskItem, error) {
	recordType, err := toAPIType(t.Type)
	if err != nil {
		return api.TaskItem{}, err
	}

	var date *openapi_types.Date
	if t.Date != nil {
		date = &openapi_types.Date{Time: *t.Date}
	}

	status := api.TaskItemStatus(t.Status)
	if !status.Valid() {
		return api.TaskItem{}, fmt.Errorf("unknown task status %q in storage", t.Status)
	}

	return api.TaskItem{
		Id:         t.ID,
		UserId:     t.UserID,
		RecordType: recordType,
		Title:      t.Title,
		Date:       date,
		Time:       t.Time,
		Status:     status,
		CreatedAt:  t.CreatedAt,
	}, nil
}
