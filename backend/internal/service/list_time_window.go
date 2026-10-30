package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	api "gorkycode/backend/internal/generated"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"

	"github.com/google/uuid"
	openapitypes "github.com/oapi-codegen/runtime/types"
)

// The model extracts the duration into the list action. The backend anchors it
// to the trusted request time and filters stored local date/time values.
func (t *TaskProcess) listEventsWithWindow(
	ctx context.Context, userID uuid.UUID, raw json.RawMessage,
	location *time.Location, requestTime time.Time,
) (api.TaskCollection, error) {
	var action ai.ListEventsAction
	if err := json.Unmarshal(raw, &action); err != nil {
		return api.TaskCollection{}, fmt.Errorf("%w: decode list action: %w", model.ErrInvalidAction, err)
	}
	if action.UpcomingMinutes == nil {
		return t.reader.ListEvents(ctx, userID, raw)
	}
	minutes := *action.UpcomingMinutes
	if minutes < 1 || minutes > 10080 {
		return api.TaskCollection{}, fmt.Errorf("%w: invalid upcoming_minutes", model.ErrInvalidAction)
	}
	start := requestTime.In(location)
	end := start.Add(time.Duration(minutes) * time.Minute)
	action.UpcomingMinutes = nil
	action.DateFrom = &openapitypes.Date{Time: start}
	action.DateToExclusive = &openapitypes.Date{Time: end.AddDate(0, 0, 1)}
	if action.Statuses == nil {
		statuses := ai.ListStatuses{ai.TaskStatusActive, ai.TaskStatusInProgress}
		action.Statuses = &statuses
	}
	query, err := json.Marshal(action)
	if err != nil {
		return api.TaskCollection{}, fmt.Errorf("encode list time window: %w", err)
	}
	collection, err := t.reader.ListEvents(ctx, userID, query)
	if err != nil {
		return api.TaskCollection{}, err
	}
	items := make([]api.TaskItem, 0, len(collection.Items))
	for _, item := range collection.Items {
		if item.Date == nil || item.Time == nil {
			continue
		}
		allowedStatus := false
		for _, status := range *action.Statuses {
			if string(status) == string(item.Status) {
				allowedStatus = true
				break
			}
		}
		if !allowedStatus {
			continue
		}
		scheduled, parseErr := time.ParseInLocation(
			"2006-01-02 15:04", item.Date.Time.Format("2006-01-02")+" "+*item.Time, location,
		)
		if parseErr != nil {
			return api.TaskCollection{}, fmt.Errorf("parse event time: %w", parseErr)
		}
		if !scheduled.Before(start) && scheduled.Before(end) {
			items = append(items, item)
		}
	}
	return api.TaskCollection{Items: items}, nil
}
