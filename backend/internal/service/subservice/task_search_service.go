package subservice

import (
	"context"
	"encoding/json"
	"fmt"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"
	"strings"
	"time"

	api "gorkycode/backend/internal/generated"

	"github.com/google/uuid"
)

// SearchEvents returns records whose titles contain the query, ignoring case.
func (l *Lister) SearchEvents(ctx context.Context, userID uuid.UUID, action json.RawMessage) (api.TaskCollection, error) {
	if userID == uuid.Nil {
		return api.TaskCollection{}, fmt.Errorf("%w: empty user_id", model.ErrInvalidAction)
	}
	var act ai.SearchEventsAction
	if err := json.Unmarshal(action, &act); err != nil {
		return api.TaskCollection{}, fmt.Errorf("%w: decode: %w", model.ErrInvalidAction, err)
	}
	query := strings.TrimSpace(act.Query)
	if query == "" {
		return api.TaskCollection{}, fmt.Errorf("%w: empty search query", model.ErrInvalidAction)
	}
	var from, to *time.Time
	if act.DateFrom != nil {
		from = &act.DateFrom.Time
		if from.IsZero() {
			return api.TaskCollection{}, fmt.Errorf("%w: invalid date_from", model.ErrInvalidAction)
		}
	}
	if act.DateToExclusive != nil {
		to = &act.DateToExclusive.Time
		if to.IsZero() {
			return api.TaskCollection{}, fmt.Errorf("%w: invalid date_to_exclusive", model.ErrInvalidAction)
		}
	}
	if from != nil && to != nil && !from.Before(*to) {
		return api.TaskCollection{}, fmt.Errorf("%w: invalid period", model.ErrInvalidAction)
	}
	types, err := parseRecordTypes(act.RecordTypes)
	if err != nil {
		return api.TaskCollection{}, err
	}
	tasks, err := l.store.SearchEvents(ctx, userID, query, from, to, types)
	if err != nil {
		return api.TaskCollection{}, fmt.Errorf("search tasks: %w", err)
	}
	items := make([]api.TaskItem, 0, len(tasks))
	for _, task := range tasks {
		item, err := toItem(task)
		if err != nil {
			return api.TaskCollection{}, err
		}
		items = append(items, item)
	}
	return api.TaskCollection{Items: items}, nil
}
