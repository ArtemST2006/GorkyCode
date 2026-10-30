// Package subservice contains internal application services.
package subservice

import (
	"context"
	"encoding/json"
	"fmt"
	api "gorkycode/backend/internal/generated"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// TaskLister reads user tasks from storage. from is inclusive, to is exclusive;
// empty types means all record types.
type TaskLister interface {
	ListEvents(ctx context.Context, userID uuid.UUID, from, to time.Time, types []string) ([]model.Task, error)
	ListEventsFiltered(ctx context.Context, userID uuid.UUID, from, to *time.Time, types, statuses []string) ([]model.Task, error)
}

// TaskSearcher finds user tasks by title with optional date and type filters.
type TaskSearcher interface {
	SearchEvents(ctx context.Context, userID uuid.UUID, query string, from, to *time.Time, types []string) ([]model.Task, error)
}

// TaskSemanticSearcher finds compatible title vectors with the same filters.
type TaskSemanticSearcher interface {
	FindSemantic(ctx context.Context, userID uuid.UUID, query []float32, modelName, version string, dateFrom, dateTo *time.Time, timeFrom, timeTo *string, types []string) ([]model.Task, error)
}

// TaskReader supports both list_events and search_events.
type TaskReader interface {
	TaskLister
	TaskSearcher
}

// Lister returns user events for list and search actions.
type Lister struct {
	store         TaskReader
	semanticStore TaskSemanticSearcher
	embedder      model.Embedder
}

// NewLister constructs an event lister over the given task storage.
func NewLister(store TaskReader) *Lister {
	return &Lister{store: store}
}

// NewSemanticLister also searches compatible title embeddings for search_events.
func NewSemanticLister(store TaskReader, semanticStore TaskSemanticSearcher, embedder model.Embedder) *Lister {
	return &Lister{store: store, semanticStore: semanticStore, embedder: embedder}
}

// ListAllTasks returns the user's tasks without any dialogue action. If a search
// query is supplied it is applied by substring; otherwise the dated range [from,to]
// limits the result (nil bounds defaults to the whole stored range). Items are
// windowsliced by limit and offset for simple pagination.
func (l *Lister) ListAllTasks(
	ctx context.Context,
	userID uuid.UUID,
	query string,
	types []string,
	from, to *time.Time,
	limit, offset int,
) (api.TaskCollection, error) {
	if userID == uuid.Nil {
		return api.TaskCollection{}, fmt.Errorf("%w: empty user_id", model.ErrInvalidAction)
	}
	if limit <= 0 {
		limit = 200
	} else if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}

	var tasks []model.Task
	var err error
	dateFrom := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	dateTo := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	if from != nil {
		dateFrom = *from
	}
	if to != nil {
		dateTo = *to
	}

	if strings.TrimSpace(query) != "" {
		tasks, err = l.store.SearchEvents(ctx, userID, strings.TrimSpace(query), from, to, types)
	} else {
		tasks, err = l.store.ListEvents(ctx, userID, dateFrom, dateTo, types)
	}
	if err != nil {
		return api.TaskCollection{}, fmt.Errorf("list tasks: %w", err)
	}
	// SearchEvents already returns the matching whole result set.
	if offset > len(tasks) {
		offset = len(tasks)
	}
	end := offset + limit
	if end > len(tasks) {
		end = len(tasks)
	}
	tasks = tasks[offset:end]

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

// ListEvents returns a collection of tasks for the given user and action.
func (l *Lister) ListEvents(ctx context.Context, userID uuid.UUID, action json.RawMessage) (api.TaskCollection, error) {
	if userID == uuid.Nil {
		return api.TaskCollection{}, fmt.Errorf("%w: empty user_id", model.ErrInvalidAction)
	}
	if string(action) == "null" || len(action) == 0 {
		return api.TaskCollection{}, fmt.Errorf("%w: missing action", model.ErrInvalidAction)
	}
	var act ai.ListEventsAction
	if err := json.Unmarshal(action, &act); err != nil {
		return api.TaskCollection{}, fmt.Errorf("%w: decode: %w", model.ErrInvalidAction, err)
	}

	var from, to *time.Time
	if act.DateFrom != nil {
		from = &act.DateFrom.Time
	}
	if act.DateToExclusive != nil {
		to = &act.DateToExclusive.Time
	}
	if from != nil && to != nil && !from.Before(*to) {
		return api.TaskCollection{}, fmt.Errorf("%w: invalid period", model.ErrInvalidAction)
	}

	types, err := parseRecordTypes(act.RecordTypes)
	if err != nil {
		return api.TaskCollection{}, err
	}

	var statuses []string
	if act.Statuses != nil {
		if len(*act.Statuses) == 0 {
			return api.TaskCollection{}, fmt.Errorf("%w: empty statuses", model.ErrInvalidAction)
		}
		seen := map[ai.TaskStatus]bool{}
		for _, status := range *act.Statuses {
			if !status.Valid() || seen[status] {
				return api.TaskCollection{}, fmt.Errorf("%w: invalid status %q", model.ErrInvalidAction, status)
			}
			seen[status] = true
			statuses = append(statuses, string(status))
		}
	}
	tasks, err := l.store.ListEventsFiltered(ctx, userID, from, to, types, statuses)
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
	if !status.Valid() || (status == api.TaskItemStatusInProgress && recordType != api.TaskItemRecordTypeTask) {
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
