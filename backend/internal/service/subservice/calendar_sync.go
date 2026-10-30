package subservice

import (
	"context"
	"gorkycode/backend/internal/model"
)

// CalendarSync mirrors deleted and updated records into an external calendar.
// It is best effort: the records are already committed, so it reports nothing
// back and logs its own failures.
type CalendarSync interface {
	SyncDeletedTasks(ctx context.Context, tasks []model.Task)
	SyncUpdatedTasks(ctx context.Context, tasks []model.Task)
}
